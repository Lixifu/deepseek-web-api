package core

import (
	"fmt"
	"strings"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

// ModelConfig 描述一次对话的模式与开关
type ModelConfig struct {
	Mode     string // "default" / "expert" / "vision"
	Thinking bool   // 深度思考
	Search   bool   // 智能搜索
}

// ModelName 返回 OpenAI 兼容的 model 名称
// 格式: deepseek-<mode>[-think][-search]
// 例: deepseek-chat / deepseek-chat-think / deepseek-expert-think-search
func (m ModelConfig) ModelName() string {
	var prefix string
	switch m.Mode {
	case "expert":
		prefix = "deepseek-expert"
	case "vision":
		prefix = "deepseek-vision"
	default:
		prefix = "deepseek-chat"
	}
	if m.Thinking {
		prefix += "-think"
	}
	if m.Search {
		prefix += "-search"
	}
	return prefix
}

// ParseModelName 从 model 字符串解析出 ModelConfig
// 支持的格式: deepseek-chat[-think][-search] / deepseek-expert[-think][-search] / deepseek-vision
func ParseModelName(model string) ModelConfig {
	mc := ModelConfig{Mode: "default"}
	lower := strings.ToLower(strings.TrimSpace(model))

	switch {
	case lower == "deepseek-reasoner":
		mc.Mode = "default"
		mc.Thinking = true
		return mc
	case strings.HasPrefix(lower, "deepseek-expert"):
		mc.Mode = "expert"
	case strings.HasPrefix(lower, "deepseek-vision"):
		mc.Mode = "vision"
	default:
		mc.Mode = "default"
	}

	mc.Thinking = strings.Contains(lower, "think")
	mc.Search = strings.Contains(lower, "search")
	return mc
}

// SupportedModels 返回所有受支持的 model 名称（供 /v1/models 接口）
func SupportedModels() []string {
	modes := []string{"chat", "expert", "vision"}
	suffixes := [][]string{{}, {"think"}, {"search"}, {"think", "search"}}
	var list []string
	for _, mode := range modes {
		for _, suf := range suffixes {
			name := "deepseek-" + mode
			for _, s := range suf {
				name += "-" + s
			}
			list = append(list, name)
		}
	}
	// vision 不支持 think/search
	return []string{
		"deepseek-chat",
		"deepseek-reasoner",
		"deepseek-chat-think",
		"deepseek-chat-search",
		"deepseek-chat-think-search",
		"deepseek-expert",
		"deepseek-expert-think",
		"deepseek-expert-search",
		"deepseek-expert-think-search",
		"deepseek-vision",
	}
}

// IsSupportedModel 检查请求模型是否在公开支持列表中。
func IsSupportedModel(model string) bool {
	for _, supported := range SupportedModels() {
		if model == supported {
			return true
		}
	}
	return false
}

// ApplyMode 在 DeepSeek 页面上切换模式与开关
// 仅当目标状态与当前状态不同时才点击
func (d *DeepSeekDriver) ApplyMode(page playwright.Page, mc ModelConfig) error {
	// 1. 切换模式（radio）
	modeType := mc.Mode
	if modeType == "" {
		modeType = "default"
	}
	sel := fmt.Sprintf(d.sel.ModelRadio, modeType)
	radio, err := page.QuerySelector(sel)
	if err != nil {
		return fmt.Errorf("query model radio: %w", err)
	}
	if radio == nil {
		return fmt.Errorf("model radio not found: %s", modeType)
	}
	checked, _ := radio.GetAttribute("aria-checked")
	if checked != "true" {
		if err := radio.Click(); err != nil {
			return fmt.Errorf("click model radio: %w", err)
		}
		d.logger.Info("model mode switched",
			zap.String("mode", modeType))
	}

	// 2. 切换深度思考
	if err := d.toggleIfNeed(page, d.sel.ThinkingToggle, mc.Thinking, "thinking"); err != nil {
		return err
	}

	// 3. 切换智能搜索
	if err := d.toggleIfNeed(page, d.sel.SearchToggle, mc.Search, "search"); err != nil {
		return err
	}

	return nil
}

// toggleIfNeed 当目标状态与当前不同时点击切换
func (d *DeepSeekDriver) toggleIfNeed(page playwright.Page, selector string, want bool, name string) error {
	el, err := page.QuerySelector(selector)
	if err != nil {
		d.logger.Warn("query toggle failed", zap.String("name", name), zap.Error(err))
		return nil // 非致命，继续
	}
	if el == nil {
		// 开关不存在（可能页面未加载完或该模式不支持），跳过
		return nil
	}
	cls, _ := el.GetAttribute("class")
	isSelected := strings.Contains(cls, "ds-toggle-button--selected")
	if isSelected != want {
		if err := el.Click(); err != nil {
			d.logger.Warn("click toggle failed", zap.String("name", name), zap.Error(err))
			return nil
		}
		d.logger.Info("toggle switched",
			zap.String("name", name), zap.Bool("want", want))
	}
	return nil
}
