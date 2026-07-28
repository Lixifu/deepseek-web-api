package admin

import (
	"net/http"
	"strconv"
	"time"

	"deepseek-web-api/internal/repository"
	"github.com/gin-gonic/gin"
)

// ListConversations GET /admin/conversations?page=&size=&api_key_id=&account_id=&from=&to=
func (h *Handler) ListConversations(c *gin.Context) {
	q := repository.ConvQuery{Page: atoi(c.Query("page")), Size: atoi(c.Query("size"))}
	if v := c.Query("api_key_id"); v != "" {
		q.APIKeyID = uint(atoi(v))
	}
	if v := c.Query("account_id"); v != "" {
		q.AccountID = uint(atoi(v))
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.From = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.To = t
		}
	}
	items, total, err := h.Repo.ListConversations(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":  items,
		"total": total,
		"page":  q.Page,
		"size":  q.Size,
	})
}

// GetConversation GET /admin/conversations/:id
func (h *Handler) GetConversation(c *gin.Context) {
	conv, err := h.Repo.GetConversation(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, conv)
}

// Dashboard GET /admin/dashboard
func (h *Handler) Dashboard(c *gin.Context) {
	stat, err := h.Repo.Dashboard(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"stats":     stat,
		"available": h.Pool.Available(),
	})
}

func atoi(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
