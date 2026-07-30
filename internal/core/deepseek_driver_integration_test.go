package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestDeepSeekDriverLiveSequentialReplies 验证同一页面连续对话时可以识别每一轮回复。
// 默认跳过；手动设置 DEEPSEEK_STORAGE_STATE 为有效登录态文件后运行。
func TestDeepSeekDriverLiveSequentialReplies(t *testing.T) {
	storageState := os.Getenv("DEEPSEEK_STORAGE_STATE")
	if storageState == "" {
		t.Skip("DEEPSEEK_STORAGE_STATE is not set")
	}

	logger := zap.NewNop()
	pool := NewBrowserPool(true, logger)
	pool.Configure(1, 1, 30*time.Second)
	if err := pool.Start([]AccountConfig{{
		ID:          1,
		Name:        "driver-integration-test",
		StoragePath: storageState,
	}}); err != nil {
		t.Fatalf("start browser pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Stop()
	})

	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer acquireCancel()
	session, err := pool.Acquire(acquireCtx, nil)
	if err != nil {
		t.Fatalf("acquire browser session: %v", err)
	}
	defer session.Release()

	driver := NewDeepSeekDriver(session, DefaultSelectors, logger)
	tests := []struct {
		prompt string
		want   string
	}{
		{"仅回复以下验证码，不要解释：API_FIX_A731", "API_FIX_A731"},
		{"仅回复以下验证码，不要解释：API_FIX_B842", "API_FIX_B842"},
	}

	for index, test := range tests {
		requestCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		chunks, err := driver.SendMessage(requestCtx, test.prompt)
		if err != nil {
			cancel()
			t.Fatalf("turn %d send message: %v", index+1, err)
		}
		_, content := consumeAllChunks(requestCtx, chunks)
		cancel()
		if !strings.Contains(content, test.want) {
			t.Fatalf("turn %d content = %q, want it to contain %q", index+1, content, test.want)
		}
	}
}
