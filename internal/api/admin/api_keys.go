package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"deepseek-web-api/internal/core"
	"deepseek-web-api/internal/model"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// ListAPIKeys GET /admin/api-keys
// 返回的每一项附带今日用量（today_used / success_cnt / failed_cnt / remaining）。
// remaining = -1 表示不限配额。
func (h *Handler) ListAPIKeys(c *gin.Context) {
	keys, err := h.Repo.ListAPIKeysWithUsage(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": keys})
}

// CreateAPIKey POST /admin/api-keys
// body: {name, quota_per_day, allowed_models: ["deepseek-chat"], default_model: "deepseek-chat"}
// 返回明文 key（仅此一次）
func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req struct {
		Name          string   `json:"name" binding:"required"`
		QuotaPerDay   int      `json:"quota_per_day"`
		AllowedModels []string `json:"allowed_models"`
		DefaultModel  string   `json:"default_model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.QuotaPerDay < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "quota_per_day must be >= 0"})
		return
	}

	// 生成 key: sk-dsk-<32 hex>
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		h.Logger.Error("generate api key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "generate api key failed"})
		return
	}
	randHex := hex.EncodeToString(buf)
	plainKey := "sk-dsk-" + randHex
	prefix := randHex[:8]

	hash, err := bcrypt.GenerateFromPassword([]byte(plainKey), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	models := req.AllowedModels
	if len(models) == 0 {
		models = []string{"deepseek-chat", "deepseek-reasoner"}
	}
	allowed := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		if !core.IsSupportedModel(modelName) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported allowed model: " + modelName})
			return
		}
		allowed[modelName] = struct{}{}
	}
	if req.DefaultModel != "" {
		if !core.IsSupportedModel(req.DefaultModel) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported default model: " + req.DefaultModel})
			return
		}
		if _, ok := allowed[req.DefaultModel]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "default_model must be included in allowed_models"})
			return
		}
	}
	modelsJSON, _ := json.Marshal(models)

	k := &model.APIKey{
		Name:          req.Name,
		KeyPrefix:     prefix,
		KeyHash:       string(hash),
		QuotaPerDay:   req.QuotaPerDay,
		AllowedModels: string(modelsJSON),
		DefaultModel:  req.DefaultModel,
		Enabled:       true,
	}
	if k.QuotaPerDay == 0 {
		k.QuotaPerDay = 1000
	}
	if err := h.Repo.CreateAPIKey(c.Request.Context(), k); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":            k.ID,
		"name":          k.Name,
		"key_prefix":    k.KeyPrefix,
		"key":           plainKey, // 明文仅此一次返回
		"default_model": k.DefaultModel,
		"created_at":    k.CreatedAt,
	})
}

// DeleteAPIKey DELETE /admin/api-keys/:id
func (h *Handler) DeleteAPIKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.Repo.DeleteAPIKey(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// UpdateAPIKey PATCH /admin/api-keys/:id
// body: {enabled?: bool, default_model?: string, quota_per_day?: int}
// 支持启停、修改绑定模型、修改日配额（0 表示不限）
func (h *Handler) UpdateAPIKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req struct {
		Enabled      *bool   `json:"enabled"`
		DefaultModel *string `json:"default_model"`
		QuotaPerDay  *int    `json:"quota_per_day"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Enabled != nil {
		if err := h.Repo.SetAPIKeyEnabled(c.Request.Context(), id, *req.Enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if req.DefaultModel != nil {
		if *req.DefaultModel != "" && !core.IsSupportedModel(*req.DefaultModel) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported default model: " + *req.DefaultModel})
			return
		}
		if *req.DefaultModel != "" {
			key, err := h.Repo.GetAPIKey(c.Request.Context(), id)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
				return
			}
			var allowed []string
			if err := json.Unmarshal([]byte(key.AllowedModels), &allowed); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "api key model policy is invalid"})
				return
			}
			permitted := false
			for _, modelName := range allowed {
				if modelName == *req.DefaultModel {
					permitted = true
					break
				}
			}
			if !permitted {
				c.JSON(http.StatusBadRequest, gin.H{"error": "default_model must be included in allowed_models"})
				return
			}
		}
		if err := h.Repo.UpdateAPIKeyDefaultModel(c.Request.Context(), id, *req.DefaultModel); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if req.QuotaPerDay != nil {
		if *req.QuotaPerDay < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "quota_per_day must be >= 0"})
			return
		}
		if err := h.Repo.UpdateAPIKeyQuota(c.Request.Context(), id, *req.QuotaPerDay); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// APIKeyUsage GET /admin/api-keys/:id/usage?days=N
// 返回某 API key 最近 N 天（默认 7）按小时聚合的用量序列。
func (h *Handler) APIKeyUsage(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	days := 7
	if d, err := strconv.Atoi(c.Query("days")); err == nil && d > 0 && d <= 90 {
		days = d
	}
	to := time.Now().Truncate(time.Hour).Add(time.Hour)
	from := to.AddDate(0, 0, -days)
	points, err := h.Repo.APIKeyUsageRange(c.Request.Context(), id, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 同时返回今日汇总与配额，便于前端展示
	succ, fail, _ := h.Repo.TodayUsage(c.Request.Context(), id)
	k, err := h.Repo.GetAPIKey(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	remaining := int64(-1)
	if k.QuotaPerDay > 0 {
		remaining = int64(k.QuotaPerDay) - succ - fail
		if remaining < 0 {
			remaining = 0
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"points":     points,
		"today_used": succ + fail,
		"success":    succ,
		"failed":     fail,
		"quota":      k.QuotaPerDay,
		"remaining":  remaining,
	})
}
