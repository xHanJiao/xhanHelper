package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

type bridgeRow struct {
	EventID  string  `gorm:"column:event_id"`
	Type     string  `gorm:"column:type"`
	Actor    string  `gorm:"column:actor"`
	Trigger  string  `gorm:"column:trigger"`
	Outcome  *string `gorm:"column:outcome"`
	ParentID *string `gorm:"column:parent_event_id"`
	ConvID   *string `gorm:"column:conversation_id"`
	TurnID   *string `gorm:"column:turn_id"`
	CallID   *string `gorm:"column:call_id"`
	BizKey   *string `gorm:"column:biz_key"`
	Attrs    *string `gorm:"column:attrs"`
}

func readBridge(t *testing.T, s *activity.Store, typ string) []bridgeRow {
	t.Helper()
	var rows []bridgeRow
	if err := s.DB().Raw(`SELECT * FROM events WHERE type=? ORDER BY rowid`, typ).Scan(&rows).Error; err != nil {
		t.Fatalf("scan %s: %v", typ, err)
	}
	return rows
}

// bridgeSender 返回一个 mustSend：解析下发 payload 的 call_id，并把回传结果投进桥。
func bridgeSender(b *ClientToolBridge, userID string, result ClientToolResult) (func(*llm.StreamEvent) bool, func() string) {
	var callID string
	send := func(evt *llm.StreamEvent) bool {
		if evt.Type == "client_tool_call" {
			var m map[string]any
			if err := json.Unmarshal([]byte(evt.Content), &m); err == nil {
				callID, _ = m["call_id"].(string)
			}
			if callID != "" {
				go func() { _ = b.OnResult(callID, userID, result) }()
			}
		}
		return true
	}
	return send, func() string { return callID }
}

func TestBridgeDispatchedAndReturned(t *testing.T) {
	s, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer s.Close()

	b := NewClientToolBridge()
	cc := &clientToolCaller{bridge: b, timeout: time.Second, activity: s}
	send, callID := bridgeSender(b, "u1", ClientToolResult{Result: json.RawMessage(`{"ok":1}`)})
	ctx := activity.WithTurn(context.Background(), activity.TurnCtx{TurnID: "t1", StartEventID: "s1"})

	if _, _ = cc.Call(ctx, send, func(*llm.StreamEvent) {}, "u1",
		tool.ToolCall{Tool: "browser", Function: "navigate"}, "", "main", "c1", "t1"); callID() == "" {
		t.Fatal("call_id not captured")
	}
	s.Flush()

	disp := readBridge(t, s, activity.TypeToolBridgeDispatched)
	ret := readBridge(t, s, activity.TypeToolBridgeReturned)
	if len(disp) != 1 || len(ret) != 1 {
		t.Fatalf("counts dispatched=%d returned=%d, want 1/1", len(disp), len(ret))
	}
	d, r := disp[0], ret[0]
	cid := callID()
	if d.CallID == nil || *d.CallID != cid || d.BizKey == nil || *d.BizKey != cid {
		t.Errorf("dispatched call_id/biz_key = %v/%v, want %s", d.CallID, d.BizKey, cid)
	}
	if d.ParentID == nil || *d.ParentID != "s1" || d.TurnID == nil || *d.TurnID != "t1" {
		t.Errorf("dispatched parent/turn = %v/%v", d.ParentID, d.TurnID)
	}
	if d.Actor != "assistant" || d.Trigger != "manual" || d.Outcome != nil {
		t.Errorf("dispatched row = %+v", d)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(*d.Attrs), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["tool_name"] != "browser__navigate" {
		t.Errorf("attrs tool_name = %v", attrs["tool_name"])
	}
	if r.Outcome == nil || *r.Outcome != activity.OutcomeCompleted {
		t.Errorf("returned outcome = %v, want completed", r.Outcome)
	}
	if r.CallID == nil || *r.CallID != cid || r.ParentID == nil || *r.ParentID != "s1" {
		t.Errorf("returned call_id/parent = %v/%v", r.CallID, r.ParentID)
	}
}

func TestBridgeReturnedFailedNoTurn(t *testing.T) {
	s, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer s.Close()

	b := NewClientToolBridge()
	cc := &clientToolCaller{bridge: b, timeout: time.Second, activity: s}
	send, _ := bridgeSender(b, "u1", ClientToolResult{Error: "boom"})
	// 无 turn 上下文：根事件、trigger=batch
	cc.Call(context.Background(), send, func(*llm.StreamEvent) {}, "u1",
		tool.ToolCall{Tool: "fs", Function: "read"}, "", "main", "c1", "")
	s.Flush()

	d := readBridge(t, s, activity.TypeToolBridgeDispatched)
	r := readBridge(t, s, activity.TypeToolBridgeReturned)
	if len(d) != 1 || len(r) != 1 {
		t.Fatalf("counts dispatched=%d returned=%d, want 1/1", len(d), len(r))
	}
	if d[0].ParentID != nil || d[0].Trigger != "batch" {
		t.Errorf("bg dispatched must be root/batch: %+v", d[0])
	}
	if r[0].Outcome == nil || *r[0].Outcome != activity.OutcomeFailed {
		t.Errorf("outcome = %v, want failed", r[0].Outcome)
	}
}

// 未送达（断连）→ 不落任何 bridge 事件。
func TestBridgeNotSentNoEvents(t *testing.T) {
	s, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer s.Close()

	cc := &clientToolCaller{bridge: NewClientToolBridge(), timeout: time.Second, activity: s}
	_, unavailable := cc.Call(context.Background(), func(*llm.StreamEvent) bool { return false },
		func(*llm.StreamEvent) {}, "u1", tool.ToolCall{Tool: "browser", Function: "navigate"}, "", "main", "c1", "t1")
	if !unavailable {
		t.Fatal("expected unavailable on send failure")
	}
	s.Flush()
	if len(readBridge(t, s, activity.TypeToolBridgeDispatched)) != 0 || len(readBridge(t, s, activity.TypeToolBridgeReturned)) != 0 {
		t.Error("send failure must not emit bridge events")
	}
}
