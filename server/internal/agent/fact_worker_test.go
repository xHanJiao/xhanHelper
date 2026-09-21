package agent

import (
	"context"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
)

func TestFactWorker_SkipsWhenDisabled(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"facts":[]}`}}}
	w := NewFactWorker(&FactWorkerConfig{
		Extractor: NewFactExtractor(&FactExtractorConfig{LLMClient: mock}),
		Enabled:   false,
	})
	err := w.Process(context.Background(), &SessionSnapshot{
		ConvID: "c", UserID: "u",
		msgs: []model.Message{{ID: "m1", Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.reqs) != 0 {
		t.Errorf("disabled worker should not consume LLM, got %d call(s)", len(mock.reqs))
	}
}

func TestFactWorker_EnabledAnnotatesScenarioAndCallsExtractor(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"facts":[{"subject":"X","kind":"state","content":"y"}]}`}}}
	w := NewFactWorker(&FactWorkerConfig{
		Extractor: NewFactExtractor(&FactExtractorConfig{LLMClient: mock}),
		Enabled:   true,
	})
	if w.Name() != "fact" {
		t.Errorf("expected name 'fact', got %q", w.Name())
	}
	err := w.Process(context.Background(), &SessionSnapshot{
		ConvID: "conv-1", UserID: "user-1",
		msgs: []model.Message{
			{ID: "m1", Role: "user", Content: "hi"},
			{ID: "m2", Role: "assistant", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.reqs) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mock.reqs))
	}
	if len(mock.scenarios) != 1 {
		t.Fatalf("expected 1 scenario, got %d", len(mock.scenarios))
	}
	sc := mock.scenarios[0]
	if sc.Category != llmcost.CategoryPostChat || sc.SubCategory != llmcost.SubFactExtract {
		t.Errorf("unexpected scenario: %+v", sc)
	}
	if sc.UserID != "user-1" || sc.ConvID != "conv-1" {
		t.Errorf("scenario should carry user/conv, got %+v", sc)
	}
	// assistant 消息应带上（GetAllMessages）
	if !containsStr(mock.reqs[0].Messages[0].Content, `>hello</turn>`) {
		t.Errorf("assistant message should be included: %.200s", mock.reqs[0].Messages[0].Content)
	}
}
