package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

// DeepSeekDriver 封装对 DeepSeek 网页的单次会话操作。
//
// 流式实现策略：DOM 轮询。
// 监听网络 SSE 在 playwright-go 中无法直接拿到增量（Body() 会阻塞到结束），
// 因此改为在发送后轮询最新一条助手消息的文本，与上次比较产出增量，
// 直到"停止"按钮消失表示生成完成。这种方式抗改版能力较强。
type DeepSeekDriver struct {
	sess   *BrowserSession
	sel    Selectors
	logger *zap.Logger
	mc     ModelConfig // 模式与开关配置
}

func NewDeepSeekDriver(sess *BrowserSession, sel Selectors, logger *zap.Logger) *DeepSeekDriver {
	return &DeepSeekDriver{sess: sess, sel: sel, logger: logger, mc: ModelConfig{Mode: "default"}}
}

// SetModelConfig 设置模式与开关配置（在 SendMessage 前调用）
func (d *DeepSeekDriver) SetModelConfig(mc ModelConfig) {
	d.mc = mc
}

// ensurePage 懒创建并打开 DeepSeek 页面
func (d *DeepSeekDriver) ensurePage(ctx context.Context) error {
	// 如果已有页面且未关闭，先尝试用 JS 检测是否健康
	page := d.sess.Page()
	if page != nil && !page.IsClosed() {
		// 快速检测页面是否可用（有 textarea）
		result, err := page.Evaluate(`() => document.querySelectorAll('textarea').length`)
		if err == nil {
			if n, ok := result.(int); ok && n > 0 {
				return nil // 页面可用
			}
		}
		// 页面不可用，关闭后重建
		d.logger.Warn("ensure_page: existing page unhealthy, recreating")
		_ = page.Close()
		d.sess.SetPage(nil)
	}

	// 最多重试 2 次
	for attempt := 0; attempt < 2; attempt++ {
		p, err := d.sess.Ctx.NewPage()
		if err != nil {
			return err
		}
		d.sess.SetPage(p)
		d.logger.Info("ensure_page: navigating to chat.deepseek.com", zap.Int("attempt", attempt+1))
		if _, err := p.Goto("https://chat.deepseek.com", playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(30000),
		}); err != nil {
			d.logger.Error("ensure_page: goto failed", zap.Error(err))
			_ = p.Close()
			d.sess.SetPage(nil)
			continue
		}
		// DeepSeek 未登录会重定向到 /sign_in，先检查 URL
		currentURL := p.URL()
		d.logger.Info("ensure_page: page loaded", zap.String("url", currentURL))
		if strings.Contains(currentURL, "/sign_in") {
			_ = p.Close()
			d.sess.SetPage(nil)
			return ErrSessionExpired
		}
		// 等待输入框出现，作为页面就绪标志
		if _, err := p.WaitForSelector(d.sel.ChatInput, playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(45000),
		}); err != nil {
			// 记录页面诊断信息
			title, _ := p.Title()
			// 用 JS 检测页面上有哪些元素
			diag, _ := p.Evaluate(`() => ({
				url: location.href,
				title: document.title,
				textareaCount: document.querySelectorAll('textarea').length,
				inputCount: document.querySelectorAll('input').length,
				buttonCount: document.querySelectorAll('button, div[role="button"]').length,
				bodyText: (document.body.innerText || '').substring(0, 500)
			})`)
			d.logger.Error("ensure_page: wait for chat input failed",
				zap.String("url", currentURL),
				zap.String("title", title),
				zap.String("selector", d.sel.ChatInput),
				zap.Any("diag", diag),
				zap.Error(err))
			_ = p.Close()
			d.sess.SetPage(nil)
			continue
		}
		d.logger.Info("ensure_page: chat input found, page ready")
		return nil
	}
	return errors.New("ensure_page: failed after 2 attempts")
}

// SendMessage 发送消息，返回增量 StreamChunk channel；channel 关闭表示生成完成。
// StreamChunk 包含 Reasoning（思维链）和 Content（正文）两个增量字段。
func (d *DeepSeekDriver) SendMessage(ctx context.Context, text string) (<-chan StreamChunk, error) {
	if err := d.ensurePage(ctx); err != nil {
		return nil, err
	}
	page := d.sess.Page()

	// 切换模式与开关（在发送前，页面已就绪）
	if err := d.ApplyMode(page, d.mc); err != nil {
		d.logger.Warn("apply mode failed, continue with current mode",
			zap.String("mode", d.mc.Mode), zap.Error(err))
	}
	d.logger.Info("send_message: apply mode done",
		zap.String("mode", d.mc.Mode),
		zap.Bool("thinking", d.mc.Thinking),
		zap.Bool("search", d.mc.Search),
		zap.Int("prompt_len", len(text)))

	// 给发送前已有的回复节点做快照。不能依赖节点数量判断新回复：
	// DeepSeek 会复用/虚拟化历史消息 DOM，新增一轮后节点总数可能保持不变。
	snapshot, err := captureReplySnapshot(page, d.sel.AssistantMsg)
	if err != nil {
		d.logger.Error("send_message: capture reply snapshot failed", zap.Error(err))
		return nil, fmt.Errorf("capture reply snapshot: %w", err)
	}
	d.logger.Info("send_message: reply snapshot captured",
		zap.Int("main_count", snapshot.MainCount),
		zap.Int("reasoning_count", snapshot.ReasoningCount),
		zap.Int("content_len", len(snapshot.Content)),
		zap.Int("reasoning_len", len(snapshot.Reasoning)))

	// 输入文本（兼容 textarea 与 contenteditable）
	if err := fillInput(page, d.sel.ChatInput, text); err != nil {
		d.logger.Error("send_message: fill input failed", zap.Error(err))
		return nil, err
	}
	d.logger.Info("send_message: input filled")

	// 等待 300ms 让 React 更新按钮状态
	time.Sleep(300 * time.Millisecond)

	// 优先按 Enter 发送（更可靠，不依赖 send button 选择器）
	enterSent := false
	if err := page.Keyboard().Press("Enter"); err == nil {
		enterSent = true
		d.logger.Info("send_message: pressed Enter to send")
	}

	// 如果 Enter 失败，回退到点击发送按钮
	if !enterSent {
		if err := page.Click(d.sel.SendButton, playwright.PageClickOptions{
			Timeout: playwright.Float(5000),
		}); err != nil {
			d.logger.Error("send_message: both Enter and send button click failed", zap.Error(err))
			return nil, err
		}
		d.logger.Info("send_message: send button clicked, starting streamReply")
	} else {
		d.logger.Info("send_message: starting streamReply")
	}

	ch := make(chan StreamChunk, 32)
	go d.streamReply(ctx, page, snapshot, ch)
	return ch, nil
}

const replySnapshotAttribute = "data-deepseek-api-snapshot"

type jsEvaluator interface {
	Evaluate(expression string, arg ...interface{}) (interface{}, error)
}

type replySnapshot struct {
	Marker         string
	Reasoning      string
	Content        string
	MainCount      int
	ReasoningCount int
}

type replyPollResult struct {
	Reasoning      string
	Content        string
	TurnDetected   bool
	Generating     bool
	MainCount      int
	ReasoningCount int
}

type replyStreamConfig struct {
	PollInterval          time.Duration
	StableAfterStop       time.Duration
	StableWithoutControl  time.Duration
	ReasoningOnlyStable   time.Duration
	StaleControlStable    time.Duration
	StaleReasoningControl time.Duration
	MaxWait               time.Duration
}

var defaultReplyStreamConfig = replyStreamConfig{
	PollInterval:          300 * time.Millisecond,
	StableAfterStop:       1200 * time.Millisecond,
	StableWithoutControl:  4 * time.Second,
	ReasoningOnlyStable:   10 * time.Second,
	StaleControlStable:    12 * time.Second,
	StaleReasoningControl: 20 * time.Second,
	MaxWait:               3 * time.Minute,
}

// captureReplySnapshot 标记发送前已有的回复节点并保存最后一条文本。
// 新一轮回复即使复用了 DOM 节点，也能通过文本相对快照的变化识别。
func captureReplySnapshot(page jsEvaluator, assistantSelector string) (replySnapshot, error) {
	marker := fmt.Sprintf("turn-%d", time.Now().UnixNano())
	script := buildReplySnapshotScript(assistantSelector, marker)
	result, err := page.Evaluate(script)
	if err != nil {
		return replySnapshot{}, err
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		return replySnapshot{}, fmt.Errorf("unexpected snapshot result type %T", result)
	}
	return replySnapshot{
		Marker:         marker,
		Reasoning:      stringValue(m["reasoning"]),
		Content:        stringValue(m["content"]),
		MainCount:      intValue(m["mainCount"]),
		ReasoningCount: intValue(m["reasoningCount"]),
	}, nil
}

func buildReplySnapshotScript(assistantSelector, marker string) string {
	return fmt.Sprintf(`() => {
	  const assistantSelector = %q;
	  const marker = %q;
	  const textOf = (node) => node ? (node.innerText || node.textContent || '') : '';
	  const isReasoning = (node) => !!(node && node.closest(
	    '[class*="think"], [class*="Think"], [class*="reason"], [class*="Reason"]'
	  ));
	  const legacyNodes = Array.from(document.querySelectorAll(assistantSelector));
	  const preferredMain = Array.from(document.querySelectorAll('.ds-assistant-message-main-content'));
	  const mainNodes = preferredMain.length
	    ? preferredMain
	    : legacyNodes.filter((node) => !isReasoning(node));
	  const reasoningNodes = legacyNodes.filter(isReasoning);
	  const nodesToMark = new Set([...legacyNodes, ...preferredMain]);
	  for (const node of nodesToMark) {
	    node.setAttribute(%q, marker);
	  }
	  const currentMain = mainNodes.length ? mainNodes[mainNodes.length - 1] : null;
	  const currentReasoning = reasoningNodes.length
	    ? reasoningNodes[reasoningNodes.length - 1]
	    : null;
	  return {
	    content: textOf(currentMain),
	    reasoning: textOf(currentReasoning),
	    mainCount: mainNodes.length,
	    reasoningCount: reasoningNodes.length
	  };
	}`, assistantSelector, marker, replySnapshotAttribute)
}

func buildReplyPollScript(assistantSelector string, snapshot replySnapshot) string {
	return fmt.Sprintf(`() => {
	  const assistantSelector = %q;
	  const marker = %q;
	  const baselineContent = %q;
	  const baselineReasoning = %q;
	  const textOf = (node) => node ? (node.innerText || node.textContent || '') : '';
	  const isReasoning = (node) => !!(node && node.closest(
	    '[class*="think"], [class*="Think"], [class*="reason"], [class*="Reason"]'
	  ));
	  const isVisible = (node) => {
	    if (!node || node.getAttribute('aria-hidden') === 'true') return false;
	    const style = window.getComputedStyle(node);
	    if (style.display === 'none' || style.visibility === 'hidden' || Number(style.opacity) === 0) {
	      return false;
	    }
	    const rect = node.getBoundingClientRect();
	    return rect.width > 0 && rect.height > 0;
	  };
	  const isNew = (node, text, baseline) => !!node && (
	    node.getAttribute(%q) !== marker || text !== baseline
	  );

	  const legacyNodes = Array.from(document.querySelectorAll(assistantSelector));
	  const preferredMain = Array.from(document.querySelectorAll('.ds-assistant-message-main-content'));
	  const mainNodes = preferredMain.length
	    ? preferredMain
	    : legacyNodes.filter((node) => !isReasoning(node));
	  const reasoningNodes = legacyNodes.filter(isReasoning);
	  const currentMain = mainNodes.length ? mainNodes[mainNodes.length - 1] : null;
	  const currentReasoning = reasoningNodes.length
	    ? reasoningNodes[reasoningNodes.length - 1]
	    : null;
	  const currentContent = textOf(currentMain);
	  const currentReasoningText = textOf(currentReasoning);
	  const contentIsNew = isNew(currentMain, currentContent, baselineContent);
	  const reasoningIsNew = isNew(currentReasoning, currentReasoningText, baselineReasoning);
	  const turnDetected = contentIsNew || reasoningIsNew;

	  let generating = false;
	  const controls = document.querySelectorAll('button, [role="button"]');
	  for (const control of controls) {
	    if (!isVisible(control)) continue;
	    const label = [
	      control.innerText,
	      control.textContent,
	      control.getAttribute('aria-label'),
	      control.getAttribute('title')
	    ].filter(Boolean).join(' ').replace(/\s+/g, '').toLowerCase();
	    if (label.includes('停止') || label.includes('中止') ||
	        label.includes('stop') || label.includes('cancelgeneration')) {
	      generating = true;
	      break;
	    }
	  }

	  return {
	    reasoning: turnDetected && reasoningIsNew ? currentReasoningText : '',
	    content: turnDetected && contentIsNew ? currentContent : '',
	    turnDetected,
	    generating,
	    mainCount: mainNodes.length,
	    reasoningCount: reasoningNodes.length
	  };
	}`, assistantSelector, snapshot.Marker, snapshot.Content, snapshot.Reasoning, replySnapshotAttribute)
}

func decodeReplyPoll(result interface{}) (replyPollResult, error) {
	m, ok := result.(map[string]interface{})
	if !ok {
		return replyPollResult{}, fmt.Errorf("unexpected poll result type %T", result)
	}
	return replyPollResult{
		Reasoning:      stringValue(m["reasoning"]),
		Content:        stringValue(m["content"]),
		TurnDetected:   boolValue(m["turnDetected"]),
		Generating:     boolValue(m["generating"]),
		MainCount:      intValue(m["mainCount"]),
		ReasoningCount: intValue(m["reasoningCount"]),
	}, nil
}

func stringValue(v interface{}) string {
	s, _ := v.(string)
	return s
}

func boolValue(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func intValue(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

// streamReply 轮询本轮助手回复，推送思维链和正文增量。
// 完成判断优先使用可见的生成控制按钮，并以文本稳定窗口兜底。
func (d *DeepSeekDriver) streamReply(ctx context.Context, page jsEvaluator, snapshot replySnapshot, ch chan<- StreamChunk) {
	d.streamReplyWithConfig(ctx, page, snapshot, ch, defaultReplyStreamConfig)
}

func (d *DeepSeekDriver) streamReplyWithConfig(
	ctx context.Context,
	page jsEvaluator,
	snapshot replySnapshot,
	ch chan<- StreamChunk,
	cfg replyStreamConfig,
) {
	defer close(ch)
	d.logger.Info("stream_reply: started",
		zap.Int("main_count", snapshot.MainCount),
		zap.Int("reasoning_count", snapshot.ReasoningCount))

	var lastReasoning, lastContent string
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	jsPoll := buildReplyPollScript(d.sel.AssistantMsg, snapshot)

	startTime := time.Now()
	deadline := startTime.Add(cfg.MaxWait)
	tick := 0
	lastLogTick := 0
	started := false
	generationObserved := false
	lastChangedAt := time.Time{}
	var lastPoll replyPollResult

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			d.logger.Info("stream_reply: ctx done",
				zap.Int("tick", tick),
				zap.Bool("started", started),
				zap.Error(ctx.Err()))
			return
		case <-ticker.C:
		}
		tick++

		result, err := page.Evaluate(jsPoll)
		if err != nil {
			if tick%10 == 0 {
				d.logger.Warn("stream: evaluate failed",
					zap.Int("tick", tick), zap.Error(err))
			}
			continue
		}

		poll, err := decodeReplyPoll(result)
		if err != nil {
			if tick%10 == 0 {
				d.logger.Warn("stream: evaluate result not map",
					zap.Int("tick", tick), zap.Error(err))
			}
			continue
		}
		lastPoll = poll
		if poll.Generating {
			generationObserved = true
		}

		// 必须确认文本属于本轮，避免把历史回复当作新回复返回。
		if !started && poll.TurnDetected && (poll.Reasoning != "" || poll.Content != "") {
			started = true
			lastChangedAt = time.Now()
			d.logger.Info("stream: generation started",
				zap.Int("tick", tick),
				zap.Duration("waited", time.Since(startTime)),
				zap.Bool("generating", poll.Generating),
				zap.Int("reasoning_len", len(poll.Reasoning)),
				zap.Int("content_len", len(poll.Content)))
		}

		// 每 5s 记录一次状态
		if tick-lastLogTick >= 17 {
			d.logger.Info("stream: polling status",
				zap.Int("tick", tick),
				zap.Bool("started", started),
				zap.Bool("turn_detected", poll.TurnDetected),
				zap.Bool("generating", poll.Generating),
				zap.Bool("generation_observed", generationObserved),
				zap.Int("main_count", poll.MainCount),
				zap.Int("reasoning_count", poll.ReasoningCount),
				zap.Int("reasoning_len", len(poll.Reasoning)),
				zap.Int("content_len", len(poll.Content)))
			lastLogTick = tick
		}

		// DOM 重排时本轮节点可能短暂消失。空值不用于回退已捕获文本，
		// 否则节点恢复后会把完整回复再次当增量发送。
		if poll.TurnDetected && poll.Reasoning != "" && poll.Reasoning != lastReasoning {
			if len(poll.Reasoning) > len(lastReasoning) && strings.HasPrefix(poll.Reasoning, lastReasoning) {
				select {
				case ch <- StreamChunk{Reasoning: poll.Reasoning[len(lastReasoning):]}:
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case ch <- StreamChunk{Reasoning: poll.Reasoning}:
				case <-ctx.Done():
					return
				}
			}
			lastReasoning = poll.Reasoning
			lastChangedAt = time.Now()
		}

		// 推送正文增量
		if poll.TurnDetected && poll.Content != "" && poll.Content != lastContent {
			if len(poll.Content) > len(lastContent) && strings.HasPrefix(poll.Content, lastContent) {
				select {
				case ch <- StreamChunk{Content: poll.Content[len(lastContent):]}:
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case ch <- StreamChunk{Content: poll.Content}:
				case <-ctx.Done():
					return
				}
			}
			lastContent = poll.Content
			lastChangedAt = time.Now()
		}

		if started {
			stableFor := time.Since(lastChangedAt)
			if complete, reason := shouldFinalizeReply(
				poll, generationObserved, lastContent != "", stableFor, cfg,
			); complete {
				d.logger.Info("stream: reply stable, finalizing",
					zap.String("reason", reason),
					zap.Int("tick", tick),
					zap.Duration("stable_for", stableFor),
					zap.Int("reasoning_len", len(lastReasoning)),
					zap.Int("content_len", len(lastContent)))
				d.logger.Info("stream_reply: completed",
					zap.Int("final_reasoning_len", len(lastReasoning)),
					zap.Int("final_content_len", len(lastContent)))
				return
			}
		}
	}

	d.logger.Warn("stream: timeout",
		zap.Int("ticks", tick),
		zap.Bool("started", started),
		zap.Bool("turn_detected", lastPoll.TurnDetected),
		zap.Bool("generating", lastPoll.Generating),
		zap.Int("main_count", lastPoll.MainCount),
		zap.Int("reasoning_count", lastPoll.ReasoningCount),
		zap.Int("reasoning_len", len(lastReasoning)),
		zap.Int("content_len", len(lastContent)))
}

func shouldFinalizeReply(
	poll replyPollResult,
	generationObserved bool,
	hasContent bool,
	stableFor time.Duration,
	cfg replyStreamConfig,
) (bool, string) {
	if generationObserved && !poll.Generating && stableFor >= cfg.StableAfterStop {
		return true, "generation control disappeared"
	}
	if !generationObserved && !poll.Generating {
		required := cfg.ReasoningOnlyStable
		if hasContent {
			required = cfg.StableWithoutControl
		}
		if stableFor >= required {
			return true, "text stable without generation control"
		}
	}
	if poll.Generating {
		required := cfg.StaleReasoningControl
		if hasContent {
			required = cfg.StaleControlStable
		}
		if stableFor >= required {
			return true, "generation control stale"
		}
	}
	return false, ""
}

// fillInput 兼容 textarea 与 contenteditable 的输入。
// 用 React 友好的方式设置值：先 Focus + Click 聚焦，再用原生 setter 设值并派发 input 事件，
// 确保 React 的 onChange 被触发（否则 send 按钮可能保持 disabled）。
func fillInput(page playwright.Page, selector, text string) error {
	el, err := page.QuerySelector(selector)
	if err != nil {
		return errors.New("chat input not found: " + err.Error())
	}
	if el == nil {
		return errors.New("chat input not found")
	}
	// 先聚焦
	_ = el.Click()

	// 用 JS 原生 setter 设值并派发 input 事件（React 兼容）
	// 同时支持 textarea 和 contenteditable
	jsSet := `function(text) {
		const el = this;
		if (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT') {
			const nativeSetter = Object.getOwnPropertyDescriptor(
				window.HTMLTextAreaElement.prototype, 'value'
			).set;
			nativeSetter.call(el, text);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
		} else {
			// contenteditable
			el.innerText = text;
			el.dispatchEvent(new InputEvent('input', { bubbles: true, data: text, inputType: 'insertText' }));
		}
	}`
	result, err := el.Evaluate(jsSet, text)
	if err == nil && result != nil {
		return nil
	}

	// JS 方式失败：回退到 Fill
	if err := el.Fill(text); err == nil {
		return nil
	}

	// 最后兜底：逐字输入
	if err := el.Click(); err != nil {
		return err
	}
	return page.Keyboard().Type(text, playwright.KeyboardTypeOptions{Delay: playwright.Float(0)})
}

// HealthCheck 打开页面探测登录态是否有效
func (d *DeepSeekDriver) HealthCheck(ctx context.Context) (bool, error) {
	if err := d.ensurePage(ctx); err != nil {
		if IsSessionExpired(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
