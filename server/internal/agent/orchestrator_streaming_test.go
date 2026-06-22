package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// —— mock 实现 ——

type mockMsgRepo struct {
	createCalls int
}

func (m *mockMsgRepo) Create(ctx context.Context, msg *model.Message) error {
	m.createCalls++
	return nil
}

func (m *mockMsgRepo) ListRecentUserMessages(ctx context.Context, convID string, limit int) ([]model.Message, error) {
	return nil, nil
}

type mockConvRepo struct {
	touchCalls int
}

func (m *mockConvRepo) Touch(ctx context.Context, id string) error {
	m.touchCalls++
	return nil
}

type mockLLMResponse struct {
	events []*llm.StreamEvent
	err    error
}

type mockLLMClient struct {
	responses []mockLLMResponse
	callCount int
}

func (m *mockLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
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

func (m *mockLLMClient) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	return llm.Message{}, nil
}

func (m *mockLLMClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, fmt.Errorf("mock: ChatWithRawJSON not implemented")
}

func (m *mockLLMClient) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return base
}

func (m *mockLLMClient) ModelName() string { return "mock" }

// —— helpers ——

func newTestOrchestrator(msgRepo msgCreator, convRepo convToucher) *Orchestrator {
	return &Orchestrator{
		msgRepo:       msgRepo,
		convRepo:      convRepo,
		maxIterations: 3,
	}
}

// makeTextDeltas 将完整文本随机拆成不定长 text_delta 事件，模拟真实 LLM 流式输出。
// 使用固定种子保证可复现。
func makeTextDeltas(content string) []*llm.StreamEvent {
	rng := rand.New(rand.NewSource(42))
	var events []*llm.StreamEvent
	runes := []rune(content)
	for i := 0; i < len(runes); {
		// 随机 1~10 个字符为一块
		chunkSize := 1 + rng.Intn(10)
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		events = append(events, &llm.StreamEvent{
			Type:    "text_delta",
			Content: string(runes[i:end]),
		})
		i = end
	}
	return events
}

// makeDone 创建一个 done 事件
func makeDone() *llm.StreamEvent {
	return &llm.StreamEvent{
		Type:  "done",
		Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5},
	}
}

func collectTextDeltas(events []*llm.StreamEvent) string {
	var b strings.Builder
	for _, e := range events {
		if e.Type == "text_delta" {
			b.WriteString(e.Content)
		}
	}
	return b.String()
}

func findStatusEvent(events []*llm.StreamEvent) *llm.StreamEvent {
	for _, e := range events {
		if e.Type == "status" {
			return e
		}
	}
	return nil
}

func hasDoneEvent(events []*llm.StreamEvent) bool {
	for _, e := range events {
		if e.Type == "done" {
			return true
		}
	}
	return false
}

// —— 测试用例 ——

func TestRunReActLoop_StrictJSON_TextOnlyReply(t *testing.T) {
	// LLM 返回 {"reply":"hello"} → clean text "hello" → done
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	jsonContent := `{"reply":"hello"}`
	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(jsonContent), makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)

	result := &promptbuilder.BuildResult{Prompt: "test prompt"}
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "你好",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	// 应提取到 "hello"
	text := collectTextDeltas(collected)
	if text != "hello" {
		t.Errorf("expected text_delta='hello', got '%s'", text)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event")
	}
}

func TestRunReActLoop_StrictJSON_ReplyWithEmptyToolCalls(t *testing.T) {
	// LLM 返回 {"reply":"今天天气不错","tool_calls":[]} → clean text → done
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	jsonContent := `{"reply":"今天天气不错","tool_calls":[]}`
	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(jsonContent), makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := &promptbuilder.BuildResult{Prompt: "test prompt"}
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "天气如何",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	text := collectTextDeltas(collected)
	if text != "今天天气不错" {
		t.Errorf("expected text_delta='今天天气不错', got '%s'", text)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event")
	}
}

func TestRunReActLoop_PureText_AcceptedGracefully(t *testing.T) {
	// 纯文本 → 无 JSON → 容错接受，直接推送全文
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas("今天天气不错"), makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := &promptbuilder.BuildResult{Prompt: "test prompt"}
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "天气如何",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	// 不应有重试 status
	status := findStatusEvent(collected)
	if status != nil {
		t.Errorf("expected no retry, got status: '%s'", status.Content)
	}

	// 全文直接推送
	text := collectTextDeltas(collected)
	if text != "今天天气不错" {
		t.Errorf("expected full text '今天天气不错', got '%s'", text)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event")
	}
}

func TestRunReActLoop_StrictJSON_InvalidJSONTriggersRetry(t *testing.T) {
	// 非法 JSON → parse error → 重试
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	client := &mockLLMClient{
		responses: []mockLLMResponse{
			{events: append(makeTextDeltas(`{"reply":"broken`), makeDone())},
			{events: append(makeTextDeltas(`{"reply":"fixed now"}`), makeDone())},
		},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := &promptbuilder.BuildResult{Prompt: "test prompt"}
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hi",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	status := findStatusEvent(collected)
	if status == nil {
		t.Fatal("expected status event about retry")
	}
	if !strings.Contains(status.Content, "重试") {
		t.Errorf("status should mention 重试, got '%s'", status.Content)
	}

	// 中间轮（parse error 重试）不再推送 text_delta，只展示最终轮正确 JSON 的 reply
	text := collectTextDeltas(collected)
	if text != "fixed now" {
		t.Errorf("expected only final round text, got '%s'", text)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event after retry succeeded")
	}
}

func TestRunReActLoop_LLMError(t *testing.T) {
	// LLM Chat 返回 error → error 事件转发
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			err: fmt.Errorf("LLM API timeout"),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := &promptbuilder.BuildResult{Prompt: "test prompt"}
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hi",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	var errorEvents []*llm.StreamEvent
	for _, e := range collected {
		if e.Type == "error" {
			errorEvents = append(errorEvents, e)
		}
	}
	if len(errorEvents) == 0 {
		t.Fatal("expected error event when LLM fails")
	}
}

func TestRun_EmptyConversationID(t *testing.T) {
	// ConversationID 为空 → Run 直接返回 error
	o := &Orchestrator{}
	req := &RunRequest{
		ConversationID:     "", // 空！
		UserID:             "user-1",
		UserMessageContent: "hi",
	}

	_, err := o.Run(context.Background(), req)

	if err == nil {
		t.Fatal("expected error for empty conversation_id")
	}
	if err.Error() != "conversation id is required" {
		t.Errorf("expected 'conversation id is required', got '%s'", err.Error())
	}
}

func TestRunReActLoop_JSONWithPrefixText_AcceptedGracefully(t *testing.T) {
	// 容错：前导文本 + JSON → prefix 拼入 reply，正常结束，不重试
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	content := "好的，我来查看\n{\"reply\":\"正在查询...\",\"tool_calls\":[]}"
	client := &mockLLMClient{
		responses: []mockLLMResponse{
			{events: append(makeTextDeltas(content), makeDone())},
		},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := &promptbuilder.BuildResult{Prompt: "test prompt"}
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "查看一下",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	// 不应有重试 status
	status := findStatusEvent(collected)
	if status != nil {
		t.Errorf("expected no retry, got status: '%s'", status.Content)
	}

	// prefix 拼入 reply，正常结束
	text := collectTextDeltas(collected)
	if text != "好的，我来查看\n\n正在查询..." {
		t.Errorf("expected prefix merged into reply, got '%s'", text)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event")
	}
}

func TestRunReActLoop_StreamingBuffersUntilDone(t *testing.T) {
	// 验证：原始 JSON 碎片不推到前端，streamTextChunks 按 chunk 模拟流式
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	// LLM 返回很多 JSON 碎片，前端应收到的不是原始碎片而是按 chunk 拆分后的干净文本
	jsonContent := `{"reply":"你好，世界！"}`
	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(jsonContent), makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := &promptbuilder.BuildResult{Prompt: "test prompt"}
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hello",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	// 6 个 rune，chunkSize=3 → 2 个 chunk: "你好，" "世界！"
	var deltas []string
	for _, e := range collected {
		if e.Type == "text_delta" {
			deltas = append(deltas, e.Content)
		}
	}
	if len(deltas) != 2 {
		t.Errorf("expected 2 text_delta chunks, got %d: %v", len(deltas), deltas)
	}
	if len(deltas) >= 2 {
		if deltas[0] != "你好，" {
			t.Errorf("expected chunk 0 '你好，', got '%s'", deltas[0])
		}
		if deltas[1] != "世界！" {
			t.Errorf("expected chunk 1 '世界！', got '%s'", deltas[1])
		}
	}
}

// —— promptBuilder error ——

type mockPromptBuilder struct {
	buildErr error
}

func (m *mockPromptBuilder) Build(ctx context.Context, opts promptbuilder.BuildOptions) (*promptbuilder.BuildResult, error) {
	return nil, m.buildErr
}

func TestRun_PromptBuildError(t *testing.T) {
	o := &Orchestrator{
		flashClient:         &mockLLMClient{},
		systemPromptBuilder: &mockPromptBuilder{buildErr: fmt.Errorf("theme state query failed")},
	}

	ctx := context.Background()
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hi",
	}

	_, err := o.Run(ctx, req)
	if err == nil {
		t.Fatal("expected error from Run when prompt build fails")
	}
	if err.Error() != "theme state query failed" {
		t.Errorf("expected 'theme state query failed', got '%s'", err.Error())
	}
}
