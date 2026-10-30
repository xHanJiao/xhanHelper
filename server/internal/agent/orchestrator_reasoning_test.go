package agent

import (
	"encoding/json"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// reasoningContent 重放测试：BuildMessagesForFlash 必须从 DB 恢复 assistant 的
// reasoning_content，否则重放请求与 ReAct 循环内请求字节不一致，破坏 DeepSeek 前缀缓存。

func TestBuildMessagesForFlash_RestoresReasoningContent(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "问题"},
		{Role: "assistant", Content: "回复", ReasoningContent: strPtr("思考过程")},
	}
	msgs := BuildMessagesForFlash(history, 2000, false, false)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(msgs))
	}
	if msgs[1].ReasoningContent != "思考过程" {
		t.Errorf("reasoning_content 应被恢复, got %q", msgs[1].ReasoningContent)
	}
}

func TestBuildMessagesForFlash_NoReasoningStaysEmpty(t *testing.T) {
	history := []model.Message{
		{Role: "assistant", Content: "回复"},
	}
	msgs := BuildMessagesForFlash(history, 2000, false, false)
	if msgs[0].ReasoningContent != "" {
		t.Errorf("无 reasoning 的消息应保持为空, got %q", msgs[0].ReasoningContent)
	}
}

func TestBuildMessagesForFlash_ReasoningSerialized(t *testing.T) {
	// llm.Message 序列化后应带 reasoning_content 字段（deepseek.buildMessages 会原样发送）
	history := []model.Message{
		{Role: "assistant", Content: "回复", ReasoningContent: strPtr("思考过程")},
	}
	msgs := BuildMessagesForFlash(history, 2000, true, false)
	b, err := json.Marshal(msgs[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if !jsonContains(b, `"reasoning_content":"思考过程"`) {
		t.Errorf("序列化结果应包含 reasoning_content, got %s", b)
	}
}

func TestBuildMessagesForFlash_ReasoningWithNativeFCToolCall(t *testing.T) {
	// 工具调用轮：reasoning + tool_calls + tool 结果，重放后与 in-loop 字节对齐
	history := []model.Message{
		{Role: "assistant", Content: "", ReasoningContent: strPtr("决定调用工具"),
			ToolCallsJSON: strPtr(`[{"id":"call_1","function":{"name":"note__record_note","arguments":"{\"topic\":\"t\"}"}}]`)},
		{Role: "tool", Content: `{"success":true}`, ToolCallID: strPtr("call_1")},
	}
	msgs := BuildMessagesForFlash(history, 2000, true, false)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(msgs))
	}
	if msgs[0].ReasoningContent != "决定调用工具" {
		t.Errorf("工具调用轮的 reasoning_content 必须回传, got %q", msgs[0].ReasoningContent)
	}
	if len(msgs[0].ToolCalls) != 1 || msgs[0].ToolCalls[0].ID != "call_1" {
		t.Errorf("tool_calls 应被恢复, got %+v", msgs[0].ToolCalls)
	}
	if msgs[1].Role != "tool" || msgs[1].ToolCallID != "call_1" {
		t.Errorf("tool 消息应保留 role/tool_call_id, got %+v", msgs[1])
	}
}

func TestBuildMessagesForFlash_ReplayByteStable(t *testing.T) {
	// 同一批 DB 消息两次转换必须字节一致（缓存前缀稳定性）
	history := []model.Message{
		{Role: "user", Content: "问题"},
		{Role: "assistant", Content: "回复", ReasoningContent: strPtr("思考"), ToolCallsJSON: strPtr(`[{"id":"call_1","function":{"name":"a__b","arguments":"{}"}}]`)},
		{Role: "tool", Content: `{"success":true}`, ToolCallID: strPtr("call_1")},
	}
	a := BuildMessagesForFlash(history, 2000, true, false)
	b := BuildMessagesForFlash(history, 2000, true, false)

	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Error("两次转换应产生字节一致的输出")
	}

	// 同时验证重放输出与"循环内消息"（llm.Message 直连）字节一致：
	// 循环内 assistant 消息带 reasoning + tool_calls，与重放结果应完全相同
	inLoop := []llm.Message{
		{Role: "user", Content: "问题"},
		{Role: "assistant", Content: "回复", ReasoningContent: "思考", ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.ToolCallFunction{Name: "a__b", Arguments: "{}"}}}},
		{Role: "tool", Content: `{"success":true}`, ToolCallID: "call_1"},
	}
	ji, _ := json.Marshal(inLoop)
	if string(ja) != string(ji) {
		t.Error("重放消息应与 ReAct 循环内消息字节一致（缓存命中前提）")
	}
}

func jsonContains(b []byte, sub string) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == sub {
			return true
		}
	}
	return false
}
