package llmcost

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"xhan-helper/internal/llm"
)

// mockRecorder 收集上报的 UsageRecord
type mockRecorder struct {
	mu      sync.Mutex
	records []UsageRecord
}

func (m *mockRecorder) Record(record UsageRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, record)
}

func (m *mockRecorder) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

// mockClient 可定制的 LLMClient
type mockClient struct {
	chatFn     func(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error)
	chatSyncFn func(ctx context.Context, req *llm.ChatRequest) (llm.Message, error)
	modelName  string
}

func (m *mockClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return m.chatFn(ctx, req)
}

func (m *mockClient) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	return m.chatSyncFn(ctx, req)
}

func (m *mockClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, nil
}

func (m *mockClient) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return base
}

func (m *mockClient) ModelName() string { return m.modelName }

func waitRecords(r *mockRecorder, n int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.count() >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestWrapLLMClient_NilRecorder_ReturnsInner(t *testing.T) {
	inner := &mockClient{modelName: "flash"}
	got := WrapLLMClient(inner, nil)
	if got != inner {
		t.Fatal("nil recorder should return inner client unchanged")
	}
}

func TestChat_RecordsUsageFromDoneEvent(t *testing.T) {
	rec := &mockRecorder{}
	inner := &mockClient{
		modelName: "deepseek-v4-flash",
		chatFn: func(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
			ch := make(chan *llm.StreamEvent, 2)
			ch <- &llm.StreamEvent{Type: "text_delta", Content: "hi"}
			ch <- &llm.StreamEvent{Type: "done", Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}}
			close(ch)
			return ch, nil
		},
	}
	tracker := WrapLLMClient(inner, rec).(*CostTracker)

	ctx := WithScenario(context.Background(), Scenario{
		Category:    CategoryMainChat,
		SubCategory: SubReActLoop,
		ConvID:      "conv-1",
		UserID:      "user-1",
	})
	ch, err := tracker.Chat(ctx, &llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var events []*llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events forwarded, got %d", len(events))
	}
	if !waitRecords(rec, 1) {
		t.Fatal("record not reported within timeout")
	}

	got := rec.records[0]
	if got.Category != CategoryMainChat || got.SubCategory != SubReActLoop {
		t.Errorf("scenario mismatch: %+v", got)
	}
	if got.ConvID != "conv-1" || got.UserID != "user-1" {
		t.Errorf("conv/user mismatch: %+v", got)
	}
	if got.Model != "deepseek-v4-flash" {
		t.Errorf("model mismatch: %s", got.Model)
	}
	if got.TotalTokens != 150 || got.PromptTokens != 100 || got.CompletionTokens != 50 {
		t.Errorf("token mismatch: %+v", got)
	}
	if got.Date.IsZero() {
		t.Error("Date should be set")
	}
}

func TestChat_DoneEventWithoutUsage_Skips(t *testing.T) {
	rec := &mockRecorder{}
	inner := &mockClient{
		chatFn: func(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
			ch := make(chan *llm.StreamEvent, 1)
			ch <- &llm.StreamEvent{Type: "done"} // Usage nil
			close(ch)
			return ch, nil
		},
	}
	tracker := WrapLLMClient(inner, rec).(*CostTracker)
	ch, err := tracker.Chat(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	time.Sleep(50 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("expected no records for nil usage, got %d", rec.count())
	}
}

func TestChatSync_RecordsUsage(t *testing.T) {
	rec := &mockRecorder{}
	inner := &mockClient{
		modelName: "deepseek-v4-pro",
		chatSyncFn: func(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
			return llm.Message{
				Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			}, nil
		},
	}
	tracker := WrapLLMClient(inner, rec).(*CostTracker)

	ctx := WithScenario(context.Background(), Scenario{
		Category:    CategoryPostChat,
		SubCategory: SubMemoryExtract,
	})
	if _, err := tracker.ChatSync(ctx, &llm.ChatRequest{UserID: "user-9"}); err != nil {
		t.Fatal(err)
	}
	if !waitRecords(rec, 1) {
		t.Fatal("record not reported within timeout")
	}

	got := rec.records[0]
	if got.Category != CategoryPostChat || got.SubCategory != SubMemoryExtract {
		t.Errorf("scenario mismatch: %+v", got)
	}
	// scenario.UserID 为空时 fallback 到 req.UserID
	if got.UserID != "user-9" {
		t.Errorf("user fallback mismatch: %s", got.UserID)
	}
	if got.TotalTokens != 30 {
		t.Errorf("token mismatch: %+v", got)
	}
}

func TestChatSync_ErrorOrNilUsage_Skips(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		rec := &mockRecorder{}
		inner := &mockClient{
			chatSyncFn: func(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
				return llm.Message{}, context.DeadlineExceeded
			},
		}
		tracker := WrapLLMClient(inner, rec).(*CostTracker)
		if _, err := tracker.ChatSync(context.Background(), &llm.ChatRequest{}); err == nil {
			t.Fatal("expected error")
		}
		time.Sleep(50 * time.Millisecond)
		if rec.count() != 0 {
			t.Fatal("should not record on error")
		}
	})

	t.Run("nil usage", func(t *testing.T) {
		rec := &mockRecorder{}
		inner := &mockClient{
			chatSyncFn: func(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
				return llm.Message{}, nil // Usage nil
			},
		}
		tracker := WrapLLMClient(inner, rec).(*CostTracker)
		if _, err := tracker.ChatSync(context.Background(), &llm.ChatRequest{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		if rec.count() != 0 {
			t.Fatal("should not record on nil usage")
		}
	})
}

func TestChatSync_NoScenario_SkipsRecordButKeepsUserFallback(t *testing.T) {
	rec := &mockRecorder{}
	inner := &mockClient{
		modelName: "flash",
		chatSyncFn: func(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
			return llm.Message{Usage: &llm.Usage{TotalTokens: 1}}, nil
		},
	}
	tracker := WrapLLMClient(inner, rec).(*CostTracker)
	// 无 Scenario 的 ctx
	if _, err := tracker.ChatSync(context.Background(), &llm.ChatRequest{UserID: "u"}); err != nil {
		t.Fatal(err)
	}
	if !waitRecords(rec, 1) {
		t.Fatal("record not reported")
	}
	if rec.records[0].UserID != "u" {
		t.Errorf("user fallback mismatch: %s", rec.records[0].UserID)
	}
}
