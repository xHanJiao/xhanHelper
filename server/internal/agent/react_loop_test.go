package agent

import (
	"context"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
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

// NewReactLoop 的契约是 traceRepo 可为 nil（不写 trace）。Run 里除了
// createDecisionSafe，还会写 LLMCall；这条路径必须同样跳过，否则 nil 契约是假的。
func TestReactLoopRunWithNilTraceRepo(t *testing.T) {
	client := &syncMockLLM{responses: []llm.Message{{Content: `{"reply":"ok","tool_calls":[]}`}}}
	rl := NewReactLoop(client, nil, "sys", 2, "u1", "test", nil, "")

	res, err := rl.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("nil traceRepo 不应影响执行: %v", err)
	}
	if res == nil || !strings.Contains(res.FinalResponse, "ok") {
		t.Fatalf("unexpected result: %+v", res)
	}

	// endTraceSafe 是另一条 nil 入口（反思编排器/状态更新器共用），也不能解引用
	endTraceSafe(nil, context.Background(), "trace-1", "completed", 0, 0, nil)
}
