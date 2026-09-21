package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

func TestFactExtractor_ParsesCandidatesAndPrompt(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"facts":[
		{"subject":"项目K","kind":"state","content":"本人兼产品与开发"},
		{"subject":"老K","kind":"event","content":"因群消息争吵"},
		{"subject":"本人","kind":"state","fields":{"due_at":"2026-09-21"},"content":"21 号对需求"}
	]}`}}}
	e := NewFactExtractor(&FactExtractorConfig{LLMClient: mock})

	got, err := e.Extract(context.Background(), []model.Message{
		{ID: "m1", Role: "user", Content: "我兼产品和开发"},
		{ID: "m2", Role: "assistant", Content: "好的"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(got))
	}
	if got[0].Subject != "项目K" || got[0].Kind != factKindState {
		t.Errorf("unexpected first candidate: %+v", got[0])
	}
	if got[2].Fields["due_at"] != "2026-09-21" {
		t.Errorf("expected due_at preserved, got %+v", got[2].Fields)
	}

	// 提示词与渲染
	if len(mock.reqs) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mock.reqs))
	}
	if mock.reqs[0].SystemPrompt != factExtractSystemPrompt {
		t.Error("expected fact-specific system prompt")
	}
	if len(mock.reqs[0].Messages) != 1 || mock.reqs[0].Messages[0].Role != "user" {
		t.Fatalf("expected single user message, got %+v", mock.reqs[0].Messages)
	}
	content := mock.reqs[0].Messages[0].Content
	if !strings.HasPrefix(content, "你是事实记录员") {
		t.Error("instruction constant should be at the very front")
	}
	if !strings.Contains(content, `<turn role="user" at="`) || !strings.Contains(content, `<turn role="assistant" at="`) {
		t.Errorf("expected timestamped user and assistant turns, got: %.300s", content)
	}
}

func TestFactExtractor_PassesTemperature(t *testing.T) {
	temp := 0.2
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"facts":[]}`}}}
	e := NewFactExtractor(&FactExtractorConfig{LLMClient: mock, Temperature: &temp})

	if _, err := e.Extract(context.Background(), []model.Message{{ID: "m1", Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.reqs[0].Temperature == nil || *mock.reqs[0].Temperature != 0.2 {
		t.Errorf("expected temperature 0.2 passed through, got %v", mock.reqs[0].Temperature)
	}

	// nil → 不传，用模型默认
	mock2 := &syncMockLLM{responses: []llm.Message{{Content: `{"facts":[]}`}}}
	e2 := NewFactExtractor(&FactExtractorConfig{LLMClient: mock2})
	if _, err := e2.Extract(context.Background(), []model.Message{{ID: "m1", Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock2.reqs[0].Temperature != nil {
		t.Errorf("expected nil temperature, got %v", *mock2.reqs[0].Temperature)
	}
}

func TestFactExtractor_ParseFailureDegrades(t *testing.T) {
	// 无 JSON 的纯文本：ParseFlashJSON 不报错，得 0 候选
	prose := &syncMockLLM{responses: []llm.Message{{Content: "抱歉，我无法处理"}}}
	got, err := NewFactExtractor(&FactExtractorConfig{LLMClient: prose}).Extract(
		context.Background(), []model.Message{{ID: "m1", Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("prose response should degrade, not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 candidates, got %+v", got)
	}

	// 结构损坏的 JSON：解析报错 → 降级为 nil、不向上抛
	broken := &syncMockLLM{responses: []llm.Message{{Content: `{"facts":`}}}
	got, err = NewFactExtractor(&FactExtractorConfig{LLMClient: broken}).Extract(
		context.Background(), []model.Message{{ID: "m1", Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("parse failure should degrade, not error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil candidates, got %+v", got)
	}
}

func TestFactExtractor_EmptyFacts(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"facts":[]}`}}}
	e := NewFactExtractor(&FactExtractorConfig{LLMClient: mock})

	got, err := e.Extract(context.Background(), []model.Message{{ID: "m1", Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(got))
	}
}

func TestSanitizeFactCandidates_DropsInvalidAndTruncates(t *testing.T) {
	in := []factCandidate{
		{Subject: "A", Kind: "state", Content: "合法1"},
		{Subject: "B", Kind: "fact", Content: "非法 kind"},
		{Subject: "C", Kind: "event", Content: "   "},
		{Subject: "D", Kind: "event", Content: " 合法2 "},
		{Subject: "E", Kind: "state", Content: "合法3"},
	}
	got := sanitizeFactCandidates(in, 5)
	if len(got) != 3 {
		t.Fatalf("expected 3 kept, got %d: %+v", len(got), got)
	}
	if got[1].Content != "合法2" {
		t.Errorf("expected content trimmed, got %q", got[1].Content)
	}

	// 截断
	got = sanitizeFactCandidates(in, 2)
	if len(got) != 2 {
		t.Fatalf("expected truncation to 2, got %d", len(got))
	}
}

func TestRenderFactDialog_SkipsEmptyContentAndEscapes(t *testing.T) {
	out := renderFactDialog([]model.Message{
		{ID: "m1", Role: "user", Content: "a < b & c", CreatedAt: time.Date(2026, 9, 21, 14, 3, 0, 0, time.UTC)},
		{ID: "m2", Role: "assistant", Content: "   "}, // tool-call-only assistant
		{ID: "m3", Role: "assistant", Content: "收到"},
	})
	if strings.Contains(out, "<turn role=\"assistant\"") && strings.Contains(out, "></turn>") {
		t.Error("empty-content turn should be skipped")
	}
	if strings.Count(out, "<turn ") != 2 {
		t.Errorf("expected 2 turns, got: %s", out)
	}
	if !strings.Contains(out, "a &lt; b &amp; c") {
		t.Errorf("content should be XML-escaped, got: %s", out)
	}
	if !strings.Contains(out, `<turn role="assistant" at="`) {
		t.Errorf("assistant turn should still carry a timestamp, got: %s", out)
	}
	if !strings.Contains(out, "收到</turn>") {
		t.Errorf("expected assistant turn, got: %s", out)
	}
}
