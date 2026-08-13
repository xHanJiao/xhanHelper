package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

// 客户端审批（长耗时调用）对 subAgent 的影响：
// 场景模拟——客户端收到 client_tool_call 后先走审批，~60ms 后才回传结果，
// 在 200ms 的服务端等待预算内（比例等价于真实 120s 预算 vs 60s 审批）：
//   1. 审批通过 → 工具成功，结果以 role=tool 回传 LLM，子 agent 正常完成
//   2. 审批拒绝 → 工具失败（错误回传），子 agent 继续执行不中断
// 均验证不会因等待被服务端提前杀死（预算内）。

type captureSink struct {
	events []*llm.StreamEvent
}

func (s *captureSink) onEvent(evt *llm.StreamEvent) { s.events = append(s.events, evt) }

func (s *captureSink) clientToolCallID() string {
	for _, evt := range s.events {
		if evt.Type != "client_tool_call" {
			continue
		}
		var payload struct {
			CallID string `json:"call_id"`
		}
		if json.Unmarshal([]byte(evt.Content), &payload) == nil && payload.CallID != "" {
			return payload.CallID
		}
	}
	return ""
}

func newClientToolMiniReAct(responses []llm.Message) (*MiniReAct, *ClientToolBridge, *captureSink, *syncMockLLM) {
	bridge := NewClientToolBridge()
	sink := &captureSink{}
	llmClient := &syncMockLLM{responses: responses}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent: SubAgentExt{
			ClientTools:       []ClientTool{{ServerName: "browser", Name: "navigate", IsReadOnly: true}},
			ClientBridge:      bridge,
			ClientToolTimeout: 200 * time.Millisecond, // 等价缩放：真实 120s 预算中的 60s 审批 → 200ms 中的 80ms 审批
		},
		OnEvent: sink.onEvent,
	})
	return m, bridge, sink, llmClient
}

func TestSubAgent_ClientApprovalThenSuccess(t *testing.T) {
	m, bridge, sink, llmClient := newClientToolMiniReAct([]llm.Message{
		{Content: "", ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolCallFunction{Name: "browser__navigate", Arguments: `{"url":"https://example.com"}`}}}},
		{Content: "调研完成"},
	})

	// 模拟客户端：收到指令 → 审批 80ms → 通过并回传结果
	callIDCh := make(chan string, 1)
	go func() {
		for {
			id := sink.clientToolCallID()
			if id != "" {
				callIDCh <- id
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	resultCh := make(chan *MiniReActResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := m.Run(context.Background(), "调研任务")
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- r
	}()

	callID := <-callIDCh
	time.Sleep(80 * time.Millisecond) // 审批耗时
	if err := bridge.OnResult(callID, "u1", ClientToolResult{Result: json.RawMessage(`{"ok":true,"title":"Example"}`)}); err != nil {
		t.Fatalf("OnResult: %v", err)
	}

	select {
	case r := <-resultCh:
		if r.FinalContent != "调研完成" {
			t.Errorf("子 agent 应正常完成: %s", r.FinalContent)
		}
		if len(r.ToolCallLog) != 1 || !r.ToolCallLog[0].Result.Success {
			t.Fatalf("审批通过后工具应成功: %+v", r.ToolCallLog)
		}
		if !strings.Contains(string(r.ToolCallLog[0].Result.Data), "ok") {
			t.Errorf("工具结果应含回传数据: %s", r.ToolCallLog[0].Result.Data)
		}
		// 结果以 role=tool 回传 LLM（第二轮请求消息里）
		if len(llmClient.reqs) < 2 {
			t.Fatal("第二轮 LLM 请求应存在")
		}
		var hasToolRole bool
		for _, msg := range llmClient.reqs[1].Messages {
			if msg.Role == "tool" && msg.ToolCallID == "call-1" && strings.Contains(msg.Content, "ok") {
				hasToolRole = true
			}
		}
		if !hasToolRole {
			t.Error("工具结果应以 role=tool 回传 LLM")
		}
	case err := <-errCh:
		t.Fatalf("Run error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("子 agent 未在预算内完成（可能被提前杀死或卡死）")
	}
}

func TestSubAgent_ClientApprovalThenReject(t *testing.T) {
	m, bridge, sink, _ := newClientToolMiniReAct([]llm.Message{
		{Content: "", ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolCallFunction{Name: "browser__navigate", Arguments: `{"url":"https://example.com"}`}}}},
		{Content: "无法完成该操作，请换用 webfetch"},
	})

	callIDCh := make(chan string, 1)
	go func() {
		for {
			id := sink.clientToolCallID()
			if id != "" {
				callIDCh <- id
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	resultCh := make(chan *MiniReActResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := m.Run(context.Background(), "调研任务")
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- r
	}()

	callID := <-callIDCh
	time.Sleep(80 * time.Millisecond) // 审批耗时
	if err := bridge.OnResult(callID, "u1", ClientToolResult{Error: "用户拒绝执行"}); err != nil {
		t.Fatalf("OnResult: %v", err)
	}

	select {
	case r := <-resultCh:
		if len(r.ToolCallLog) != 1 || r.ToolCallLog[0].Result.Success {
			t.Fatalf("审批拒绝后工具应失败: %+v", r.ToolCallLog)
		}
		if !strings.Contains(r.ToolCallLog[0].Result.Error, "用户拒绝执行") {
			t.Errorf("错误应透传客户端拒绝原因: %s", r.ToolCallLog[0].Result.Error)
		}
		if r.FinalContent == "" {
			t.Error("工具失败后子 agent 应继续执行并产出最终回复")
		}
	case err := <-errCh:
		t.Fatalf("Run error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("子 agent 未在预算内完成")
	}
}

// TestSubAgent_ClientApprovalBeyondBudget — 审批超过服务端等待预算 → 工具超时失败，
// 且后续客户端工具快速失败（不重复白等）。
func TestSubAgent_ClientApprovalBeyondBudget(t *testing.T) {
	m, _, _, _ := newClientToolMiniReAct([]llm.Message{
		{Content: "", ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolCallFunction{Name: "browser__navigate", Arguments: `{}`}}}},
		{Content: "done"},
	})
	// 不回传结果：等预算 200ms 自然超时
	result, err := m.Run(context.Background(), "调研任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalContent != "done" {
		t.Errorf("超时后子 agent 仍应完成: %s", result.FinalContent)
	}
	if result.ToolCallLog[0].Result.Success {
		t.Fatal("超时应失败")
	}
	if !strings.Contains(result.ToolCallLog[0].Result.Error, "执行超时") {
		t.Errorf("错误应说明超时: %s", result.ToolCallLog[0].Result.Error)
	}
	if !m.clientToolsUnavailable {
		t.Error("超时后应置位 clientToolsUnavailable")
	}
	// 后续客户端工具快速失败（不白等）
	r, _ := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "navigate"})
	if r.Success || !strings.Contains(r.Error, "已跳过") {
		t.Errorf("后续客户端工具应快速失败: %+v", r)
	}
}

// TestSubAgent_ClientToolFastFailExemptsUI — fast-fail 后 ask_user（ui 工具）仍可调用：
// 用户介入不依赖浏览器执行链路，浏览器断连时用户仍能响应子任务请求。
func TestSubAgent_ClientToolFastFailExemptsUI(t *testing.T) {
	bridge := NewClientToolBridge()
	m := NewMiniReAct(&MiniReActConfig{
		Client:        &blockingLLM{},
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent: SubAgentExt{
			ClientTools:       []ClientTool{{ServerName: "browser", Name: "navigate", IsReadOnly: true}},
			ClientBridge:      bridge,
			ClientToolTimeout: 30 * time.Millisecond,
		},
		OnEvent: func(*llm.StreamEvent) {},
	})

	// 置位 fast-fail（模拟浏览器断连）
	m.clientToolsUnavailable = true

	// browser 工具：快速失败
	r, _ := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "navigate"})
	if r.Success || !strings.Contains(r.Error, "已跳过") {
		t.Errorf("fast-fail 后 browser 应快速失败: %+v", r)
	}

	// ui 工具：豁免 → 正常走调用（此处无人回传，走 30ms 超时路径而非"已跳过"）
	start := time.Now()
	r, _ = m.execClientTool(context.Background(), tool.ToolCall{Tool: "ui", Function: "ask_user"})
	if strings.Contains(r.Error, "已跳过") {
		t.Error("ask_user 不应被 fast-fail 拦截")
	}
	if !strings.Contains(r.Error, "执行超时") {
		t.Errorf("ask_user 应走正常调用（此处超时）: %+v", r)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Error("ask_user 应真实等待而非快速失败")
	}
}
