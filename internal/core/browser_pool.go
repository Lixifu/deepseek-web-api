package core

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

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
	AccountID      uint
	AccountName    string
	StoragePath    string
	Ctx            playwright.BrowserContext
	mu             sync.Mutex
	busy           bool
	healthy        bool
	page           playwright.Page
	closeOnRelease bool
	onRelease      func()
	generation     uint64
	queueLease     SharedQueueLease
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
	s.busy = false
	closeNow := s.closeOnRelease
	ctx := s.Ctx
	onRelease := s.onRelease
	queueLease := s.queueLease
	s.queueLease = nil
	s.mu.Unlock()
	if queueLease != nil {
		queueLease.Release()
	}
	if closeNow && ctx != nil {
		_ = ctx.Close()
	}
	if onRelease != nil {
		onRelease()
	}
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

func (s *BrowserSession) releaseSharedQueueLease() {
	s.mu.Lock()
	lease := s.queueLease
	s.queueLease = nil
	s.mu.Unlock()
	if lease != nil {
		lease.Release()
	}
}

// Healthy 只读
func (s *BrowserSession) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy
}

// Snapshot 返回会话的健康与占用状态。
func (s *BrowserSession) Snapshot() (healthy, busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy, s.busy
}

// Retire 停止接收新请求；若仍有请求在执行，则在 Release 时关闭上下文。
func (s *BrowserSession) Retire() {
	s.mu.Lock()
	s.healthy = false
	if s.busy {
		s.closeOnRelease = true
		s.mu.Unlock()
		return
	}
	ctx := s.Ctx
	s.closeOnRelease = true
	s.mu.Unlock()
	if ctx != nil {
		_ = ctx.Close()
	}
}

// Page 返回复用的页面（懒创建由 driver 控制）
func (s *BrowserSession) Page() playwright.Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.page
}
func (s *BrowserSession) SetPage(page playwright.Page) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.page = page
}

type acquireResult struct {
	session *BrowserSession
	err     error
}

type poolWaiter struct {
	id     uint64
	tried  map[uint]bool
	result chan acquireResult
}

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
	restarting   sync.Mutex
	generation   uint64
	waiters      []*poolWaiter
	nextWaiterID uint64
	maxQueue     int
	queueTimeout time.Duration
	maxSessions  int
	sharedQueue  SharedQueue
}

func NewBrowserPool(headless bool, logger *zap.Logger) *BrowserPool {
	return &BrowserPool{
		headless:     headless,
		logger:       logger,
		maxQueue:     100,
		queueTimeout: 120 * time.Second,
	}
}

// Configure 设置池容量和等待队列限制。
func (p *BrowserPool) Configure(maxSessions, maxQueue int, queueTimeout time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxSessions = maxSessions
	p.maxQueue = maxQueue
	p.queueTimeout = queueTimeout
}

func (p *BrowserPool) SetSharedQueue(queue SharedQueue) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sharedQueue = queue
}

// Start 启动 Playwright 并为每个账号创建 BrowserContext
func (p *BrowserPool) Start(accounts []AccountConfig) error {
	p.mu.Lock()
	p.accCfgs = append([]AccountConfig(nil), accounts...)
	p.mu.Unlock()
	return p.startInternal(accounts)
}

func (p *BrowserPool) startInternal(accounts []AccountConfig) error {
	pw, err := playwright.Run()
	if err != nil {
		return err
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
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
		_ = pw.Stop()
		return err
	}

	p.mu.Lock()
	p.pw = pw
	p.browser = browser
	p.mu.Unlock()

	initializedSessions := make([]*BrowserSession, 0, len(accounts))
	for _, acc := range accounts {
		s, err := p.newSession(acc)
		if err != nil {
			p.logger.Warn("failed to init session for account, skipping",
				zap.Uint("account_id", acc.ID),
				zap.String("name", acc.Name),
				zap.Error(err))
			continue
		}
		initializedSessions = append(initializedSessions, s)
		p.logger.Info("session ready", zap.Uint("account_id", acc.ID), zap.String("name", acc.Name))
	}
	if len(accounts) > 0 && len(initializedSessions) == 0 {
		_ = browser.Close()
		_ = pw.Stop()
		p.mu.Lock()
		p.browser = nil
		p.pw = nil
		p.mu.Unlock()
		return errors.New("no session initialized, check storage_state files")
	}
	p.mu.Lock()
	p.generation++
	for _, session := range initializedSessions {
		session.generation = p.generation
	}
	p.sessions = append(p.sessions, initializedSessions...)
	p.mu.Unlock()
	p.notifyWaiters()
	return nil
}

func (p *BrowserPool) newSession(acc AccountConfig) (*BrowserSession, error) {
	if acc.StoragePath == "" {
		return nil, errors.New("storage path is required")
	}
	if _, err := os.Stat(acc.StoragePath); err != nil {
		return nil, err
	}
	p.mu.Lock()
	browser := p.browser
	p.mu.Unlock()
	if browser == nil {
		return nil, errors.New("browser not started")
	}
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
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
		onRelease:   p.notifyWaiters,
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
		healthy, busy := s.Snapshot()
		if healthy && !busy {
			n++
		}
	}
	return n
}

// QueueLength 返回当前等待浏览器会话的请求数。
func (p *BrowserPool) QueueLength() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.waiters)
}

// EffectiveQueueLength returns the cluster-wide waiting count when Redis
// coordination is enabled, otherwise the local in-process queue length.
func (p *BrowserPool) EffectiveQueueLength(ctx context.Context) int {
	p.mu.Lock()
	queue := p.sharedQueue
	local := len(p.waiters)
	p.mu.Unlock()
	if queue == nil {
		return local
	}
	waiting, err := queue.Waiting(ctx)
	if err != nil {
		return local
	}
	return waiting
}

// SessionStats 返回 (总数, 健康数, 忙碌数)。
func (p *BrowserPool) SessionStats() (total, healthy, busy int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	total = len(p.sessions)
	for _, session := range p.sessions {
		isHealthy, isBusy := session.Snapshot()
		if isHealthy {
			healthy++
		}
		if isBusy {
			busy++
		}
	}
	return
}

// Generation identifies the currently running browser instance.
func (p *BrowserPool) Generation() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

type acquireState int

const (
	acquireUnavailable acquireState = iota
	acquireBusy
	acquireAllTried
)

func (p *BrowserPool) tryAcquireLocked(tried map[uint]bool) (*BrowserSession, acquireState) {
	if len(p.sessions) == 0 {
		return nil, acquireUnavailable
	}
	allTried := true
	healthyUntried := 0
	for _, session := range p.sessions {
		if tried[session.AccountID] {
			continue
		}
		allTried = false
		if session.Acquire() {
			return session, acquireBusy
		}
		if session.Healthy() {
			healthyUntried++
		}
	}
	if allTried {
		return nil, acquireAllTried
	}
	if healthyUntried > 0 {
		return nil, acquireBusy
	}
	return nil, acquireUnavailable
}

// Acquire first obtains a cluster-wide Redis lease when configured, then waits
// for a local browser session. The lease lives until BrowserSession.Release.
func (p *BrowserPool) Acquire(ctx context.Context, tried map[uint]bool) (*BrowserSession, error) {
	p.mu.Lock()
	sharedQueue := p.sharedQueue
	queueTimeout := p.queueTimeout
	p.mu.Unlock()

	queueCtx := ctx
	cancel := func() {}
	if queueTimeout > 0 {
		queueCtx, cancel = context.WithTimeout(ctx, queueTimeout)
	}
	defer cancel()

	var lease SharedQueueLease
	var err error
	if sharedQueue != nil {
		lease, err = sharedQueue.Acquire(queueCtx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return nil, ErrQueueTimeout
			}
			return nil, err
		}
	}

	session, err := p.acquireLocal(queueCtx, tried)
	if err != nil {
		if lease != nil {
			lease.Release()
		}
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, ErrQueueTimeout
		}
		return nil, err
	}
	if lease != nil {
		session.mu.Lock()
		previousLease := session.queueLease
		session.queueLease = lease
		session.mu.Unlock()
		if previousLease != nil {
			previousLease.Release()
		}
	}
	return session, nil
}

// acquireLocal 选中空闲会话；池繁忙时进入有界 FIFO 队列等待。
func (p *BrowserPool) acquireLocal(ctx context.Context, tried map[uint]bool) (*BrowserSession, error) {
	p.mu.Lock()
	if len(p.waiters) == 0 {
		if session, state := p.tryAcquireLocked(tried); session != nil {
			p.mu.Unlock()
			return session, nil
		} else if state == acquireAllTried {
			p.mu.Unlock()
			return nil, ErrAllSessionsDown
		} else if state == acquireUnavailable {
			p.mu.Unlock()
			return nil, ErrNoSession
		}
	}
	if p.maxQueue > 0 && len(p.waiters) >= p.maxQueue {
		p.mu.Unlock()
		return nil, ErrQueueFull
	}
	p.nextWaiterID++
	waiter := &poolWaiter{
		id:     p.nextWaiterID,
		tried:  cloneTried(tried),
		result: make(chan acquireResult, 1),
	}
	p.waiters = append(p.waiters, waiter)
	queueTimeout := p.queueTimeout
	p.dispatchWaitersLocked()
	p.mu.Unlock()

	var timer *time.Timer
	var timeout <-chan time.Time
	if queueTimeout > 0 {
		timer = time.NewTimer(queueTimeout)
		timeout = timer.C
		defer timer.Stop()
	}

	select {
	case result := <-waiter.result:
		return result.session, result.err
	case <-ctx.Done():
		return p.cancelWaiter(waiter, ctx.Err())
	case <-timeout:
		return p.cancelWaiter(waiter, ErrQueueTimeout)
	}
}

func cloneTried(tried map[uint]bool) map[uint]bool {
	out := make(map[uint]bool, len(tried))
	for id, value := range tried {
		out[id] = value
	}
	return out
}

func (p *BrowserPool) cancelWaiter(waiter *poolWaiter, reason error) (*BrowserSession, error) {
	p.mu.Lock()
	for i, queued := range p.waiters {
		if queued.id == waiter.id {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			p.mu.Unlock()
			return nil, reason
		}
	}
	p.mu.Unlock()

	// 已被分配但取消信号先被 select 选中，释放会话避免泄漏。
	result := <-waiter.result
	if result.session != nil {
		result.session.Release()
	}
	return nil, reason
}

func (p *BrowserPool) dispatchWaitersLocked() {
	for len(p.waiters) > 0 {
		waiter := p.waiters[0]
		session, state := p.tryAcquireLocked(waiter.tried)
		switch {
		case session != nil:
			p.waiters = p.waiters[1:]
			waiter.result <- acquireResult{session: session}
		case state == acquireAllTried:
			p.waiters = p.waiters[1:]
			waiter.result <- acquireResult{err: ErrAllSessionsDown}
		case state == acquireUnavailable:
			// Wake one caller so the orchestrator can run the browser restart
			// path. Remaining callers stay queued until that restart completes.
			p.waiters = p.waiters[1:]
			waiter.result <- acquireResult{err: ErrNoSession}
			return
		default:
			return
		}
	}
}

func (p *BrowserPool) notifyWaiters() {
	p.mu.Lock()
	p.dispatchWaitersLocked()
	p.mu.Unlock()
}

// UpsertAccount 热加载或替换账号会话，不中断旧会话正在执行的请求。
func (p *BrowserPool) UpsertAccount(acc AccountConfig) error {
	// Serialize hot updates with a full browser restart so the restart cannot
	// re-add a stale account or create a duplicate session.
	p.restarting.Lock()
	defer p.restarting.Unlock()

	newSession, err := p.newSession(acc)
	if err != nil {
		return err
	}

	var oldSession *BrowserSession
	p.mu.Lock()
	replaced := false
	for i, session := range p.sessions {
		if session.AccountID == acc.ID {
			oldSession = session
			p.sessions[i] = newSession
			replaced = true
			break
		}
	}
	if !replaced {
		if p.maxSessions > 0 && len(p.sessions) >= p.maxSessions {
			p.mu.Unlock()
			newSession.Retire()
			return ErrPoolCapacity
		}
		p.sessions = append(p.sessions, newSession)
	}
	newSession.generation = p.generation
	upsertAccountConfig(&p.accCfgs, acc)
	p.dispatchWaitersLocked()
	p.mu.Unlock()

	if oldSession != nil {
		oldSession.Retire()
	}
	p.logger.Info("account hot loaded",
		zap.Uint("account_id", acc.ID),
		zap.String("name", acc.Name),
		zap.Bool("replaced", replaced))
	return nil
}

// RemoveAccount 从池中移除账号；忙碌会话将在当前请求结束后关闭。
func (p *BrowserPool) RemoveAccount(accountID uint) {
	p.restarting.Lock()
	defer p.restarting.Unlock()

	var removed *BrowserSession
	p.mu.Lock()
	for i, session := range p.sessions {
		if session.AccountID == accountID {
			removed = session
			p.sessions = append(p.sessions[:i], p.sessions[i+1:]...)
			break
		}
	}
	for i, cfg := range p.accCfgs {
		if cfg.ID == accountID {
			p.accCfgs = append(p.accCfgs[:i], p.accCfgs[i+1:]...)
			break
		}
	}
	p.dispatchWaitersLocked()
	p.mu.Unlock()
	if removed != nil {
		removed.Retire()
		p.logger.Info("account removed from browser pool", zap.Uint("account_id", accountID))
	}
}

func upsertAccountConfig(configs *[]AccountConfig, acc AccountConfig) {
	for i := range *configs {
		if (*configs)[i].ID == acc.ID {
			(*configs)[i] = acc
			return
		}
	}
	*configs = append(*configs, acc)
}

// RestartIfGeneration restarts only if the caller observed the current browser
// generation. Concurrent callers that waited behind a successful restart skip
// a redundant second restart.
func (p *BrowserPool) RestartIfGeneration(expected uint64) (bool, error) {
	p.restarting.Lock()
	defer p.restarting.Unlock()

	p.mu.Lock()
	if p.generation != expected {
		p.mu.Unlock()
		return false, nil
	}
	oldSessions := p.sessions
	oldBrowser := p.browser
	oldPw := p.pw
	accCfgs := append([]AccountConfig(nil), p.accCfgs...)
	p.sessions = nil
	p.mu.Unlock()

	// 关闭旧资源
	for _, s := range oldSessions {
		s.releaseSharedQueueLease()
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
	return true, p.startInternal(accCfgs)
}

// Restart closes the current browser and reloads all configured sessions.
func (p *BrowserPool) Restart() error {
	_, err := p.RestartIfGeneration(p.Generation())
	return err
}

// Stop 关闭所有资源
func (p *BrowserPool) Stop() {
	p.mu.Lock()
	sessions := p.sessions
	browser := p.browser
	pw := p.pw
	waiters := p.waiters
	p.sessions = nil
	p.waiters = nil
	p.browser = nil
	p.pw = nil
	p.mu.Unlock()

	for _, waiter := range waiters {
		waiter.result <- acquireResult{err: ErrNoSession}
	}

	for _, s := range sessions {
		s.releaseSharedQueueLease()
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
