package transport

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"vorax/internal/ai"
)

func registerVisibleAI(api *gin.RouterGroup) {
	api.GET("/ai/catalog", func(c *gin.Context) { c.JSON(200, ai.PublicCatalog()) })
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
