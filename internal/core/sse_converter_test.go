package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildSSEIncludesCreatedTimestamp(t *testing.T) {
	line := buildSSE("id", "deepseek-chat", map[string]any{"content": "ok"}, nil)
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &payload); err != nil {
		t.Fatal(err)
	}
	if created, ok := payload["created"].(float64); !ok || created <= 0 {
		t.Fatalf("created = %#v, want positive Unix timestamp", payload["created"])
	}
}

func TestToolCallIndexesAreUnique(t *testing.T) {
	line := buildToolCallsSSE("id", "deepseek-chat", []ParsedToolCall{
		{Name: "first", Arguments: json.RawMessage(`{}`)},
		{Name: "second", Arguments: json.RawMessage(`{}`)},
	})
	var payload struct {
		Choices []struct {
			Delta struct {
				ToolCalls []struct {
					Index int `json:"index"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Choices) != 1 || len(payload.Choices[0].Delta.ToolCalls) != 2 {
		t.Fatalf("unexpected tool_calls payload: %#v", payload)
	}
	if payload.Choices[0].Delta.ToolCalls[0].Index != 0 || payload.Choices[0].Delta.ToolCalls[1].Index != 1 {
		t.Fatalf("tool call indexes are not sequential: %#v", payload.Choices[0].Delta.ToolCalls)
	}
}
