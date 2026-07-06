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

	// 记录发送前的助手消息数量，便于定位"本次回复"
	beforeCount, _ := page.Locator(d.sel.AssistantMsg).Count()
	d.logger.Info("send_message: before assistant count", zap.Int("beforeCount", beforeCount))

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
	go d.streamReply(ctx, page, beforeCount, ch)
	return ch, nil
}

// jsGetReasoningAndContent 用 JavaScript 同时获取思维链和正文文本。
// DeepSeek 深度思考模式下，思维链在含 "think" class 的容器内，正文不在。
// 返回 {reasoning: "...", content: "..."}。
var jsGetText = `() => {
  const sel = '.ds-markdown--block, .ds-markdown, .markdown-body';
  const blocks = document.querySelectorAll(sel);
  let reasoning = '', content = '';
  for (const block of blocks) {
    const text = block.innerText || '';
    if (!text) continue;
    // 检查是否在 think/reason 容器内
    const thinkParent = block.closest('[class*="think"], [class*="Think"], [class*="reason"], [class*="Reason"]');
    if (thinkParent) {
      reasoning += text;
    } else {
      content += text;
    }
  }
  return {reasoning, content};
}`

// streamReply 轮询最新助手消息，推送思维链和正文增量。
// 用「停止」按钮消失判断生成完成（比文本稳定更可靠，不会在思维链→正文停顿时提前结束）。
// 全程用 JS evaluate 获取文本，避免 Locator.Count() 在某些页面状态下挂起。
func (d *DeepSeekDriver) streamReply(ctx context.Context, page playwright.Page, beforeCount int, ch chan<- StreamChunk) {
	defer close(ch)
	d.logger.Info("stream_reply: started", zap.Int("beforeCount", beforeCount))

	var lastReasoning, lastContent string
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	// 用 JS 同时检测：停止按钮是否存在 + 思维链/正文文本
	// 停止按钮存在 = 正在生成；消失 = 生成完成
	jsPoll := `() => {
	  const sel = '.ds-markdown--block, .ds-markdown, .markdown-body';
	  const blocks = document.querySelectorAll(sel);
	  let reasoning = '', content = '';
	  for (const block of blocks) {
	    const text = block.innerText || '';
	    if (!text) continue;
	    const thinkParent = block.closest('[class*="think"], [class*="Think"], [class*="reason"], [class*="Reason"]');
	    if (thinkParent) { reasoning += text; } else { content += text; }
	  }
	  // 检测停止按钮（正在生成时会出现）
	  let stopBtn = false;
	  const btns = document.querySelectorAll('div[role="button"]');
	  for (const b of btns) {
	    const t = (b.innerText || '').trim();
	    if (t === '停止') { stopBtn = true; break; }
	  }
	  return {reasoning, content, stopBtn};
	}`

	startTime := time.Now()
	maxWait := 600 // 最多 180s（600 ticks * 300ms）
	tick := 0
	lastLogTick := 0
	started := false

	for tick < maxWait {
		select {
		case <-ctx.Done():
			d.logger.Info("stream_reply: ctx done", zap.Int("tick", tick))
			return
		case <-ticker.C:
		}
		tick++

		// 用 JS 获取思维链、正文和停止按钮状态
		result, err := page.Evaluate(jsPoll)
		if err != nil {
			if tick%10 == 0 {
				d.logger.Warn("stream: evaluate failed",
					zap.Int("tick", tick), zap.Error(err))
			}
			continue
		}

		m, ok := result.(map[string]interface{})
		if !ok {
			if tick%10 == 0 {
				d.logger.Warn("stream: evaluate result not map",
					zap.Int("tick", tick), zap.Any("type", fmt.Sprintf("%T", result)))
			}
			continue
		}
		curReasoning, _ := m["reasoning"].(string)
		curContent, _ := m["content"].(string)
		stopBtn, _ := m["stopBtn"].(bool)

		// 标记生成已开始（有任何文本）
		if !started && (curReasoning != "" || curContent != "") {
			started = true
			d.logger.Info("stream: generation started",
				zap.Int("tick", tick),
				zap.Duration("waited", time.Since(startTime)),
				zap.Int("reasoning_len", len(curReasoning)),
				zap.Int("content_len", len(curContent)))
		}

		// 每 5s 记录一次状态
		if tick-lastLogTick >= 17 {
			d.logger.Info("stream: polling status",
				zap.Int("tick", tick),
				zap.Bool("started", started),
				zap.Bool("stopBtn", stopBtn),
				zap.Int("reasoning_len", len(curReasoning)),
				zap.Int("content_len", len(curContent)))
			lastLogTick = tick
		}

		// 推送思维链增量
		if curReasoning != lastReasoning {
			if len(curReasoning) > len(lastReasoning) && strings.HasPrefix(curReasoning, lastReasoning) {
				select {
				case ch <- StreamChunk{Reasoning: curReasoning[len(lastReasoning):]}:
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case ch <- StreamChunk{Reasoning: curReasoning}:
				case <-ctx.Done():
					return
				}
			}
			lastReasoning = curReasoning
		}

		// 推送正文增量
		if curContent != lastContent {
			if len(curContent) > len(lastContent) && strings.HasPrefix(curContent, lastContent) {
				select {
				case ch <- StreamChunk{Content: curContent[len(lastContent):]}:
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case ch <- StreamChunk{Content: curContent}:
				case <-ctx.Done():
					return
				}
			}
			lastContent = curContent
		}

		// 检查生成是否完成：停止按钮消失 + 已开始生成
		if started && !stopBtn {
			d.logger.Info("stream: stop button disappeared, finalizing",
				zap.Int("tick", tick),
				zap.Int("reasoning_len", len(lastReasoning)),
				zap.Int("content_len", len(lastContent)))
			// 停止按钮消失，再等 1s 确保最终文本稳定
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			// 最后再读一次，确保拿到最终文本
			result2, err := page.Evaluate(jsPoll)
			if err == nil {
				if m2, ok := result2.(map[string]interface{}); ok {
					finalReasoning, _ := m2["reasoning"].(string)
					finalContent, _ := m2["content"].(string)
					if len(finalReasoning) > len(lastReasoning) {
						select {
						case ch <- StreamChunk{Reasoning: finalReasoning[len(lastReasoning):]}:
						case <-ctx.Done():
							return
						}
						lastReasoning = finalReasoning
					}
					if len(finalContent) > len(lastContent) {
						select {
						case ch <- StreamChunk{Content: finalContent[len(lastContent):]}:
						case <-ctx.Done():
							return
						}
						lastContent = finalContent
					}
				}
			}
			d.logger.Info("stream_reply: completed",
				zap.Int("final_reasoning_len", len(lastReasoning)),
				zap.Int("final_content_len", len(lastContent)))
			return
		}
	}

	// 超时
	d.logger.Warn("stream: timeout",
		zap.Int("ticks", tick),
		zap.Bool("started", started),
		zap.Int("reasoning_len", len(lastReasoning)),
		zap.Int("content_len", len(lastContent)))
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
