// debug_page 调试 DeepSeek 页面 DOM 结构
// 用法: ./debug_page -out /tmp/ds.png -html /tmp/ds.html
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/playwright-community/playwright-go"
)

func main() {
	out := flag.String("out", "/tmp/ds.png", "screenshot output path")
	html := flag.String("html", "/tmp/ds.html", "html dump output path")
	storage := flag.String("storage", "", "optional storage_state.json path")
	flag.Parse()

	pw, err := playwright.Run()
	if err != nil {
		fmt.Println("playwright:", err)
		os.Exit(1)
	}
	defer pw.Stop()

	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args: []string{
			"--no-sandbox",
			"--disable-dev-shm-usage",
			"--disable-gpu",
			"--single-process",
		},
	}
	browser, err := pw.Chromium.Launch(opts)
	if err != nil {
		fmt.Println("launch:", err)
		os.Exit(1)
	}
	defer browser.Close()

	ctxOpts := playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(
			"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
				"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"),
		Viewport: &playwright.Size{Width: 1280, Height: 800},
		Locale:   playwright.String("zh-CN"),
	}
	if *storage != "" {
		ctxOpts.StorageStatePath = playwright.String(*storage)
	}
	ctx, err := browser.NewContext(ctxOpts)
	if err != nil {
		fmt.Println("context:", err)
		os.Exit(1)
	}
	defer ctx.Close()

	page, err := ctx.NewPage()
	if err != nil {
		fmt.Println("page:", err)
		os.Exit(1)
	}
	defer page.Close()

	fmt.Println("navigating to chat.deepseek.com...")
	if _, err := page.Goto("https://chat.deepseek.com", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(60000),
	}); err != nil {
		fmt.Println("goto:", err)
	}
	// 多等 5 秒让动态内容加载完
	time.Sleep(5 * time.Second)

	fmt.Println("current URL:", page.URL())
	fmt.Println("title:", must(page.Title()))

	// 截图全页
	if _, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(*out),
		FullPage: playwright.Bool(true),
	}); err != nil {
		fmt.Println("screenshot:", err)
	} else {
		fmt.Println("screenshot saved to", *out)
	}

	// dump HTML
	content, err := page.Content()
	if err != nil {
		fmt.Println("content:", err)
	} else {
		if err := os.WriteFile(*html, []byte(content), 0o644); err == nil {
			fmt.Println("html saved to", *html, "size:", len(content))
		}
	}

	// 列出常见元素
	fmt.Println("\n=== DOM 元素探测 ===")
	probes := []string{
		"textarea", "div[contenteditable='true']",
		"textarea#chat-input", "#chat-input",
		"button", "[aria-label]",
		".user-avatar", "[data-testid='user-menu']",
		"input[type='email']", "input[type='password']",
		".login", ".login-btn", "[class*='login']",
		"img.avatar", "[class*='avatar']",
	}
	for _, p := range probes {
		els, err := page.QuerySelectorAll(p)
		if err != nil {
			fmt.Printf("  %-40s error: %v\n", p, err)
			continue
		}
		fmt.Printf("  %-40s found %d\n", p, len(els))
	}
}

func must(s string, err error) string {
	if err != nil {
		return "(err: " + err.Error() + ")"
	}
	return s
}
