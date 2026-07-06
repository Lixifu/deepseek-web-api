package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// toolCallOpenTag / toolCallCloseTag 是模型被指示输出的工具调用标签。
// 使用双方括号而非尖括号，避免被 DeepSeek 网页的 HTML 渲染器当作 HTML 标签吃掉。
const (
	toolCallOpenTag  = "[[TOOL]]"
	toolCallCloseTag = "[[/TOOL]]"
)

// toolCallCounter 用于生成唯一的 tool_call id
var toolCallCounter uint64

// ParsedToolCall 从模型输出解析出的工具调用
type ParsedToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"` // 原始 JSON，原样回传
}

// StreamEvent 流式事件：要么是文本增量，要么是工具调用
type StreamEvent struct {
	Text      string           // 非空：文本增量
	ToolCalls []ParsedToolCall // 非空：本批次工具调用
	Done      bool             // 流结束
}

// ParseToolCallsStream 解析输入文本流，分离普通文本和 <tool_call> 标签内的工具调用。
//
// 状态机：content → buffering → content（循环）
//   - content 状态：正常累积文本，遇到 <tool_call> 切换到 buffering
//   - buffering 状态：累积到 </tool_call> 解析 JSON 后切回 content
//
// 为防止标签被拆到多个 delta，每次保留可能的不完整前缀不发送。
func ParseToolCallsStream(in <-chan string) <-chan StreamEvent {
	out := make(chan StreamEvent, 32)
	go func() {
		defer close(out)
		var buf strings.Builder
		state := "content"
		for delta := range in {
			buf.WriteString(delta)
			for again := true; again; {
				again = processBuf(&buf, &state, out)
			}
		}
		// 流结束：刷新剩余 buf
		flushRemainder(&buf, &state, out)
		out <- StreamEvent{Done: true}
	}()
	return out
}

// processBuf 处理 buf 中的一次状态转换。返回 true 表示需要再处理一次。
func processBuf(buf *strings.Builder, state *string, out chan<- StreamEvent) bool {
	s := buf.String()
	switch *state {
	case "content":
		idx := strings.Index(s, toolCallOpenTag)
		if idx >= 0 {
			// 输出标签前的文本
			if before := s[:idx]; before != "" {
				out <- StreamEvent{Text: before}
			}
			// 跳过开标签
			buf.Reset()
			buf.WriteString(s[idx+len(toolCallOpenTag):])
			*state = "buffering"
			return true // 继续处理 buffering
		}
		// 没有完整开标签：检查 buf 末尾是否是开标签的不完整前缀
		safe, hold := splitSafeContent(s, toolCallOpenTag)
		if safe != "" {
			out <- StreamEvent{Text: safe}
		}
		buf.Reset()
		buf.WriteString(hold)
		return false

	case "buffering":
		idx := strings.Index(s, toolCallCloseTag)
		if idx >= 0 {
			// 解析闭标签前的 JSON
			jsonStr := strings.TrimSpace(s[:idx])
			if tc := parseOneToolCall(jsonStr); tc != nil {
				out <- StreamEvent{ToolCalls: []ParsedToolCall{*tc}}
			} else if jsonStr != "" {
				// 解析失败：把原始内容当文本回退，避免丢失信息
				out <- StreamEvent{Text: toolCallOpenTag + jsonStr + toolCallCloseTag}
			}
			// 跳过闭标签
			buf.Reset()
			buf.WriteString(s[idx+len(toolCallCloseTag):])
			*state = "content"
			return true // 继续处理 content
		}
		// 闭标签还没出现，继续等
		return false
	}
	return false
}

// flushRemainder 流结束时刷新 buf
func flushRemainder(buf *strings.Builder, state *string, out chan<- StreamEvent) {
	s := buf.String()
	if s == "" {
		return
	}
	switch *state {
	case "content":
		out <- StreamEvent{Text: s}
	case "buffering":
		// 模型可能漏了闭标签，尝试当作完整 JSON 解析
		jsonStr := strings.TrimSpace(s)
		if tc := parseOneToolCall(jsonStr); tc != nil {
			out <- StreamEvent{ToolCalls: []ParsedToolCall{*tc}}
		} else if jsonStr != "" {
			out <- StreamEvent{Text: toolCallOpenTag + jsonStr}
		}
	}
	buf.Reset()
}

// splitSafeContent 把 buf 分成「肯定可以发送的安全部分」和「可能是开标签前缀的保留部分」。
// 例如 buf="hello [[TO" → safe="hello ", hold="[[TO"
// 从 buf 末尾向前找最长的、是 tag 前缀的子串保留下来。
func splitSafeContent(buf, tag string) (safe, hold string) {
	for i := 1; i <= len(buf) && i < len(tag); i++ {
		suffix := buf[len(buf)-i:]
		if strings.HasPrefix(tag, suffix) {
			return buf[:len(buf)-i], suffix
		}
	}
	return buf, ""
}

// parseOneToolCall 解析工具调用标签内的 JSON。
// 支持两种 arguments 格式：对象 或 字符串（被双重编码）
// 如果 JSON 解析失败，会尝试修复常见的格式问题（如 Windows 路径的单个反斜杠）。
func parseOneToolCall(s string) *ParsedToolCall {
	if s == "" {
		return nil
	}
	s = strings.TrimSpace(s)
	// 容错：模型可能误加 markdown 代码块围栏
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		// 尝试修复常见的 JSON 格式问题（如 Windows 路径 "c:\Users" 里的单个反斜杠）
		repaired := repairJSONString(s)
		if repaired == s {
			return nil
		}
		if err2 := json.Unmarshal([]byte(repaired), &raw); err2 != nil {
			return nil
		}
	}
	if raw.Name == "" {
		return nil
	}
	args := raw.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	} else {
		// arguments 可能是字符串形式（被双重编码），解一层
		var s2 string
		if json.Unmarshal(args, &s2) == nil {
			args = json.RawMessage(s2)
		}
	}
	return &ParsedToolCall{Name: raw.Name, Arguments: args}
}

// repairJSONString 修复 JSON 字符串字面量里的非法反斜杠转义。
// 例如 Windows 路径 "c:\Users\lbx06" 里的 \U \l 等不是合法 JSON 转义，
// 会被修复成 "c:\\Users\\lbx06"。
// 只修改字符串字面量内部，不影响 JSON 结构外的反斜杠。
func repairJSONString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	inString := false
	i := 0
	for i < len(s) {
		c := s[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			sb.WriteByte(c)
			i++
			continue
		}
		// 在字符串字面量内
		if c == '\\' {
			if i+1 < len(s) {
				next := s[i+1]
				// 合法 JSON 转义：\" \\ \/ \uXXXX 几乎总是真转义，直接保留
				if next == '"' || next == '\\' || next == '/' || next == 'u' {
					sb.WriteByte(c)
					sb.WriteByte(next)
					i += 2
					continue
				}
				// \b \f \n \r \t 是合法转义，但在 Windows 路径场景里
				// 后面跟字母/数字时几乎总是字面量反斜杠（如 \network \temp \home）
				if next == 'b' || next == 'f' || next == 'n' || next == 'r' || next == 't' {
					if i+2 < len(s) && isAlphaNum(s[i+2]) {
						// 路径场景：把单个 \ 替换成 \\
						sb.WriteString("\\\\")
						i++
						continue
					}
					// 否则当作真转义保留
					sb.WriteByte(c)
					sb.WriteByte(next)
					i += 2
					continue
				}
				// 非法转义：把单个 \ 替换成 \\
				sb.WriteString("\\\\")
				i++
				continue
			}
			// 字符串末尾的孤立反斜杠
			sb.WriteString("\\\\")
			i++
			continue
		}
		if c == '"' {
			inString = false
		}
		sb.WriteByte(c)
		i++
	}
	return sb.String()
}

// isAlphaNum 判断字节是否是字母或数字
func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// parseTextForToolCalls 从完整文本中解析工具调用，多策略：
//  1. 先找 [[TOOL]]...[[/TOOL]] 标签（标准格式）
//  2. 若未找到，尝试自然语言兜底格式：
//     - [调用 X] {json}  /  [Call X] {json}
//     - 调用 X: {json}
//     - 裸 JSON {"name":"X","arguments":{...}}
func parseTextForToolCalls(text string) []ParsedToolCall {
	// 策略 1：[[TOOL]] 标签
	calls := parseAllToolCallsFromText(text)
	if len(calls) > 0 {
		return calls
	}
	// 策略 2：自然语言兜底
	return parseFallbackToolCalls(text)
}

// parseFallbackToolCalls 用正则匹配模型自然输出的工具调用格式。
// 支持：
//   [调用 LS] {"path": "/home"}
//   [Call LS] {"path": "/home"}
//   调用 LS: {"path": "/home"}
//   裸 JSON {"name":"X","arguments":{...}}
//   JSON 数组 [{"name":"X","arguments":{...}}]（DeepSeek 常输出的 markdown 代码块格式）
func parseFallbackToolCalls(text string) []ParsedToolCall {
	var calls []ParsedToolCall
	// 模式 A: [调用 X] {json}  或  [Call X] {json}
	for _, match := range fallbackPatternA.FindAllStringSubmatch(text, -1) {
		name := strings.TrimSpace(match[1])
		jsonStr := strings.TrimSpace(match[2])
		if tc := parseOneToolCall(jsonStr); tc != nil && tc.Name == "" {
			tc.Name = name
			calls = append(calls, *tc)
		} else if tc := parseOneToolCall(fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, jsonStr)); tc != nil {
			calls = append(calls, *tc)
		}
	}
	if len(calls) > 0 {
		return calls
	}

	// 清理 DeepSeek 网页渲染 markdown 代码块后混入的按钮文本
	// 模型输出 ```json [...] ``` ，网页 innerText 会变成 "json\n复制\n下载\n[...]"
	cleaned := cleanDeepSeekButtons(text)

	// 模式 C: JSON 数组 [{"name":"X","arguments":{...}}, ...]
	for _, jsonStr := range findJSONArrays(cleaned) {
		items := parseToolCallArray(jsonStr)
		calls = append(calls, items...)
	}
	if len(calls) > 0 {
		return calls
	}

	// 模式 B: 裸 JSON 对象 {"name":"X","arguments":{...}}
	for _, match := range fallbackPatternB.FindAllString(cleaned, -1) {
		if tc := parseOneToolCall(match); tc != nil {
			calls = append(calls, *tc)
		}
	}
	return calls
}

// cleanDeepSeekButtons 清理 DeepSeek 网页渲染后混入的按钮文本。
// markdown 代码块 ```json ... ``` 被渲染后，innerText 会包含「复制」「下载」按钮文本。
func cleanDeepSeekButtons(text string) string {
	// 去掉独占一行的「复制」「下载」按钮文本
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "复制" || trimmed == "下载" {
			continue
		}
		// 去掉行首的 "json"（代码块语言标识被 innerText 提取）
		if trimmed == "json" || trimmed == "```json" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// findJSONArrays 通过括号匹配从文本中提取所有 JSON 数组 [...]。
// 比正则更可靠，能处理嵌套对象和多行格式。
func findJSONArrays(text string) []string {
	var arrays []string
	for i := 0; i < len(text); i++ {
		if text[i] != '[' {
			continue
		}
		// 找匹配的 ]
		depth := 0
		inString := false
		escape := false
		end := -1
		for j := i; j < len(text); j++ {
			c := text[j]
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if c == '[' {
				depth++
			} else if c == ']' {
				depth--
				if depth == 0 {
					end = j
					break
				}
			}
		}
		if end > i {
			arrays = append(arrays, text[i:end+1])
			i = end
		}
	}
	return arrays
}

// parseToolCallArray 解析 JSON 数组格式的工具调用 [{"name":"X","arguments":{...}}]
func parseToolCallArray(jsonStr string) []ParsedToolCall {
	var arr []struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &arr); err != nil {
		// 尝试修复 JSON（如 Windows 路径的单个反斜杠）
		repaired := repairJSONString(jsonStr)
		if repaired == jsonStr {
			return nil
		}
		if err := json.Unmarshal([]byte(repaired), &arr); err != nil {
			return nil
		}
	}
	var calls []ParsedToolCall
	for _, item := range arr {
		if item.Name == "" {
			continue
		}
		args := item.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage(`{}`)
		} else {
			// arguments 可能是字符串形式（被双重编码），解一层
			var s2 string
			if json.Unmarshal(args, &s2) == nil {
				args = json.RawMessage(s2)
			}
		}
		calls = append(calls, ParsedToolCall{Name: item.Name, Arguments: args})
	}
	return calls
}

// stripFallbackToolCallText 移除自然语言工具调用格式文本
func stripFallbackToolCallText(text string) string {
	// 清理 DeepSeek 按钮文本
	text = cleanDeepSeekButtons(text)
	// 清理 [调用 X] {json} 格式
	text = fallbackPatternA.ReplaceAllString(text, "")
	// 清理 JSON 数组
	cleaned := text
	for _, arr := range findJSONArrays(cleaned) {
		// 只移除看起来是工具调用的数组（包含 "name" 和 "arguments"）
		if strings.Contains(arr, "\"name\"") && strings.Contains(arr, "\"arguments\"") {
			cleaned = strings.Replace(cleaned, arr, "", 1)
		}
	}
	// 清理裸 JSON 对象
	cleaned = fallbackPatternB.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

// 预编译正则
var (
	// [调用 X] {json} 或 [Call X] {json}（中英文兼容）
	fallbackPatternA = regexp.MustCompile(`(?:\[调用\s+|\[Call\s+)([A-Za-z_][A-Za-z0-9_]*)\]\s*(\{[^{}
]*(?:\{[^{}]*\}[^{}
]*)*\})`)
	// 裸 {"name":"X","arguments":{...}}
	fallbackPatternB = regexp.MustCompile(`\{"name"\s*:\s*"[^"]+"\s*,\s*"arguments"\s*:\s*\{[^}]*\}\s*\}`)
)

// nextToolCallID 生成唯一 tool_call id
func nextToolCallID() string {
	n := atomic.AddUint64(&toolCallCounter, 1)
	return fmt.Sprintf("call_%d", n)
}
