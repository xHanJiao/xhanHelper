package tool

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/model"
)

func TestTruncateContent(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short", "hello", 100, "hello"},
		{"equal", "abcde", 5, "abcde"},
		{"long ascii", "hello world, this is a long message", 11, "hello world..."},
		{"chinese short", "你好世界", 10, "你好世界"},
		{"chinese long", "这是一条很长的中文消息需要被截断处理", 10, "这是一条很长的中文消..."},
		{"empty", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateContent(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateContent(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestFilterMessageRound(t *testing.T) {
	now := time.Now()
	m1 := model.Message{ID: "m1", Role: "user", Content: "帮我查账单", CreatedAt: now}
	m2 := model.Message{ID: "m2", Role: "assistant", Content: "好的，我来查", CreatedAt: now.Add(time.Second)}
	m3 := model.Message{ID: "m3", Role: "tool", Content: `{"result":"ok"}`, CreatedAt: now.Add(2 * time.Second), IsInternal: true}
	m4 := model.Message{ID: "m4", Role: "assistant", Content: "你的账单如下...", CreatedAt: now.Add(3 * time.Second)}
	m5 := model.Message{ID: "m5", Role: "user", Content: "谢谢", CreatedAt: now.Add(4 * time.Second)}

	t.Run("filters tool and internal messages", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m3, m4}
		got := filterMessageRound(msgs, "m1")
		if len(got) != 3 {
			t.Errorf("expected 3 messages, got %d", len(got))
		}
		for _, m := range got {
			if m.Role == "tool" || m.IsInternal {
				t.Errorf("tool/internal message should be filtered: %v", m)
			}
		}
	})

	t.Run("stops at next user message", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m5} // m5 is next user msg
		got := filterMessageRound(msgs, "m1")
		if len(got) != 2 {
			t.Errorf("expected 2 messages (stopped at next user), got %d", len(got))
		}
		if got[1].ID != "m2" {
			t.Errorf("expected assistant msg, got %s", got[1].ID)
		}
	})

	t.Run("full round chain", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m3, m4, m5}
		got := filterMessageRound(msgs, "m1")
		if len(got) != 3 { // m1, m2, m4 (m3=tool filtered, m5=next user stopped)
			t.Errorf("expected 3 messages, got %d", len(got))
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		got := filterMessageRound(nil, "m1")
		if len(got) != 0 {
			t.Errorf("expected empty, got %d", len(got))
		}
	})

	t.Run("skip empty content", func(t *testing.T) {
		mEmpty := model.Message{ID: "me", Role: "assistant", Content: "", CreatedAt: now}
		got := filterMessageRound([]model.Message{m1, mEmpty}, "m1")
		if len(got) != 1 {
			t.Errorf("expected 1 message (empty content skipped), got %d", len(got))
		}
	})
}

func TestFormatMessageRound(t *testing.T) {
	now := time.Now()
	msgs := []model.Message{
		{ID: "m1", ConversationID: "conv1", Role: "user", Content: "帮我查账单", CreatedAt: now},
		{ID: "m2", ConversationID: "conv1", Role: "assistant", Content: "你的账单如下：共计342元", CreatedAt: now.Add(time.Second)},
	}

	result := formatMessageRound(msgs, "测试会话")
	if !strings.Contains(result, "测试会话") {
		t.Errorf("expected result to contain conversation title, got: %s", result)
	}
	if !strings.Contains(result, "用户:") && !strings.Contains(result, "用户：") {
		t.Errorf("expected result to contain user indicator, got: %s", result)
	}
	if !strings.Contains(result, "助手:") && !strings.Contains(result, "助手：") {
		t.Errorf("expected result to contain assistant indicator, got: %s", result)
	}
	if !strings.Contains(result, "帮我查账单") {
		t.Errorf("expected result to contain user message content, got: %s", result)
	}
	if !strings.Contains(result, "342元") {
		t.Errorf("expected result to contain assistant message content, got: %s", result)
	}
}

func TestMessageSearchTool_Functions_Schema(t *testing.T) {
	tool := NewMessageSearchTool(nil, nil)
	fns := tool.Functions()
	if len(fns) != 1 {
		t.Fatalf("expected 1 function, got %d", len(fns))
	}
	fn := fns[0]
	if fn.Name != "search" {
		t.Errorf("expected function name 'search', got %q", fn.Name)
	}
	params, ok := fn.Parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties in parameters")
	}
	if _, ok := params["query"]; !ok {
		t.Error("expected 'keyword' parameter")
	}
	if _, ok := params["limit"]; !ok {
		t.Error("expected 'limit' parameter")
	}
	if _, ok := params["conversation_id"]; !ok {
		t.Error("expected 'conversation_id' parameter")
	}
	required, ok := fn.Parameters["required"].([]string)
	if !ok {
		t.Fatal("expected required array")
	}
	if len(required) != 1 || required[0] != "query" {
		t.Errorf("expected ['keyword'] as required, got %v", required)
	}
}

func TestMessageSearchTool_Execute_Errors(t *testing.T) {
	tool := NewMessageSearchTool(nil, nil)

	t.Run("empty keyword", func(t *testing.T) {
		args, _ := json.Marshal(map[string]string{"query": ""})
		result := tool.Execute(nil, "search", args)
		if result.Success {
			t.Error("expected error for empty keyword")
		}
	})

	t.Run("missing keyword", func(t *testing.T) {
		args, _ := json.Marshal(map[string]interface{}{"limit": 10})
		result := tool.Execute(nil, "search", args)
		if result.Success {
			t.Error("expected error for missing keyword")
		}
	})

	t.Run("invalid args", func(t *testing.T) {
		result := tool.Execute(nil, "search", []byte(`{bad json`))
		if result.Success {
			t.Error("expected error for malformed args")
		}
	})
}

func TestSearchArgsParsing(t *testing.T) {
	t.Run("default limit", func(t *testing.T) {
		var a struct {
			Keyword        string `json:"query"`
			Limit          int    `json:"limit"`
			ConversationID string `json:"conversation_id"`
		}
		json.Unmarshal([]byte(`{"query":"test"}`), &a)
		if a.Limit <= 0 {
			a.Limit = 20
		}
		if a.Limit != 20 {
			t.Errorf("expected default limit 20, got %d", a.Limit)
		}
		if a.Keyword != "test" {
			t.Errorf("expected keyword 'test', got %q", a.Keyword)
		}
	})

	t.Run("custom limit capped at 50", func(t *testing.T) {
		var a struct {
			Keyword        string `json:"query"`
			Limit          int    `json:"limit"`
			ConversationID string `json:"conversation_id"`
		}
		json.Unmarshal([]byte(`{"query":"test","limit":100}`), &a)
		if a.Limit > 50 {
			a.Limit = 50
		}
		if a.Limit != 50 {
			t.Errorf("expected limit capped at 50, got %d", a.Limit)
		}
	})

	t.Run("conversation_id optional", func(t *testing.T) {
		var a struct {
			Keyword        string `json:"query"`
			Limit          int    `json:"limit"`
			ConversationID string `json:"conversation_id"`
		}
		json.Unmarshal([]byte(`{"query":"test"}`), &a)
		if a.ConversationID != "" {
			t.Errorf("expected empty default for conversation_id, got %q", a.ConversationID)
		}
	})
}
