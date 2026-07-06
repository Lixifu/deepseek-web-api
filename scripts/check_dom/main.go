package main

import (
	"fmt"
	"log"
	"time"

	"github.com/playwright-community/playwright-go"
)

func main() {
	pw, err := playwright.Run()
	if err != nil {
		log.Fatal(err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer browser.Close()

	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		StorageStatePath: playwright.String("data/storage_states/account_1.json"),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer ctx.Close()

	page, err := ctx.NewPage()
	if err != nil {
		log.Fatal(err)
	}

	if _, err := page.Goto("https://chat.deepseek.com", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		log.Fatal(err)
	}

	if _, err := page.WaitForSelector(`textarea[placeholder*="DeepSeek"]`, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(20000),
	}); err != nil {
		log.Fatal("input not found:", err)
	}

	// 开启深度思考
	toggle := page.Locator(`div.ds-toggle-button:has-text('深度思考')`)
	if cnt, _ := toggle.Count(); cnt > 0 {
		cls, _ := toggle.GetAttribute("class")
		if !contains(cls, "--selected") {
			toggle.Click()
			fmt.Println("已开启深度思考")
		}
	}

	// 发送消息
	input := page.Locator(`textarea[placeholder*="DeepSeek"]`)
	input.Fill("1+1等于几？")
	page.Click(`div[role='button'].ds-button--primary.ds-button--filled.ds-button--circle`)
	fmt.Println("消息已发送，等待 20s...")

	time.Sleep(20 * time.Second)

	// 获取页面上所有 div 的 class（去重）
	html, err := page.Content()
	if err != nil {
		fmt.Println("Content error:", err)
	} else {
		fmt.Printf("页面 HTML 长度: %d\n", len(html))
		// 输出含 markdown 或 think 的片段
		fmt.Printf("HTML 前 2000 字符:\n%s\n", html[:min(2000, len(html))])
	}

	// 等到停止按钮消失
	for i := 0; i < 30; i++ {
		stopCnt, _ := page.Locator(`div[role='button']:has-text('停止')`).Count()
		if stopCnt == 0 {
			fmt.Println("=== 停止按钮消失 ===")
			break
		}
		time.Sleep(2 * time.Second)
	}
	time.Sleep(2 * time.Second)

	// 生成完成后获取 HTML
	html2, _ := page.Content()
	fmt.Printf("\n========== 生成完成后 HTML 长度: %d ==========\n", len(html2))
	// 找所有 class 含 markdown 的元素
	loc := page.Locator(`[class*="markdown"]`)
	cnt, _ := loc.Count()
	fmt.Printf("含 markdown 的元素数量: %d\n", cnt)
	for i := 0; i < cnt && i < 10; i++ {
		el := loc.Nth(i)
		cls, _ := el.GetAttribute("class")
		text, _ := el.InnerText()
		fmt.Printf("  [%d] class=%s len=%d head=%q\n", i, cls, len(text), text[:min(80, len(text))])
	}

	// 找含 think 的元素
	loc2 := page.Locator(`[class*="think"], [class*="Think"]`)
	cnt2, _ := loc2.Count()
	fmt.Printf("\n含 think 的元素数量: %d\n", cnt2)
	for i := 0; i < cnt2 && i < 10; i++ {
		el := loc2.Nth(i)
		cls, _ := el.GetAttribute("class")
		text, _ := el.InnerText()
		fmt.Printf("  [%d] class=%s len=%d head=%q\n", i, cls, len(text), text[:min(80, len(text))])
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
