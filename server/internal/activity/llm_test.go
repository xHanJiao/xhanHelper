package activity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"xhan-helper/internal/llm"
)

type fakeLLM struct {
	stream []*llm.StreamEvent
	msg    llm.Message
	err    error
}

func (f *fakeLLM) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan *llm.StreamEvent, len(f.stream))
	for _, e := range f.stream {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (f *fakeLLM) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	return f.msg, f.err
}

func (f *fakeLLM) ChatWithRawJSON(ctx context.Context, body json.RawMessage) (llm.Message, error) {
	return llm.Message{}, nil
}

func (f *fakeLLM) BuildSystemPrompt(base string, _ []llm.ToolDefinition) string { return base }
func (f *fakeLLM) ModelName() string                                            { return "fake-model" }

func readEvents(t *testing.T, s *Store, typ string) []eventRow {
	t.Helper()
	var rows []eventRow
	if err := s.db.Raw(`SELECT * FROM events WHERE type=? ORDER BY rowid`, typ).Scan(&rows).Error; err != nil {
		t.Fatalf("scan %s: %v", typ, err)
	}
	return rows
}

func attrsOf(t *testing.T, row eventRow) map[string]any {
	t.Helper()
	if row.Attrs == nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(*row.Attrs), &m); err != nil {
		t.Fatalf("unmarshal attrs: %v", err)
	}
	return m
}

func TestLLMStreamEmitsCallAndFirstToken(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	inner := &fakeLLM{stream: []*llm.StreamEvent{
		{Type: "text_delta", Content: "你"},
		{Type: "text_delta", Content: "好"},
		{Type: "done", Usage: &llm.Usage{PromptTokens: 3, CompletionTokens: 4}},
	}}
	c := WrapLLMClient(inner, s, "main_flash")
	ctx := WithTurn(context.Background(), TurnCtx{TurnID: "t1", StartEventID: "s1"})

	ch, err := c.Chat(ctx, &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	for range ch {
	}
	s.Flush()

	starts := readEvents(t, s, TypeLLMCallStarted)
	ends := readEvents(t, s, TypeLLMCallEnded)
	firsts := readEvents(t, s, TypeLLMFirstToken)
	if len(starts) != 1 || len(ends) != 1 || len(firsts) != 1 {
		t.Fatalf("counts start=%d end=%d first=%d, want 1/1/1", len(starts), len(ends), len(firsts))
	}

	start, end, first := starts[0], ends[0], firsts[0]
	if start.TurnID == nil || *start.TurnID != "t1" {
		t.Errorf("start.turn_id = %v, want t1", start.TurnID)
	}
	if start.ParentEventID == nil || *start.ParentEventID != "s1" {
		t.Errorf("start.parent = %v, want s1", start.ParentEventID)
	}
	if start.BizKey == nil || *start.BizKey == "" {
		t.Errorf("start.biz_key = %v, want 非空 call id", start.BizKey)
	}
	if start.PairID == nil || *start.PairID != end.EventID {
		t.Errorf("start.pair_id = %v, want end.event_id %s", start.PairID, end.EventID)
	}
	if end.PairID == nil || *end.PairID != start.EventID {
		t.Errorf("end.pair_id = %v, want start.event_id %s", end.PairID, start.EventID)
	}
	sa := attrsOf(t, start)
	if sa["model"] != "fake-model" || sa["call_kind"] != "main_flash" {
		t.Errorf("start attrs = %v", sa)
	}
	if end.Outcome == nil || *end.Outcome != OutcomeCompleted {
		t.Errorf("end.outcome = %v, want completed", end.Outcome)
	}
	ea := attrsOf(t, end)
	if ea["prompt_tokens"] != float64(3) || ea["completion_tokens"] != float64(4) {
		t.Errorf("end attrs = %v", ea)
	}
	if end.BizKey == nil || *end.BizKey != *start.BizKey {
		t.Errorf("end.biz_key = %v, want start call id %v", end.BizKey, start.BizKey)
	}
	if first.ParentEventID == nil || *first.ParentEventID != start.EventID {
		t.Errorf("first.parent = %v, want start event %s", first.ParentEventID, start.EventID)
	}
	if first.BizKey == nil || *first.BizKey != "t1" {
		t.Errorf("first.biz_key = %v, want t1", first.BizKey)
	}
}

// 后台调用（无 turn）：归组键为空、父为空=根、trigger=batch。
func TestLLMCallBackgroundNoTurn(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	c := WrapLLMClient(&fakeLLM{msg: llm.Message{Usage: &llm.Usage{PromptTokens: 1, CompletionTokens: 2}}}, s, "bg_flash")
	if _, err := c.ChatSync(context.Background(), &llm.ChatRequest{}); err != nil {
		t.Fatalf("chatsync: %v", err)
	}
	s.Flush()

	starts := readEvents(t, s, TypeLLMCallStarted)
	if len(starts) != 1 {
		t.Fatalf("starts = %d, want 1", len(starts))
	}
	if starts[0].TurnID != nil || starts[0].ParentEventID != nil {
		t.Errorf("bg call must be root with empty turn: %+v", starts[0])
	}
	if starts[0].Trigger != TriggerBatch {
		t.Errorf("trigger = %q, want %q", starts[0].Trigger, TriggerBatch)
	}
	if len(readEvents(t, s, TypeLLMFirstToken)) != 0 {
		t.Error("ChatSync must not emit first_token")
	}
}

func TestLLMCallFailed(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	c := WrapLLMClient(&fakeLLM{err: errors.New("boom")}, s, "main_flash")
	if _, err := c.ChatSync(context.Background(), &llm.ChatRequest{}); err == nil {
		t.Fatal("expected error")
	}
	s.Flush()

	ends := readEvents(t, s, TypeLLMCallEnded)
	if len(ends) != 1 || ends[0].Outcome == nil || *ends[0].Outcome != OutcomeFailed {
		t.Fatalf("expected one failed end event, got %+v", ends)
	}
}

// TestLLMIntervalsClosePaired 端到端钉住：真实 emitter 产出的 llm.call.* 经 RebuildIntervals
// 必须判为 paired（回归 pair_id 互指；修复前 llm 区间永远配不上、走 inferred/fallback）。
func TestLLMIntervalsClosePaired(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	c := WrapLLMClient(&fakeLLM{msg: llm.Message{Usage: &llm.Usage{PromptTokens: 1, CompletionTokens: 2}}}, s, "main_flash")
	if _, err := c.ChatSync(context.Background(), &llm.ChatRequest{}); err != nil {
		t.Fatalf("chatsync: %v", err)
	}
	s.Flush()
	if err := s.RebuildIntervals(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var cb string
	if err := s.db.Raw(`SELECT i.closed_by FROM interval_closed i JOIN events e ON e.event_id = i.start_event_id
		WHERE e.type = ?`, TypeLLMCallStarted).Scan(&cb).Error; err != nil {
		t.Fatalf("scan closed_by: %v", err)
	}
	if cb != ClosedPaired {
		t.Errorf("llm interval closed_by = %q, want %q", cb, ClosedPaired)
	}
}
