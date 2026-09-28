package main

import (
	"testing"
	"time"
)

func at(min int) time.Time {
	return time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute)
}

func TestOverlapMinutes(t *testing.T) {
	cases := []struct {
		as, ae, bs, be string
		want           int
	}{
		{"00:10", "00:33", "00:27", "00:57", 6},   // 部分重叠
		{"05:33", "06:50", "05:24", "08:12", 77},  // 块在段内
		{"14:16", "14:16", "12:03", "14:42", 1},   // 零长块落在段内
		{"14:53", "14:53", "12:03", "14:42", 0},   // 零长块在段外
		{"20:24", "23:28", "20:16", "23:59", 184}, // 长块
		{"00:00", "00:01", "01:00", "02:00", 0},   // 不相交
	}
	for _, c := range cases {
		if got := overlapMinutes(c.as, c.ae, c.bs, c.be); got != c.want {
			t.Errorf("overlap(%s-%s, %s-%s)=%d, want %d", c.as, c.ae, c.bs, c.be, got, c.want)
		}
	}
}

func TestBuildTurnsSplitsOnUser(t *testing.T) {
	// user → assistant → tool → user → assistant：应切出 2 个 turn，且各自收边到末条消息。
	rows := []msgRow{
		{ID: "u1", ConversationID: "c", Role: "user", CreatedAt: at(0)},
		{ID: "a1", ConversationID: "c", Role: "assistant", CreatedAt: at(1)},
		{ID: "t1", ConversationID: "c", Role: "tool", CreatedAt: at(2)},
		{ID: "u2", ConversationID: "c", Role: "user", CreatedAt: at(3)},
		{ID: "a2", ConversationID: "c", Role: "assistant", CreatedAt: at(4)},
	}
	turns := buildTurns(rows)
	if len(turns) != 2 {
		t.Fatalf("turns=%d, want 2", len(turns))
	}
	if !turns[0].started.OccurredAt.Equal(at(0)) || !turns[0].ended.OccurredAt.Equal(at(2)) {
		t.Errorf("turn0 span %v–%v, want 0–2", turns[0].started.OccurredAt, turns[0].ended.OccurredAt)
	}
	if !turns[1].started.OccurredAt.Equal(at(3)) || !turns[1].ended.OccurredAt.Equal(at(4)) {
		t.Errorf("turn1 span %v–%v, want 3–4", turns[1].started.OccurredAt, turns[1].ended.OccurredAt)
	}
}

func TestDetIDIsDeterministic(t *testing.T) {
	if detID("msg", "x") != detID("msg", "x") {
		t.Error("detID not deterministic for same key")
	}
	if detID("msg", "x") == detID("msg", "y") {
		t.Error("detID collides for different keys")
	}
}

// TestTurnPairIDsMutuallyReference 钉住「区间事件两端 pair_id 互指对端 event_id」这条约定
// （与活动包 pairedEnd 的匹配口径一致，照 agent/turn_test.go 的断言方式）。
func TestTurnPairIDsMutuallyReference(t *testing.T) {
	tr := newTurn("c", msgRow{ID: "u1", ConversationID: "c", Role: "user", CreatedAt: at(0)})
	if tr.started.PairID != tr.ended.EventID {
		t.Errorf("turn.started.PairID=%q, want ended.EventID=%q", tr.started.PairID, tr.ended.EventID)
	}
	if tr.ended.PairID != tr.started.EventID {
		t.Errorf("turn.ended.PairID=%q, want started.EventID=%q", tr.ended.PairID, tr.started.EventID)
	}
	if tr.started.ParentEventID != detID("msg", "u1") {
		t.Errorf("turn.started.ParentEventID=%q, want 根点事件 %q", tr.started.ParentEventID, detID("msg", "u1"))
	}
}

// TestToolCallPairIDsMutuallyReference 钉住 tool.call.* 两端互指；且 ended 用配对结果的时刻。
func TestToolCallPairIDsMutuallyReference(t *testing.T) {
	calls := `[{"id":"call_1","function":{"name":"note__search"}}]`
	tcid := "call_1"
	tr := newTurn("c", msgRow{ID: "u1", ConversationID: "c", Role: "user", CreatedAt: at(0)})
	tr.messages = []msgRow{
		{ID: "a1", ConversationID: "c", Role: "assistant", ToolCallsJSON: &calls, CreatedAt: at(1)},
		{ID: "t1", ConversationID: "c", Role: "tool", ToolCallID: &tcid, CreatedAt: at(2)},
	}
	evs := toolCallEvents(tr.messages[0], tr)
	if len(evs) != 2 {
		t.Fatalf("tool events=%d, want 2", len(evs))
	}
	start, end := evs[0], evs[1]
	if start.PairID != end.EventID || end.PairID != start.EventID {
		t.Errorf("tool pair_id 未互指: start.PairID=%q end.EventID=%q; end.PairID=%q start.EventID=%q",
			start.PairID, end.EventID, end.PairID, start.EventID)
	}
	if !end.OccurredAt.Equal(at(2)) {
		t.Errorf("tool.call.ended 时刻=%v, want 配对结果时刻 %v", end.OccurredAt, at(2))
	}
	if end.OccurredAt.Before(start.OccurredAt) {
		t.Error("tool.call.ended 早于 started")
	}
}

// TestLLMPairIDsMutuallyReference 钉住 llm.call.* 两端互指。
func TestLLMPairIDsMutuallyReference(t *testing.T) {
	startID, endID := detID("llm-s", "L1"), detID("llm-e", "L1")
	if startID == endID {
		t.Fatal("llm start/end event_id 不应相同")
	}
}
