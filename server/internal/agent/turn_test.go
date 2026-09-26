package agent

import (
	"context"
	"path/filepath"
	"testing"

	"xhan-helper/internal/activity"
)

type turnEventRow struct {
	EventID string  `gorm:"column:event_id"`
	Type    string  `gorm:"column:type"`
	TurnID  *string `gorm:"column:turn_id"`
	ConvID  *string `gorm:"column:conversation_id"`
	Outcome *string `gorm:"column:outcome"`
	PairID  *string `gorm:"column:pair_id"`
	BizKey  *string `gorm:"column:biz_key"`
	Parent  *string `gorm:"column:parent_event_id"`
}

func readTurnEvents(t *testing.T, store *activity.Store) map[string]turnEventRow {
	t.Helper()
	var rows []turnEventRow
	err := store.DB().Raw(
		`SELECT * FROM events WHERE type IN (?, ?)`,
		activity.TypeChatTurnStarted, activity.TypeChatTurnEnded,
	).Scan(&rows).Error
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	m := make(map[string]turnEventRow, len(rows))
	for _, r := range rows {
		m[r.Type] = r
	}
	return m
}

func TestStartTurnEmitsStartAndEnd(t *testing.T) {
	store, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer store.Close()

	o := &Orchestrator{activity: store}
	turn := o.StartTurn(context.Background(), "c1", activity.TriggerManual, "")
	if turn.ID == "" {
		t.Fatal("turn id should be non-empty when activity enabled")
	}
	turn.Finish(activity.OutcomeCompleted)
	turn.Finish(activity.OutcomeFailed) // 幂等：第二次无效
	store.Flush()

	events := readTurnEvents(t, store)
	if len(events) != 2 {
		t.Fatalf("got %d turn events, want 2", len(events))
	}
	start := events[activity.TypeChatTurnStarted]
	end := events[activity.TypeChatTurnEnded]

	if start.TurnID == nil || *start.TurnID != turn.ID {
		t.Errorf("start.turn_id = %v, want %s", start.TurnID, turn.ID)
	}
	if start.ConvID == nil || *start.ConvID != "c1" {
		t.Errorf("start.conversation_id = %v, want c1", start.ConvID)
	}
	if start.BizKey == nil || *start.BizKey != turn.ID {
		t.Errorf("start.biz_key = %v, want %s", start.BizKey, turn.ID)
	}
	if start.Outcome != nil {
		t.Errorf("start.outcome should be NULL, got %v", *start.Outcome)
	}
	if end.Outcome == nil || *end.Outcome != activity.OutcomeCompleted {
		t.Errorf("end.outcome = %v, want completed", end.Outcome)
	}
	// pair_id 互指：start.pair_id = end.event_id，end.pair_id = start.event_id
	if start.PairID == nil || *start.PairID != end.EventID {
		t.Errorf("start.pair_id = %v, want end event %s", start.PairID, end.EventID)
	}
	if end.PairID == nil || *end.PairID != start.EventID {
		t.Errorf("end.pair_id = %v, want start event %s", end.PairID, start.EventID)
	}
}

// 采集关闭（store 为 nil）时：turn 退化为空 id，不发事件、不 panic。
func TestStartTurnDisabledIsNoop(t *testing.T) {
	o := &Orchestrator{}
	turn := o.StartTurn(context.Background(), "c1", activity.TriggerManual, "")
	if turn.ID != "" {
		t.Errorf("turn id should be empty when disabled, got %q", turn.ID)
	}
	turn.Finish(activity.OutcomeCompleted) // 不应 panic
}
