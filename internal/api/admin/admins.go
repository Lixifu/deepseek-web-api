package admin

import (
	"net/http"
	"regexp"
	"strings"

	"deepseek-web-api/internal/model"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)

func validRole(role string) bool {
	switch role {
	case "viewer", "admin", "superadmin":
		return true
	default:
		return false
	}
}

func validatePassword(password string) string {
	if len(password) < 12 {
		return "password must be at least 12 characters"
	}
	if len(password) > 128 {
		return "password must not exceed 128 characters"
	}
	return ""
}

// ChangePassword POST /admin/change-password
func (h *Handler) ChangePassword(c *gin.Context) {
	var request struct {
		CurrentPassword string `json:"current_password" binding:"required"`
		NewPassword     string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if message := validatePassword(request.NewPassword); message != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
		return
	}
	admin, err := h.Repo.GetAdmin(c.Request.Context(), c.GetUint("admin_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "admin not found"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(request.CurrentPassword)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(request.NewPassword)) == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new password must differ from current password"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash password failed"})
		return
	}
	admin.PasswordHash = string(hash)
	admin.TokenVersion++
	if admin.TokenVersion == 0 {
		admin.TokenVersion = 1
	}
	if err := h.Repo.UpdateAdmin(c.Request.Context(), admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "reauth_required": true})
}

// ListAdmins GET /admin/admins
func (h *Handler) ListAdmins(c *gin.Context) {
	admins, err := h.Repo.ListAdmins(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": admins})
}

// CreateAdmin POST /admin/admins
func (h *Handler) CreateAdmin(c *gin.Context) {
	var request struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Role     string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	if !adminUsernamePattern.MatchString(request.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username must be 3-64 letters, digits, dot, underscore or hyphen"})
		return
	}
	if !validRole(request.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
		return
	}
	if message := validatePassword(request.Password); message != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash password failed"})
		return
	}
	admin := &model.Admin{
		Username:     request.Username,
		PasswordHash: string(hash),
		Role:         request.Role,
		Enabled:      true,
		TokenVersion: 1,
	}
	if err := h.Repo.CreateAdmin(c.Request.Context(), admin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, admin)
}

// UpdateAdmin PATCH /admin/admins/:id
func (h *Handler) UpdateAdmin(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var request struct {
		Role     *string `json:"role"`
		Enabled  *bool   `json:"enabled"`
		Password *string `json:"password"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if request.Role != nil && !validRole(*request.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
		return
	}
	admin, err := h.Repo.GetAdmin(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "admin not found"})
		return
	}
	if id == c.GetUint("admin_id") {
		if request.Enabled != nil && !*request.Enabled {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot disable the current administrator"})
			return
		}
		if request.Role != nil && *request.Role != "superadmin" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot demote the current super administrator"})
			return
		}
	}

	removesSuperadmin := admin.Role == "superadmin" && admin.Enabled &&
		((request.Role != nil && *request.Role != "superadmin") ||
			(request.Enabled != nil && !*request.Enabled))
	if removesSuperadmin {
		count, err := h.Repo.CountEnabledSuperadmins(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if count <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "at least one enabled super administrator is required"})
			return
		}
	}

	securityChanged := false
	if request.Role != nil {
		if admin.Role != *request.Role {
			admin.Role = *request.Role
			securityChanged = true
		}
	}
	if request.Enabled != nil && admin.Enabled != *request.Enabled {
		admin.Enabled = *request.Enabled
		securityChanged = true
	}
	if request.Password != nil {
		if message := validatePassword(*request.Password); message != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": message})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*request.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "hash password failed"})
			return
		}
		admin.PasswordHash = string(hash)
		securityChanged = true
	}
	if securityChanged {
		admin.TokenVersion++
		if admin.TokenVersion == 0 {
			admin.TokenVersion = 1
		}
	}
	if err := h.Repo.UpdateAdmin(c.Request.Context(), admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, admin)
}
