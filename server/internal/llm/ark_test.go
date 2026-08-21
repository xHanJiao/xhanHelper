package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestArkBuildBody_IncludesTools 回归：请求体必须带 tools（原生 function calling），
// 否则模型没有工具契约，会瞎编文本工具调用。
func TestArkBuildBody_IncludesTools(t *testing.T) {
	client := &ArkClient{model: "deepseek-v4-flash"}
	req := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []ToolDefinition{{
			Name: "note", Description: "笔记",
			Functions: []FuncDef{{Name: "query_notes", Description: "查笔记", Parameters: map[string]interface{}{"type": "object"}}},
		}},
	}
	body, _ := client.buildStreamBody(req)
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tools, ok := payload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("expected tools array of 1, got %v", payload["tools"])
	}
	fn := tools[0].(map[string]interface{})["function"].(map[string]interface{})
	if name := fn["name"]; name != "note__query_notes" {
		t.Errorf("tool name expected note__query_notes, got %v", name)
	}
}

// TestArkStream_EncryptedContentAndUsage 验证方舟流式解析：
// encrypted_content 独立 chunk 捕获、include_usage 的 usage chunk（choices 为空数组）、tool_calls 增量拼接。
func TestArkStream_EncryptedContentAndUsage(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"role":"assistant","reasoning_content":"先思考"},"index":0}]}`,
		// 思考完成：独立 chunk 返回完整 encrypted_content（content/reasoning 均为空）
		`{"choices":[{"delta":{"encrypted_content":"Zm9vYmFy"},"index":0}]}`,
		`{"choices":[{"delta":{"content":"回复"},"index":0}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"a__b","arguments":""}}]},"index":0}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":1}"}}]},"index":0}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`[DONE]`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			io.WriteString(w, "data: "+c+"\n\n")
		}
	}))
	defer srv.Close()

	client := &ArkClient{baseURL: srv.URL, model: "deepseek-v4-pro", httpClient: srv.Client()}
	events, err := client.Chat(context.Background(), &ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	var text, reasoning strings.Builder
	var done *StreamEvent
	for evt := range events {
		switch evt.Type {
		case "text_delta":
			text.WriteString(evt.Content)
		case "thought_delta":
			reasoning.WriteString(evt.Content)
		case "done":
			done = evt
		}
	}
	if done == nil {
		t.Fatal("no done event")
	}
	if got := text.String(); got != "回复" {
		t.Errorf("content expected 回复, got %q", got)
	}
	if got := reasoning.String(); got != "先思考" {
		t.Errorf("reasoning expected 先思考, got %q", got)
	}
	if got := done.EncryptedContent; got != "Zm9vYmFy" {
		t.Errorf("encrypted_content expected Zm9vYmFy, got %q", got)
	}
	if done.Usage == nil || done.Usage.TotalTokens != 15 || done.Usage.PromptTokens != 10 {
		t.Errorf("usage expected total=15 prompt=10, got %+v", done.Usage)
	}
	if len(done.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(done.ToolCalls))
	}
	if done.ToolCalls[0].Function.Name != "a__b" || done.ToolCalls[0].Function.Arguments != `{"x":1}` {
		t.Errorf("tool call mismatch: %+v", done.ToolCalls[0])
	}
}
