package observability

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSnapshot(t *testing.T) {
	before := Snapshot()
	RecordCall("deepseek-chat", 2*time.Second, true)
	RecordCall("deepseek-chat", time.Second, false)
	UpdatePool(3, 2, 1, 4)
	UpdateBrowserMemory(128 * 1024 * 1024)

	got := Snapshot()
	if got.TotalCalls != before.TotalCalls+2 {
		t.Fatalf("TotalCalls = %d, want %d", got.TotalCalls, before.TotalCalls+2)
	}
	if got.QueueLength != 4 || got.AccountHealthy != 2 || got.AccountTotal != 3 {
		t.Fatalf("unexpected pool snapshot: %#v", got)
	}
	if got.BrowserMemoryBytes != 128*1024*1024 {
		t.Fatalf("BrowserMemoryBytes = %d", got.BrowserMemoryBytes)
	}
}

func TestPrometheusHandler(t *testing.T) {
	request := httptest.NewRequest("GET", "/metrics", nil)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, name := range []string{
		"deepseek_web_api_requests_total",
		"deepseek_web_api_browser_queue_length",
		"deepseek_web_api_browser_memory_bytes",
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("metrics response missing %s", name)
		}
	}
}

func TestReadRSSBytes(t *testing.T) {
	path := t.TempDir() + "/status"
	if err := os.WriteFile(path, []byte("Name:\ttest\nVmRSS:\t1234 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readRSSBytes(path); got != 1234*1024 {
		t.Fatalf("readRSSBytes() = %d", got)
	}
}
