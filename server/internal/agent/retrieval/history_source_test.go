package retrieval

import (
	"testing"
	"time"

	"xhan-helper/internal/model"
)

// makeMsg 快速构造消息，参数：id, role, content, offset(秒)
func makeMsg(id, role, content string, baseTime time.Time, offsetSec int) model.Message {
	return model.Message{
		ID:        id,
		Role:      role,
		Content:   content,
		CreatedAt: baseTime.Add(time.Duration(offsetSec) * time.Second),
	}
}

func TestBuildPackets_NoPrevMsg(t *testing.T) {
	now := time.Now()
	hits := []historyHit{
		{msg: &model.Message{ID: "u1", Role: "user", Content: "帮我查账单", CreatedAt: now}, vecScore: 0.9},
	}
	roundByUserMsgID := map[string][]model.Message{
		"u1": {
			makeMsg("u1", "user", "帮我查账单", now, 0),
			makeMsg("a1", "assistant", "好的，已记录", now, 1),
		},
	}

	packets, nearMiss := buildPackets(hits, roundByUserMsgID, nil)
	if len(nearMiss) != 0 {
		t.Errorf("expected 0 nearMiss, got %d", len(nearMiss))
	}
	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}
	pkt := packets[0]
	if pkt.ID != "u1" {
		t.Errorf("expected ID=u1, got %s", pkt.ID)
	}

	meta, ok := pkt.Meta.(HistoryMeta)
	if !ok {
		t.Fatal("expected HistoryMeta")
	}
	if len(meta.Messages) != 2 {
		t.Errorf("expected 2 messages (user + assistant), got %d", len(meta.Messages))
	}
	if meta.HitDate == nil {
		t.Error("expected HitDate to be set")
	} else if !meta.HitDate.Equal(now) {
		t.Errorf("HitDate mismatch: %v != %v", meta.HitDate, now)
	}
}

func TestBuildPackets_WithPrevMsg(t *testing.T) {
	now := time.Now()
	prevMsg := makeMsg("u0", "user", "明天有个面试要准备", now, -3600) // 1 小时前
	hits := []historyHit{
		{msg: &model.Message{ID: "u1", Role: "user", Content: "6/22 去面试，地点在望京", CreatedAt: now}, vecScore: 0.85},
	}
	roundByUserMsgID := map[string][]model.Message{
		"u1": {
			makeMsg("u1", "user", "6/22 去面试，地点在望京", now, 0),
			makeMsg("a1", "assistant", "好的，我记下了", now, 1),
		},
	}
	prevUserMsgs := map[string]*model.Message{
		"u1": &prevMsg,
	}

	packets, nearMiss := buildPackets(hits, roundByUserMsgID, prevUserMsgs)
	if len(nearMiss) != 0 {
		t.Errorf("expected 0 nearMiss, got %d", len(nearMiss))
	}
	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}

	meta, ok := packets[0].Meta.(HistoryMeta)
	if !ok {
		t.Fatal("expected HistoryMeta")
	}
	if len(meta.Messages) != 3 {
		t.Fatalf("expected 3 messages (prev + user + assistant), got %d", len(meta.Messages))
	}
	// 第一条应该是 prev msg
	if meta.Messages[0].ID != "u0" {
		t.Errorf("expected first msg u0 (prev), got %s", meta.Messages[0].ID)
	}
	if meta.Messages[0].Content != "明天有个面试要准备" {
		t.Errorf("prev msg content mismatch: %s", meta.Messages[0].Content)
	}
	// 第二条是命中的 user msg
	if meta.Messages[1].ID != "u1" {
		t.Errorf("expected second msg u1 (hit), got %s", meta.Messages[1].ID)
	}
	// HitDate 应该等于命中消息的时间
	if meta.HitDate == nil {
		t.Fatal("expected HitDate to be set")
	}
	if !meta.HitDate.Equal(now) {
		t.Errorf("HitDate should equal hit msg time (%v), got %v", now, meta.HitDate)
	}
}

func TestBuildPackets_PartialPrevMsgs(t *testing.T) {
	now := time.Now()
	prevMsg := makeMsg("u0", "user", "昨天说的那个事...", now, -3600)

	hits := []historyHit{
		{msg: &model.Message{ID: "u1", Role: "user", Content: "第一轮消息", CreatedAt: now}, vecScore: 0.9},
		{msg: &model.Message{ID: "u2", Role: "user", Content: "第二轮消息", CreatedAt: now.Add(-time.Hour)}, vecScore: 0.8},
	}
	roundByUserMsgID := map[string][]model.Message{
		"u1": {
			makeMsg("u1", "user", "第一轮消息", now, 0),
			makeMsg("a1", "assistant", "收到", now, 1),
		},
		"u2": {
			makeMsg("u2", "user", "第二轮消息", now, -3600),
			makeMsg("a2", "assistant", "好的", now, -3599),
		},
	}
	// 只有 u1 有 prev msg，u2 没有
	prevUserMsgs := map[string]*model.Message{
		"u1": &prevMsg,
	}

	packets, _ := buildPackets(hits, roundByUserMsgID, prevUserMsgs)
	if len(packets) != 2 {
		t.Fatalf("expected 2 packets, got %d", len(packets))
	}

	// u1 应该有 3 条（prev+user+assistant）
	meta1, _ := packets[0].Meta.(HistoryMeta)
	if len(meta1.Messages) != 3 {
		t.Errorf("u1: expected 3 messages, got %d", len(meta1.Messages))
	}

	// u2 只有 2 条（user+assistant）
	meta2, _ := packets[1].Meta.(HistoryMeta)
	if len(meta2.Messages) != 2 {
		t.Errorf("u2: expected 2 messages (no prev), got %d", len(meta2.Messages))
	}
}

func TestBuildPackets_EmptyPrevMap(t *testing.T) {
	now := time.Now()
	hits := []historyHit{
		{msg: &model.Message{ID: "u1", Role: "user", Content: "测试", CreatedAt: now}, vecScore: 0.8},
	}
	roundByUserMsgID := map[string][]model.Message{
		"u1": {
			makeMsg("u1", "user", "测试", now, 0),
			makeMsg("a1", "assistant", "收到", now, 1),
		},
	}

	// 空 map 不应崩溃
	packets, _ := buildPackets(hits, roundByUserMsgID, map[string]*model.Message{})
	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}
	meta, _ := packets[0].Meta.(HistoryMeta)
	if len(meta.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(meta.Messages))
	}
}
