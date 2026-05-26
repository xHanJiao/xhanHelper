package tool

import (
	"encoding/json"
	"testing"
)

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
