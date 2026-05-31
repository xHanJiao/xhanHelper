package promptbuilder

import (
	"context"
	"testing"

	"xhan-helper/internal/agent/retrieval"
)

func TestBoostFactor(t *testing.T) {
	mod := BoostFactor(1.5)
	pkt := retrieval.ContextPacket{
		Text:         "test",
		FinalScore:   0.6,
		Score:        0.5,
		KeywordScore: 0.4,
	}
	got := mod(pkt)
	if got.FinalScore < 0.8999 || got.FinalScore > 0.9001 {
		t.Errorf("FinalScore: expected ~0.9, got %f", got.FinalScore)
	}
	if got.Score < 0.7499 || got.Score > 0.7501 {
		t.Errorf("Score: expected ~0.75, got %f", got.Score)
	}
	if got.KeywordScore < 0.5999 || got.KeywordScore > 0.6001 {
		t.Errorf("KeywordScore: expected ~0.6, got %f", got.KeywordScore)
	}
	if got.Text != "test" {
		t.Errorf("Text should be unchanged")
	}
}

func TestBoostFactorEquivalent(t *testing.T) {
	// BoostFactor(0.5) 降权效果正确
	pkt := retrieval.ContextPacket{FinalScore: 0.8}
	got := BoostFactor(0.5)(pkt)
	if got.FinalScore != 0.4 {
		t.Errorf("FinalScore: expected 0.4, got %f", got.FinalScore)
	}
}

type simpleProvider struct {
	name   string
	header string
	items  []retrieval.ContextPacket
}

func (p *simpleProvider) Name() string          { return p.name }
func (p *simpleProvider) SectionHeader() string { return p.header }
func (p *simpleProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	return &SearchResult{Items: p.items}, nil
}

func TestWithScoreModifier(t *testing.T) {
	inner := &simpleProvider{
		name:   "test",
		header: "### test",
		items: []retrieval.ContextPacket{
			{Text: "a", FinalScore: 0.5},
			{Text: "b", FinalScore: 0.8},
		},
	}
	wrapped := WithScoreModifier(inner, BoostFactor(2.0))

	if wrapped.Name() != "test" {
		t.Errorf("Name should delegate to inner")
	}
	if wrapped.SectionHeader() != "### test" {
		t.Errorf("SectionHeader should delegate to inner")
	}

	sr, err := wrapped.Search(context.Background(), retrieval.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sr.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(sr.Items))
	}
	if sr.Items[0].FinalScore != 1.0 {
		t.Errorf("item 0: expected 1.0, got %f", sr.Items[0].FinalScore)
	}
	if sr.Items[1].FinalScore != 1.0 {
		t.Errorf("item 1: expected 1.0 (clamped), got %f", sr.Items[1].FinalScore)
	}
}

func TestBoostClampedAtOne(t *testing.T) {
	// 0.9 * 2.0 = 1.8 → clamped to 1.0
	got := BoostFactor(2.0)(retrieval.ContextPacket{FinalScore: 0.9, Score: 0.8})
	if got.FinalScore != 1.0 {
		t.Errorf("FinalScore should be clamped to 1.0, got %f", got.FinalScore)
	}
}

func TestBoostClampedAtZero(t *testing.T) {
	// -0.1 * 2.0 = -0.2 → clamped to 0
	got := BoostFactor(2.0)(retrieval.ContextPacket{FinalScore: -0.1})
	if got.FinalScore != 0 {
		t.Errorf("FinalScore should be clamped to 0, got %f", got.FinalScore)
	}
}

func TestWithScoreModifierNilResult(t *testing.T) {
	inner := &simpleProvider{name: "empty"}
	wrapped := WithScoreModifier(inner, BoostFactor(2.0))
	sr, err := wrapped.Search(context.Background(), retrieval.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sr.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(sr.Items))
	}
}

func TestWithScoreModifierSyncsTraceItems(t *testing.T) {
	inner := &traceProvider{
		items: []retrieval.ContextPacket{
			{ID: "a", FinalScore: 0.6, Type: "test"},
			{ID: "b", FinalScore: 0.4, Type: "test"},
		},
		traceItems: []TraceItem{
			{ID: "a", Score: 0.6}, // 已选中
			{ID: "b", Score: 0.4}, // 已选中
			{ID: "c", Score: 0.3, DiscardReason: "below_threshold"}, // near-miss，不改
		},
	}
	wrapped := WithScoreModifier(inner, BoostFactor(2.0))

	sr, err := wrapped.Search(context.Background(), retrieval.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Items 已提权
	if sr.Items[0].FinalScore != 1.0 {
		t.Errorf("item a FinalScore: expected 1.0 (clamped), got %f", sr.Items[0].FinalScore)
	}
	if sr.Items[1].FinalScore != 0.8 {
		t.Errorf("item b FinalScore: expected 0.8, got %f", sr.Items[1].FinalScore)
	}

	// TraceItems 中已选中的应该同步提权
	if sr.TraceItems[0].Score != 1.0 {
		t.Errorf("trace a Score: expected 1.0 (synced), got %f", sr.TraceItems[0].Score)
	}
	if sr.TraceItems[1].Score != 0.8 {
		t.Errorf("trace b Score: expected 0.8 (synced), got %f", sr.TraceItems[1].Score)
	}
	// near-miss 不应被修改
	if sr.TraceItems[2].Score != 0.3 {
		t.Errorf("trace c Score: expected 0.3 (unchanged near-miss), got %f", sr.TraceItems[2].Score)
	}
}

type traceProvider struct {
	name       string
	items      []retrieval.ContextPacket
	traceItems []TraceItem
}

func (p *traceProvider) Name() string          { return "trace-test" }
func (p *traceProvider) SectionHeader() string { return "" }
func (p *traceProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	return &SearchResult{Items: p.items, TraceItems: p.traceItems}, nil
}
