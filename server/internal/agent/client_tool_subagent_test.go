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

func (s *captureSink) onEvent(evt *llm.StreamEvent) bool {
	s.events = append(s.events, evt)
	return true
}

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
	if m.clientToolsUnavailable {
		t.Error("执行超时不应置位 clientToolsUnavailable（可重试）")
	}
	// 后续调用真实执行（可重试），而非快速失败
	r, _ := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "navigate"})
	if r.Success || strings.Contains(r.Error, "已跳过") {
		t.Errorf("超时后应可重试（真实执行）: %+v", r)
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
		OnEvent: func(*llm.StreamEvent) bool { return true },
	})

	// 置位 fast-fail（模拟浏览器断连），并记录失败时间（衰减窗口内）
	m.clientToolsUnavailable = true
	m.clientUnavailableAt = time.Now()

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

// TestSubAgent_AskUserDispatch — ask_user 从"LLM 调用"到"客户端收到 client_tool_call"全链路：
// ui 工具必须同时注入 def 与 ClientTools（clientIdx），否则 LLM 能调但路由不到客户端。
func TestSubAgent_AskUserDispatch(t *testing.T) {
	bridge := NewClientToolBridge()
	sink := &captureSink{}
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: "", ToolCalls: []llm.ToolCall{{ID: "ask-1", Function: llm.ToolCallFunction{Name: "ui__ask_user", Arguments: `{"message":"请帮我处理验证码"}`}}}},
		{Content: "验证码已处理，继续调研"},
	}}

	// 模拟客户端：收到 ui__ask_user → 回传"已处理"
	dispatchCh := make(chan string, 4)
	go func() {
		for {
			id := sink.clientToolCallID()
			if id != "" {
				dispatchCh <- id
				_ = bridge.OnResult(id, "u1", ClientToolResult{Result: json.RawMessage(`{"answered":true}`)})
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent: SubAgentExt{
			// 模拟 RunSubAgent 注入结果：browser + ui 都在 ClientTools（clientIdx 依赖）
			ClientTools: []ClientTool{
				{ServerName: "browser", Name: "agent_browser_open", IsReadOnly: true},
				{ServerName: "ui", Name: "ask_user", IsReadOnly: true},
			},
			ClientBridge:      bridge,
			ClientToolTimeout: 2 * time.Second,
			AllowedTools:      map[string][]string{"browser": nil, "ui": nil},
		},
		OnEvent: sink.onEvent,
	})

	result, err := m.Run(context.Background(), "调研")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalContent != "验证码已处理，继续调研" {
		t.Errorf("ask_user 回传后子循环应继续: %s", result.FinalContent)
	}
	if len(result.ToolCallLog) != 1 || !result.ToolCallLog[0].Result.Success {
		t.Fatalf("ask_user 应成功: %+v", result.ToolCallLog)
	}
	select {
	case <-dispatchCh:
	default:
		t.Fatal("客户端未收到 ui__ask_user 分发")
	}
}

// TestMiniReAct_ClientToolProbeRecovers — unavailable 后探活函数（list_tabs）真实执行，
// 成功即清除标志，后续浏览器调用恢复正常（并行任务"锁死到结束"修复）。
func TestMiniReAct_ClientToolProbeRecovers(t *testing.T) {
	bridge := NewClientToolBridge()
	sink := &captureSink{}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        &blockingLLM{},
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent: SubAgentExt{
			ClientTools:       []ClientTool{{ServerName: "browser", Name: "agent_browser_list_tabs", IsReadOnly: true}},
			ClientBridge:      bridge,
			ClientToolTimeout: 2 * time.Second,
		},
		OnEvent: sink.onEvent,
	})
	m.clientToolsUnavailable = true
	m.clientUnavailableAt = time.Now()

	// 客户端模拟：收到 client_tool_call → 回传成功
	go func() {
		for {
			id := sink.clientToolCallID()
			if id != "" {
				_ = bridge.OnResult(id, "u1", ClientToolResult{Result: json.RawMessage(`{"tabs":[]}`)})
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	r, unavailable := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "agent_browser_list_tabs"})
	if !r.Success {
		t.Fatalf("探活应真实执行成功: %+v", r)
	}
	if unavailable {
		t.Fatal("探活成功不应标记 unavailable")
	}
	if m.clientToolsUnavailable {
		t.Error("探活成功应清除 unavailable 标志（客户端恢复）")
	}
}

// TestSubAgent_ClientToolPayloadHasOwner — client_tool_call payload 带归属（子任务 convID），
// 客户端据此做标签隔离（open 记录 / list_tabs 标注 / close_tab 保护）。
func TestSubAgent_ClientToolPayloadHasOwner(t *testing.T) {
	bridge := NewClientToolBridge()
	sink := &captureSink{}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        &blockingLLM{},
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		ConvID:        "sub-9",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent: SubAgentExt{
			ClientTools:       []ClientTool{{ServerName: "browser", Name: "agent_browser_open", IsReadOnly: true}},
			ClientBridge:      bridge,
			ClientToolTimeout: 100 * time.Millisecond,
		},
		OnEvent: sink.onEvent,
	})
	_, _ = m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "agent_browser_open"})
	for _, evt := range sink.events {
		if evt.Type != "client_tool_call" {
			continue
		}
		var payload struct {
			Owner string `json:"owner"`
		}
		if err := json.Unmarshal([]byte(evt.Content), &payload); err != nil {
			t.Fatalf("payload 解析失败: %v", err)
		}
		if payload.Owner != "subagent:sub-9" {
			t.Errorf("owner 应为 subagent:sub-9, got %q", payload.Owner)
		}
		return
	}
	t.Fatal("未捕获 client_tool_call 事件")
}
