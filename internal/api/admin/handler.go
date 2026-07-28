package admin

import (
	"net/http"
	"strconv"

	"deepseek-web-api/internal/auth"
	"deepseek-web-api/internal/core"
	"deepseek-web-api/internal/maintenance"
	"deepseek-web-api/internal/middleware"
	"deepseek-web-api/internal/repository"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func parseID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return uint(id), true
}

// Handler 管理后台
type Handler struct {
	Repo               *repository.Repository
	Pool               *core.BrowserPool
	JWTSecret          string
	StorageDir         string
	Selectors          core.Selectors
	Logger             *zap.Logger
	LoginLimiter       core.Limiter
	AuditArchiver      *maintenance.AuditArchiver
	AuditExportMaxRows int
}

// Register 注册管理后台路由
func Register(rg *gin.RouterGroup, h *Handler) {
	// 登录不需要鉴权
	rg.POST("/login", h.Login)

	// 以下需要鉴权
	authed := rg.Group("")
	authed.Use(
		middleware.AdminAuth(h.JWTSecret, h.Repo, h.Logger),
		middleware.AuditAdminActions(h.Repo, h.Logger),
	)
	{
		authed.GET("/dashboard", h.Dashboard)
		authed.GET("/metrics", h.RuntimeMetrics)

		authed.GET("/conversations", h.ListConversations)
		authed.GET("/conversations/:id", h.GetConversation)

		authed.GET("/accounts", h.ListAccounts)
		authed.GET("/api-keys", h.ListAPIKeys)
		authed.GET("/api-keys/:id/usage", h.APIKeyUsage)
		authed.POST("/change-password", h.ChangePassword)

		operators := authed.Group("", middleware.RequireRoles("admin", "superadmin"))
		operators.POST("/accounts", h.CreateAccount)
		operators.PATCH("/accounts/:id", h.UpdateAccount)
		operators.DELETE("/accounts/:id", h.DeleteAccount)
		operators.POST("/accounts/:id/storage-state", h.UploadStorageState)
		operators.POST("/accounts/:id/health-check", h.HealthCheckAccount)
		operators.POST("/api-keys", h.CreateAPIKey)
		operators.DELETE("/api-keys/:id", h.DeleteAPIKey)
		operators.PATCH("/api-keys/:id", h.UpdateAPIKey)

		superadmins := authed.Group("", middleware.RequireRoles("superadmin"))
		superadmins.GET("/admins", h.ListAdmins)
		superadmins.POST("/admins", h.CreateAdmin)
		superadmins.PATCH("/admins/:id", h.UpdateAdmin)
		superadmins.GET("/audit-logs", h.ListAuditLogs)
		superadmins.GET("/audit-logs/export", h.ExportAuditLogs)
		superadmins.POST("/audit-logs/archive", h.ArchiveAuditLogs)
	}
}

// Login POST /admin/login
func (h *Handler) Login(c *gin.Context) {
	if h.LoginLimiter != nil {
		allowed, _, err := h.LoginLimiter.Allow(c.Request.Context(), "admin-login:"+c.ClientIP())
		if err != nil {
			h.Logger.Warn("admin login rate limiter", zap.Error(err))
		} else if !allowed {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts"})
			return
		}
	}
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a, err := h.Repo.GetAdminByUsername(c.Request.Context(), req.Username)
	if err != nil || !a.Enabled {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	token, err := auth.SignJWT(h.JWTSecret, a.ID, a.Username, a.Role, a.TokenVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sign token failed"})
		return
	}
	_ = h.Repo.TouchAdminLogin(c.Request.Context(), a.ID)
	c.JSON(http.StatusOK, gin.H{
		"token":    token,
		"username": a.Username,
		"role":     a.Role,
	})
}
