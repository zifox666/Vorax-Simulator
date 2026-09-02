package transport

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"vorax/internal/ai"
	pb "vorax/internal/protocol"
	"vorax/internal/training"
)

func registerVisibleAI(api *gin.RouterGroup, spec *training.Spec) {
	api.GET("/ai/catalog", func(c *gin.Context) { c.JSON(200, ai.PublicCatalog()) })
	api.GET("/ai/model/spec", func(c *gin.Context) {
		writeProtoJSON(c, spec.Message)
	})
	api.POST("/ai/model/input", func(c *gin.Context) {
		visible, ok := decodeVisible(c, "本地模型输入")
		if !ok {
			return
		}
		o, err := ai.FromVisible(visible)
		if err != nil {
			c.JSON(400, gin.H{"message": err.Error()})
			return
		}
		semantic, tensor, legal, mask := spec.Encode(o)
		writeProtoJSON(c, &pb.TrainingTransition{
			Observation: semantic, TensorObservation: tensor, LegalActions: legal,
			ActionMask: mask, Terminated: o.Done(), Truncated: false,
			Info: &pb.TrainingInfo{
				Score: o.Score, RulesVersion: spec.Message.RulesVersion,
				ContentVersion: spec.Message.ContentVersion,
				RngVersion:     spec.Message.RngVersion, SpecVersion: spec.Message.SpecVersion,
			},
		})
	})
	api.POST("/ai/visible", func(c *gin.Context) {
		var req struct {
			Visible  *ai.VisibleInput `json:"visible"`
			Strategy ai.Strategy      `json:"strategy"`
			Rollouts int              `json:"rollouts"`
			Samples  int              `json:"samples"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			c.JSON(400, gin.H{"message": "可见数据请求格式无效：" + err.Error()})
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			c.JSON(400, gin.H{"message": "请求只能包含一个 JSON 对象"})
			return
		}
		o, err := ai.FromVisible(req.Visible)
		if err != nil {
			c.JSON(400, gin.H{"message": err.Error()})
			return
		}
		if o.Done() {
			c.JSON(200, gin.H{"action": nil, "observation": o})
			return
		}
		if req.Strategy == "" {
			req.Strategy = ai.StrategySampler
		}
		a, err := ai.Decide(o, req.Strategy, ai.Params{Samples: req.Samples, Rollouts: req.Rollouts})
		if err != nil {
			c.JSON(400, gin.H{"message": err.Error()})
			return
		}
		c.JSON(200, gin.H{"action": a, "observation": o})
	})
}

func decodeVisible(c *gin.Context, label string) (*ai.VisibleInput, bool) {
	var req struct {
		Visible *ai.VisibleInput `json:"visible"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		c.JSON(400, gin.H{"message": label + "请求格式无效：" + err.Error()})
		return nil, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		c.JSON(400, gin.H{"message": "请求只能包含一个 JSON 对象"})
		return nil, false
	}
	return req.Visible, true
}

func writeProtoJSON(c *gin.Context, message proto.Message) {
	data, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(message)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", data)
}
