package ctxflow

import (
	"testing"

	"xhan-helper/internal/agent/retrieval"
)

func TestBoostFactor(t *testing.T) {
	mod := BoostFactor(1.5)
	pkt := retrieval.ContextPacket{
		Text:  "test",
		Score: 0.5,
	}
	got := mod(pkt)
	if got.Score < 0.7499 || got.Score > 0.7501 {
		t.Errorf("Score: expected ~0.75, got %f", got.Score)
	}
	if got.Text != "test" {
		t.Errorf("Text should be unchanged")
	}
}

func TestBoostFactorEquivalent(t *testing.T) {
	pkt := retrieval.ContextPacket{Score: 0.8}
	got := BoostFactor(0.5)(pkt)
	if got.Score != 0.4 {
		t.Errorf("Score: expected 0.4, got %f", got.Score)
	}
}

func TestBoostClampedAtOne(t *testing.T) {
	got := BoostFactor(2.0)(retrieval.ContextPacket{Score: 0.9})
	if got.Score != 1.0 {
		t.Errorf("Score should be clamped to 1.0, got %f", got.Score)
	}
}

func TestBoostClampedAtZero(t *testing.T) {
	got := BoostFactor(2.0)(retrieval.ContextPacket{Score: -0.1})
	if got.Score != 0 {
		t.Errorf("Score should be clamped to 0, got %f", got.Score)
	}
}
