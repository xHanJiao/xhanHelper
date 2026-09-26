package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// mockFormatter 简单实现 ContextFormatter 接口供测试使用
type mockFormatter struct{}

func (m *mockFormatter) FormatUserMessage(packets map[string][]retrieval.ContextPacket, userQuery string) string {
	return "<user_query>" + userQuery + "</user_query>"
}

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

type mockUserProfileFactRepo struct{}

func (m *mockUserProfileFactRepo) GetActiveByUser(ctx context.Context, userID string) ([]model.UserProfileFact, error) {
	return nil, nil
}

type mockConvRepo struct {
	touchCalls int
	extra      *model.ConversationExtra // 模拟会话 Extra（GetExtra 返回，UpdateExtraField 更新）
}

func (m *mockConvRepo) Touch(ctx context.Context, id string) error {
	m.touchCalls++
	return nil
}

func (m *mockConvRepo) GetExtra(ctx context.Context, convID string) (*model.ConversationExtra, error) {
	if m.extra == nil {
		return &model.ConversationExtra{}, nil
	}
	return m.extra, nil
}

func (m *mockConvRepo) UpdateExtraField(ctx context.Context, convID string, fn func(extra *model.ConversationExtra)) error {
	e := m.GetExtraSync()
	fn(e)
	m.extra = e
	return nil
}

func (m *mockConvRepo) GetExtraSync() *model.ConversationExtra {
	base := &model.ConversationExtra{}
	if m.extra != nil {
		base = m.extra
	}
	b, _ := json.Marshal(base)
	var e model.ConversationExtra
	_ = json.Unmarshal(b, &e)
	return &e
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
		contextFormatter: &mockFormatter{},
		msgRepo:          msgRepo,
		convRepo:         convRepo,
		profileFactRepo:  &mockUserProfileFactRepo{},
		registry:         tool.NewRegistry(),
		maxIterations:    3,
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

func TestRunReActLoop_TextContainingJSON_SavedVerbatim(t *testing.T) {
	// 回归：主聊天只走原生 FC，正文里的 JSON（命令、JSON-RPC 示例）不再被当 Flash 信封解析，
	// 流式输出与落库内容都必须是完整原文（修复前会从第一个 '{' 起被截断）。
	msgRepo := &recordingMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	content := "在你的终端里跑：\n\n```\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}' | opencode acp\n```\n"
	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(content), makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "怎么发？",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, "test prompt", nil, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	if text := collectTextDeltas(collected); text != content {
		t.Errorf("流式文本被截断/改写\n got: %q\nwant: %q", text, content)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event")
	}

	var savedAssistant string
	for _, m := range msgRepo.saved {
		if m.Role == "assistant" {
			savedAssistant = m.Content
		}
	}
	if savedAssistant != content {
		t.Errorf("落库内容被截断/改写\n got: %q\nwant: %q", savedAssistant, content)
	}
}

func TestRunReActLoop_JSONEnvelopeText_NotStripped(t *testing.T) {
	// 回归：即使是 {"reply":...} 这种信封形态的文本，主聊天也不再抽取 reply，
	// 一律按原文处理（信封解析只属于反思链路的 prompt engineering）。
	msgRepo := &recordingMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	content := `{"reply":"今天天气不错","tool_calls":[]}`
	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(content), makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := "test prompt"
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "天气如何",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	if text := collectTextDeltas(collected); text != content {
		t.Errorf("expected raw envelope text streamed, got '%s'", text)
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
	result := "test prompt"
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "天气如何",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, nil, req)

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

func TestRunReActLoop_InvalidJSONText_NoRetry(t *testing.T) {
	// 主聊天无 JSON 契约：非法 JSON 文本也只是普通 markdown，不重试、不报错，原文推送
	msgRepo := &recordingMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	content := `{"reply":"broken`
	client := &mockLLMClient{
		responses: []mockLLMResponse{
			{events: append(makeTextDeltas(content), makeDone())},
		},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := "test prompt"
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hi",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	if status := findStatusEvent(collected); status != nil {
		t.Errorf("不应有重试 status, got: '%s'", status.Content)
	}
	if client.callCount != 1 {
		t.Errorf("不应重试（应只调用 1 次 LLM）, got %d", client.callCount)
	}
	if text := collectTextDeltas(collected); text != content {
		t.Errorf("expected raw text streamed, got '%s'", text)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event")
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
	result := "test prompt"
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hi",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, nil, req)

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
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
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

func TestRunReActLoop_PrefixTextWithJSON_Passthrough(t *testing.T) {
	// 前导文本 + JSON 信封形态文本 → 一律按原文处理，不抽取 prefix/reply，不重试
	msgRepo := &recordingMsgRepo{}
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
	result := "test prompt"
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "查看一下",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	// 不应有重试 status
	status := findStatusEvent(collected)
	if status != nil {
		t.Errorf("expected no retry, got status: '%s'", status.Content)
	}

	if text := collectTextDeltas(collected); text != content {
		t.Errorf("expected raw text passthrough, got '%s'", text)
	}
	if !hasDoneEvent(collected) {
		t.Error("expected done event")
	}
}

func TestRunReActLoop_TextStreamsLive(t *testing.T) {
	// 不再有「首字符 { 暂缓推送」：文本 delta 原样实时转发，不重排、不合并
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)

	content := `{"reply":"你好，世界！"}`
	deltas := makeTextDeltas(content)
	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(deltas, makeDone()),
		}},
	}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	result := "test prompt"
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hello",
	}

	o.flashClient = client
	go o.runReActLoop(ctx, events, result, nil, nil, req)

	var collected []*llm.StreamEvent
	for evt := range events {
		collected = append(collected, evt)
	}

	var forwarded []string
	for _, e := range collected {
		if e.Type == "text_delta" {
			forwarded = append(forwarded, e.Content)
		}
	}
	if len(forwarded) != len(deltas) {
		t.Fatalf("expected %d live deltas, got %d: %v", len(deltas), len(forwarded), forwarded)
	}
	for i := range deltas {
		if forwarded[i] != deltas[i].Content {
			t.Errorf("delta %d mutated: got %q want %q", i, forwarded[i], deltas[i].Content)
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
		contextFormatter:    &mockFormatter{},
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

func TestRunReActLoop_DoneCarriesContextWindow(t *testing.T) {
	msgRepo := &mockMsgRepo{}
	convRepo := &mockConvRepo{}
	o := newTestOrchestrator(msgRepo, convRepo)
	o.contextWindow = 1_000_000
	o.strongContextWindow = 2_000_000

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(`{"reply":"hi"}`), makeDone()),
		}},
	}
	o.flashClient = client
	o.proClient = client

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	req := &RunRequest{
		ConversationID:     "conv-1",
		UserID:             "user-1",
		UserMessageContent: "hi",
	}
	go o.runReActLoop(ctx, events, "test prompt", nil, nil, req)

	var done *llm.StreamEvent
	for evt := range events {
		if evt.Type == "done" {
			done = evt
		}
	}
	if done == nil {
		t.Fatal("expected done event")
	}
	// makeDone 的 PromptTokens=10，弱档上限 1_000_000
	if !strings.Contains(done.Content, `"context_tokens":10`) {
		t.Errorf("done missing context_tokens: %s", done.Content)
	}
	if !strings.Contains(done.Content, `"context_window":1000000`) {
		t.Errorf("done missing weak context_window: %s", done.Content)
	}

	// DeepMode → 强档上限
	client2 := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(`{"reply":"hi"}`), makeDone()),
		}},
	}
	o.proClient = client2
	events2 := make(chan *llm.StreamEvent, 256)
	req2 := &RunRequest{
		ConversationID:     "conv-2",
		UserID:             "user-1",
		UserMessageContent: "hi",
		DeepMode:           true,
	}
	go o.runReActLoop(ctx, events2, "test prompt", nil, nil, req2)
	var done2 *llm.StreamEvent
	for evt := range events2 {
		if evt.Type == "done" {
			done2 = evt
		}
	}
	if done2 == nil {
		t.Fatal("expected done event (deep mode)")
	}
	if !strings.Contains(done2.Content, `"context_window":2000000`) {
		t.Errorf("done missing strong context_window: %s", done2.Content)
	}
}
