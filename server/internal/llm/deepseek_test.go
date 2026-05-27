package llm

import (
	"encoding/json"
	"testing"
)

func TestBuildMessagesFiltersEmptyContent(t *testing.T) {
	req := &ChatRequest{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{
				{ID: "call_1", Function: ToolCallFunction{Name: "get_weather", Arguments: `{"city":"NYC"}`}},
			}},
			{Role: "tool", Content: "sunny", ToolCallID: "call_1"},
			{Role: "assistant", Content: "The weather is sunny."},
		},
	}

	messages := buildMessages(req)

	// The bug: assistant message with empty Content but with ToolCalls
	// should be included (it's a valid tool_calls message), BUT
	// messages with NEITHER Content NOR ToolCalls should be filtered.

	// Let's also test the pure empty case (no content, no tool calls, no tool_call_id)
	req2 := &ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: ""}, // empty, no tool_calls, no tool_call_id — should be filtered
			{Role: "user", Content: "Are you there?"},
		},
	}

	messages2 := buildMessages(req2)
	payload2, _ := json.Marshal(map[string]interface{}{"messages": messages2})
	t.Logf("Payload with empty assistant message:\n%s", string(payload2))

	// BUG CONFIRMATION: The empty assistant message IS included
	if len(messages2) != 2 {
		t.Errorf("Expected 2 messages (filtering the empty one), got %d. Payload: %s", len(messages2), string(payload2))
	}

	// Check that the empty message has no "content" key at all
	for i, m := range messages2 {
		hasContent := m["content"] != nil && m["content"] != ""
		_, hasToolCalls := m["tool_calls"]
		hasToolCallID := m["tool_call_id"] != nil && m["tool_call_id"] != ""

		// A message with no content, no tool_calls, no tool_call_id is effectively empty
		isEmpty := !hasContent && !hasToolCalls && !hasToolCallID
		if isEmpty {
			t.Errorf("Found empty message at index %d that should have been filtered: %v", i, m)
		}
	}

	_ = messages // use first test
}
