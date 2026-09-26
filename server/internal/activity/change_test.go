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
