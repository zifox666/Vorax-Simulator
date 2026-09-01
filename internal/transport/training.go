package transport

import (
	"crypto/subtle"
	"io"
	"math"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"vorax/internal/application"
	pb "vorax/internal/protocol"
	"vorax/internal/training"
)

const trainingKeyContext = "vorax.training.key"

func registerTraining(r *gin.Engine, assets http.FileSystem, publicOrigin string, deps *TrainingDependencies) {
	sameOrigin := func(c *gin.Context) {
		if origin := c.GetHeader("Origin"); origin != "" && origin != requestOrigin(c.Request, publicOrigin) {
			c.AbortWithStatusJSON(403, gin.H{"code": "ORIGIN_REJECTED", "message": "仅接受同源请求"})
			return
		}
		c.Next()
	}
	train := r.Group("/api/v1/training")
	train.Use(sameOrigin, func(c *gin.Context) {
		secret := bearer(c.GetHeader("Authorization"))
		record, err := deps.Keys.Authenticate(c.Request.Context(), secret)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"code": "UNAUTHORIZED", "message": "训练 API Key 无效、已过期或已吊销"})
			return
		}
		c.Set(trainingKeyContext, record)
		c.Next()
	})
	train.GET("/spec", func(c *gin.Context) { writeProto(c, 200, deps.Service.Spec.Message) })
	train.POST("/reset", func(c *gin.Context) {
		req := new(pb.TrainingResetRequest)
		if !decodeProto(c, req, 256<<10) || !chargeTraining(c, deps, 1) {
			return
		}
		result, err := deps.Service.Reset(req)
		writeTrainingResult(c, result, err)
	})
	train.POST("/step", func(c *gin.Context) {
		req := new(pb.TrainingStepRequest)
		if !decodeProto(c, req, 256<<10) || !chargeTraining(c, deps, 1) {
			return
		}
		result, err := deps.Service.Step(req)
		writeTrainingResult(c, result, err)
	})
	train.POST("/batch/reset", func(c *gin.Context) {
		req := new(pb.TrainingBatchResetRequest)
		if !decodeProto(c, req, 4<<20) || !validBatch(c, len(req.Items)) || !chargeTraining(c, deps, int64(len(req.Items))) {
			return
		}
		response := &pb.TrainingBatchResponse{Results: make([]*pb.TrainingItemResult, len(req.Items))}
		parallelItems(len(req.Items), func(i int) {
			result, err := deps.Service.Reset(req.Items[i])
			response.Results[i] = itemResult(result, err)
		})
		writeProto(c, 200, response)
	})
	train.POST("/batch/step", func(c *gin.Context) {
		req := new(pb.TrainingBatchStepRequest)
		if !decodeProto(c, req, 4<<20) || !validBatch(c, len(req.Items)) || !chargeTraining(c, deps, int64(len(req.Items))) {
			return
		}
		response := &pb.TrainingBatchResponse{Results: make([]*pb.TrainingItemResult, len(req.Items))}
		parallelItems(len(req.Items), func(i int) {
			result, err := deps.Service.Step(req.Items[i])
			response.Results[i] = itemResult(result, err)
		})
		writeProto(c, 200, response)
	})

	if deps.AdminToken == "" {
		return
	}
	r.GET("/admin", func(c *gin.Context) {
		file, err := assets.Open("admin.html")
		if err != nil {
			c.Status(404)
			return
		}
		defer file.Close()
		c.Header("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(c.Writer, file)
	})
	admin := r.Group("/api/v1/admin")
	admin.Use(sameOrigin, func(c *gin.Context) {
		provided := bearer(c.GetHeader("Authorization"))
		if len(provided) != len(deps.AdminToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(deps.AdminToken)) != 1 {
			c.AbortWithStatusJSON(401, gin.H{"code": "UNAUTHORIZED", "message": "管理员令牌无效"})
			return
		}
		c.Next()
	})
	admin.GET("/training-keys", func(c *gin.Context) {
		result, err := deps.Keys.List(c.Request.Context())
		writeTrainingResult(c, result, err)
	})
	admin.POST("/training-keys", func(c *gin.Context) {
		req := new(pb.CreateTrainingKeyRequest)
		if !decodeProto(c, req, 64<<10) {
			return
		}
		result, err := deps.Keys.Create(c.Request.Context(), req)
		writeTrainingResult(c, result, err)
	})
	admin.PATCH("/training-keys/:id", func(c *gin.Context) {
		req := new(pb.UpdateTrainingKeyRequest)
		if !decodeProto(c, req, 64<<10) {
			return
		}
		result, err := deps.Keys.Update(c.Request.Context(), c.Param("id"), req)
		if err == nil && deps.Limiter != nil {
			err = deps.Limiter.Reset(c.Request.Context(), c.Param("id"))
		}
		writeTrainingResult(c, result, err)
	})
	admin.DELETE("/training-keys/:id", func(c *gin.Context) {
		result, err := deps.Keys.Revoke(c.Request.Context(), c.Param("id"))
		if err == nil && deps.Limiter != nil {
			err = deps.Limiter.Reset(c.Request.Context(), c.Param("id"))
		}
		writeTrainingResult(c, result, err)
	})
}

func bearer(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func decodeProto(c *gin.Context, message proto.Message, limit int64) bool {
	if !strings.HasPrefix(c.ContentType(), "application/json") {
		c.JSON(415, gin.H{"code": "INVALID_CONTENT_TYPE", "message": "请使用 application/json"})
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	if err != nil {
		c.JSON(413, gin.H{"code": "BODY_TOO_LARGE", "message": "请求过大"})
		return false
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, message); err != nil {
		c.JSON(400, gin.H{"code": "INVALID_JSON", "message": "请求格式不符合训练协议"})
		return false
	}
	return true
}

func writeProto(c *gin.Context, status int, message proto.Message) {
	b, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(message)
	if err != nil {
		c.Status(500)
		return
	}
	c.Data(status, "application/json; charset=utf-8", b)
}

func writeTrainingResult(c *gin.Context, result proto.Message, err error) {
	if err == nil {
		writeProto(c, 200, result)
		return
	}
	code := application.ErrorCode(err)
	status := 400
	if code == "KEY_UNAVAILABLE" {
		status = 422
	} else if code == "NOT_FOUND" {
		status = 404
	} else if code == "INTERNAL_ERROR" {
		status = 500
	}
	c.JSON(status, gin.H{"code": code, "message": err.Error()})
}

func validBatch(c *gin.Context, size int) bool {
	if size < 1 || size > 256 {
		c.JSON(400, gin.H{"code": "INVALID_INPUT", "message": "批量 items 数量必须为 1–256"})
		return false
	}
	return true
}

func chargeTraining(c *gin.Context, deps *TrainingDependencies, cost int64) bool {
	if deps.LimiterUnavailable || deps.Limiter == nil {
		c.JSON(503, gin.H{"code": "RATE_LIMIT_UNAVAILABLE", "message": "训练限流服务不可用"})
		return false
	}
	value, exists := c.Get(trainingKeyContext)
	if !exists {
		c.JSON(401, gin.H{"code": "UNAUTHORIZED", "message": "训练 API Key 无效"})
		return false
	}
	record := value.(*training.KeyRecord)
	result, err := deps.Limiter.Allow(c.Request.Context(), record.ID, cost, record.BucketCapacity, record.RefillTokensPerSecond)
	if err != nil {
		c.JSON(503, gin.H{"code": "RATE_LIMIT_UNAVAILABLE", "message": "训练限流服务不可用"})
		return false
	}
	c.Header("RateLimit-Limit", strconv.FormatInt(record.BucketCapacity, 10))
	c.Header("RateLimit-Remaining", strconv.FormatInt(int64(math.Max(0, result.Remaining)), 10))
	if !result.Allowed {
		retry := int(math.Ceil(result.RetryAfter.Seconds()))
		if retry < 1 {
			retry = 1
		}
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(429, gin.H{"code": "RATE_LIMITED", "message": "训练 API Key 令牌不足"})
		return false
	}
	return true
}

func itemResult(result *pb.TrainingTransition, err error) *pb.TrainingItemResult {
	if err == nil {
		return &pb.TrainingItemResult{Outcome: &pb.TrainingItemResult_Transition{Transition: result}}
	}
	return &pb.TrainingItemResult{Outcome: &pb.TrainingItemResult_Error{Error: &pb.APIError{Code: application.ErrorCode(err), Message: err.Error()}}}
}

func parallelItems(size int, fn func(int)) {
	workers := min(runtime.GOMAXPROCS(0), size)
	jobs := make(chan int)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				fn(index)
			}
		}()
	}
	for index := 0; index < size; index++ {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
}
