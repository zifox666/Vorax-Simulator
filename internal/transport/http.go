package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"vorax/internal/application"
	pb "vorax/internal/protocol"
	"vorax/internal/storage"
)

func Router(svc *application.Service, cache *storage.Cache, assets http.FileSystem) *gin.Engine {
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
			expected := "http://" + c.Request.Host
			if c.Request.TLS != nil {
				expected = "https://" + c.Request.Host
			}
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
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Status(404)
			return
		}
		http.FileServer(assets).ServeHTTP(c.Writer, c.Request)
	})
	return r
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
