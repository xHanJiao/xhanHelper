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
	req := &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}
	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, newMsgID := o.handleFlashResponse(ctx, events, &msgs, msg, req, &allToolCalls, &lastMsgID, nil, nil)

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
	req := &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}
	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, _ := o.handleFlashResponse(ctx, events, &msgs, msg, req, &allToolCalls, &lastMsgID, nil, nil)

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
	req := &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}
	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, _ := o.handleFlashResponse(ctx, events, &msgs, msg, req, &allToolCalls, &lastMsgID, nil, nil)

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
	req := &RunRequest{ConversationID: "test-conv-1", UserID: "test-user-1"}
	var allToolCalls []tool.ToolCall
	var lastMsgID string

	done, _ := o.handleFlashResponse(ctx, events, &msgs, msg, req, &allToolCalls, &lastMsgID, nil, nil)

	// 纯文本容错接受，正常结束
	if !done {
		t.Error("expected done=true for pure text (graceful accept)")
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 retry messages, got %d", len(msgs))
	}
}
