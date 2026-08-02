package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

func TestHandleOneRoundResponse_ParseErrorTriggersRetry(t *testing.T) {
	// 非法 JSON → 应触发重试，不标记 done
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	msg := llm.Message{
		Role:    "assistant",
		Content: "这不是合法的 JSON {{{ broken",
	}

	state := &reactState{}
	done := o.handleOneRoundResponse(ctx, events, msg, &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}, "", (*ctxflow.RetrieveResult)(nil), state)

	if done {
		t.Error("expected done=false for parse error (should retry)")
	}
	if state.lastAssistantMsgID != "" {
		t.Errorf("expected empty msgID, got '%s'", state.lastAssistantMsgID)
	}

	// 应追加一条系统重试指令
	if len(state.msgs) != 1 {
		t.Fatalf("expected 1 retry message appended, got %d", len(state.msgs))
	}
	retryMsg := state.msgs[0]
	if retryMsg.Role != "user" {
		t.Errorf("expected retry role='user', got '%s'", retryMsg.Role)
	}
	if !strings.Contains(retryMsg.Content, "[系统]") {
		t.Errorf("expected [系统] in retry, got '%s'", retryMsg.Content)
	}
	if !strings.Contains(retryMsg.Content, "JSON") {
		t.Errorf("expected JSON mention in retry, got '%s'", retryMsg.Content)
	}

	// 应有 status 事件通知前端
	close(events)
	var statusEvents []*llm.StreamEvent
	for evt := range events {
		if evt.Type == "status" {
			statusEvents = append(statusEvents, evt)
		}
	}
	if len(statusEvents) == 0 {
		t.Error("expected status event about retry")
	} else if !strings.Contains(statusEvents[0].Content, "重试") {
		t.Errorf("expected 重试 in status, got '%s'", statusEvents[0].Content)
	}
}

func TestHandleOneRoundResponse_InvalidJSON_UnescapedQuotes(t *testing.T) {
	// LLM 常见错误：JSON 字符串里放了未转义的双引号，应触发重试
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	msg := llm.Message{
		Role:    "assistant",
		Content: `{"reply":"他说"太断子绝孙"是错的","tool_calls":[]}`,
	}

	state := &reactState{}
	done := o.handleOneRoundResponse(ctx, events, msg, &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}, "", (*ctxflow.RetrieveResult)(nil), state)

	if done {
		t.Error("expected done=false for unescaped quotes (should retry)")
	}
	if len(state.msgs) != 1 {
		t.Fatalf("expected 1 retry message, got %d", len(state.msgs))
	}
	if !strings.Contains(state.msgs[0].Content, "[系统]") {
		t.Error("expected retry instruction with [系统]")
	}
}

func TestHandleOneRoundResponse_ValidJSONTextOnly(t *testing.T) {
	// 严格 JSON 模式：纯文本回复带空 tool_calls → 应标记 done
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	msg := llm.Message{
		Role:    "assistant",
		Content: `{"reply":"今天天气不错，适合出去走走"}`,
	}

	state := &reactState{}
	done := o.handleOneRoundResponse(ctx, events, msg, &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}, "", (*ctxflow.RetrieveResult)(nil), state)

	if !done {
		t.Error("expected done=true for text-only JSON reply (no tool calls)")
	}
}

func TestHandleOneRoundResponse_PureTextFallback(t *testing.T) {
	// 严格 JSON 模式：纯文本（无 JSON）→ parseLLMResponse 返回 nil → 触发重试
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	msg := llm.Message{
		Role:    "assistant",
		Content: "今天天气不错",
	}

	state := &reactState{}
	done := o.handleOneRoundResponse(ctx, events, msg, &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}, "", (*ctxflow.RetrieveResult)(nil), state)

	// 纯文本容错接受，正常结束
	if !done {
		t.Error("expected done=true for pure text (graceful accept)")
	}
	if len(state.msgs) != 0 {
		t.Errorf("expected 0 retry messages, got %d", len(state.msgs))
	}
}

// —— newAssistantMessage ——

func TestNewAssistantMessage_Basic(t *testing.T) {
	se := &llm.StreamEvent{
		Type: "done",
	}
	msg := newAssistantMessage("hello", "", se)

	if msg.Role != "assistant" {
		t.Errorf("expected role='assistant', got '%s'", msg.Role)
	}
	if msg.Content != "hello" {
		t.Errorf("expected content='hello', got '%s'", msg.Content)
	}
	if msg.ReasoningContent != "" {
		t.Errorf("expected empty reasoning, got '%s'", msg.ReasoningContent)
	}
}

func TestNewAssistantMessage_WithReasoning(t *testing.T) {
	se := &llm.StreamEvent{Type: "done"}
	msg := newAssistantMessage("reply", "thinking...", se)

	if msg.Content != "reply" {
		t.Errorf("expected content='reply', got '%s'", msg.Content)
	}
	if msg.ReasoningContent != "thinking..." {
		t.Errorf("expected reasoning='thinking...', got '%s'", msg.ReasoningContent)
	}
}

func TestNewAssistantMessage_WithToolCalls(t *testing.T) {
	tc := []llm.ToolCall{{ID: "tc1", Function: llm.ToolCallFunction{Name: "search"}}}
	reqJSON := `{"req":1}`
	respJSON := `{"resp":2}`
	se := &llm.StreamEvent{
		Type:         "done",
		ToolCalls:    tc,
		RequestJSON:  &reqJSON,
		ResponseJSON: &respJSON,
	}
	msg := newAssistantMessage("", "", se)

	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected function name 'search', got '%s'", msg.ToolCalls[0].Function.Name)
	}
	if msg.RequestJSON == nil || *msg.RequestJSON != `{"req":1}` {
		t.Errorf("expected RequestJSON, got %v", msg.RequestJSON)
	}
	if msg.ResponseJSON == nil || *msg.ResponseJSON != `{"resp":2}` {
		t.Errorf("expected ResponseJSON, got %v", msg.ResponseJSON)
	}
}

// —— finalizeRound ——

func TestFinalizeRound_ThoughtDone(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	done := o.finalizeRound(context.Background(), events, se,
		`{"reply":"ok"}`, "thinking...",
		&RunRequest{ConversationID: "cv", UserID: "u1"}, "", (*ctxflow.RetrieveResult)(nil), state,
	)

	if !done {
		t.Error("expected done=true for text-only JSON reply")
	}
	close(events)
	var thoughtDoneFound bool
	for evt := range events {
		if evt.Type == "thought_done" {
			thoughtDoneFound = true
		}
	}
	if !thoughtDoneFound {
		t.Error("expected thought_done event when reasoning is present")
	}
}

func TestFinalizeRound_NoThoughtDoneWithoutReasoning(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	_ = o.finalizeRound(context.Background(), events, se,
		`{"reply":"ok"}`, "", // 空 reasoning
		&RunRequest{ConversationID: "cv", UserID: "u1"}, "", (*ctxflow.RetrieveResult)(nil), state,
	)

	close(events)
	for evt := range events {
		if evt.Type == "thought_done" {
			t.Error("expected no thought_done when reasoning is empty")
		}
	}
}

func TestFinalizeRound_CleanTextExtraction(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	_ = o.finalizeRound(context.Background(), events, se,
		`{"reply":"你好，世界"}`, "",
		&RunRequest{ConversationID: "cv", UserID: "u1"}, "", (*ctxflow.RetrieveResult)(nil), state,
	)

	close(events)
	var textDeltas []string
	for evt := range events {
		if evt.Type == "text_delta" {
			textDeltas = append(textDeltas, evt.Content)
		}
	}
	// 5 个 rune，chunkSize=3 → 2 个 chunk: "你好，" "世界"
	if len(textDeltas) != 2 {
		t.Fatalf("expected 2 text_delta, got %d: %v", len(textDeltas), textDeltas)
	}
	if textDeltas[0] != "你好，" {
		t.Errorf("expected chunk 0 '你好，', got '%s'", textDeltas[0])
	}
	if textDeltas[1] != "世界" {
		t.Errorf("expected chunk 1 '世界', got '%s'", textDeltas[1])
	}
}

func TestFinalizeRound_ReturnsFalseOnParseError(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	done := o.finalizeRound(context.Background(), events, se,
		"这不是合法的 JSON {{{ broken", "",
		&RunRequest{ConversationID: "cv", UserID: "u1"}, "", (*ctxflow.RetrieveResult)(nil), state,
	)

	if done {
		t.Error("expected done=false on parse error (should retry)")
	}
	close(events)
	var hasStatus bool
	for evt := range events {
		if evt.Type == "status" {
			hasStatus = true
		}
	}
	if !hasStatus {
		t.Error("expected status event about retry on parse error")
	}
}

func TestFinalizeRound_ReturnsTrueOnTextOnly(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	done := o.finalizeRound(context.Background(), events, se,
		`{"reply":"ok"}`, "",
		&RunRequest{ConversationID: "cv", UserID: "u1"}, "", (*ctxflow.RetrieveResult)(nil), state,
	)

	// 无 tool_calls → handleOneRoundResponse 返回 done=true
	// 但因为 msgRepo=nil，msgID 为空，所以行为取决于 handleOneRoundResponse 内部逻辑
	// saveAssistantMsg 对于纯文本会返回 ""，导致 *lastMsgID=""，emitDone 仍触发
	if !done {
		t.Error("expected done=true for text-only JSON reply")
	}
	close(events)
	var hasDone bool
	for evt := range events {
		if evt.Type == "done" {
			hasDone = true
		}
	}
	if !hasDone {
		t.Error("expected done event")
	}
}

// —— streamTextChunks ——

func TestStreamTextChunks_ShortText(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	events := make(chan *llm.StreamEvent, 16)

	o.streamTextChunks(context.Background(), events, "好的")
	close(events)

	var texts []string
	for evt := range events {
		if evt.Type == "text_delta" {
			texts = append(texts, evt.Content)
		}
	}
	if len(texts) != 1 {
		t.Fatalf("short text: expected 1 chunk, got %d", len(texts))
	}
	if texts[0] != "好的" {
		t.Errorf("expected '好的', got '%s'", texts[0])
	}
}

func TestStreamTextChunks_LongText(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	events := make(chan *llm.StreamEvent, 64)

	o.streamTextChunks(context.Background(), events, "你好世界今天天气不错")
	close(events)

	var texts []string
	for evt := range events {
		if evt.Type == "text_delta" {
			texts = append(texts, evt.Content)
		}
	}
	// 10个 rune，chunkSize=3 → expect 4 chunks: "你好世" "界今天" "天气不" "错"
	if len(texts) != 4 {
		t.Fatalf("long text: expected 4 chunks, got %d: %v", len(texts), texts)
	}
	if texts[0] != "你好世" {
		t.Errorf("chunk 0: expected '你好世', got '%s'", texts[0])
	}
	if texts[1] != "界今天" {
		t.Errorf("chunk 1: expected '界今天', got '%s'", texts[1])
	}
	if texts[2] != "天气不" {
		t.Errorf("chunk 2: expected '天气不', got '%s'", texts[2])
	}
	if texts[3] != "错" {
		t.Errorf("chunk 3: expected '错', got '%s'", texts[3])
	}
}

func TestStreamTextChunks_CancelMidStream(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	events := make(chan *llm.StreamEvent, 64)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	o.streamTextChunks(ctx, events, "你好世界今天天气不错")
	close(events)

	var texts []string
	for evt := range events {
		if evt.Type == "text_delta" {
			texts = append(texts, evt.Content)
		}
	}
	if len(texts) > 0 {
		t.Errorf("cancelled: expected 0 chunks, got %d: %v", len(texts), texts)
	}
}

func TestStreamTextChunks_EmptyText(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	events := make(chan *llm.StreamEvent, 16)

	o.streamTextChunks(context.Background(), events, "")
	close(events)

	var texts []string
	for evt := range events {
		if evt.Type == "text_delta" {
			texts = append(texts, evt.Content)
		}
	}
	if len(texts) != 0 {
		t.Errorf("empty text: expected 0 chunks, got %d", len(texts))
	}
}

// —— mkLlmChatReq ——

func ptr[T any](v T) *T { return &v }

func TestMkLlmChatReq_ParameterResolution(t *testing.T) {
	defaultTemp := ptr(0.5)
	defaultMax := ptr(2048)

	tests := []struct {
		name string
		// Orchestrator fields
		proClient   llm.LLMClient
		defaultTemp *float64
		defaultMax  *int
		// req fields
		deepMode bool
		reqTemp  *float64
		reqMax   *int
		reqThink *bool
		// expected
		wantTemp  float64
		wantMax   int
		wantThink *bool
	}{
		{
			name:        "all req fields set, no fallback",
			proClient:   &mockLLMClient{},
			defaultTemp: defaultTemp,
			defaultMax:  defaultMax,
			deepMode:    false,
			reqTemp:     ptr(0.8),
			reqMax:      ptr(4096),
			reqThink:    ptr(true),
			wantTemp:    0.8,
			wantMax:     4096,
			wantThink:   ptr(true),
		},
		{
			name:        "temperature nil falls back to default",
			proClient:   &mockLLMClient{},
			defaultTemp: ptr(0.7),
			defaultMax:  defaultMax,
			reqTemp:     nil,
			reqMax:      ptr(1024),
			reqThink:    nil,
			wantTemp:    0.7,
			wantMax:     1024,
			wantThink:   nil,
		},
		{
			name:        "maxTokens nil falls back to default",
			proClient:   &mockLLMClient{},
			defaultTemp: defaultTemp,
			defaultMax:  ptr(3072),
			reqTemp:     ptr(1.0),
			reqMax:      nil,
			reqThink:    nil,
			wantTemp:    1.0,
			wantMax:     3072,
			wantThink:   nil,
		},
		{
			name:        "deepMode with proClient auto-enables thinking",
			proClient:   &mockLLMClient{},
			defaultTemp: defaultTemp,
			defaultMax:  defaultMax,
			deepMode:    true,
			reqTemp:     nil,
			reqMax:      nil,
			reqThink:    nil,
			wantTemp:    *defaultTemp,
			wantMax:     *defaultMax,
			wantThink:   ptr(true),
		},
		{
			name:        "deepMode but no proClient, thinking stays nil",
			proClient:   nil,
			defaultTemp: defaultTemp,
			defaultMax:  defaultMax,
			deepMode:    true,
			reqTemp:     nil,
			reqMax:      nil,
			reqThink:    nil,
			wantTemp:    *defaultTemp,
			wantMax:     *defaultMax,
			wantThink:   nil,
		},
		{
			name:        "thinking explicitly false overrides deepMode auto-enable",
			proClient:   &mockLLMClient{},
			defaultTemp: defaultTemp,
			defaultMax:  defaultMax,
			deepMode:    true,
			reqTemp:     nil,
			reqMax:      nil,
			reqThink:    ptr(false),
			wantTemp:    *defaultTemp,
			wantMax:     *defaultMax,
			wantThink:   ptr(false),
		},
		{
			name:        "deepMode=false, proClient exists, thinking stays nil",
			proClient:   &mockLLMClient{},
			defaultTemp: defaultTemp,
			defaultMax:  defaultMax,
			deepMode:    false,
			reqTemp:     nil,
			reqMax:      nil,
			reqThink:    nil,
			wantTemp:    *defaultTemp,
			wantMax:     *defaultMax,
			wantThink:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Orchestrator{
				contextFormatter:   &mockFormatter{},
				proClient:          tt.proClient,
				defaultTemperature: tt.defaultTemp,
				defaultMaxTokens:   tt.defaultMax,
			}
			req := &RunRequest{
				UserID:      "u1",
				DeepMode:    tt.deepMode,
				Temperature: tt.reqTemp,
				MaxTokens:   tt.reqMax,
				Thinking:    tt.reqThink,
			}
			result := "sys-prompt"
			msgs := []llm.Message{{Role: "user", Content: "hi"}}

			cr := o.mkLlmChatReq(req, result, msgs, false)

			if cr.SystemPrompt != "sys-prompt" {
				t.Errorf("SystemPrompt: want 'sys-prompt', got '%s'", cr.SystemPrompt)
			}
			if *cr.Temperature != tt.wantTemp {
				t.Errorf("Temperature: want %v, got %v", tt.wantTemp, *cr.Temperature)
			}
			if *cr.MaxTokens != tt.wantMax {
				t.Errorf("MaxTokens: want %d, got %d", tt.wantMax, *cr.MaxTokens)
			}
			if tt.wantThink == nil {
				if cr.Thinking != nil {
					t.Errorf("Thinking: want nil, got %v", *cr.Thinking)
				}
			} else {
				if cr.Thinking == nil {
					t.Error("Thinking: want non-nil, got nil")
				} else if *cr.Thinking != *tt.wantThink {
					t.Errorf("Thinking: want %v, got %v", *tt.wantThink, *cr.Thinking)
				}
			}
		})
	}
}

func TestMkLlmChatReq_PassThroughFields(t *testing.T) {
	o := &Orchestrator{
		contextFormatter:   &mockFormatter{},
		defaultTemperature: ptr(0.5),
		defaultMaxTokens:   ptr(2048),
	}

	result := "custom system prompt"
	msgs := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	req := &RunRequest{
		UserID:      "user-42",
		Temperature: ptr(0.9),
		MaxTokens:   ptr(4096),
	}

	cr := o.mkLlmChatReq(req, result, msgs, false)

	if cr.SystemPrompt != "custom system prompt" {
		t.Errorf("SystemPrompt: want 'custom system prompt', got '%s'", cr.SystemPrompt)
	}
	if len(cr.Messages) != 2 {
		t.Fatalf("Messages: want 2, got %d", len(cr.Messages))
	}
	if cr.Messages[0].Content != "hello" {
		t.Errorf("Messages[0].Content: want 'hello', got '%s'", cr.Messages[0].Content)
	}
	if cr.UserID != "user-42" {
		t.Errorf("UserID: want 'user-42', got '%s'", cr.UserID)
	}
	if cr.Tools != nil {
		t.Errorf("Tools: want nil, got %v", cr.Tools)
	}
}

func TestMkLlmChatReq_DefaultsNilWhenNotSet(t *testing.T) {
	// Orchestrator without defaults → Temperature/MaxTokens should stay nil
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	req := &RunRequest{
		UserID:      "u1",
		Temperature: nil,
		MaxTokens:   nil,
	}
	msgs := []llm.Message{}

	cr := o.mkLlmChatReq(req, "", msgs, false)

	if cr.Temperature != nil {
		t.Errorf("Temperature: want nil, got %v", *cr.Temperature)
	}
	if cr.MaxTokens != nil {
		t.Errorf("MaxTokens: want nil, got %d", *cr.MaxTokens)
	}
	if cr.Thinking != nil {
		t.Errorf("Thinking: want nil, got %v", cr.Thinking)
	}
}

// —— runOneRound ——

// recordingLLMClient 包装 mockLLMClient，记录最后一次 Chat 请求。
type recordingLLMClient struct {
	mockLLMClient
	lastReq *llm.ChatRequest
}

func (m *recordingLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	m.lastReq = req
	return m.mockLLMClient.Chat(ctx, req)
}

func newTestOrch() *Orchestrator {
	return &Orchestrator{
		contextFormatter:   &mockFormatter{},
		msgRepo:            nil,
		defaultTemperature: ptr(0.5),
		defaultMaxTokens:   ptr(2048),
	}
}

func TestRunOneRound_ContextCancelled(t *testing.T) {
	o := newTestOrch()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{msgs: []llm.Message{}}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	done := o.runOneRound(ctx, events, &mockLLMClient{}, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if !done {
		t.Error("expected done=true when context cancelled")
	}
	close(events)
	var errorCount, doneCount int
	for evt := range events {
		switch evt.Type {
		case "error":
			errorCount++
			if evt.Error != UserErrorMessage(ErrContextCancelled) {
				t.Errorf("expected ErrContextCancelled, got '%s'", evt.Error)
			}
		case "done":
			doneCount++
		}
	}
	if errorCount != 1 {
		t.Errorf("expected 1 error event, got %d", errorCount)
	}
	if doneCount != 1 {
		t.Errorf("expected 1 done event, got %d", doneCount)
	}
}

func TestRunOneRound_LLMError(t *testing.T) {
	o := newTestOrch()
	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{msgs: []llm.Message{}}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	client := &mockLLMClient{
		responses: []mockLLMResponse{{err: fmt.Errorf("API timeout")}},
	}

	done := o.runOneRound(ctx, events, client, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if !done {
		t.Error("expected done=true on LLM error")
	}
	close(events)
	var errorEvents []*llm.StreamEvent
	for evt := range events {
		if evt.Type == "error" {
			errorEvents = append(errorEvents, evt)
		}
	}
	if len(errorEvents) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(errorEvents))
	}
	if errorEvents[0].Error != UserErrorMessage(ErrLLMApi) {
		t.Errorf("expected ErrLLMApi, got '%s'", errorEvents[0].Error)
	}
}

func TestRunOneRound_StreamError(t *testing.T) {
	o := newTestOrch()
	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{msgs: []llm.Message{}}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: []*llm.StreamEvent{
				{Type: "text_delta", Content: "partial..."},
				{Type: "error", Error: "stream broken"},
			},
		}},
	}

	done := o.runOneRound(ctx, events, client, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if !done {
		t.Error("expected done=true on stream error")
	}
	close(events)
	for evt := range events {
		if evt.Type == "error" && evt.Error == "stream broken" {
			return // found it
		}
	}
	t.Error("expected forwarded error event 'stream broken'")
}

func TestRunOneRound_DoneTextOnly(t *testing.T) {
	o := newTestOrch()
	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	state := &reactState{msgs: []llm.Message{}}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas(`{"reply":"hello world"}`), makeDone()),
		}},
	}

	done := o.runOneRound(ctx, events, client, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if !done {
		t.Error("expected done=true for text-only reply")
	}
	close(events)
	text := collectTextDeltasFromChan(events)
	if text != "hello world" {
		t.Errorf("expected clean text 'hello world', got '%s'", text)
	}
}

func TestRunOneRound_DoneParseErrorRetry(t *testing.T) {
	o := newTestOrch()
	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	state := &reactState{msgs: []llm.Message{}}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: append(makeTextDeltas("not valid json {{{"), makeDone()),
		}},
	}

	done := o.runOneRound(ctx, events, client, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if done {
		t.Error("expected done=false on parse error (should retry)")
	}
	close(events)
	// 应有 status 事件通知前端重试
	var hasStatus bool
	for evt := range events {
		if evt.Type == "status" {
			hasStatus = true
		}
	}
	if !hasStatus {
		t.Error("expected status event about retry")
	}
	// state.msgs 应被追加了重试消息
	if len(state.msgs) != 1 {
		t.Fatalf("expected 1 retry message in state.msgs, got %d", len(state.msgs))
	}
	if !strings.Contains(state.msgs[0].Content, "[系统]") {
		t.Errorf("expected [系统] retry instruction, got '%s'", state.msgs[0].Content)
	}
}

func TestRunOneRound_DoneWithReasoning(t *testing.T) {
	o := newTestOrch()
	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	state := &reactState{msgs: []llm.Message{}}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: []*llm.StreamEvent{
				{Type: "thought_delta", Content: "I need to think..."},
				{Type: "text_delta", Content: `{"reply":"ok"}`},
				makeDone(),
			},
		}},
	}

	done := o.runOneRound(ctx, events, client, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if !done {
		t.Error("expected done=true")
	}
	close(events)
	var hasThoughtDone bool
	for evt := range events {
		if evt.Type == "thought_done" {
			hasThoughtDone = true
		}
	}
	if !hasThoughtDone {
		t.Error("expected thought_done event when reasoning present")
	}
}

func TestRunOneRound_EmptyStream(t *testing.T) {
	o := newTestOrch()
	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	state := &reactState{msgs: []llm.Message{}}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	client := &mockLLMClient{
		responses: []mockLLMResponse{{
			events: []*llm.StreamEvent{}, // 空流，无 done 无 error
		}},
	}

	done := o.runOneRound(ctx, events, client, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if done {
		t.Error("expected done=false when stream closes without done/error")
	}
}

func TestRunOneRound_UsesStateMsgs(t *testing.T) {
	o := newTestOrch()
	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 256)
	state := &reactState{
		msgs: []llm.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
	}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}

	recorder := &recordingLLMClient{
		mockLLMClient: mockLLMClient{
			responses: []mockLLMResponse{{
				events: append(makeTextDeltas(`{"reply":"ok"}`), makeDone()),
			}},
		},
	}

	o.runOneRound(ctx, events, recorder, req, "", (*ctxflow.RetrieveResult)(nil), state, 0)

	if recorder.lastReq == nil {
		t.Fatal("expected Chat to be called")
	}
	if len(recorder.lastReq.Messages) != 2 {
		t.Fatalf("expected 2 messages in Chat request, got %d", len(recorder.lastReq.Messages))
	}
	if recorder.lastReq.Messages[1].Content != "hello" {
		t.Errorf("expected assistant msg 'hello', got '%s'", recorder.lastReq.Messages[1].Content)
	}
}

// collectTextDeltasFromChan 从已关闭的 channel 中收集所有 text_delta。
func collectTextDeltasFromChan(ch chan *llm.StreamEvent) string {
	var b strings.Builder
	for evt := range ch {
		if evt.Type == "text_delta" {
			b.WriteString(evt.Content)
		}
	}
	return b.String()
}

// —— FlattenRound ——

func TestFlattenRound_Basic(t *testing.T) {
	now := time.Now()
	round := []model.Message{
		{ID: "u1", Role: "user", Content: "帮我记账", CreatedAt: now},
		{ID: "a1", Role: "assistant", Content: "好的，已记录", CreatedAt: now.Add(time.Second)},
	}
	got := FlattenRound(round)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].ID != "u1" || got[1].ID != "a1" {
		t.Errorf("expected [u1, a1], got [%s, %s]", got[0].ID, got[1].ID)
	}
}

func TestFlattenRound_SkipsInternalAssistant(t *testing.T) {
	now := time.Now()
	// a1 和 a2 都保留，draft(internal) 和 system 过滤
	round := []model.Message{
		{ID: "u1", Role: "user", Content: "查账单", CreatedAt: now},
		{ID: "a1", Role: "assistant", Content: "账单：22元", CreatedAt: now.Add(time.Second)},
		{ID: "draft", Role: "assistant", Content: "账单：22元（草稿）", CreatedAt: now.Add(2 * time.Second), IsInternal: true},
		{ID: "sys", Role: "system", Content: "检验不通过", CreatedAt: now.Add(3 * time.Second), IsInternal: true},
		{ID: "a2", Role: "assistant", Content: "账单：福佳白啤22元，5月1日", CreatedAt: now.Add(4 * time.Second)},
	}
	got := FlattenRound(round)
	if len(got) != 3 {
		t.Fatalf("expected 3 (u1+a1+a2, draft+system filtered), got %d: %v", len(got), got)
	}
	if got[1].ID != "a1" || got[2].ID != "a2" {
		t.Errorf("expected [a1, a2], got [%s, %s]", got[1].ID, got[2].ID)
	}
}

func TestFlattenRound_ReActMultiRound(t *testing.T) {
	now := time.Now()
	// 保留完整 ReAct 链：user + 所有 assistant(tool_calls) + tool 结果
	round := []model.Message{
		{ID: "u1", Role: "user", Content: "今天干了什么", CreatedAt: now},
		{ID: "a1", Role: "assistant", Content: `{"reply":"","tool_calls":[{"tool":"note","function":"search"}]}`, CreatedAt: now.Add(time.Second)},
		{ID: "t1", Role: "tool", Content: `[{"id":"n1","content":"..."}]`, CreatedAt: now.Add(2 * time.Second), IsInternal: true},
		{ID: "a2", Role: "assistant", Content: `{"reply":"","tool_calls":[{"tool":"memory_search","function":"search"}]}`, CreatedAt: now.Add(3 * time.Second)},
		{ID: "t2", Role: "tool", Content: `[{"id":"m1","text":"..."}]`, CreatedAt: now.Add(4 * time.Second), IsInternal: true},
		{ID: "a3", Role: "assistant", Content: "今天你记了笔记、喝了啤酒，过得挺充实😄", CreatedAt: now.Add(5 * time.Second)},
	}
	got := FlattenRound(round)
	// u1 + a1 + t1 + a2 + t2 + a3 = 6
	if len(got) != 6 {
		t.Fatalf("expected 6 (full ReAct chain), got %d", len(got))
	}
	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.ID
	}
	wantIDs := []string{"u1", "a1", "t1", "a2", "t2", "a3"}
	for i, want := range wantIDs {
		if ids[i] != want {
			t.Errorf("pos %d: expected %s, got %s", i, want, ids[i])
		}
	}
}

func TestFlattenRound_OnlyAssistantIsInternal(t *testing.T) {
	now := time.Now()
	// internal assistant draft 被过滤，只剩 user
	round := []model.Message{
		{ID: "u1", Role: "user", Content: "查一下", CreatedAt: now},
		{ID: "draft", Role: "assistant", Content: "草稿回复", CreatedAt: now.Add(time.Second), IsInternal: true},
	}
	got := FlattenRound(round)
	if len(got) != 1 {
		t.Fatalf("expected 1 (user only), got %d: %v", len(got), got)
	}
	if got[0].ID != "u1" {
		t.Errorf("expected user u1, got %s", got[0].ID)
	}
}
