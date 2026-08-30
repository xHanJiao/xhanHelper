package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// recordingMsgRepo 记录保存的消息内容，供验证"收尾指令不落库 / 总结被保存"。
type recordingMsgRepo struct {
	saved []model.Message
}

func (m *recordingMsgRepo) Create(ctx context.Context, msg *model.Message) error {
	m.saved = append(m.saved, *msg)
	return nil
}

// toolCallJSONResponse 生成 prompt-eng 格式的工具调用响应（JSON 文本 + done）。
// 工具名指向不存在的工具，执行后返回错误结果但不会 panic，保证循环持续不收敛。
func toolCallJSONResponse() mockLLMResponse {
	content := `{"reply":"","tool_calls":[{"tool":"nonexistent","function":"nope","args":{}}]}`
	return mockLLMResponse{events: append(makeTextDeltas(content), makeDone())}
}

// newFinalRoundTestOrchestrator 构造带空 registry 的测试 orchestrator
// （executeConcurrently 需要非 nil registry；nonexistent 工具会走模糊纠错安全返回错误）
func newFinalRoundTestOrchestrator(msgRepo msgCreator, convRepo convToucher) *Orchestrator {
	o := newTestOrchestrator(msgRepo, convRepo)
	o.registry = tool.NewRegistry()
	return o
}

// —— 收尾轮主流程 ——

func TestRunReActLoop_FinalRound_ForcesSummary(t *testing.T) {
	// LLM 前 3 轮（maxIterations=3）都返回工具调用 → 触发收尾轮 → 收尾轮输出总结
	msgRepo := &recordingMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newFinalRoundTestOrchestrator(msgRepo, convRepo)

	summary := `{"reply":"已完成查询，未完成部分是 X，建议下一步做 Y","tool_calls":[]}`
	client := &mockLLMClient{
		responses: []mockLLMResponse{
			toolCallJSONResponse(),                                // 轮 1：调工具
			toolCallJSONResponse(),                                // 轮 2：调工具
			toolCallJSONResponse(),                                // 轮 3：调工具
			{events: append(makeTextDeltas(summary), makeDone())}, // 收尾轮：总结
		},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "帮我做一个超长任务",
	}
	o.flashClient = client
	go o.runReActLoop(ctx, events, "test prompt", nil, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	// 1. 不应有 error 事件（不再是硬失败）
	for _, evt := range collected {
		if evt.Type == "error" {
			t.Fatalf("unexpected error event: %s", evt.Error)
		}
	}
	// 2. done 事件存在
	if !hasDoneEvent(collected) {
		t.Fatal("expected done event")
	}
	// 3. 收尾总结文本出现在流式输出中
	text := collectTextDeltas(collected)
	if !strings.Contains(text, "已完成查询") {
		t.Errorf("expected summary text in stream, got: %s", text)
	}
	// 4. 共 4 次 LLM 调用（3 轮 + 1 收尾轮）
	if client.callCount != 4 {
		t.Errorf("expected 4 LLM calls, got %d", client.callCount)
	}
	// 5. 收尾指令不落库：保存的消息中没有任何一条 content 等于 finalRoundPrompt
	for _, m := range msgRepo.saved {
		if m.Role == "assistant" && m.Content == finalRoundPrompt {
			t.Error("finalRoundPrompt 不应落库（会污染历史前缀缓存）")
		}
	}
	// 6. 收尾轮总结已保存为最终回复
	foundSummary := false
	for _, m := range msgRepo.saved {
		if m.Role == "assistant" && strings.Contains(m.Content, "已完成查询") {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Error("收尾轮总结应保存为最终 assistant 消息")
	}
}

func TestRunReActLoop_ConvergesBeforeLimit_NoFinalRound(t *testing.T) {
	// LLM 第 1 轮即收敛 → 不触发收尾轮（mock 只有 1 个响应，若触发收尾轮会报 error 事件）
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(`{"reply":"直接回答"}`), makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "简单问题",
	}
	o.flashClient = client
	go o.runReActLoop(ctx, events, "test prompt", nil, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	for _, evt := range collected {
		if evt.Type == "error" {
			t.Fatalf("unexpected error event: %s", evt.Error)
		}
	}
	if client.callCount != 1 {
		t.Errorf("expected only 1 LLM call (no final round), got %d", client.callCount)
	}
}

func TestRunReActLoop_FinalRound_StateMsgsCarryInstruction(t *testing.T) {
	// 验证收尾轮请求的 messages 末尾携带收尾指令、且状态标记 finalRound=true
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newFinalRoundTestOrchestrator(msgRepo, convRepo)

	var captured *llm.ChatRequest
	client := &capturingLLMClient{
		responses: []mockLLMResponse{
			toolCallJSONResponse(),
			toolCallJSONResponse(),
			toolCallJSONResponse(),
			{events: append(makeTextDeltas(`{"reply":"收尾总结"}`), makeDone())},
		},
		capture: func(req *llm.ChatRequest) { captured = req },
	}
	o.flashClient = client

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "任务",
	}
	go o.runReActLoop(ctx, events, "test prompt", nil, nil, req)
	for range events {
	}

	if captured == nil {
		t.Fatal("expected final round request captured")
	}
	last := captured.Messages[len(captured.Messages)-1]
	if last.Role != "assistant" || last.Content != finalRoundPrompt {
		t.Errorf("收尾轮请求最后一条消息应为收尾指令, got role=%s content=%q", last.Role, last.Content)
	}
}

// —— mkLlmChatReq 收尾轮禁用工具（native FC）——

func TestMkLlmChatReq_FinalRound_NoTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&namedMockTool{name: "note", fns: []string{"record_note"}})
	o := &Orchestrator{
		contextFormatter: &mockFormatter{},
		registry:         reg,
		useNativeFC:      true,
	}
	req := &RunRequest{UserID: "u1"}
	msgs := []llm.Message{{Role: "user", Content: "hi"}}

	// 正常轮：常驻 expose_tools + 已暴露函数
	normal := o.mkLlmChatReq(req, "sys", &reactState{msgs: msgs, exposedTools: map[string]bool{"note.record_note": true}})
	if len(normal.Tools) == 0 {
		t.Error("正常轮应下发工具定义")
	}
	// 收尾轮：禁工具
	final := o.mkLlmChatReq(req, "sys", &reactState{msgs: msgs, finalRound: true})
	if final.Tools != nil && len(final.Tools) != 0 {
		t.Errorf("收尾轮 tools 应为空, got %d 个", len(final.Tools))
	}
}

// —— mkLlmChatReq plan 模式隐藏全部工具（native FC）——

func TestMkLlmChatReq_SubAgentOnlyClientTools_NotExposed(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&fakeTool{name: "note"})
	o := &Orchestrator{
		contextFormatter:        &mockFormatter{},
		registry:                reg,
		useNativeFC:             true,
		subAgentOnlyClientTools: []string{"browser", "fs"},
	}
	msgs := []llm.Message{{Role: "user", Content: "hi"}}
	req := &RunRequest{UserID: "u1", Mode: model.ConvModeWork, ClientTools: []ClientTool{
		{ServerName: "browser", Name: "navigate"},
		{ServerName: "fs", Name: "read"},
	}}
	// 即使 exposedTools 里有 browser/fs 函数，主循环也绝不下发定义
	state := &reactState{msgs: msgs, exposedTools: map[string]bool{
		"browser.navigate": true,
		"fs.read":          true,
	}}
	out := o.mkLlmChatReq(req, "sys", state)
	for _, d := range out.Tools {
		if d.Name == "browser" || d.Name == "fs" {
			t.Errorf("subagent-only client tool leaked in main loop: %s", d.Name)
		}
	}
}

func TestMkLlmChatReq_PlanMode_NoTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&fakeTool{name: "note"})
	o := &Orchestrator{
		contextFormatter: &mockFormatter{},
		registry:         reg,
		useNativeFC:      true,
	}
	msgs := []llm.Message{{Role: "user", Content: "hi"}}
	clientTools := []ClientTool{{ServerName: "browser", Name: "navigate"}}

	// work 模式：常驻 expose_tools；客户端工具不再无条件下发（需先 expose_tools.enable）
	work := o.mkLlmChatReq(&RunRequest{UserID: "u1", Mode: model.ConvModeWork, ClientTools: clientTools}, "sys", &reactState{msgs: msgs})
	if len(work.Tools) == 0 {
		t.Error("work 模式应下发工具定义")
	}
	for _, d := range work.Tools {
		if d.Name == "browser" {
			t.Error("browser 未暴露时不应下发")
		}
	}
	// 暴露 browser.navigate 后：browser def 只含 navigate
	exposed := o.mkLlmChatReq(&RunRequest{UserID: "u1", Mode: model.ConvModeWork, ClientTools: clientTools}, "sys",
		&reactState{msgs: msgs, exposedTools: map[string]bool{"browser.navigate": true}})
	var browser *llm.ToolDefinition
	for i := range exposed.Tools {
		if exposed.Tools[i].Name == "browser" {
			browser = &exposed.Tools[i]
		}
	}
	if browser == nil {
		t.Error("暴露后应下发 browser def")
	} else if len(browser.Functions) != 1 || browser.Functions[0].Name != "navigate" {
		t.Errorf("browser 应只含 navigate, got %+v", browser.Functions)
	}
	// plan 模式：工具全隐藏（服务端 + 客户端都不下发）
	plan := o.mkLlmChatReq(&RunRequest{UserID: "u1", Mode: model.ConvModePlan, ClientTools: clientTools}, "sys", &reactState{msgs: msgs})
	if plan.Tools != nil && len(plan.Tools) != 0 {
		t.Errorf("plan 模式 tools 应为空, got %d 个", len(plan.Tools))
	}
}

// —— shouldVerify 收尾轮跳过 ——

func TestShouldVerify_SkipInFinalRound(t *testing.T) {
	o := &Orchestrator{verifyEnabled: true}
	state := &reactState{
		allToolCalls: []tool.ToolCall{{Tool: "note", Function: "record_note"}},
	}
	req := &RunRequest{}

	if !o.shouldVerify(req, state) {
		t.Error("正常轮有工具调用时应 verify")
	}
	state.finalRound = true
	if o.shouldVerify(req, state) {
		t.Error("收尾轮应跳过 verify")
	}
}

// —— 辅助 mock ——

// capturingLLMClient 在每次 Chat 调用时捕获请求。
type capturingLLMClient struct {
	responses []mockLLMResponse
	callCount int
	capture   func(req *llm.ChatRequest)
}

func (m *capturingLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	if m.capture != nil {
		m.capture(req)
	}
	if m.callCount >= len(m.responses) {
		return nil, fmt.Errorf("mock: no more responses")
	}
	resp := m.responses[m.callCount]
	m.callCount++
	if resp.err != nil {
		return nil, resp.err
	}
	ch := make(chan *llm.StreamEvent, len(resp.events))
	for _, e := range resp.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (m *capturingLLMClient) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	return llm.Message{}, nil
}

func (m *capturingLLMClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, fmt.Errorf("mock: no more responses")
}

func (m *capturingLLMClient) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return base
}

func (m *capturingLLMClient) ModelName() string { return "mock" }
