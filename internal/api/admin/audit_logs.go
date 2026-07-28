package admin

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"deepseek-web-api/internal/repository"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func auditRequestQuery(c *gin.Context) (repository.AuditQuery, string, bool) {
	query := repository.AuditQuery{
		AdminID:  uint(atoi(c.Query("admin_id"))),
		Action:   strings.ToLower(strings.TrimSpace(c.Query("action"))),
		Resource: strings.TrimSpace(c.Query("resource")),
		Page:     atoi(c.Query("page")),
		Size:     atoi(c.Query("size")),
	}
	if query.Page < 1 {
		query.Page = 1
	}
	if query.Size < 1 || query.Size > 200 {
		query.Size = 50
	}
	for _, item := range []struct {
		value       string
		destination *time.Time
	}{
		{value: c.Query("from"), destination: &query.From},
		{value: c.Query("to"), destination: &query.To},
	} {
		value := item.value
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "from and to must use RFC3339"})
			return query, "", false
		}
		*item.destination = parsed
	}
	scope := strings.ToLower(strings.TrimSpace(c.DefaultQuery("scope", "all")))
	if scope != "all" && scope != "active" && scope != "archive" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be all, active or archive"})
		return query, "", false
	}
	return query, scope, true
}

// ListAuditLogs GET /admin/audit-logs
func (h *Handler) ListAuditLogs(c *gin.Context) {
	query, scope, ok := auditRequestQuery(c)
	if !ok {
		return
	}
	offset := (query.Page - 1) * query.Size
	entries, total, err := h.Repo.QueryAuditRecords(
		c.Request.Context(),
		query,
		scope,
		offset,
		query.Size,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":  entries,
		"total": total,
		"page":  query.Page,
		"size":  query.Size,
		"scope": scope,
	})
}

// ExportAuditLogs GET /admin/audit-logs/export?format=csv|json
func (h *Handler) ExportAuditLogs(c *gin.Context) {
	query, scope, ok := auditRequestQuery(c)
	if !ok {
		return
	}
	limit := h.AuditExportMaxRows
	if limit < 1 {
		limit = 10000
	}
	entries, total, err := h.Repo.QueryAuditRecords(c.Request.Context(), query, scope, 0, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if total > int64(limit) {
		c.Header("X-Export-Truncated", "true")
	}
	c.Header("X-Export-Total", strconv.FormatInt(total, 10))

	format := strings.ToLower(c.DefaultQuery("format", "csv"))
	if format == "json" {
		c.Header("Content-Disposition", `attachment; filename="audit-logs-`+time.Now().Format("20060102-150405")+`.json"`)
		c.JSON(http.StatusOK, gin.H{
			"data":      entries,
			"total":     total,
			"exported":  len(entries),
			"truncated": total > int64(len(entries)),
			"scope":     scope,
		})
		return
	}
	if format != "csv" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be csv or json"})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="audit-logs-`+time.Now().Format("20060102-150405")+`.csv"`)
	c.Status(http.StatusOK)
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{
		"id", "created_at", "admin_id", "admin_name", "action", "resource",
		"resource_id", "method", "path", "client_ip", "status", "archived", "archived_at",
	})
	for _, entry := range entries {
		archivedAt := ""
		if entry.ArchivedAt != nil {
			archivedAt = entry.ArchivedAt.Format(time.RFC3339Nano)
		}
		if err := writer.Write([]string{
			strconv.FormatUint(uint64(entry.ID), 10),
			entry.CreatedAt.Format(time.RFC3339Nano),
			strconv.FormatUint(uint64(entry.AdminID), 10),
			entry.AdminName,
			entry.Action,
			entry.Resource,
			entry.ResourceID,
			entry.Method,
			entry.Path,
			entry.ClientIP,
			strconv.Itoa(entry.Status),
			strconv.FormatBool(entry.Archived),
			archivedAt,
		}); err != nil {
			h.Logger.Warn("write audit export", zap.Error(err))
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		h.Logger.Warn("flush audit export", zap.Error(err))
	}
}

// ArchiveAuditLogs POST /admin/audit-logs/archive
func (h *Handler) ArchiveAuditLogs(c *gin.Context) {
	if h.AuditArchiver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "audit archiver is unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	result, err := h.AuditArchiver.RunOnce(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
