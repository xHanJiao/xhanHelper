package agent

import (
	"context"
	"strings"
	"testing"

	"xhan-helper/internal/tool"
)

// 工具 panic 必须被单个 goroutine 吸收成失败结果：反射子系统跑在后台，
// 未 recover 的 panic 会直接终止整个进程（gin.Recovery 只覆盖 HTTP handler）。
// traceRepo 传 nil 走「不写 trace」分支（同时覆盖 createDecisionSafe 的 nil 契约）。
func TestReactLoopToolPanicIsRecovered(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&panicTool{})
	rl := &ReactLoop{toolReg: reg, userID: "u1"}

	results := rl.executeConcurrently(context.Background(),
		[]tool.ToolCall{{Tool: "panic_tool", Function: "boom"}},
		&ReactLoopResult{})

	if len(results) != 1 {
		t.Fatalf("应返回 1 条工具结果, got %d", len(results))
	}
	if results[0].Result == nil || results[0].Result.Success {
		t.Fatalf("panic 应转成失败结果: %+v", results[0])
	}
	if !strings.Contains(results[0].Result.Error, "intentional panic") {
		t.Errorf("错误信息应包含 panic 值: %q", results[0].Result.Error)
	}
}
