package retrieval

import (
	"math"
	"testing"
	"time"
)

func TestNewTimeDecay(t *testing.T) {
	d := NewTimeDecay(0)
	if d.Rate != 0 {
		t.Errorf("NewTimeDecay(0): expected rate 0, got %.4f", d.Rate)
	}
}

func TestFreshness_NoDecay(t *testing.T) {
	d := NewTimeDecay(0)
	now := time.Now()
	f := d.Freshness(now)
	if math.Abs(f-1.0) > 0.0001 {
		t.Errorf("expected 1.0, got %.4f", f)
	}
	f = d.Freshness(now.AddDate(0, 0, -365))
	if math.Abs(f-1.0) > 0.0001 {
		t.Errorf("expected 1.0 for old item with no decay, got %.4f", f)
	}
}

func TestFreshness_WithDecay(t *testing.T) {
	d := NewTimeDecay(0.1)
	now := time.Now()

	f := d.Freshness(now)
	if math.Abs(f-1.0) > 0.0001 {
		t.Errorf("today: expected 1.0, got %.4f", f)
	}

	f = d.Freshness(now.AddDate(0, 0, -10))
	if math.Abs(f-0.5) > 0.01 {
		t.Errorf("10 days ago: expected ~0.5, got %.4f", f)
	}

	f = d.Freshness(now.AddDate(0, 0, -30))
	if math.Abs(f-0.25) > 0.01 {
		t.Errorf("30 days ago: expected ~0.25, got %.4f", f)
	}
}

func TestFreshness_VeryOld(t *testing.T) {
	d := NewTimeDecay(0.02)
	now := time.Now()

	f := d.Freshness(now.AddDate(-3, 0, 0))
	if f > 0.1 {
		t.Errorf("3 years ago: expected < 0.1, got %.4f", f)
	}
}

func TestFreshness_HistoryDecay(t *testing.T) {
	d := NewTimeDecay(0.005)
	now := time.Now()

	f := d.Freshness(now.AddDate(0, 0, -7))
	if math.Abs(f-0.966) > 0.01 {
		t.Errorf("7 days ago: expected ~0.966, got %.4f", f)
	}

	f = d.Freshness(now.AddDate(0, 0, -90))
	if math.Abs(f-0.690) > 0.01 {
		t.Errorf("90 days ago: expected ~0.690, got %.4f", f)
	}

	f = d.Freshness(now.AddDate(0, 0, -365))
	if math.Abs(f-0.354) > 0.01 {
		t.Errorf("365 days ago: expected ~0.354, got %.4f", f)
	}
}

func TestFreshness_Rounded(t *testing.T) {
	d := NewTimeDecay(0.005)
	now := time.Now()
	f := d.Freshness(now.AddDate(0, 0, -7))
	if f != math.Round(f*10000)/10000 {
		t.Errorf("Freshness not rounded: %.10f", f)
	}
}

func TestFreshness_MemoryDecay(t *testing.T) {
	d := NewTimeDecay(0.01)
	now := time.Now()

	f := d.Freshness(now.AddDate(0, 0, -30))
	if math.Abs(f-0.769) > 0.01 {
		t.Errorf("30 days ago with 0.01 decay: expected ~0.769, got %.4f", f)
	}
}

func TestFreshness_Future(t *testing.T) {
	d := NewTimeDecay(0.1)
	now := time.Now()
	f := d.Freshness(now.AddDate(0, 0, 1))
	if f < 1.0 {
		t.Errorf("future item: expected >= 1.0, got %.4f", f)
	}
}
