package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

func TestMiniReAct_DirectFinalContent(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "请提取画像")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalContent == "" {
		t.Fatal("expected FinalContent")
	}
	if !strings.Contains(result.FinalContent, "籍贯") {
		t.Errorf("unexpected content: %s", result.FinalContent)
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.TerminatedEarly {
		t.Error("should not terminate early")
	}
}

func TestMiniReAct_ToolCallThenFinal(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			Content: "",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"用户基本信息"}`}},
			},
		},
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         registryWithUnifiedSearch().GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  registryWithUnifiedSearch(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "请提取画像")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
	if result.ToolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", result.ToolCalls)
	}
	if result.FinalContent != `{"籍贯":"河南焦作"}` {
		t.Errorf("unexpected final: %s", result.FinalContent)
	}
	// 工具结果应以 role=tool 回传
	if len(llmClient.reqs) < 2 {
		t.Fatalf("expected 2 requests, got %d", len(llmClient.reqs))
	}
	second := llmClient.reqs[1]
	found := false
	for _, msg := range second.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call-1" && strings.Contains(msg.Content, "result") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected role=tool message with ToolCallID=call-1, got messages: %+v", second.Messages)
	}
}

func TestMiniReAct_FinalRound_ForcesContent(t *testing.T) {
	// 主循环 2 轮全 tool_call，强制轮（第 3 轮，Tools=nil）产出最终内容
	llmClient := &syncMockLLM{responses: []llm.Message{
		{ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"x"}`}}}},
		{ToolCalls: []llm.ToolCall{{ID: "call-2", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"y"}`}}}},
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         registryWithUnifiedSearch().GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  registryWithUnifiedSearch(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 3,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TerminatedEarly {
		t.Error("final round should produce content, not terminate early")
	}
	if result.FinalContent != `{"籍贯":"河南焦作"}` {
		t.Errorf("expected forced final content, got: %s", result.FinalContent)
	}
	// 强制轮计入总轮数
	if result.Iterations != 3 {
		t.Errorf("expected iterations=3 (incl final round), got %d", result.Iterations)
	}
	if result.ToolCalls != 2 {
		t.Errorf("expected 2 tool calls (2 search rounds), got %d", result.ToolCalls)
	}
	// 强制轮应使用空工具集（Tools=nil）
	if len(llmClient.reqs) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(llmClient.reqs))
	}
	if len(llmClient.reqs[2].Tools) != 0 {
		t.Errorf("final round should disable tools, got %d tool defs", len(llmClient.reqs[2].Tools))
	}
}

func TestMiniReAct_FinalRound_CustomPrompt(t *testing.T) {
	custom := "[系统] 请直接输出画像 JSON"
	llmClient := &syncMockLLM{responses: []llm.Message{
		{ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"x"}`}}}},
		{Content: `{}`},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:           llmClient,
		Tools:            registryWithUnifiedSearch().GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:     registryWithUnifiedSearch(),
		SystemPrompt:     "sys",
		FinalRoundPrompt: custom,
		UserID:           "user-1",
		MaxIterations:    2,
		Caller:           "test",
	})
	if _, err := m.Run(context.Background(), "任务"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llmClient.reqs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(llmClient.reqs))
	}
	// 强制轮请求中应包含自定义指令（作为 assistant 消息追加）
	foundCustom := false
	for _, msg := range llmClient.reqs[1].Messages {
		if msg.Content == custom {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Errorf("final round should append custom prompt, got messages: %+v", llmClient.reqs[1].Messages)
	}
}

func TestMiniReAct_FinalRound_LLMError(t *testing.T) {
	// 主循环 1 轮 tool_call，强制轮（第 2 轮）LLM 报错
	llmClient := &syncMockLLM{responses: []llm.Message{
		{ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"x"}`}}}},
		{Content: `{}`}, // 占位，实际被 errorLLMClient 替代
	}}
	_ = llmClient
	failClient := &errorLLMClient{err: errors.New("boom")}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        failClient,
		ToolRegistry:  registryWithUnifiedSearch(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 2,
		Caller:        "test",
	})
	_, err := m.Run(context.Background(), "任务")
	if err == nil {
		t.Fatal("expected error from final round LLM call")
	}
}

func TestMiniReAct_LLMError(t *testing.T) {
	failClient := &errorLLMClient{err: errors.New("boom")}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        failClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	_, err := m.Run(context.Background(), "任务")
	if err == nil {
		t.Fatal("expected error from LLM")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected boom in error, got: %v", err)
	}
}

func TestMiniReAct_PanicInToolRecovered(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			Content: "",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "panic_tool__boom", Arguments: `{}`}},
			},
		},
		{Content: `{}`},
	}}
	r := tool.NewRegistry()
	r.Register(&panicTool{})
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         r.GetLLMToolsFiltered([]string{"panic_tool"}),
		ToolRegistry:  r,
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalContent != `{}` {
		t.Errorf("expected final after panic recovery, got: %s", result.FinalContent)
	}
}

// errorLLMClient 始终返回错误的 LLM client。
type errorLLMClient struct {
	err error
}

func (e *errorLLMClient) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	return llm.Message{}, e.err
}
func (e *errorLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return nil, e.err
}
func (e *errorLLMClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, e.err
}
func (e *errorLLMClient) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return base
}
func (e *errorLLMClient) ModelName() string { return "mock" }

// panicTool 工具执行时 panic，验证 MiniReAct 恢复逻辑。
type panicTool struct{}

func (t *panicTool) Name() string { return "panic_tool" }
func (t *panicTool) Description() string {
	return "panic tool"
}
func (t *panicTool) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{Name: "boom", Description: "boom", Parameters: map[string]interface{}{}}}
}
func (t *panicTool) ReadOnlyFuncs() map[string]bool { return nil }
func (t *panicTool) Execute(_ *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	panic("intentional panic")
}

// ---- 事件系统测试 ----

func TestMiniReAct_Events_Sequence(t *testing.T) {
	var evts []*llm.StreamEvent
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			ReasoningContent: "先搜索用户信息",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"用户基本信息"}`}},
			},
		},
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	reg := registryWithUnifiedSearch()
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         reg.GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  reg,
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
		OnEvent:       func(evt *llm.StreamEvent) { evts = append(evts, evt) },
	})

	result, err := m.Run(context.Background(), "请提取画像")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 断言事件序列：status(开始) → status(第1轮) → thought → tool_call → tool_result → status(第2轮) → done
	wantTypes := []string{
		MiniReactEventStatus,
		MiniReactEventStatus,
		MiniReactEventThought,
		MiniReactEventToolCall,
		MiniReactEventToolResult,
		MiniReactEventStatus,
		MiniReactEventDone,
	}
	if len(evts) != len(wantTypes) {
		t.Fatalf("expected %d events, got %d: %+v", len(wantTypes), len(evts), evtTypes(evts))
	}
	for i, w := range wantTypes {
		if evts[i].Type != w {
			t.Fatalf("event[%d]: expected %q, got %q", i, w, evts[i].Type)
		}
	}

	// done 事件 Content 是 JSON，含 iterations/tool_calls/tool_call_logs
	var done struct {
		Iterations  int           `json:"iterations"`
		ToolCalls   int           `json:"tool_calls"`
		ToolCallLog []ToolCallLog `json:"tool_call_logs"`
	}
	doneEvt := evts[len(evts)-1]
	if err := json.Unmarshal([]byte(doneEvt.Content), &done); err != nil {
		t.Fatalf("parse done event: %v", err)
	}
	if done.Iterations != 2 || done.ToolCalls != 1 {
		t.Errorf("done mismatch: iterations=%d tool_calls=%d", done.Iterations, done.ToolCalls)
	}
	if len(done.ToolCallLog) != 1 {
		t.Fatalf("expected 1 tool_call_log, got %d", len(done.ToolCallLog))
	}
	if done.ToolCallLog[0].Tool != "unified_search" || done.ToolCallLog[0].Function != "semantic_search" {
		t.Errorf("unexpected log: %+v", done.ToolCallLog[0])
	}

	// 返回值也应带完整历史
	if len(result.ToolCallLog) != 1 {
		t.Errorf("expected ToolCallLog in result, got %d", len(result.ToolCallLog))
	}
}

func TestMiniReAct_Events_OnEventNil_NoPanic(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	reg := registryWithUnifiedSearch()
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         reg.GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  reg,
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
		// OnEvent 不设 → 静默
	})
	_, err := m.Run(context.Background(), "任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestMiniReAct_Events_Error(t *testing.T) {
	var evts []*llm.StreamEvent
	failClient := &errorLLMClient{err: errors.New("boom")}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        failClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
		OnEvent:       func(evt *llm.StreamEvent) { evts = append(evts, evt) },
	})
	_, err := m.Run(context.Background(), "任务")
	if err == nil {
		t.Fatal("expected error")
	}
	foundErr := false
	for _, e := range evts {
		if e.Type == MiniReactEventError && e.Error == "boom" {
			foundErr = true
		}
	}
	if !foundErr {
		t.Errorf("expected mini_react:error event, got: %+v", evtTypes(evts))
	}
}

func TestMiniReAct_Events_Thought(t *testing.T) {
	var evts []*llm.StreamEvent
	llmClient := &syncMockLLM{responses: []llm.Message{
		{ReasoningContent: "思考过程", Content: `{"籍贯":"河南焦作"}`},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
		OnEvent:       func(evt *llm.StreamEvent) { evts = append(evts, evt) },
	})
	if _, err := m.Run(context.Background(), "任务"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	foundThought := false
	for _, e := range evts {
		if e.Type == MiniReactEventThought && e.Content == "思考过程" {
			foundThought = true
		}
	}
	if !foundThought {
		t.Errorf("expected mini_react:thought event, got: %+v", evtTypes(evts))
	}
}

func TestMiniReAct_Events_ToolCallHasDisplay(t *testing.T) {
	var evts []*llm.StreamEvent
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"x"}`}},
			},
		},
		{Content: `{}`},
	}}
	reg := registryWithUnifiedSearch()
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         reg.GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  reg,
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
		OnEvent:       func(evt *llm.StreamEvent) { evts = append(evts, evt) },
	})
	if _, err := m.Run(context.Background(), "任务"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	foundCall := false
	for _, e := range evts {
		if e.Type != MiniReactEventToolCall {
			continue
		}
		var tc struct {
			Tool     string `json:"tool"`
			Function string `json:"function"`
			Display  string `json:"display"`
		}
		if err := json.Unmarshal([]byte(e.Content), &tc); err != nil {
			t.Fatalf("parse tool_call event: %v", err)
		}
		if tc.Tool == "unified_search" && tc.Function == "semantic_search" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Errorf("expected tool_call event with unified_search.semantic_search, got: %+v", evtTypes(evts))
	}
}

func evtTypes(evts []*llm.StreamEvent) []string {
	out := make([]string, len(evts))
	for i, e := range evts {
		out[i] = e.Type
	}
	return out
}
