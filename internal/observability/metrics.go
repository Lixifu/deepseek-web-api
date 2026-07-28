package observability

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "deepseek_web_api",
			Name:      "requests_total",
			Help:      "Completed chat requests by model and outcome.",
		},
		[]string{"model", "outcome"},
	)
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "deepseek_web_api",
			Name:      "request_duration_seconds",
			Help:      "End-to-end chat request duration.",
			Buckets:   []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 180},
		},
		[]string{"model", "outcome"},
	)
	browserAccounts = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "deepseek_web_api",
			Name:      "browser_accounts",
			Help:      "Browser account sessions by state.",
		},
		[]string{"state"},
	)
	browserQueueLength = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "deepseek_web_api",
			Name:      "browser_queue_length",
			Help:      "Requests currently waiting for a browser session.",
		},
	)
	browserMemoryBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "deepseek_web_api",
			Name:      "browser_memory_bytes",
			Help:      "Resident memory used by Chromium processes.",
		},
	)

	totalCalls      atomic.Uint64
	successCalls    atomic.Uint64
	failedCalls     atomic.Uint64
	totalDurationNS atomic.Uint64
	queueLength     atomic.Int64
	accountTotal    atomic.Int64
	accountHealthy  atomic.Int64
	accountBusy     atomic.Int64
	browserMemory   atomic.Int64
)

func init() {
	prometheus.MustRegister(
		requestsTotal,
		requestDuration,
		browserAccounts,
		browserQueueLength,
		browserMemoryBytes,
	)
}

// RuntimeSnapshot 是管理后台使用的进程生命周期指标快照。
type RuntimeSnapshot struct {
	TotalCalls         uint64  `json:"total_calls"`
	SuccessCalls       uint64  `json:"success_calls"`
	FailedCalls        uint64  `json:"failed_calls"`
	SuccessRate        float64 `json:"success_rate"`
	AverageLatencyMS   float64 `json:"average_latency_ms"`
	QueueLength        int64   `json:"queue_length"`
	AccountTotal       int64   `json:"account_total"`
	AccountHealthy     int64   `json:"account_healthy"`
	AccountBusy        int64   `json:"account_busy"`
	BrowserMemoryBytes int64   `json:"browser_memory_bytes"`
}

// RecordCall 记录一次已结束的聊天请求。
func RecordCall(model string, duration time.Duration, success bool) {
	outcome := "success"
	if !success {
		outcome = "failed"
		failedCalls.Add(1)
	} else {
		successCalls.Add(1)
	}
	totalCalls.Add(1)
	totalDurationNS.Add(uint64(duration))
	requestsTotal.WithLabelValues(model, outcome).Inc()
	requestDuration.WithLabelValues(model, outcome).Observe(duration.Seconds())
}

// UpdatePool 更新浏览器池的瞬时指标。
func UpdatePool(total, healthy, busy, queued int) {
	accountTotal.Store(int64(total))
	accountHealthy.Store(int64(healthy))
	accountBusy.Store(int64(busy))
	queueLength.Store(int64(queued))
	browserAccounts.WithLabelValues("total").Set(float64(total))
	browserAccounts.WithLabelValues("healthy").Set(float64(healthy))
	browserAccounts.WithLabelValues("unhealthy").Set(float64(total - healthy))
	browserAccounts.WithLabelValues("busy").Set(float64(busy))
	browserQueueLength.Set(float64(queued))
}

// UpdateBrowserMemory 更新 Chromium RSS。
func UpdateBrowserMemory(bytes int64) {
	if bytes < 0 {
		bytes = 0
	}
	browserMemory.Store(bytes)
	browserMemoryBytes.Set(float64(bytes))
}

func Snapshot() RuntimeSnapshot {
	total := totalCalls.Load()
	success := successCalls.Load()
	failed := failedCalls.Load()
	var rate, averageMS float64
	if total > 0 {
		rate = float64(success) / float64(total) * 100
		averageMS = float64(totalDurationNS.Load()) / float64(total) / float64(time.Millisecond)
	}
	return RuntimeSnapshot{
		TotalCalls:         total,
		SuccessCalls:       success,
		FailedCalls:        failed,
		SuccessRate:        rate,
		AverageLatencyMS:   averageMS,
		QueueLength:        queueLength.Load(),
		AccountTotal:       accountTotal.Load(),
		AccountHealthy:     accountHealthy.Load(),
		AccountBusy:        accountBusy.Load(),
		BrowserMemoryBytes: browserMemory.Load(),
	}
}

func Handler() http.Handler {
	return promhttp.Handler()
}
