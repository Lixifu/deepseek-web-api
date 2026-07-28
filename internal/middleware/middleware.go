package middleware

import (
	"context"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"deepseek-web-api/internal/auth"
	"deepseek-web-api/internal/model"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// APIKeyStore API Key 查询接口（由 repository 实现）
type APIKeyStore interface {
	FindAPIKeysByPrefix(ctx context.Context, prefix string) ([]model.APIKey, error)
}

type AdminStore interface {
	GetAdmin(ctx context.Context, id uint) (*model.Admin, error)
}

type AuditStore interface {
	CreateAuditLog(ctx context.Context, entry *model.AuditLog) error
}

// APIKeyAuth 校验 Authorization: Bearer <key>
func APIKeyAuth(store APIKeyStore, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			return
		}
		key := strings.TrimPrefix(auth, "Bearer ")
		// key 格式: sk-dsk-<32 hex>，前缀取 hex 部分前 8 位（与 api_keys.key_prefix 对齐）
		stripped := strings.TrimPrefix(key, "sk-dsk-")
		if len(stripped) < 8 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
		prefix := stripped[:8]
		candidates, err := store.FindAPIKeysByPrefix(c.Request.Context(), prefix)
		if err != nil {
			logger.Error("api key lookup failed", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
		for _, k := range candidates {
			if bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(key)) == nil {
				if !k.Enabled {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "key disabled"})
					return
				}
				c.Set("api_key_id", k.ID)
				c.Set("api_key_name", k.Name)
				c.Set("api_key_default_model", k.DefaultModel)
				c.Set("api_key_quota_per_day", k.QuotaPerDay)
				c.Set("api_key_allowed_models", k.AllowedModels)
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
	}
}

// Logger 请求日志
func Logger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Next()
		logger.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}

// Recovery panic 恢复
func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered",
					zap.Any("error", r),
					zap.String("stack", string(debug.Stack())))
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			}
		}()
		c.Next()
	}
}

// AdminAuth JWT 鉴权（管理后台）
func AdminAuth(jwtSecret string, store AdminStore, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("X-Admin-Token")
		if token == "" {
			auth := c.GetHeader("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				token = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		claims, err := auth.ParseJWT(token, jwtSecret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		admin, err := store.GetAdmin(c.Request.Context(), claims.AdminID)
		if err != nil || !admin.Enabled || admin.TokenVersion != claims.TokenVersion {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token revoked"})
			return
		}
		c.Set("admin_id", admin.ID)
		c.Set("admin_name", admin.Username)
		c.Set("admin_role", admin.Role)
		c.Next()
	}
}

// RequireRoles 限制管理路由可访问的角色。
func RequireRoles(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(c *gin.Context) {
		role := c.GetString("admin_role")
		if _, ok := allowed[role]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}
		c.Next()
	}
}

// AuditAdminActions 记录所有改变状态的管理请求，不保存请求体或凭据。
func AuditAdminActions(store AuditStore, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		action := strings.ToLower(c.Request.Method)
		switch c.Request.Method {
		case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		case http.MethodGet:
			if !strings.HasSuffix(c.Request.URL.Path, "/audit-logs/export") {
				return
			}
			action = "export"
		default:
			return
		}
		adminID := c.GetUint("admin_id")
		if adminID == 0 {
			return
		}
		resource, resourceID := auditResource(c.Request.URL.Path)
		entry := &model.AuditLog{
			AdminID:    adminID,
			AdminName:  c.GetString("admin_name"),
			Action:     action,
			Resource:   resource,
			ResourceID: resourceID,
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			ClientIP:   c.ClientIP(),
			Status:     c.Writer.Status(),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := store.CreateAuditLog(ctx, entry); err != nil {
			logger.Warn("write admin audit log", zap.Error(err))
		}
	}
}

func auditResource(path string) (string, string) {
	trimmed := strings.Trim(strings.TrimPrefix(path, "/admin"), "/")
	if trimmed == "" {
		return "admin", ""
	}
	parts := strings.Split(trimmed, "/")
	resource := parts[0]
	resourceID := ""
	if len(parts) > 1 {
		resourceID = parts[1]
	}
	if resource == "change-password" {
		return "admins", "self"
	}
	return resource, resourceID
}
