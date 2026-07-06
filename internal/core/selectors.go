package core

// Selectors DeepSeek 页面 DOM 选择器，集中维护便于热更新。
// 注：DeepSeek 前端会改版，应定期用管理后台的"健康检查"验证。
type Selectors struct {
	ChatInput       string // 输入框（textarea 或 contenteditable）
	SendButton      string // 发送按钮
	AssistantMsg    string // 助手消息容器（取最后一个）
	StopButton      string // 生成中的"停止"按钮（出现表示正在输出）
	LoginIndicator  string // 已登录态指示（如头像/用户菜单）
	ModelRadio      string // 模式单选：[data-model-type='default'|'expert'|'vision']
	ThinkingToggle  string // 深度思考开关
	SearchToggle    string // 智能搜索开关
}

// DefaultSelectors 默认选择器，按 2026-07 实测 chat.deepseek.com 校正
// DeepSeek 用 div role="button" 替代 <button>，无 aria-label，无 id
// 模式：快速(default)/专家(expert)/识图(vision)，通过 [data-model-type] radio 切换
// 开关：深度思考/智能搜索，通过 ds-toggle-button 切换，--selected 表示已开启
var DefaultSelectors = Selectors{
	ChatInput:      `textarea`,
	SendButton:     `div[role='button'].ds-button--primary.ds-button--filled.ds-button--circle`,
	AssistantMsg:   ".ds-markdown--block, .ds-markdown, .markdown-body",
	StopButton:     `div[role='button'].ds-button--primary.ds-button--filled:has-text('停止')`,
	LoginIndicator: ".ds-sign-in-form-wrapper",
	ModelRadio:     `[role='radio'][data-model-type='%s']`,
	ThinkingToggle: `div.ds-toggle-button:has-text('深度思考')`,
	SearchToggle:   `div.ds-toggle-button:has-text('智能搜索')`,
}
