package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"vorax/internal/ai"
	"vorax/internal/application"
	pb "vorax/internal/protocol"
	"vorax/internal/storage"
	"vorax/internal/training"
)

type TrainingDependencies struct {
	Service            *training.Service
	Keys               *training.KeyManager
	Limiter            training.BucketLimiter
	LimiterUnavailable bool
	AdminToken         string
}

// Router serves the application. publicOrigin is the externally visible origin
// (for example, https://ky.dscan.icu) when TLS terminates at a reverse proxy.
// An empty value derives the origin from the direct request, which is suitable
// for local HTTP development.
func Router(svc *application.Service, cache *storage.Cache, assets http.FileSystem, publicOrigin string) (*gin.Engine, error) {
	return RouterWithTraining(svc, cache, assets, publicOrigin, nil)
}

func RouterWithTraining(svc *application.Service, cache *storage.Cache, assets http.FileSystem, publicOrigin string, trainingDeps *TrainingDependencies) (*gin.Engine, error) {
	publicOrigin, err := normalizeOrigin(publicOrigin)
	if err != nil {
		return nil, err
	}
	r := gin.New()
	r.Use(gin.Recovery())
	_ = r.SetTrustedProxies(nil)
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "rulesVersion": svc.Rules.Version, "contentVersion": svc.Rules.ContentVersion})
	})
	api := r.Group("/api/v1")
	api.Use(func(c *gin.Context) {
		if origin := c.GetHeader("Origin"); origin != "" {
			expected := requestOrigin(c.Request, publicOrigin)
			if origin != expected {
				c.AbortWithStatusJSON(403, gin.H{"code": "ORIGIN_REJECTED", "message": "仅接受同源请求"})
				return
			}
		}
		ip := sha256.Sum256([]byte(c.ClientIP()))
		if !cache.Allow(c.Request.Context(), hex.EncodeToString(ip[:])) {
			c.AbortWithStatusJSON(429, gin.H{"code": "RATE_LIMITED", "message": "操作过于频繁，请稍后重试"})
			return
		}
		c.Next()
	})
	api.POST("/runs", endpoint(cache, func() proto.Message { return new(pb.CreateRunRequest) }, func(c *gin.Context, m proto.Message) (proto.Message, error) {
		return svc.Create(m.(*pb.CreateRunRequest))
	}))
	api.POST("/runs/:id/commands", endpoint(cache, func() proto.Message { return new(pb.CommandRequest) }, func(c *gin.Context, m proto.Message) (proto.Message, error) {
		return svc.Command(c.Param("id"), m.(*pb.CommandRequest))
	}))
	api.POST("/runs/:id/restore", endpoint(nil, func() proto.Message { return new(pb.RestoreRequest) }, func(c *gin.Context, m proto.Message) (proto.Message, error) {
		return svc.Restore(c.Param("id"), m.(*pb.RestoreRequest))
	}))
	api.POST("/replays/verify", endpoint(nil, func() proto.Message { return new(pb.ReplayRequest) }, func(c *gin.Context, m proto.Message) (proto.Message, error) { return svc.Replay(m.(*pb.ReplayRequest)) }))
	// 有限信息 AI 决策：AI 只能看到 UI 渲染内容（Observation），看不到 seed/RNG。
	// 两种调用方式：
	//   1. stateToken 模式：服务端从签名存档构建观察（推荐，信息边界由服务端保证）。
	//   2. observation 模式：调用方直接传入观察 JSON（适合外部研究脚本）。
	modelSpec, err := training.NewSpec(svc.Rules)
	if err != nil {
		return nil, fmt.Errorf("create public model specification: %w", err)
	}
	registerVisibleAI(api, modelSpec)
	api.POST("/ai/decide", func(c *gin.Context) {
		var req struct {
			StateToken  string          `json:"stateToken"`
			Observation *ai.Observation `json:"observation"`
			Strategy    string          `json:"strategy"`
			Samples     int             `json:"samples"`
			Rollouts    int             `json:"rollouts"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"code": "INVALID_JSON", "message": "请求体不符合 AI 接口结构"})
			return
		}
		if req.Strategy == "" {
			req.Strategy = string(ai.StrategySampler)
		}
		var (
			act *ai.Action
			obs *ai.Observation
			err error
		)
		if req.StateToken != "" {
			act, obs, err = svc.AIDecide(req.StateToken, ai.Strategy(req.Strategy), ai.Params{Samples: req.Samples, Rollouts: req.Rollouts})
		} else if req.Observation != nil {
			obs = req.Observation
			act, err = ai.Decide(obs, ai.Strategy(req.Strategy), ai.Params{Samples: req.Samples, Rollouts: req.Rollouts})
		} else {
			c.JSON(400, gin.H{"code": "INVALID_INPUT", "message": "需要 stateToken 或 observation"})
			return
		}
		if err != nil {
			code := application.ErrorCode(err)
			if code == "STALE_STATE" || code == "STALE_OFFER" {
				c.JSON(409, gin.H{"code": code, "message": err.Error()})
				return
			}
			c.JSON(400, gin.H{"code": "AI_DECIDE_FAILED", "message": err.Error()})
			return
		}
		c.JSON(200, gin.H{"action": act, "strategy": req.Strategy, "observation": obs})
	})
	if trainingDeps != nil && trainingDeps.Service != nil && trainingDeps.Keys != nil {
		registerTraining(r, assets, publicOrigin, trainingDeps)
	}
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Status(404)
			return
		}
		http.FileServer(assets).ServeHTTP(c.Writer, c.Request)
	})
	return r, nil
}

func normalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("PUBLIC_ORIGIN 必须是无路径、参数或片段的 http(s) 来源，例如 https://example.com")
	}
	return u.Scheme + "://" + u.Host, nil
}

func requestOrigin(r *http.Request, publicOrigin string) string {
	if publicOrigin != "" {
		return publicOrigin
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func endpoint(cache *storage.Cache, factory func() proto.Message, call func(*gin.Context, proto.Message) (proto.Message, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.ContentType(), "application/json") {
			c.JSON(415, gin.H{"code": "INVALID_CONTENT_TYPE", "message": "请使用 application/json"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10))
		if err != nil {
			c.JSON(413, gin.H{"code": "BODY_TOO_LARGE", "message": "请求过大"})
			return
		}
		m := factory()
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, m); err != nil {
			c.JSON(400, gin.H{"code": "INVALID_JSON", "message": "请求格式不符合协议"})
			return
		}
		digest := sha256.Sum256(append([]byte(c.Request.URL.Path+"\x00"), body...))
		key := hex.EncodeToString(digest[:])
		if b := cache.Get(c.Request.Context(), key); b != nil {
			c.Data(200, "application/json; charset=utf-8", b)
			return
		}
		result, err := call(c, m)
		if err != nil {
			code := application.ErrorCode(err)
			status := 400
			if code == "STALE_STATE" || code == "STALE_OFFER" {
				status = 409
			}
			if code == "VERSION_UNAVAILABLE" || code == "KEY_UNAVAILABLE" {
				status = 422
			}
			if code == "INTERNAL_ERROR" {
				status = 500
				c.JSON(status, gin.H{"code": code, "message": "服务内部错误"})
				return
			}
			c.JSON(status, gin.H{"code": code, "message": err.Error()})
			return
		}
		b, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(result)
		if err != nil {
			c.Status(500)
			return
		}
		cache.Put(c.Request.Context(), key, b)
		c.Data(200, "application/json; charset=utf-8", b)
	}
}
