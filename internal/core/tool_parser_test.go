package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseTextForToolCalls_StandardTag 标准标签格式
func TestParseTextForToolCalls_StandardTag(t *testing.T) {
	text := "我来分析。\n[[TOOL]]\n{\"name\": \"LS\", \"arguments\": {\"path\": \"/home\"}}\n[[/TOOL]]\n"
	calls := parseTextForToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want 1", calls)
	}
	if calls[0].Name != "LS" {
		t.Errorf("name = %s, want LS", calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v", err)
	}
	if args["path"] != "/home" {
		t.Errorf("path = %v, want /home", args["path"])
	}
}

// TestParseTextForToolCalls_FallbackChinese [调用 X] {json} 中文兜底
func TestParseTextForToolCalls_FallbackChinese(t *testing.T) {
	text := "我来分析当前目录的项目。先看看有哪些文件。\n[调用 LS] {\"path\": \"c:\\\\Users\\\\lbx06\\\\Desktop\"}"
	calls := parseTextForToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want 1", calls)
	}
	if calls[0].Name != "LS" {
		t.Errorf("name = %s, want LS", calls[0].Name)
	}
}

// TestParseTextForToolCalls_WindowsPath Windows 路径单个反斜杠（非法 JSON 转义）
// 模型常见输出格式：[调用 LS] {"path": "c:\Users\lbx06\Desktop"}
// 这里 \U \l \D \h 等不是合法 JSON 转义，需要 repairJSONString 修复
func TestParseTextForToolCalls_WindowsPath(t *testing.T) {
	// 模型实际输出的文本（单个反斜杠的 Windows 路径）
	text := "我来分析当前目录的项目结构。\n\n首先查看目录下的文件列表：\n[调用 LS] {\"path\": \"c:\\Users\\lbx06\\Desktop\\textbook\\network\\history\\httpserver8.0\"}"
	calls := parseTextForToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want 1 (Windows path should be repaired)", calls)
	}
	if calls[0].Name != "LS" {
		t.Errorf("name = %s, want LS", calls[0].Name)
	}
	// arguments 应该是合法 JSON
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("arguments not valid JSON after repair: %v", err)
	}
	// 修复后路径应该是原始的 Windows 路径（单个反斜杠）
	if args["path"] != "c:\\Users\\lbx06\\Desktop\\textbook\\network\\history\\httpserver8.0" {
		t.Errorf("path = %v", args["path"])
	}
}

// TestRepairJSONString 验证 JSON 修复函数
func TestRepairJSONString(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "windows_path",
			input: `{"path": "c:\Users\lbx06\Desktop"}`,
			want:  `{"path": "c:\\Users\\lbx06\\Desktop"}`,
		},
		{
			name:  "already_escaped",
			input: `{"path": "c:\\Users\\lbx06"}`,
			want:  `{"path": "c:\\Users\\lbx06"}`,
		},
		{
			name:  "mixed_path",
			input: `{"path": "c:\Users\lbx06\Desktop\network"}`,
			want:  `{"path": "c:\\Users\\lbx06\\Desktop\\network"}`,
		},
		{
			name:  "no_string",
			input: `{"count": 42}`,
			want:  `{"count": 42}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := repairJSONString(c.input)
			if got != c.want {
				t.Errorf("repairJSONString(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestParseTextForToolCalls_FallbackEnglish [Call X] {json} 英文兜底
func TestParseTextForToolCalls_FallbackEnglish(t *testing.T) {
	text := "I'll check the directory.\n[Call LS] {\"path\": \"/home/admin\"}"
	calls := parseTextForToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want 1", calls)
	}
	if calls[0].Name != "LS" {
		t.Errorf("name = %s, want LS", calls[0].Name)
	}
}

// TestParseTextForToolCalls_BareJSON 裸 JSON 兜底
func TestParseTextForToolCalls_BareJSON(t *testing.T) {
	text := `{"name": "grep", "arguments": {"pattern": "foo"}}`
	calls := parseTextForToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want 1", calls)
	}
	if calls[0].Name != "grep" {
		t.Errorf("name = %s, want grep", calls[0].Name)
	}
}

// TestParseTextForToolCalls_DeepSeekMarkdownArray DeepSeek 网页渲染的 markdown 代码块格式
// 模型输出 ```json [{"name":"LS","arguments":{...}}] ```，
// DeepSeek 网页 innerText 会变成 "json\n复制\n下载\n[...]"
func TestParseTextForToolCalls_DeepSeekMarkdownArray(t *testing.T) {
	// 模拟 DeepSeek 网页 innerText 的实际输出
	text := "我来分析当前目录的项目结构。\n\njson\n复制\n下载\n[\n  {\n    \"name\": \"LS\",\n    \"arguments\": {\n      \"path\": \"c:\\\\Users\\\\lbx06\\\\Desktop\"\n    }\n  }\n]"
	calls := parseTextForToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want 1", calls)
	}
	if calls[0].Name != "LS" {
		t.Errorf("name = %s, want LS", calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v", err)
	}
}

// TestParseTextForToolCalls_DeepSeekMarkdownArrayWindowsPath 带 Windows 路径的数组格式
func TestParseTextForToolCalls_DeepSeekMarkdownArrayWindowsPath(t *testing.T) {
	text := "我来分析当前目录的项目。\n\njson\n复制\n下载\n[\n  {\n    \"name\": \"LS\",\n    \"arguments\": {\n      \"path\": \"c:\\Users\\lbx06\\Desktop\\textbook\\network\\history\\httpserver8.0\"\n    }\n  }\n]"
	calls := parseTextForToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want 1 (Windows path should be repaired)", calls)
	}
	if calls[0].Name != "LS" {
		t.Errorf("name = %s, want LS", calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("arguments not valid JSON after repair: %v", err)
	}
	if args["path"] != "c:\\Users\\lbx06\\Desktop\\textbook\\network\\history\\httpserver8.0" {
		t.Errorf("path = %v", args["path"])
	}
}

// TestCleanDeepSeekButtons 清理 DeepSeek 按钮文本
func TestCleanDeepSeekButtons(t *testing.T) {
	text := "我来分析。\n\njson\n复制\n下载\n[{\"name\":\"LS\",\"arguments\":{}}]"
	cleaned := cleanDeepSeekButtons(text)
	if strings.Contains(cleaned, "复制") || strings.Contains(cleaned, "下载") {
		t.Errorf("cleaned still contains button text: %q", cleaned)
	}
	if strings.Contains(cleaned, "\njson\n") {
		t.Errorf("cleaned still contains 'json' line: %q", cleaned)
	}
}

// TestParseTextForToolCalls_NoToolCall 无工具调用
func TestParseTextForToolCalls_NoToolCall(t *testing.T) {
	text := "这是一段普通文本，没有工具调用。"
	calls := parseTextForToolCalls(text)
	if len(calls) != 0 {
		t.Errorf("calls = %v, want empty", calls)
	}
}

// TestStripFallbackToolCallText 中文兜底文本清理
func TestStripFallbackToolCallText(t *testing.T) {
	text := "我来分析当前目录的项目。先看看有哪些文件。\n[调用 LS] {\"path\": \"/home\"}"
	cleaned := stripFallbackToolCallText(text)
	if cleaned != "我来分析当前目录的项目。先看看有哪些文件。" {
		t.Errorf("cleaned = %q", cleaned)
	}
}

// TestParseToolCallsStream_NoToolCall 无工具调用标签，纯文本流
func TestParseToolCallsStream_NoToolCall(t *testing.T) {
	in := make(chan string, 5)
	go func() {
		in <- "Hello "
		in <- "world!"
		close(in)
	}()

	var texts []string
	var toolCalls []ParsedToolCall
	var doneCount int
	for ev := range ParseToolCallsStream(in) {
		if ev.Text != "" {
			texts = append(texts, ev.Text)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
		if ev.Done {
			doneCount++
		}
	}
	if len(texts) != 2 || texts[0] != "Hello " || texts[1] != "world!" {
		t.Errorf("texts = %v, want [Hello  world!]", texts)
	}
	if len(toolCalls) != 0 {
		t.Errorf("toolCalls = %v, want empty", toolCalls)
	}
	if doneCount != 1 {
		t.Errorf("doneCount = %d, want 1", doneCount)
	}
}

// TestParseToolCallsStream_SingleToolCall 单个工具调用，标签在单个 delta
func TestParseToolCallsStream_SingleToolCall(t *testing.T) {
	in := make(chan string, 5)
	go func() {
		in <- "I'll read the file. [[TOOL]]\n"
		in <- `{"name": "read_file", "arguments": {"path": "/tmp/test.txt"}}`
		in <- "\n[[/TOOL]]"
		close(in)
	}()

	var texts []string
	var toolCalls []ParsedToolCall
	for ev := range ParseToolCallsStream(in) {
		if ev.Text != "" {
			texts = append(texts, ev.Text)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
	}
	if len(texts) != 1 || texts[0] != "I'll read the file. " {
		t.Errorf("texts = %q, want [I'll read the file. ]", texts)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls = %v, want 1", toolCalls)
	}
	if toolCalls[0].Name != "read_file" {
		t.Errorf("name = %s, want read_file", toolCalls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal(toolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v", err)
	}
	if args["path"] != "/tmp/test.txt" {
		t.Errorf("path = %v, want /tmp/test.txt", args["path"])
	}
}

// TestParseToolCallsStream_TagSplitAcrossDeltas 标签被拆到多个 delta
func TestParseToolCallsStream_TagSplitAcrossDeltas(t *testing.T) {
	in := make(chan string, 10)
	go func() {
		in <- "Sure. [[TO"
		in <- "OL]]\n"
		in <- `{"name": "list_dir", "arguments": {"dir": "/home"}}`
		in <- "\n[[/TOO"
		in <- "L]]"
		close(in)
	}()

	var texts []string
	var toolCalls []ParsedToolCall
	for ev := range ParseToolCallsStream(in) {
		if ev.Text != "" {
			texts = append(texts, ev.Text)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
	}
	if len(texts) != 1 || texts[0] != "Sure. " {
		t.Errorf("texts = %q, want [Sure. ]", texts)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("toolCalls = %v, want 1", toolCalls)
	}
	if toolCalls[0].Name != "list_dir" {
		t.Errorf("name = %s, want list_dir", toolCalls[0].Name)
	}
}

// TestParseToolCallsStream_MultipleCalls 文本 + 工具调用 + 文本 + 工具调用
func TestParseToolCallsStream_MultipleCalls(t *testing.T) {
	in := make(chan string, 5)
	go func() {
		in <- "First, " +
			"[[TOOL]]{\"name\":\"a\",\"arguments\":{}}[[/TOOL]]" +
			" then " +
			"[[TOOL]]{\"name\":\"b\",\"arguments\":{\"x\":1}}[[/TOOL]]" +
			" done."
		close(in)
	}()

	var texts []string
	var toolCalls []ParsedToolCall
	for ev := range ParseToolCallsStream(in) {
		if ev.Text != "" {
			texts = append(texts, ev.Text)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
	}
	// 应该有 3 段文本：First, 、 then 、 done.
	if len(texts) != 3 {
		t.Errorf("texts = %q, want 3 segments", texts)
	} else {
		want := []string{"First, ", " then ", " done."}
		for i, w := range want {
			if texts[i] != w {
				t.Errorf("texts[%d] = %q, want %q", i, texts[i], w)
			}
		}
	}
	if len(toolCalls) != 2 {
		t.Fatalf("toolCalls = %v, want 2", toolCalls)
	}
	if toolCalls[0].Name != "a" || toolCalls[1].Name != "b" {
		t.Errorf("names = %s, %s, want a, b", toolCalls[0].Name, toolCalls[1].Name)
	}
}

// TestParseToolCallsStream_UnclosedTag 流结束时未闭标签，应回退为文本
func TestParseToolCallsStream_UnclosedTag(t *testing.T) {
	in := make(chan string, 5)
	go func() {
		in <- "hi [[TOOL]]"
		in <- `{"name": "x", "arguments": {}}`
		close(in)
	}()

	var texts []string
	var toolCalls []ParsedToolCall
	for ev := range ParseToolCallsStream(in) {
		if ev.Text != "" {
			texts = append(texts, ev.Text)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
	}
	// 流结束时 buffering 状态：JSON 应被解析为 tool_call
	if len(toolCalls) != 1 {
		t.Errorf("toolCalls = %v, want 1 (recovery)", toolCalls)
	}
	if len(texts) != 1 || texts[0] != "hi " {
		t.Errorf("texts = %q, want [hi ]", texts)
	}
}

// TestBuildCompletion_WithToolCalls 非流式响应里有 tool_call 标签
func TestBuildCompletion_WithToolCalls(t *testing.T) {
	tools := []Tool{{Type: "function"}}
	content := "Let me check.\n[[TOOL]]\n{\"name\":\"grep\",\"arguments\":{\"pattern\":\"foo\"}}\n[[/TOOL]]\n"
	result := BuildCompletion("req-1", "deepseek-chat", "", content, tools)

	choices := result["choices"].([]map[string]any)
	if len(choices) != 1 {
		t.Fatalf("choices len = %d, want 1", len(choices))
	}
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choices[0]["finish_reason"])
	}
	msg := choices[0]["message"].(map[string]any)
	tcs, ok := msg["tool_calls"].([]map[string]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("tool_calls = %v, want 1 call", msg["tool_calls"])
	}
	fn := tcs[0]["function"].(map[string]any)
	if fn["name"] != "grep" {
		t.Errorf("name = %s, want grep", fn["name"])
	}
	// content 应该是去掉标签后的纯文本
	if msg["content"] != "Let me check." {
		t.Errorf("content = %q, want 'Let me check.'", msg["content"])
	}
}

// TestBuildCompletion_WithFallbackToolCalls 兜底格式也应被识别
func TestBuildCompletion_WithFallbackToolCalls(t *testing.T) {
	tools := []Tool{{Type: "function"}}
	content := "我来分析当前目录的项目。先看看有哪些文件。\n[调用 LS] {\"path\": \"/home\"}"
	result := BuildCompletion("req-2", "deepseek-chat", "", content, tools)

	choices := result["choices"].([]map[string]any)
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choices[0]["finish_reason"])
	}
	msg := choices[0]["message"].(map[string]any)
	tcs, ok := msg["tool_calls"].([]map[string]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("tool_calls = %v, want 1 call", msg["tool_calls"])
	}
	fn := tcs[0]["function"].(map[string]any)
	if fn["name"] != "LS" {
		t.Errorf("name = %s, want LS", fn["name"])
	}
}

// TestBuildCompletion_NoToolCalls 没有标签，应返回 stop
func TestBuildCompletion_NoToolCalls(t *testing.T) {
	tools := []Tool{{Type: "function"}}
	content := "Just a normal response."
	result := BuildCompletion("req-3", "deepseek-chat", "", content, tools)

	choices := result["choices"].([]map[string]any)
	if choices[0]["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choices[0]["finish_reason"])
	}
	msg := choices[0]["message"].(map[string]any)
	if msg["content"] != content {
		t.Errorf("content = %v, want original", msg["content"])
	}
	if _, has := msg["tool_calls"]; has {
		t.Errorf("tool_calls should not be set")
	}
}
