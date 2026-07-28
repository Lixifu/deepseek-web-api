package admin

import (
	"context"
	"net/http"
	"time"

	"deepseek-web-api/internal/observability"
	"github.com/gin-gonic/gin"
)

// RuntimeMetrics GET /admin/metrics
func (h *Handler) RuntimeMetrics(c *gin.Context) {
	total, healthy, busy := h.Pool.SessionStats()
	queueCtx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	queued := h.Pool.EffectiveQueueLength(queueCtx)
	observability.UpdatePool(total, healthy, busy, queued)
	observability.UpdateBrowserMemory(observability.ChromiumMemoryBytes())
	c.JSON(http.StatusOK, gin.H{"metrics": observability.Snapshot()})
}
