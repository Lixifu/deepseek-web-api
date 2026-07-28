package core

import "testing"

func TestSanitizeRedisQueuePrefix(t *testing.T) {
	if got := sanitizeRedisQueuePrefix(" team {api} "); got != "team__api_" {
		t.Fatalf("sanitizeRedisQueuePrefix() = %q", got)
	}
	if got := sanitizeRedisQueuePrefix(""); got != "deepseek_web_api" {
		t.Fatalf("default prefix = %q", got)
	}
}

func TestQueueScriptPair(t *testing.T) {
	got, err := queueScriptPair([]any{int64(3), "9"})
	if err != nil {
		t.Fatal(err)
	}
	if got != [2]int64{3, 9} {
		t.Fatalf("queueScriptPair() = %v", got)
	}
}
