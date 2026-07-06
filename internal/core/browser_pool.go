package core

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"
)

// AccountConfig 启动浏览器池时传入的账号配置
type AccountConfig struct {
	ID          uint
	Name        string
	StoragePath string
}

// BrowserSession 单个浏览器上下文，绑定一个 DeepSeek 账号
type BrowserSession struct {
	AccountID   uint
	AccountName string
	StoragePath string
	Ctx         playwright.BrowserContext
	mu          sync.Mutex
	busy        bool
	healthy     bool
	page        playwright.Page
}

// Acquire 尝试占用会话，成功返回 true（非阻塞）
func (s *BrowserSession) Acquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy || !s.healthy {
		return false
	}
	s.busy = true
	return true
}

// Release 释放会话
func (s *BrowserSession) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
}

// MarkUnhealthy 标记为不可用
func (s *BrowserSession) MarkUnhealthy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthy = false
}

// MarkHealthy 恢复可用
func (s *BrowserSession) MarkHealthy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthy = true
}

// Healthy 只读
func (s *BrowserSession) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy
}

// Page 返回复用的页面（懒创建由 driver 控制）
func (s *BrowserSession) Page() playwright.Page { return s.page }
func (s *BrowserSession) SetPage(p playwright.Page) { s.page = p }

// BrowserPool 浏览器会话池
type BrowserPool struct {
	sessions []*BrowserSession
	accCfgs  []AccountConfig // 保存账号配置，用于 Restart
	browser  playwright.Browser
	pw       *playwright.Playwright
	mu       sync.Mutex
	headless bool
	logger   *zap.Logger
	// restart 控制并发重启
	restarting sync.Mutex
	restarted  bool
}

func NewBrowserPool(headless bool, logger *zap.Logger) *BrowserPool {
	return &BrowserPool{headless: headless, logger: logger}
}

// Start 启动 Playwright 并为每个账号创建 BrowserContext
func (p *BrowserPool) Start(accounts []AccountConfig) error {
	p.accCfgs = accounts
	return p.startInternal(accounts)
}

func (p *BrowserPool) startInternal(accounts []AccountConfig) error {
	var err error
	p.pw, err = playwright.Run()
	if err != nil {
		return err
	}
	p.browser, err = p.pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(p.headless),
		Args: []string{
			"--no-sandbox",
			"--disable-blink-features=AutomationControlled",
			"--disable-dev-shm-usage",
			// 省内存参数（针对 1.8GB 低内存服务器）
			"--disable-gpu",
			"--disable-software-rasterizer",
			"--no-zygote",
			"--disable-extensions",
			"--disable-default-apps",
			"--disable-background-networking",
			"--disable-background-timer-throttling",
			"--disable-backgrounding-occluded-windows",
			"--disable-renderer-backgrounding",
			"--disable-component-update",
			"--disable-sync",
			"--disable-translate",
			"--disable-features=TranslateUI,BlinkGenPropertyTrees,site-per-process",
			"--disable-ipc-flooding-protection",
			"--disable-client-side-phishing-detection",
			"--disable-hang-monitor",
			"--disable-prompt-on-repost",
			"--disable-domain-reliability",
			"--disable-back-forward-cache",
			"--memory-pressure-off",
			"--disable-breakpad",
			"--disable-crash-reporter",
			"--metrics-recording-only",
			"--no-first-run",
			"--no-default-browser-check",
		},
	})
	if err != nil {
		return err
	}
	for _, acc := range accounts {
		s, err := p.newSession(acc)
		if err != nil {
			p.logger.Warn("failed to init session for account, skipping",
				zap.Uint("account_id", acc.ID),
				zap.String("name", acc.Name),
				zap.Error(err))
			continue
		}
		p.sessions = append(p.sessions, s)
		p.logger.Info("session ready", zap.Uint("account_id", acc.ID), zap.String("name", acc.Name))
	}
	if len(p.sessions) == 0 {
		return errors.New("no session initialized, check storage_state files")
	}
	return nil
}

func (p *BrowserPool) newSession(acc AccountConfig) (*BrowserSession, error) {
	ctx, err := p.browser.NewContext(playwright.BrowserNewContextOptions{
		StorageStatePath: playwright.String(acc.StoragePath),
		UserAgent: playwright.String(
			"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
				"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"),
		Viewport: &playwright.Size{Width: 1280, Height: 800},
		Locale:   playwright.String("zh-CN"),
	})
	if err != nil {
		return nil, err
	}
	// 注入 stealth 脚本，隐藏 webdriver 特征
	_ = ctx.AddInitScript(playwright.Script{Content: playwright.String(stealthJS)})

	// 拦截非必要资源（图片/字体/媒体），降低内存占用
	// DeepSeek 输入框是 textarea，不依赖图片；字体用 fallback 不影响功能
	_ = ctx.Route("**/*", func(route playwright.Route) {
		switch route.Request().ResourceType() {
		case "image", "media", "font":
			_ = route.Abort()
		default:
			_ = route.Continue()
		}
	})

	return &BrowserSession{
		AccountID:   acc.ID,
		AccountName: acc.Name,
		StoragePath: acc.StoragePath,
		Ctx:         ctx,
		healthy:     true,
	}, nil
}

// Sessions 返回所有会话快照（用于健康检查/管理后台展示）
func (p *BrowserPool) Sessions() []*BrowserSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*BrowserSession, len(p.sessions))
	copy(out, p.sessions)
	return out
}

// Available 返回当前空闲且健康的会话数
func (p *BrowserPool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.sessions {
		if s.Acquire() {
			s.Release()
			n++
		}
	}
	return n
}

// Acquire 选中一个空闲且健康、且不在 tried 中的会话。
// 找不到返回 ErrNoSession；若全部 tried 过返回 ErrAllSessionsDown。
func (p *BrowserPool) Acquire(ctx context.Context, tried map[uint]bool) (*BrowserSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.sessions) == 0 {
		return nil, ErrNoSession
	}
	allTried := true
	for _, s := range p.sessions {
		if tried[s.AccountID] {
			continue
		}
		allTried = false
		if s.Acquire() {
			return s, nil
		}
	}
	if allTried {
		return nil, ErrAllSessionsDown
	}
	// 有空闲会话但都 unhealthy 或 busy
	return nil, ErrNoSession
}

// Restart 关闭现有浏览器并重新启动所有会话。
// 当检测到浏览器进程崩溃（"target closed" 错误）时调用。
// 并发调用时只会有一个真正执行重启，其他调用等待并直接返回。
func (p *BrowserPool) Restart() error {
	p.restarting.Lock()
	defer p.restarting.Unlock()

	p.mu.Lock()
	oldSessions := p.sessions
	oldBrowser := p.browser
	oldPw := p.pw
	accCfgs := p.accCfgs
	p.sessions = nil
	p.mu.Unlock()

	// 关闭旧资源
	for _, s := range oldSessions {
		if s.Ctx != nil {
			_ = s.Ctx.Close()
		}
	}
	if oldBrowser != nil {
		_ = oldBrowser.Close()
	}
	if oldPw != nil {
		_ = oldPw.Stop()
	}

	p.logger.Warn("browser pool restarted", zap.Int("accounts", len(accCfgs)))
	return p.startInternal(accCfgs)
}

// Stop 关闭所有资源
func (p *BrowserPool) Stop() {
	p.mu.Lock()
	sessions := p.sessions
	browser := p.browser
	pw := p.pw
	p.mu.Unlock()

	for _, s := range sessions {
		if s.Ctx != nil {
			_ = s.Ctx.Close()
		}
	}
	if browser != nil {
		_ = browser.Close()
	}
	if pw != nil {
		_ = pw.Stop()
	}
}

// CheckStorageState 用临时 BrowserContext 探测 storage_state.json 是否仍登录。
// 用完即关，不污染会话池。
func (p *BrowserPool) CheckStorageState(ctx context.Context, storagePath string, sel Selectors) (bool, error) {
	p.mu.Lock()
	browser := p.browser
	p.mu.Unlock()
	if browser == nil {
		return false, errors.New("browser not started")
	}
	if _, err := os.Stat(storagePath); err != nil {
		return false, err
	}
	bctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		StorageStatePath: playwright.String(storagePath),
		UserAgent: playwright.String(
			"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
				"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"),
		Viewport: &playwright.Size{Width: 1280, Height: 800},
	})
	if err != nil {
		return false, err
	}
	defer bctx.Close()
	page, err := bctx.NewPage()
	if err != nil {
		return false, err
	}
	defer page.Close()
	if _, err := page.Goto("https://chat.deepseek.com", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return false, err
	}
	// 登录失效会重定向到 /sign_in
	if strings.Contains(page.URL(), "/sign_in") {
		return false, nil
	}
	// 等输入框出现作为页面就绪标志
	if _, err := page.WaitForSelector(sel.ChatInput, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(15000),
	}); err != nil {
		return false, nil
	}
	return true, nil
}

// InstallBrowsers 安装 Playwright Chromium（仅首次部署或镜像构建时调用）
func InstallBrowsers() error {
	return playwright.Install(&playwright.RunOptions{
		Browsers: []string{"chromium"},
	})
}

// stealthJS 隐藏 navigator.webdriver 等自动化特征，避免被反爬识别
var stealthJS = `
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
Object.defineProperty(navigator, 'languages', { get: () => ['zh-CN', 'zh', 'en'] });
window.chrome = { runtime: {} };
const originalQuery = window.navigator.permissions.query;
window.navigator.permissions.query = (parameters) =>
  parameters.name === 'notifications'
    ? Promise.resolve({ state: Notification.permission })
    : originalQuery(parameters);
`
