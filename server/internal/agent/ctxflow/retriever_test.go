package ctxflow

import (
	"context"
	"testing"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/llm"
)

type stubContextSource struct {
	name    string
	packets []retrieval.ContextPacket
}

func (s *stubContextSource) Name() string { return s.name }

func (s *stubContextSource) Fetch(context.Context, retrieval.SearchOptions) ([]retrieval.ContextPacket, []retrieval.NearMissItem, error) {
	return s.packets, nil, nil
}

func (s *stubContextSource) Warmup(context.Context, *llm.Embedder) {}

func TestEventSourceNameMatchesTypeEvent(t *testing.T) {
	if TypeEvent != retrieval.EventSourceName {
		t.Fatalf("ctxflow.TypeEvent(%q) must equal retrieval.EventSourceName(%q)", TypeEvent, retrieval.EventSourceName)
	}
}

func TestRetrieve_PreservesEventOrder(t *testing.T) {
	src := &stubContextSource{name: retrieval.EventSourceName, packets: []retrieval.ContextPacket{
		{ID: "sticky", Type: TypeEvent, Score: 0, Meta: retrieval.EventMeta{
			EventID: "sticky", Mode: retrieval.EventModeFull,
		}},
		{ID: "fresh", Type: TypeEvent, Score: 0.9, Meta: retrieval.EventMeta{
			EventID: "fresh", Mode: retrieval.EventModeIndex,
		}},
	}}
	r := NewContextRetriever([]retrieval.ContextSource{src}, nil)
	res, err := r.Retrieve(context.Background(), retrieval.SearchOptions{
		Embedding: []float64{1},
		Event:     retrieval.SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	got := res.Packets[TypeEvent]
	if len(got) != 2 || got[0].ID != "sticky" || got[1].ID != "fresh" {
		t.Fatalf("event order must be preserved (no score re-sort), got %+v", got)
	}
}
