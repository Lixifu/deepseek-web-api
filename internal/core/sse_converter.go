package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// openAIChunk OpenAI 流式响应单个 chunk
type openAIChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
}

// chunkChoice OpenAI chunk 的 choice
type chunkChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

// ConvertStream 把 DeepSeek 增量 StreamChunk channel 转成 OpenAI 兼容 SSE 字符串 channel。
//
// StreamChunk 包含 Reasoning（思维链）和 Content（正文）：
//   - Reasoning → delta.reasoning_content（OpenAI o1/DeepSeek-R1 协议，客户端可折叠）
//   - Content   → delta.content
//
// 当 tools 非空时，采用「缓冲 + 末尾解析」策略：
//   - 先缓冲全部 Content（不实时发），等流结束后统一解析
//   - Reasoning 实时转发（思维链不参与工具调用解析）
//   - 找到工具调用 -> delta.tool_calls + finish_reason="tool_calls"
//   - 未找到 -> delta.content（一次性发全文）+ finish_reason="stop"
func ConvertStream(in <-chan StreamChunk, model, requestID string, onFinal func(string), tools []Tool) <-chan string {
	out := make(chan string, 32)
	go func() {
		defer close(out)

		var fullReasoning, fullContent string

		// 没有 tools：实时转发 reasoning 和 content
		if len(tools) == 0 {
			for chunk := range in {
				if chunk.Reasoning != "" {
					fullReasoning += chunk.Reasoning
					out <- buildSSE(requestID, model, map[string]any{"reasoning_content": chunk.Reasoning}, nil)
				}
				if chunk.Content != "" {
					fullContent += chunk.Content
					out <- buildSSE(requestID, model, map[string]any{"content": chunk.Content}, nil)
				}
			}
			stop := "stop"
			out <- buildSSE(requestID, model, map[string]any{}, &stop)
			out <- "data: [DONE]\n\n"
			if onFinal != nil {
				onFinal(fullContent)
			}
			return
		}

		// 有 tools：缓冲全部 Content，流结束后统一解析
		// Reasoning 实时转发
		for chunk := range in {
			if chunk.Reasoning != "" {
				fullReasoning += chunk.Reasoning
				out <- buildSSE(requestID, model, map[string]any{"reasoning_content": chunk.Reasoning}, nil)
			}
			if chunk.Content != "" {
				fullContent += chunk.Content
			}
		}

		// 解析工具调用：先 [[TOOL]] 标签，再兜底自然格式
		tcs := parseTextForToolCalls(fullContent)
		var finish string
		if len(tcs) > 0 {
			out <- buildToolCallsSSE(requestID, model, tcs)
			finish = "tool_calls"
			cleanContent := stripToolCallTags(fullContent)
			cleanContent = stripFallbackToolCallText(cleanContent)
			if cleanContent != "" {
				out <- buildSSE(requestID, model, map[string]any{"content": cleanContent}, nil)
			}
		} else {
			if fullContent != "" {
				out <- buildSSE(requestID, model, map[string]any{"content": fullContent}, nil)
			}
			finish = "stop"
		}
		out <- buildSSE(requestID, model, map[string]any{}, &finish)
		out <- "data: [DONE]\n\n"
		if onFinal != nil {
			onFinal(fullContent)
		}
	}()
	return out
}

// buildSSE 构造一个文本 delta chunk
func buildSSE(id, model string, delta map[string]any, finish *string) string {
	payload := openAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chunkChoice{{Index: 0, Delta: delta, FinishReason: finish}},
	}
	b, _ := json.Marshal(payload)
	return fmt.Sprintf("data: %s\n\n", string(b))
}

// buildToolCallsSSE 构造一个 tool_calls delta chunk。
// 一次性把工具调用的 id/type/function.name/function.arguments 全发出去。
// 每个 tool_call 带唯一的 index。
func buildToolCallsSSE(id, model string, calls []ParsedToolCall) string {
	tcs := make([]map[string]any, 0, len(calls))
	for i, c := range calls {
		tcs = append(tcs, map[string]any{
			"index": i,
			"id":    nextToolCallID(),
			"type":  "function",
			"function": map[string]any{
				"name":      c.Name,
				"arguments": string(c.Arguments),
			},
		})
	}
	payload := openAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chunkChoice{{
			Index:        0,
			Delta:        map[string]any{"tool_calls": tcs},
			FinishReason: nil,
		}},
	}
	b, _ := json.Marshal(payload)
	return fmt.Sprintf("data: %s\n\n", string(b))
}

// BuildCompletion 构造非流式的 OpenAI 响应。
// reasoning 放到 message.reasoning_content（可折叠），content 放到 message.content。
// 如果 content 中包含工具调用标签，会解析为 tool_calls 并设置 finish_reason="tool_calls"。
func BuildCompletion(id, model, reasoning, content string, tools []Tool) map[string]any {
	// 没传 tools，直接返回纯文本（含 reasoning_content）
	if len(tools) == 0 {
		msg := map[string]any{"role": "assistant", "content": content}
		if reasoning != "" {
			msg["reasoning_content"] = reasoning
		}
		return map[string]any{
			"id":      id,
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       msg,
				"finish_reason": "stop",
			}},
		}
	}

	// 有 tools：从完整文本里解析 tool_calls
	tcs := parseTextForToolCalls(content)
	if len(tcs) == 0 {
		msg := map[string]any{"role": "assistant", "content": content}
		if reasoning != "" {
			msg["reasoning_content"] = reasoning
		}
		return map[string]any{
			"id":      id,
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       msg,
				"finish_reason": "stop",
			}},
		}
	}

	// 有工具调用：构造 OpenAI tool_calls 响应
	toolCallsJSON := make([]map[string]any, 0, len(tcs))
	for _, tc := range tcs {
		toolCallsJSON = append(toolCallsJSON, map[string]any{
			"id":   nextToolCallID(),
			"type": "function",
			"function": map[string]any{
				"name":      tc.Name,
				"arguments": string(tc.Arguments),
			},
		})
	}
	cleanContent := stripToolCallTags(content)
	msg := map[string]any{
		"role":       "assistant",
		"content":    cleanContent,
		"tool_calls": toolCallsJSON,
	}
	if reasoning != "" {
		msg["reasoning_content"] = reasoning
	}
	return map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": "tool_calls",
		}},
	}
}

// parseAllToolCallsFromText 从完整文本中提取所有 <tool_call> 标签内的工具调用
func parseAllToolCallsFromText(text string) []ParsedToolCall {
	var calls []ParsedToolCall
	for {
		idx := indexOf(text, toolCallOpenTag)
		if idx < 0 {
			break
		}
		rest := text[idx+len(toolCallOpenTag):]
		closeIdx := indexOf(rest, toolCallCloseTag)
		var jsonStr string
		if closeIdx >= 0 {
			jsonStr = rest[:closeIdx]
			text = rest[closeIdx+len(toolCallCloseTag):]
		} else {
			jsonStr = rest
			text = ""
		}
		if tc := parseOneToolCall(jsonStr); tc != nil {
			calls = append(calls, *tc)
		}
	}
	return calls
}

// stripToolCallTags 移除文本中所有 <tool_call>...</tool_call> 标签，保留标签外的文本
func stripToolCallTags(text string) string {
	var sb []byte
	for {
		idx := indexOf(text, toolCallOpenTag)
		if idx < 0 {
			sb = append(sb, text...)
			break
		}
		sb = append(sb, text[:idx]...)
		rest := text[idx+len(toolCallOpenTag):]
		closeIdx := indexOf(rest, toolCallCloseTag)
		if closeIdx < 0 {
			// 没有闭标签，丢弃剩余
			break
		}
		text = rest[closeIdx+len(toolCallCloseTag):]
	}
	out := string(sb)
	if out == "" {
		return ""
	}
	// 只 trim 首尾空白
	return trimSpace(out)
}

// indexOf 包装 strings.Index
func indexOf(s, sub string) int {
	return strings.Index(s, sub)
}

// trimSpace 简单首尾空白裁剪
func trimSpace(s string) string {
	return strings.TrimSpace(s)
}
