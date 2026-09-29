package activity

import (
	"fmt"
	"path/filepath"
	"testing"
)

type scannedEvent struct {
	EventID       string  `gorm:"column:event_id"`
	Type          string  `gorm:"column:type"`
	Actor         string  `gorm:"column:actor"`
	Trigger       string  `gorm:"column:trigger"`
	SchemaVersion int     `gorm:"column:schema_version"`
	OccurredAt    string  `gorm:"column:occurred_at"`
	ReceivedAt    string  `gorm:"column:received_at"`
	BizKey        *string `gorm:"column:biz_key"`
	Attrs         *string `gorm:"column:attrs"`
}

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func countEvents(t *testing.T, s *Store) int64 {
	t.Helper()
	var n int64
	if err := s.db.Raw(`SELECT count(*) FROM events`).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func firstEvent(t *testing.T, s *Store) scannedEvent {
	t.Helper()
	var row scannedEvent
	if err := s.db.Raw(`SELECT * FROM events LIMIT 1`).Scan(&row).Error; err != nil {
		t.Fatalf("scan: %v", err)
	}
	return row
}

// 入口补公共字段：event_id 自动生成、actor/trigger 默认、schema_version、双时间戳。
func TestEmitFillsCommonFields(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{Type: "tool.call.started"})
	s.Flush()

	if n := countEvents(t, s); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
	row := firstEvent(t, s)
	if row.EventID == "" {
		t.Error("event_id should be generated")
	}
	if row.Actor != ActorSystem {
		t.Errorf("actor = %q, want %q", row.Actor, ActorSystem)
	}
	if row.Trigger != TriggerManual {
		t.Errorf("trigger = %q, want %q", row.Trigger, TriggerManual)
	}
	if row.SchemaVersion != schemaVersion {
		t.Errorf("schema_version = %d, want %d", row.SchemaVersion, schemaVersion)
	}
	if row.OccurredAt == "" || row.ReceivedAt == "" {
		t.Errorf("timestamps should be set: occurred=%q received=%q", row.OccurredAt, row.ReceivedAt)
	}
	if row.BizKey != nil {
		t.Errorf("biz_key should be NULL, got %q", *row.BizKey)
	}
}

// 调用方给的非空公共字段不被覆盖。
func TestEmitHonorsCallerValues(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{
		Type: "chat.turn.started", EventID: "fixed-id",
		Actor: ActorAssistant, Trigger: TriggerBatch,
		ConversationID: "c1", TurnID: "t1", BizKey: "t1",
	})
	s.Flush()

	row := firstEvent(t, s)
	if row.EventID != "fixed-id" || row.Actor != ActorAssistant || row.Trigger != TriggerBatch {
		t.Errorf("caller values overwritten: %+v", row)
	}
	if row.BizKey == nil || *row.BizKey != "t1" {
		t.Errorf("biz_key = %v, want t1", row.BizKey)
	}
}

// 幂等第一道：event_id 主键冲突忽略。
func TestEmitIdempotentByEventID(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{Type: "test.marker", EventID: "dup"})
	s.Emit(Event{Type: "test.marker", EventID: "dup"})
	s.Flush()

	if n := countEvents(t, s); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

// 幂等第二道：(type, biz_key) 唯一索引——同键不同 event_id 只留一条。
func TestEmitIdempotentByBizKey(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{Type: "llm.call.started", EventID: "e1", BizKey: "call-1"})
	s.Emit(Event{Type: "llm.call.started", EventID: "e2", BizKey: "call-1"})
	s.Flush()

	if n := countEvents(t, s); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

// 无业务键的事件存 NULL，多条必须都留（部分唯一索引不生效）。
func TestEmitNoBizKeyKeepsAll(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	for i := 0; i < 5; i++ {
		s.Emit(Event{Type: "user.action.confirmed"})
	}
	s.Flush()

	if n := countEvents(t, s); n != 5 {
		t.Errorf("count = %d, want 5 (NULL biz_key must not collide)", n)
	}
}

func TestEmitAttrsJSON(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{Type: "test.marker", EventID: "a1", Attrs: map[string]any{"tool_name": "note", "args_len": 12}})
	s.Flush()

	row := firstEvent(t, s)
	if row.Attrs == nil {
		t.Fatal("attrs should be set")
	}
	if want := `{"args_len":12,"tool_name":"note"}`; *row.Attrs != want {
		t.Errorf("attrs = %s, want %s", *row.Attrs, want)
	}
}

// 背压：队列满丢最旧并计数，且 Emit 不阻塞。
func TestEmitDropOldestWhenFull(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	e := &Emitter{store: s, maxQueue: 3, queue: make([]eventRow, 0, 3)}
	for i := 0; i < 5; i++ {
		e.Emit(Event{Type: "test.marker", EventID: fmt.Sprintf("e%d", i)})
	}
	if got := e.Dropped(); got != 2 {
		t.Errorf("dropped = %d, want 2", got)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.queue) != 3 {
		t.Fatalf("queue len = %d, want 3", len(e.queue))
	}
	want := []string{"e2", "e3", "e4"}
	for i, w := range want {
		if e.queue[i].EventID != w {
			t.Errorf("queue[%d] = %s, want %s", i, e.queue[i].EventID, w)
		}
	}
}

// validEventType 契约：必须带点、全小写、每段非空。
func TestValidEventType(t *testing.T) {
	valid := []string{"bill.created", "client.tool.exec.started", "system.boundary", "a.b", "a1.b2", "a.b_c"}
	invalid := []string{"", "t", "Chat.Turn", "chat..sent", ".chat", "chat.", "chat-x.sent", "chat turn.sent", "_a.b", "a.1", "1.a", "123.456", "a._b"}
	for _, s := range valid {
		if !validEventType(s) {
			t.Errorf("validEventType(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if validEventType(s) {
			t.Errorf("validEventType(%q) = true, want false", s)
		}
	}
}

// 违契约的 type 在 emit 入口被拒绝：不落库。
func TestEmitRejectsBadType(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	for _, bad := range []string{"", "nodot", "Chat.Turn"} {
		s.Emit(Event{Type: bad})
	}
	s.Emit(Event{Type: "good.type"}) // 合法的一条用来确认通道仍工作
	s.Flush()

	if n := countEvents(t, s); n != 1 {
		t.Errorf("count = %d, want 1（违契约的都被拒）", n)
	}
}
