package v1

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"deepseek-web-api/internal/core"
	"deepseek-web-api/internal/observability"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler v1 OpenAI 兼容接口
type Handler struct {
	Orch *core.Orchestrator
}

type chatCompletionReq struct {
	Model      string         `json:"model"` // 可选：API key 绑定模型时可不传
	Messages   []core.Message `json:"messages" binding:"required,min=1"`
	Stream     bool           `json:"stream"`
	Tools      []core.Tool    `json:"tools,omitempty"`
	ToolChoice any            `json:"tool_choice,omitempty"`
	Functions  []core.Tool    `json:"functions,omitempty"` // 旧版 OpenAI 协议兼容
}

// Completions POST /v1/chat/completions
func (h *Handler) Completions(c *gin.Context) {
	startedAt := time.Now()
	var req chatCompletionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	apiKeyID := uint(c.GetUint("api_key_id"))
	// API key 绑定模型：非空时强制使用绑定的模型，忽略请求的 model
	apiKeyDefaultModel, _ := c.Get("api_key_default_model")
	model := req.Model
	if dm, ok := apiKeyDefaultModel.(string); ok && dm != "" {
		model = dm
	}
	if model == "" {
		model = "deepseek-chat"
	}
	if !core.IsSupportedModel(model) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported model: " + model})
		return
	}
	if raw, ok := c.Get("api_key_allowed_models"); ok {
		if allowedJSON, ok := raw.(string); ok && allowedJSON != "" {
			var allowed []string
			if err := json.Unmarshal([]byte(allowedJSON), &allowed); err != nil {
				h.Orch.Logger.Error("invalid allowed_models on api key", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "api key model policy is invalid"})
				return
			}
			permitted := false
			for _, candidate := range allowed {
				if candidate == model {
					permitted = true
					break
				}
			}
			if !permitted {
				c.JSON(http.StatusForbidden, gin.H{"error": "model is not allowed for this api key"})
				return
			}
		}
	}
	// 日配额（0 表示不限）
	quotaPerDay := 0
	if q, ok := c.Get("api_key_quota_per_day"); ok {
		if v, ok := q.(int); ok {
			quotaPerDay = v
		}
	}

	// 旧版 functions 字段兼容：合并到 tools
	tools := req.Tools
	if len(tools) == 0 && len(req.Functions) > 0 {
		tools = make([]core.Tool, 0, len(req.Functions))
		for _, f := range req.Functions {
			tools = append(tools, core.Tool{Type: "function", Function: f.Function})
		}
	}

	result, streamCh, err := h.Orch.Chat(c.Request.Context(), core.ChatRequest{
		Messages:    req.Messages,
		Model:       model,
		Stream:      req.Stream,
		APIKeyID:    apiKeyID,
		QuotaPerDay: quotaPerDay,
		Tools:       tools,
		ToolChoice:  req.ToolChoice,
	})
	if err != nil {
		observability.RecordCall(model, time.Since(startedAt), false)
		status := http.StatusServiceUnavailable
		message := err.Error()
		switch {
		case err.Error() == "rate limit exceeded":
			status = http.StatusTooManyRequests
		case errors.Is(err, core.ErrQuotaExceeded):
			status = http.StatusTooManyRequests
		case errors.Is(err, core.ErrQueueFull):
			status = http.StatusTooManyRequests
			c.Header("Retry-After", "5")
		case errors.Is(err, core.ErrQueueTimeout):
			status = http.StatusServiceUnavailable
		case errors.Is(err, core.ErrSharedQueueUnavailable):
			message = core.ErrSharedQueueUnavailable.Error()
		}
		c.JSON(status, gin.H{"error": message})
		return
	}

	if req.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no") // 关闭 Nginx 缓冲
		completed := false
		c.Stream(func(w io.Writer) bool {
			select {
			case line, ok := <-streamCh:
				if !ok {
					completed = true
					return false
				}
				_, _ = w.Write([]byte(line))
				// 主动 flush
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				return true
			case <-c.Request.Context().Done():
				return false
			}
		})
		observability.RecordCall(model, time.Since(startedAt), completed)
		return
	}
	observability.RecordCall(model, time.Since(startedAt), true)
	c.JSON(http.StatusOK, result)
}

// Models GET /v1/models
func (h *Handler) Models(c *gin.Context) {
	models := core.SupportedModels()
	data := make([]gin.H, 0, len(models))
	for _, m := range models {
		data = append(data, gin.H{
			"id":       m,
			"object":   "model",
			"owned_by": "deepseek",
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

// Register 注册路由
func Register(rg *gin.RouterGroup, h *Handler) {
	rg.POST("/chat/completions", h.Completions)
	rg.GET("/models", h.Models)
}
