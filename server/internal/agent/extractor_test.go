package agent

import (
	"context"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// 抽取段：输入完整对话（user + assistant），assistant 必须标注 role，不能被当成用户的话。
// 空 content / tool-call 轮跳过。
func TestExtractCandidates_SendsFullDialogWithRoles(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"extracted":true,"memories":[
		{"summary":"决定用方案B","topic":"项目规划","facts":["截止2026-10-01"]}
	]}`}}}
	e := NewMemoryExtractor(&MemoryExtractorConfig{LLMClient: mock})

	got, err := e.ExtractCandidates(context.Background(), "u1", []model.Message{
		{ID: "m1", Role: "user", Content: "方案A和B哪个好"},
		{ID: "m2", Role: "assistant", Content: "建议B，理由是成本更低"}, // 结论在助手这轮
		{ID: "m3", Role: "assistant", Content: ""},            // 纯 tool call 轮
		{ID: "m4", Role: "user", Content: "好，就用B"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "决定用方案B" {
		t.Fatalf("unexpected candidates: %+v", got)
	}

	if len(mock.reqs) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mock.reqs))
	}
	if mock.reqs[0].SystemPrompt != postChatDialogSystemPrompt {
		t.Error("should use the dialog system prompt (not the user-only shared one)")
	}
	if len(mock.reqs[0].Messages) != 1 || mock.reqs[0].Messages[0].Role != "user" {
		t.Fatalf("expected single user message, got %+v", mock.reqs[0].Messages)
	}
	content := mock.reqs[0].Messages[0].Content
	// 助手的话必须在场且标注 role="assistant"
	if !strings.Contains(content, `<turn role="assistant"`) || !strings.Contains(content, "建议B") {
		t.Errorf("assistant turn must be present and labeled, got: %.400s", content)
	}
	if !strings.Contains(content, `<turn role="user"`) {
		t.Errorf("user turn missing, got: %.400s", content)
	}
	// 空 content 轮不出现（只有一个 assistant turn）
	if strings.Count(content, `<turn role="assistant"`) != 1 {
		t.Errorf("empty/tool-call turn should be skipped, got: %.400s", content)
	}
	// 铁律：助手的话不构成记忆来源
	if !strings.Contains(content, "助手说的不构成记忆来源") {
		t.Errorf("missing the assistant-words rule, got: %.400s", content)
	}
}

// 无用户消息 / 全空内容 → 不追加对话块，仍能拿到指令。
func TestExtractCandidates_EmptyDialog(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"extracted":false}`}}}
	e := NewMemoryExtractor(&MemoryExtractorConfig{LLMClient: mock})

	got, err := e.ExtractCandidates(context.Background(), "u1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no candidates, got %+v", got)
	}
	if strings.Contains(mock.reqs[0].Messages[0].Content, "<对话>") {
		t.Error("empty dialog should not be appended")
	}
}
