package util

import (
	"log"
	"runtime/debug"
	"strings"
)

// PanicHandler 如果非 nil，SafeGo 在 recover 到 panic 后会回调，用于监控采集。
var PanicHandler func(location string)

// SafeGo 在 goroutine 中执行 fn，自动 recover panic 并打日志，防止非关键路径崩溃拖垮整个进程。
func SafeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				log.Printf("[SafeGo] panic recovered: %v\n%s", r, stack)
				if PanicHandler != nil {
					PanicHandler(extractPanicLocation(stack))
				}
			}
		}()
		fn()
	}()
}

// extractPanicLocation 从 debug.Stack() 输出中提取 panic 发生的函数位置。
func extractPanicLocation(stack string) string {
	lines := strings.Split(stack, "\n")
	for i, line := range lines {
		if strings.Contains(line, "panic(") && i+1 < len(lines) {
			next := i + 1
			// 跳过 panic 的文件行（\t 开头）
			if strings.HasPrefix(lines[next], "\t") && next+1 < len(lines) {
				next++
			}
			loc := strings.TrimSpace(lines[next])
			// 去掉函数签名里的参数部分
			if idx := strings.Index(loc, "("); idx > 0 {
				loc = loc[:idx]
			}
			return loc
		}
	}
	return "unknown"
}
