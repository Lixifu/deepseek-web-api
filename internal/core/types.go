package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// StreamChunk 流式增量：思维链和正文可以同时出现在一个 chunk 里
type StreamChunk struct {
	Reasoning string // 思维链增量（深度思考模式，可为空）
	Content   string // 正文增量（可为空）
}

// Content 支持 string 或 array 两种格式（OpenAI 多模态格式兼容）
// - string: "hello"
// - array:  [{"type":"text","text":"hello"},{"type":"image_url",...}]
type Content string

// UnmarshalJSON 支持 string 和 array 两种 content 格式
func (c *Content) UnmarshalJSON(data []byte) error {
	// content 可能为 null（assistant 调用工具时只有 tool_calls，没有 content）
	if string(data) == "null" {
		*c = ""
		return nil
	}
	// 尝试当作 string 解析
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*c = Content(s)
		return nil
	}

	// 尝试当作 array 解析
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		*c = Content(sb.String())
		return nil
	}

	// 兜底：保持空
	*c = ""
	return nil
}

// MarshalJSON Content 序列化为 string
func (c Content) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(c))
}

// ToolCall 单次工具调用（OpenAI 协议）
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // 固定 "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON 字符串
	} `json:"function"`
}

// Message OpenAI 兼容的消息结构
type Message struct {
	Role       string     `json:"role" binding:"required"`
	Content    Content    `json:"content"`
	Name       string     `json:"name,omitempty"`        // tool 角色用：工具名
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool 角色用：对应 tool_call.id
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant 角色用：本回合发起的调用
}

// String 返回 Content 的字符串形式
func (c Content) String() string {
	return string(c)
}

// Tool 工具定义（OpenAI 协议）
type Tool struct {
	Type     string `json:"type"` // 固定 "function"
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"` // JSON Schema
	} `json:"function"`
}

// ChatRequest 一次对话请求
type ChatRequest struct {
	Messages     []Message
	Model        string
	Stream       bool
	APIKeyID     uint
	QuotaPerDay  int  // 该 API key 的日配额（0 表示不限），由中间件传入
	Tools        []Tool  // 工具定义
	ToolChoice   any     // "auto" / "none" / {"type":"function","function":{"name":"xxx"}}
}

// BuildPrompt 把 messages 数组拼成单条 prompt。
// DeepSeek 网页版输入框只能发一条文本，因此把多轮对话拼合成带角色标签的文本。
// 工具调用格式由客户端自己在 system 消息中约定，服务端不再注入提示词。
func BuildPrompt(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	var sb strings.Builder

	for _, m := range messages {
		switch m.Role {
		case "system":
			sb.WriteString("[系统指令] ")
			sb.WriteString(m.Content.String())
			sb.WriteString("\n\n")
		case "user":
			sb.WriteString("[用户] ")
			sb.WriteString(m.Content.String())
			sb.WriteString("\n\n")
		case "assistant":
			sb.WriteString("[助手] ")
			if m.Content.String() != "" {
				sb.WriteString(m.Content.String())
			}
			// 把上一轮 assistant 的 tool_calls 转成文本形式，让模型理解上下文
			for _, tc := range m.ToolCalls {
				sb.WriteString(fmt.Sprintf("\n[[TOOL]]\n{\"name\": %q, \"arguments\": %s}\n[[/TOOL]]",
					tc.Function.Name, tc.Function.Arguments))
			}
			sb.WriteString("\n\n")
		case "tool":
			// 工具执行结果（截断过长内容，避免 prompt 膨胀导致 DeepSeek 超时）
			sb.WriteString("[工具结果 ")
			if m.Name != "" {
				sb.WriteString(m.Name)
			}
			if m.ToolCallID != "" {
				sb.WriteString(" (call_id=" + m.ToolCallID + ")")
			}
			sb.WriteString("] ")
			content := m.Content.String()
			const maxToolResult = 4000
			if len(content) > maxToolResult {
				sb.WriteString(content[:maxToolResult])
				sb.WriteString(fmt.Sprintf("\n...(已截断，原始长度 %d 字符)", len(content)))
			} else {
				sb.WriteString(content)
			}
			sb.WriteString("\n\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// 错误定义
var (
	ErrNoSession           = errors.New("no available browser session")
	ErrAllSessionsDown     = errors.New("all sessions unavailable")
	ErrSessionExpired      = errors.New("session expired")
	ErrDeepSeekBlocked     = errors.New("deepseek blocked the request")
	ErrSelectorNotFound    = errors.New("selector not found on page")
)

// IsSessionExpired 判断是否登录态失效
func IsSessionExpired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSessionExpired) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "login") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "session expired") ||
		strings.Contains(msg, "未登录")
}

// IsBrowserClosed 判断是否浏览器/上下文/页面已关闭或崩溃（需调用 Pool.Restart() 恢复）。
func IsBrowserClosed(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "target closed") ||
		strings.Contains(msg, "target crashed") ||
		strings.Contains(msg, "browser has been closed") ||
		strings.Contains(msg, "context has been closed") ||
		strings.Contains(msg, "page has been closed") ||
		strings.Contains(msg, "target, context or browser has been closed") ||
		strings.Contains(msg, "page crashed") ||
		strings.Contains(msg, "navigation failed because") && strings.Contains(msg, "closed")
}
