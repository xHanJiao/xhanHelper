package retrieval

import (
	"testing"
	"time"

	"xhan-helper/internal/model"
)

func TestEntityMetaSatisfiesPacketMeta(t *testing.T) {
	var _ PacketMeta = EntityMeta{}
}

func TestHistoryMetaSatisfiesPacketMeta(t *testing.T) {
	var _ PacketMeta = HistoryMeta{}
}

func TestThemeMetaSatisfiesPacketMeta(t *testing.T) {
	var _ PacketMeta = ThemeMeta{}
}

func TestEntityMetaFields(t *testing.T) {
	m := EntityMeta{
		Content:   "昨夜睡得好",
		Topic:     "睡眠",
		CreatedAt: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}
	if m.Content != "昨夜睡得好" {
		t.Error("Content mismatch")
	}
	if m.Topic != "睡眠" {
		t.Error("Topic mismatch")
	}
	if m.CreatedAt.Year() != 2026 {
		t.Error("Year mismatch")
	}
	if m.CreatedAt.Month() != 1 {
		t.Error("Month mismatch")
	}
}

func TestHistoryMetaMessages(t *testing.T) {
	m := HistoryMeta{
		Messages: nil,
	}
	if m.Messages != nil {
		t.Error("nil messages should be nil")
	}

	m2 := HistoryMeta{
		Messages: []*model.Message{},
	}
	if len(m2.Messages) != 0 {
		t.Error("empty messages should have len 0")
	}
}
