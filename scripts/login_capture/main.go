// login_capture 用于手动登录 DeepSeek 并导出 storage_state。
// 用法: ./login_capture -out data/storage_states/account_1.json [-auto]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

func main() {
	out := flag.String("out", "data/storage_states/account_1.json", "output storage_state path")
	headless := flag.Bool("headless", false, "run browser in headless mode")
	auto := flag.Bool("auto", true, "auto-detect login success by polling URL (no Enter needed)")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Println("mkdir:", err)
		os.Exit(1)
	}

	pw, err := playwright.Run()
	if err != nil {
		fmt.Println("playwright:", err)
		os.Exit(1)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(*headless),
		Args: []string{
			"--no-sandbox",
			"--disable-dev-shm-usage",
			"--disable-gpu",
		},
	})
	if err != nil {
		fmt.Println("launch:", err)
		os.Exit(1)
	}
	defer browser.Close()

	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(
			"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
				"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"),
		Viewport: &playwright.Size{Width: 1280, Height: 800},
		Locale:   playwright.String("zh-CN"),
	})
	if err != nil {
		fmt.Println("context:", err)
		os.Exit(1)
	}
	page, err := ctx.NewPage()
	if err != nil {
		fmt.Println("page:", err)
		os.Exit(1)
	}
	if _, err := page.Goto("https://chat.deepseek.com"); err != nil {
		fmt.Println("goto:", err)
		os.Exit(1)
	}

	fmt.Println(">>> 浏览器已打开 https://chat.deepseek.com")
	fmt.Println(">>> 请在浏览器中登录 DeepSeek（手机号+验证码）")

	if *auto {
		// 自动检测：每 2 秒轮询 URL，登录成功后 URL 会从 /sign_in 跳转回 / 或 /chat
		fmt.Println(">>> 自动检测登录状态中（登录成功后会自动保存，无需按回车）...")
		deadline := time.Now().Add(10 * time.Minute) // 最多等 10 分钟
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			url := page.URL()
			if !strings.Contains(url, "/sign_in") {
				// URL 变了，再等 3 秒确保页面稳定（cookies 写完）
				fmt.Println(">>> 检测到登录成功，URL:", url)
				time.Sleep(3 * time.Second)
				break
			}
		}
		if strings.Contains(page.URL(), "/sign_in") {
			fmt.Println(">>> 超时：10 分钟内未检测到登录。退出。")
			os.Exit(1)
		}
	} else {
		fmt.Println(">>> 登录成功后回到终端按回车继续...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}

	if _, err := ctx.StorageState(*out); err != nil {
		fmt.Println("save storage state:", err)
		os.Exit(1)
	}
	fmt.Println(">>> storage_state 已保存到", *out)
	fmt.Println("    现在可以在管理后台上传该文件，或直接写入 accounts.storage_path")
}
