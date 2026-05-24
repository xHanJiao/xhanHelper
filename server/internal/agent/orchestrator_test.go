package agent

import (
	"context"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

func TestHandleFlashResponse_ParseErrorTriggersRetry(t *testing.T) {
	// 非法 JSON → 应触发重试，不标记 done
	o := &Orchestrator{msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	var msgs []llm.Message
	msg := llm.Message{
		Role:    "assistant",
		Content: "这不是合法的 JSON {{{ broken",
	}

	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, newMsgID := o.handleFlashResponse(ctx, events, &msgs, msg, "test-conv-1", "test-user-1", &allToolCalls, &lastMsgID, nil, nil)

	if done {
		t.Error("expected done=false for parse error (should retry)")
	}
	if newMsgID != "" {
		t.Errorf("expected empty msgID, got '%s'", newMsgID)
	}

	// 应追加一条系统重试指令
	if len(msgs) != 1 {
		t.Fatalf("expected 1 retry message appended, got %d", len(msgs))
	}
	retryMsg := msgs[0]
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

func TestHandleFlashResponse_InvalidJSON_UnescapedQuotes(t *testing.T) {
	// LLM 常见错误：JSON 字符串里放了未转义的双引号，应触发重试
	o := &Orchestrator{msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	var msgs []llm.Message
	msg := llm.Message{
		Role:    "assistant",
		Content: `{"reply":"他说"太断子绝孙"是错的","tool_calls":[]}`,
	}

	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, _ := o.handleFlashResponse(ctx, events, &msgs, msg, "test-conv-1", "test-user-1", &allToolCalls, &lastMsgID, nil, nil)

	if done {
		t.Error("expected done=false for unescaped quotes (should retry)")
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 retry message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "[系统]") {
		t.Error("expected retry instruction with [系统]")
	}
}

func TestHandleFlashResponse_ValidJSONTextOnly(t *testing.T) {
	// 严格 JSON 模式：纯文本回复带空 tool_calls → 应标记 done
	o := &Orchestrator{msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	var msgs []llm.Message
	msg := llm.Message{
		Role:    "assistant",
		Content: `{"reply":"今天天气不错，适合出去走走"}`,
	}

	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, _ := o.handleFlashResponse(ctx, events, &msgs, msg, "test-conv-1", "test-user-1", &allToolCalls, &lastMsgID, nil, nil)

	if !done {
		t.Error("expected done=true for text-only JSON reply (no tool calls)")
	}
}

func TestHandleFlashResponse_PureTextFallback(t *testing.T) {
	// 严格 JSON 模式：纯文本（无 JSON）→ parseLLMResponse 返回 nil → 触发重试
	o := &Orchestrator{msgRepo: nil}

	ctx := context.Background()
	events := make(chan *llm.StreamEvent, 16)
	var msgs []llm.Message
	msg := llm.Message{
		Role:    "assistant",
		Content: "今天天气不错",
	}

	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, _ := o.handleFlashResponse(ctx, events, &msgs, msg, "test-conv-1", "test-user-1", &allToolCalls, &lastMsgID, nil, nil)

	// 纯文本容错接受，正常结束
	if !done {
		t.Error("expected done=true for pure text (graceful accept)")
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 retry messages, got %d", len(msgs))
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
	o := &Orchestrator{msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	done := o.finalizeRound(context.Background(), events, se,
		`{"reply":"ok"}`, "thinking...",
		"cv", "u1", nil, nil, nil, state,
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
	o := &Orchestrator{msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	_ = o.finalizeRound(context.Background(), events, se,
		`{"reply":"ok"}`, "", // 空 reasoning
		"cv", "u1", nil, nil, nil, state,
	)

	close(events)
	for evt := range events {
		if evt.Type == "thought_done" {
			t.Error("expected no thought_done when reasoning is empty")
		}
	}
}

func TestFinalizeRound_CleanTextExtraction(t *testing.T) {
	o := &Orchestrator{msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	_ = o.finalizeRound(context.Background(), events, se,
		`{"reply":"你好，世界"}`, "",
		"cv", "u1", nil, nil, nil, state,
	)

	close(events)
	var textDeltas []string
	for evt := range events {
		if evt.Type == "text_delta" {
			textDeltas = append(textDeltas, evt.Content)
		}
	}
	if len(textDeltas) != 1 {
		t.Fatalf("expected 1 text_delta, got %d: %v", len(textDeltas), textDeltas)
	}
	if textDeltas[0] != "你好，世界" {
		t.Errorf("expected '你好，世界', got '%s'", textDeltas[0])
	}
}

func TestFinalizeRound_ReturnsFalseOnParseError(t *testing.T) {
	o := &Orchestrator{msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	done := o.finalizeRound(context.Background(), events, se,
		"这不是合法的 JSON {{{ broken", "",
		"cv", "u1", nil, nil, nil, state,
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
	o := &Orchestrator{msgRepo: nil}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}

	se := &llm.StreamEvent{Type: "done"}
	done := o.finalizeRound(context.Background(), events, se,
		`{"reply":"ok"}`, "",
		"cv", "u1", nil, nil, nil, state,
	)

	// 无 tool_calls → handleFlashResponse 返回 done=true
	// 但因为 msgRepo=nil，msgID 为空，所以行为取决于 handleFlashResponse 内部逻辑
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
			result := &BuildResult{Prompt: "sys-prompt"}
			msgs := []llm.Message{{Role: "user", Content: "hi"}}

			cr := o.mkLlmChatReq(req, result, msgs)

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
		defaultTemperature: ptr(0.5),
		defaultMaxTokens:   ptr(2048),
	}

	result := &BuildResult{Prompt: "custom system prompt"}
	msgs := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	req := &RunRequest{
		UserID:      "user-42",
		Temperature: ptr(0.9),
		MaxTokens:   ptr(4096),
	}

	cr := o.mkLlmChatReq(req, result, msgs)

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
	o := &Orchestrator{}
	req := &RunRequest{
		UserID:      "u1",
		Temperature: nil,
		MaxTokens:   nil,
	}
	result := &BuildResult{Prompt: "p"}
	msgs := []llm.Message{}

	cr := o.mkLlmChatReq(req, result, msgs)

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
