package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

type sequenceEvaluator struct {
	mu      sync.Mutex
	results []interface{}
	index   int
}

func (e *sequenceEvaluator) Evaluate(_ string, _ ...interface{}) (interface{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.results) == 0 {
		return map[string]interface{}{}, nil
	}
	if e.index >= len(e.results) {
		return e.results[len(e.results)-1], nil
	}
	result := e.results[e.index]
	e.index++
	return result, nil
}

func pollResult(content string, turnDetected, generating bool) map[string]interface{} {
	return map[string]interface{}{
		"reasoning":      "",
		"content":        content,
		"turnDetected":   turnDetected,
		"generating":     generating,
		"mainCount":      float64(1),
		"reasoningCount": float64(0),
	}
}

func fastReplyStreamConfig() replyStreamConfig {
	return replyStreamConfig{
		PollInterval:          time.Millisecond,
		StableAfterStop:       2 * time.Millisecond,
		StableWithoutControl:  4 * time.Millisecond,
		ReasoningOnlyStable:   8 * time.Millisecond,
		StaleControlStable:    5 * time.Millisecond,
		StaleReasoningControl: 10 * time.Millisecond,
		MaxWait:               100 * time.Millisecond,
	}
}

func TestStreamReplyDetectsReplyWhenDOMCountDoesNotIncrease(t *testing.T) {
	evaluator := &sequenceEvaluator{results: []interface{}{
		pollResult("", false, false),
		pollResult("新", true, true),
		pollResult("新回复", true, true),
		pollResult("新回复", true, false),
	}}
	driver := &DeepSeekDriver{sel: DefaultSelectors, logger: zap.NewNop()}
	ch := make(chan StreamChunk, 8)

	go driver.streamReplyWithConfig(
		context.Background(),
		evaluator,
		replySnapshot{Marker: "snapshot", Content: "旧回复", MainCount: 1},
		ch,
		fastReplyStreamConfig(),
	)

	var content string
	for chunk := range ch {
		content += chunk.Content
	}
	if content != "新回复" {
		t.Fatalf("content = %q, want %q", content, "新回复")
	}
}

func TestStreamReplyFinalizesWhenGenerationControlStaysStale(t *testing.T) {
	evaluator := &sequenceEvaluator{results: []interface{}{
		pollResult("已完成", true, true),
	}}
	driver := &DeepSeekDriver{sel: DefaultSelectors, logger: zap.NewNop()}
	ch := make(chan StreamChunk, 8)

	go driver.streamReplyWithConfig(
		context.Background(),
		evaluator,
		replySnapshot{Marker: "snapshot"},
		ch,
		fastReplyStreamConfig(),
	)

	var content string
	for chunk := range ch {
		content += chunk.Content
	}
	if content != "已完成" {
		t.Fatalf("content = %q, want %q", content, "已完成")
	}
}

func TestReplyPollScriptUsesSnapshotAndVisibleControls(t *testing.T) {
	script := buildReplyPollScript(DefaultSelectors.AssistantMsg, replySnapshot{
		Marker:    "turn-1",
		Content:   "old content",
		Reasoning: "old reasoning",
	})

	for _, expected := range []string{
		replySnapshotAttribute,
		".ds-assistant-message-main-content",
		"getBoundingClientRect",
		"button, [role=\"button\"]",
		"turnDetected",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("poll script does not contain %q", expected)
		}
	}
	if strings.Contains(script, "assistantNodes.length >") {
		t.Fatal("poll script must not require the assistant node count to increase")
	}
}

func TestShouldFinalizeReplyUsesLongerWindowForReasoningOnly(t *testing.T) {
	cfg := fastReplyStreamConfig()
	poll := replyPollResult{TurnDetected: true}

	if done, _ := shouldFinalizeReply(poll, false, false, cfg.StableWithoutControl, cfg); done {
		t.Fatal("reasoning-only reply finalized with the content stability window")
	}
	if done, _ := shouldFinalizeReply(poll, false, false, cfg.ReasoningOnlyStable, cfg); !done {
		t.Fatal("reasoning-only reply did not finalize after its stability window")
	}
}
