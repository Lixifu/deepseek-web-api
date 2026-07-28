package observability

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ChromiumMemoryBytes 汇总当前主机 chrome/chromium 进程的常驻内存。
// 非 Linux 或 /proc 不可用时返回 0。
func ChromiumMemoryBytes() int64 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		command := strings.ToLower(strings.ReplaceAll(string(cmdline), "\x00", " "))
		if !strings.Contains(command, "chrome-headless-shell") &&
			!strings.Contains(command, "chromium") {
			continue
		}
		total += readRSSBytes(filepath.Join("/proc", entry.Name(), "status"))
	}
	return total
}

func readRSSBytes(path string) int64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "VmRSS:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}
