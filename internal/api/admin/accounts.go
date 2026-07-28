package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"deepseek-web-api/internal/core"
	"deepseek-web-api/internal/model"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ListAccounts GET /admin/accounts
func (h *Handler) ListAccounts(c *gin.Context) {
	accs, err := h.Repo.ListAccounts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 附加运行时状态（来自池）
	sessions := h.Pool.Sessions()
	statusMap := map[uint]gin.H{}
	for _, s := range sessions {
		statusMap[s.AccountID] = gin.H{
			"in_pool": true,
			"healthy": s.Healthy(),
		}
	}
	type out struct {
		model.Account
		InPool  bool `json:"in_pool"`
		Healthy bool `json:"healthy"`
	}
	result := make([]out, 0, len(accs))
	for _, a := range accs {
		o := out{Account: a}
		if st, ok := statusMap[a.ID]; ok {
			o.InPool = true
			o.Healthy = st["healthy"].(bool)
		}
		result = append(result, o)
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "available": h.Pool.Available()})
}

// CreateAccount POST /admin/accounts
func (h *Handler) CreateAccount(c *gin.Context) {
	var a model.Account
	if err := c.ShouldBindJSON(&a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if a.Status == "" {
		a.Status = "active"
	}
	if err := h.Repo.CreateAccount(c.Request.Context(), &a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reloadErr := h.syncAccountSession(&a)
	c.JSON(http.StatusOK, gin.H{
		"data":          a,
		"pool_reloaded": reloadErr == nil && a.Status == "active" && a.StoragePath != "",
		"warning":       errStr(reloadErr),
	})
}

// UpdateAccount PATCH /admin/accounts/:id
func (h *Handler) UpdateAccount(c *gin.Context) {
	id, parsed := parseID(c)
	if !parsed {
		return
	}
	a, err := h.Repo.GetAccount(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	oldName, oldStoragePath, oldStatus := a.Name, a.StoragePath, a.Status
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if v, ok := body["name"].(string); ok {
		a.Name = v
	}
	if v, ok := body["storage_path"].(string); ok {
		a.StoragePath = v
	}
	if v, ok := body["status"].(string); ok {
		a.Status = v
	}
	if v, ok := body["note"].(string); ok {
		a.Note = v
	}
	if v, ok := body["default_model"].(string); ok {
		a.DefaultModel = v
	}
	if err := h.Repo.UpdateAccount(c.Request.Context(), a); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var reloadErr error
	if oldName != a.Name || oldStoragePath != a.StoragePath || oldStatus != a.Status {
		reloadErr = h.syncAccountSession(a)
	}
	c.JSON(http.StatusOK, gin.H{
		"data":          a,
		"pool_reloaded": reloadErr == nil && a.Status == "active" && a.StoragePath != "",
		"warning":       errStr(reloadErr),
	})
}

// DeleteAccount DELETE /admin/accounts/:id
func (h *Handler) DeleteAccount(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.Repo.DeleteAccount(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.Pool.RemoveAccount(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// UploadStorageState POST /admin/accounts/:id/storage-state
// multipart form: file=xxx.json
func (h *Handler) UploadStorageState(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	a, err := h.Repo.GetAccount(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	const maxStorageStateSize = 2 << 20
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxStorageStateSize)
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}
	if file.Size <= 0 || file.Size > maxStorageStateSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "storage state must be a non-empty JSON file no larger than 2 MiB"})
		return
	}
	// 校验是合法 JSON
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer f.Close()
	var tmp any
	dec := json.NewDecoder(f)
	if err := dec.Decode(&tmp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
		return
	}

	if err := os.MkdirAll(h.StorageDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	dst := filepath.Join(h.StorageDir, fmt.Sprintf("account_%d.json", id))
	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "secure storage state permissions: " + err.Error()})
		return
	}
	a.StoragePath = dst
	// A refreshed login state makes an expired account usable again. Keep an
	// explicitly disabled account disabled until an administrator enables it.
	if a.Status == "expired" {
		a.Status = "active"
	}
	if err := h.Repo.UpdateAccount(c.Request.Context(), a); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	reloadErr := h.syncAccountSession(a)
	c.JSON(http.StatusOK, gin.H{
		"ok":            reloadErr == nil,
		"storage_path":  dst,
		"pool_reloaded": reloadErr == nil && a.Status == "active",
		"warning":       errStr(reloadErr),
	})
}

// HealthCheckAccount POST /admin/accounts/:id/health-check
// 用账号的 storage_state 临时打开一个页面探测登录态。
func (h *Handler) HealthCheckAccount(c *gin.Context) {
	id, parsed := parseID(c)
	if !parsed {
		return
	}
	a, err := h.Repo.GetAccount(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	ok, err := h.checkAccount(ctx, a)
	_ = h.Repo.TouchAccountChecked(c.Request.Context(), a.ID)
	// 仅当探测确实跑起来（err==nil 或探测明确返回未登录 ok=false）时才更新状态；
	// 浏览器池未启动/文件缺失等基础设施错误不改状态，避免误标 expired。
	if err == nil {
		if ok {
			if a.Status != "disabled" {
				_ = h.Repo.MarkAccountStatus(c.Request.Context(), a.ID, "active")
				a.Status = "active"
			}
			if syncErr := h.syncAccountSession(a); syncErr != nil {
				h.Logger.Warn("hot load healthy account", zap.Uint("id", a.ID), zap.Error(syncErr))
			}
		} else {
			_ = h.Repo.MarkAccountStatus(c.Request.Context(), a.ID, "expired")
			h.Pool.RemoveAccount(a.ID)
		}
	}
	c.JSON(http.StatusOK, gin.H{"healthy": ok, "error": errStr(err)})
}

// checkAccount 用临时 BrowserContext 探测登录态
func (h *Handler) checkAccount(ctx context.Context, a *model.Account) (bool, error) {
	// 复用池里的会话（如果存在）以避免开新浏览器
	for _, s := range h.Pool.Sessions() {
		if s.AccountID == a.ID {
			drv := core.NewDeepSeekDriver(s, h.Selectors, h.Logger)
			return drv.HealthCheck(ctx)
		}
	}
	// 池里没有：用池的浏览器新建临时 context
	if _, err := os.Stat(a.StoragePath); err != nil {
		return false, err
	}
	// 简化：交给池的浏览器临时开 context（通过公开方法）
	ok, err := h.Pool.CheckStorageState(ctx, a.StoragePath, h.Selectors)
	if err != nil {
		h.Logger.Warn("health check failed", zap.Uint("id", a.ID), zap.Error(err))
	}
	return ok, err
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (h *Handler) syncAccountSession(account *model.Account) error {
	if account.Status != "active" || account.StoragePath == "" {
		h.Pool.RemoveAccount(account.ID)
		return nil
	}
	return h.Pool.UpsertAccount(core.AccountConfig{
		ID:          account.ID,
		Name:        account.Name,
		StoragePath: account.StoragePath,
	})
}
