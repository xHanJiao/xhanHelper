package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"xhan-helper/internal/activity"
)

type fakeTool struct {
	name string
	res  *Result
}

func (f *fakeTool) Name() string                   { return f.name }
func (f *fakeTool) Description() string            { return "fake" }
func (f *fakeTool) Functions() []FuncSchema        { return nil }
func (f *fakeTool) ReadOnlyFuncs() map[string]bool { return nil }
func (f *fakeTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	return f.res
}

type callRow struct {
	EventID   string  `gorm:"column:event_id"`
	Type      string  `gorm:"column:type"`
	Actor     string  `gorm:"column:actor"`
	Trigger   string  `gorm:"column:trigger"`
	Outcome   *string `gorm:"column:outcome"`
	ErrorType *string `gorm:"column:error_type"`
	ParentID  *string `gorm:"column:parent_event_id"`
	PairID    *string `gorm:"column:pair_id"`
	ConvID    *string `gorm:"column:conversation_id"`
	TurnID    *string `gorm:"column:turn_id"`
	CallID    *string `gorm:"column:call_id"`
	BizKey    *string `gorm:"column:biz_key"`
	Attrs     *string `gorm:"column:attrs"`
}

func readCalls(t *testing.T, s *activity.Store, typ string) []callRow {
	t.Helper()
	var rows []callRow
	if err := s.DB().Raw(`SELECT * FROM events WHERE type=? ORDER BY rowid`, typ).Scan(&rows).Error; err != nil {
		t.Fatalf("scan %s: %v", typ, err)
	}
	return rows
}

func newActivityRegistry(t *testing.T, s *activity.Store, name string, res *Result) *Registry {
	t.Helper()
	r := NewRegistry()
	r.SetActivityStore(s)
	r.Register(&fakeTool{name: name, res: res})
	return r
}

func TestToolCallEmitsStartAndEnd(t *testing.T) {
	s, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer s.Close()

	r := newActivityRegistry(t, s, "bookkeeping", &Result{Success: true, Data: json.RawMessage(`{"ok":1}`)})
	ctx := activity.WithTurn(context.Background(), activity.TurnCtx{TurnID: "t1", StartEventID: "s1"})
	ec := &ExecuteContext{Ctx: ctx, ConvID: "c1", TurnID: "t1", ToolCallID: "call-1"}

	if res := r.ExecuteByName(ec, "bookkeeping", "add_bill", json.RawMessage(`{"amount":5}`)); !res.Success {
		t.Fatal("expected success")
	}
	s.Flush()

	starts := readCalls(t, s, activity.TypeToolCallStarted)
	ends := readCalls(t, s, activity.TypeToolCallEnded)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("counts start=%d end=%d, want 1/1", len(starts), len(ends))
	}
	st, en := starts[0], ends[0]

	if st.BizKey == nil || *st.BizKey != "call-1" {
		t.Errorf("start biz_key = %v, want call-1", st.BizKey)
	}
	if st.CallID == nil || *st.CallID != "call-1" {
		t.Errorf("start call_id = %v, want call-1", st.CallID)
	}
	if st.ParentID == nil || *st.ParentID != "s1" {
		t.Errorf("start parent = %v, want s1", st.ParentID)
	}
	if st.TurnID == nil || *st.TurnID != "t1" || st.Actor != "assistant" || st.Trigger != "manual" {
		t.Errorf("start row = %+v", st)
	}
	if st.PairID == nil || *st.PairID != en.EventID {
		t.Errorf("start pair = %v, want end %s", st.PairID, en.EventID)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(*st.Attrs), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["tool_name"] != "bookkeeping__add_bill" || attrs["args_len"] != float64(len(`{"amount":5}`)) {
		t.Errorf("start attrs = %v", attrs)
	}
	if len(attrs["args_digest"].(string)) != 16 {
		t.Errorf("args_digest = %v", attrs["args_digest"])
	}

	if en.Outcome == nil || *en.Outcome != "completed" {
		t.Errorf("end outcome = %v, want completed", en.Outcome)
	}
	if en.PairID == nil || *en.PairID != st.EventID {
		t.Errorf("end pair = %v, want start %s", en.PairID, st.EventID)
	}
	var eattrs map[string]any
	if err := json.Unmarshal([]byte(*en.Attrs), &eattrs); err != nil {
		t.Fatalf("end attrs: %v", err)
	}
	if eattrs["result_len"] != float64(len(`{"ok":1}`)) {
		t.Errorf("end attrs = %v", eattrs)
	}
}

func TestToolCallFailed(t *testing.T) {
	s, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer s.Close()

	r := newActivityRegistry(t, s, "note", &Result{Success: false, Error: "boom"})
	// 无 turn 上下文：根事件、trigger=batch、无业务键（NULL）
	ec := &ExecuteContext{Ctx: context.Background()}
	r.ExecuteByName(ec, "note", "create", json.RawMessage(`{}`))
	s.Flush()

	starts := readCalls(t, s, activity.TypeToolCallStarted)
	ends := readCalls(t, s, activity.TypeToolCallEnded)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("counts start=%d end=%d, want 1/1", len(starts), len(ends))
	}
	if starts[0].ParentID != nil || starts[0].BizKey != nil {
		t.Errorf("bg call must have NULL parent/biz_key: %+v", starts[0])
	}
	if starts[0].Trigger != "batch" {
		t.Errorf("trigger = %q, want batch", starts[0].Trigger)
	}
	en := ends[0]
	if en.Outcome == nil || *en.Outcome != "failed" {
		t.Errorf("outcome = %v, want failed", en.Outcome)
	}
	if en.ErrorType == nil || *en.ErrorType != "tool_error" {
		t.Errorf("error_type = %v, want tool_error", en.ErrorType)
	}
}
