package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"deepseek-web-api/internal/auth"
	"deepseek-web-api/internal/model"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeAdminStore struct {
	admin model.Admin
}

func (store *fakeAdminStore) GetAdmin(context.Context, uint) (*model.Admin, error) {
	admin := store.admin
	return &admin, nil
}

type fakeAuditStore struct {
	mu    sync.Mutex
	entry *model.AuditLog
}

func (store *fakeAuditStore) CreateAuditLog(_ context.Context, entry *model.AuditLog) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	copy := *entry
	store.entry = &copy
	return nil
}

func TestAdminAuthRejectsRevokedToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "01234567890123456789012345678901"
	token, err := auth.SignJWT(secret, 1, "admin", "superadmin", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAdminStore{admin: model.Admin{
		ID:           1,
		Username:     "admin",
		Role:         "superadmin",
		Enabled:      true,
		TokenVersion: 2,
	}}
	router := gin.New()
	router.Use(AdminAuth(secret, store, zap.NewNop()))
	router.GET("/admin/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestRequireRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("admin_role", "viewer")
		c.Next()
	})
	router.POST("/admin/test", RequireRoles("admin", "superadmin"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/test", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestAuditAdminActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeAuditStore{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("admin_id", uint(7))
		c.Set("admin_name", "operator")
		c.Next()
	})
	router.Use(AuditAdminActions(store, zap.NewNop()))
	router.PATCH("/admin/accounts/:id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/admin/accounts/42", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	router.ServeHTTP(recorder, request)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.entry == nil {
		t.Fatal("audit entry was not written")
	}
	if store.entry.AdminID != 7 || store.entry.Resource != "accounts" ||
		store.entry.ResourceID != "42" || store.entry.Status != http.StatusNoContent {
		t.Fatalf("unexpected audit entry: %#v", store.entry)
	}
}

func TestAuditAdminExport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeAuditStore{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("admin_id", uint(7))
		c.Set("admin_name", "operator")
		c.Next()
	})
	router.Use(AuditAdminActions(store, zap.NewNop()))
	router.GET("/admin/audit-logs/export", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/audit-logs/export", nil))

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.entry == nil || store.entry.Action != "export" {
		t.Fatalf("unexpected export audit entry: %#v", store.entry)
	}
}
