package activity

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEmitChangeMetaResolution(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// 工具标记优先于用户标记：聊天链路即使带了 user 标记也必须算 assistant。
	ctx := WithUserActor(context.Background())
	ctx = WithTurn(ctx, TurnCtx{TurnID: "t1", StartEventID: "s1", ConvID: "c1"})
	ctx = WithToolCall(ctx, "tc1")
	s.EmitChange(ctx, TypeDocCreated, "d1", map[string]any{"doc_id": "d1"})

	// 纯用户链路 → user/manual/根。
	s.EmitChange(WithUserActor(context.Background()), TypeDocUpdated, "", nil)
	// 纯后台 → assistant/batch/根。
	s.EmitChange(context.Background(), TypeMemoryTopicCleaned, "", nil)
	s.Flush()

	var tool struct {
		Actor   string  `gorm:"column:actor"`
		Trigger string  `gorm:"column:trigger"`
		Parent  *string `gorm:"column:parent_event_id"`
		Biz     *string `gorm:"column:biz_key"`
	}
	if err := s.DB().Raw(`SELECT actor, trigger, parent_event_id, biz_key FROM events WHERE type=?`, TypeDocCreated).Scan(&tool).Error; err != nil {
		t.Fatalf("scan tool: %v", err)
	}
	if tool.Actor != "assistant" || tool.Trigger != "manual" || tool.Parent == nil || *tool.Parent != "tc1" || tool.Biz == nil || *tool.Biz != "d1" {
		t.Errorf("tool row = %+v", tool)
	}

	var user struct{ Actor, Trigger string }
	if err := s.DB().Raw(`SELECT actor, trigger FROM events WHERE type=?`, TypeDocUpdated).Scan(&user).Error; err != nil {
		t.Fatalf("scan user: %v", err)
	}
	if user.Actor != "user" || user.Trigger != "manual" {
		t.Errorf("user row = %+v", user)
	}

	var bg struct{ Actor, Trigger string }
	if err := s.DB().Raw(`SELECT actor, trigger FROM events WHERE type=?`, TypeMemoryTopicCleaned).Scan(&bg).Error; err != nil {
		t.Fatalf("scan bg: %v", err)
	}
	if bg.Actor != "assistant" || bg.Trigger != "batch" {
		t.Errorf("bg row = %+v", bg)
	}
}

// 契约要求带 PK 的事件拿到空 pk 时应告警（missingPKAttr 命中）；无 PK 的合法事件不告警。
func TestMissingPKAttr(t *testing.T) {
	cases := []struct {
		typ, pk, wantKey string
		want             bool
	}{
		{TypeDocUpdated, "", "doc_id", true},
		{TypeDocDeleted, "", "doc_id", true},
		{TypeMemoryEntityChanged, "", "memory_id", true},
		{TypeReminderAcknowledged, "", "reminder_id", true},
		{TypeDocUpdated, "d1", "", false},
		{TypeMemoryTopicCleaned, "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		key, ok := missingPKAttr(c.typ, c.pk)
		if ok != c.want || key != c.wantKey {
			t.Errorf("missingPKAttr(%q,%q) = (%q,%v), want (%q,%v)", c.typ, c.pk, key, ok, c.wantKey, c.want)
		}
	}
}
