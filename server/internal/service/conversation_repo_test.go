package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// setupPurgeRepo 建一个包含无痕销毁所需全部表的临时库。
func setupPurgeRepo(t *testing.T) (*ConversationRepo, *gorm.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "purge.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.Message{}, &model.Embedding{},
		&model.ConversationSummary{}, &model.ConversationPlan{}, &model.MessageShare{}, &model.Artifact{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewConversationRepo(db), db, cleanup
}

func setupConversationRepo(t *testing.T) (*ConversationRepo, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewConversationRepo(db), cleanup
}

func TestSoftDelete_AllowsDefaultConversation(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-default", UserID: "u1", IsDefault: true}); err != nil {
		t.Fatalf("create default: %v", err)
	}

	if err := repo.SoftDelete(ctx, "conv-default"); err != nil {
		t.Fatalf("SoftDelete default: %v", err)
	}

	// 默认会话不再有特殊保护，应可被软删
	conv, err := repo.FindByID(ctx, "conv-default")
	if err != nil {
		t.Fatalf("FindByID error: %v", err)
	}
	if conv == nil || !conv.IsDeleted {
		t.Fatalf("default conversation should be deletable, got %+v", conv)
	}
}

func TestSoftDelete_NormalConversation(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-normal", UserID: "u1", IsDefault: false}); err != nil {
		t.Fatalf("create normal: %v", err)
	}

	if err := repo.SoftDelete(ctx, "conv-normal"); err != nil {
		t.Fatalf("SoftDelete normal: %v", err)
	}

	conv, err := repo.FindByID(ctx, "conv-normal")
	if err != nil {
		t.Fatalf("FindByID error: %v", err)
	}
	if conv == nil || !conv.IsDeleted {
		t.Fatalf("normal conversation should be deleted, got %+v", conv)
	}
}

func TestListByUser_PinnedFirst(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()
	pinOld := now.Add(-time.Hour)
	pinNew := now

	// 已 pin：conv-a（较早 pin） / conv-default（较晚 pin，应排最前）
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-a", UserID: "u1", PinnedAt: &pinOld}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-default", UserID: "u1", IsDefault: true, PinnedAt: &pinNew}); err != nil {
		t.Fatalf("create default: %v", err)
	}
	// 未 pin：conv-b（较新） / conv-c（较旧）
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-b", UserID: "u1"}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-c", UserID: "u1"}); err != nil {
		t.Fatalf("create c: %v", err)
	}
	if err := repo.DB().Model(&model.Conversation{}).Where("id = ?", "conv-b").
		UpdateColumn("updated_at", now.Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("set updated_at b: %v", err)
	}
	if err := repo.DB().Model(&model.Conversation{}).Where("id = ?", "conv-c").
		UpdateColumn("updated_at", now.Add(-3*time.Hour)).Error; err != nil {
		t.Fatalf("set updated_at c: %v", err)
	}

	list, total, err := repo.ListByUser(ctx, "u1", 10, 0)
	if err != nil {
		t.Fatalf("ListByUser error: %v", err)
	}
	if total != 4 || len(list) != 4 {
		t.Fatalf("expected 4, got total=%d len=%d", total, len(list))
	}
	got := []string{list[0].ID, list[1].ID, list[2].ID, list[3].ID}
	want := []string{"conv-default", "conv-a", "conv-b", "conv-c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch: got %v want %v", got, want)
		}
	}
}

// 默认会话不再被特殊提升：未 pin 时按 updated_at 正常排序。
func TestListByUser_DefaultNotBoosted(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-default", UserID: "u1", IsDefault: true}); err != nil {
		t.Fatalf("create default: %v", err)
	}
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-new", UserID: "u1"}); err != nil {
		t.Fatalf("create new: %v", err)
	}
	now := time.Now()
	if err := repo.DB().Model(&model.Conversation{}).Where("id = ?", "conv-default").
		UpdateColumn("updated_at", now.Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("set updated_at default: %v", err)
	}
	if err := repo.DB().Model(&model.Conversation{}).Where("id = ?", "conv-new").
		UpdateColumn("updated_at", now.Add(-time.Hour)).Error; err != nil {
		t.Fatalf("set updated_at new: %v", err)
	}

	list, _, err := repo.ListByUser(ctx, "u1", 10, 0)
	if err != nil {
		t.Fatalf("ListByUser error: %v", err)
	}
	if len(list) != 2 || list[0].ID != "conv-new" || list[1].ID != "conv-default" {
		t.Fatalf("default should not be boosted, got order %+v", list)
	}
}

func TestSetPinned_TogglesWithoutTouchingUpdatedAt(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-1", UserID: "u1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	before, err := repo.FindByID(ctx, "conv-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	if err := repo.SetPinned(ctx, "conv-1", true); err != nil {
		t.Fatalf("SetPinned(true): %v", err)
	}
	pinned, _ := repo.FindByID(ctx, "conv-1")
	if pinned.PinnedAt == nil {
		t.Fatal("expected pinned_at set after pin")
	}
	if !pinned.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("pin should not touch updated_at: before=%v after=%v", before.UpdatedAt, pinned.UpdatedAt)
	}

	if err := repo.SetPinned(ctx, "conv-1", false); err != nil {
		t.Fatalf("SetPinned(false): %v", err)
	}
	unpinned, _ := repo.FindByID(ctx, "conv-1")
	if unpinned.PinnedAt != nil {
		t.Fatal("expected pinned_at cleared after unpin")
	}
	if !unpinned.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("unpin should not touch updated_at: before=%v after=%v", before.UpdatedAt, unpinned.UpdatedAt)
	}
}

func TestListByUser_ExcludesSubConversations(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	parent := "main-1"
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-main", UserID: "u1"}); err != nil {
		t.Fatalf("create main: %v", err)
	}
	// 子会话（subAgent）：parent_id 指向主会话，Extra 带 SubAgentType
	extraJSON, _ := json.Marshal(&model.ConversationExtra{SubAgentType: "web_explore"})
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-sub", UserID: "u1", ParentID: &parent, Extra: string(extraJSON)}); err != nil {
		t.Fatalf("create sub: %v", err)
	}

	list, total, err := repo.ListByUser(ctx, "u1", 10, 0)
	if err != nil {
		t.Fatalf("ListByUser error: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != "conv-main" {
		t.Fatalf("子会话不应出现在用户列表: total=%d list=%+v", total, list)
	}
}

func TestUpdateExtraField_MergesWithoutOverwrite(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	// 已有 extra 含 used_tool_ids
	extraJSON, _ := json.Marshal(&model.ConversationExtra{UsedToolIDs: []string{"tool-1"}})
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-extra", UserID: "u1", Extra: string(extraJSON)}); err != nil {
		t.Fatalf("create conv: %v", err)
	}

	// 只改 ToolExposure，其它字段必须保留
	err := repo.UpdateExtraField(ctx, "conv-extra", func(extra *model.ConversationExtra) {
		extra.ToolExposure = &model.ToolExposure{SessionStartMsgID: "w1", Tools: []string{"note"}}
	})
	if err != nil {
		t.Fatalf("UpdateExtraField error: %v", err)
	}

	got, err := repo.GetExtra(ctx, "conv-extra")
	if err != nil {
		t.Fatalf("GetExtra error: %v", err)
	}
	if len(got.UsedToolIDs) != 1 || got.UsedToolIDs[0] != "tool-1" {
		t.Errorf("used_tool_ids must be preserved, got %v", got.UsedToolIDs)
	}
	if got.ToolExposure == nil || got.ToolExposure.SessionStartMsgID != "w1" || len(got.ToolExposure.Tools) != 1 {
		t.Errorf("ToolExposure not written correctly: %+v", got.ToolExposure)
	}
}

func TestUpdateExtraField_OnEmptyExtra(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	// 原始 extra 为 ""（零值）
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-empty", UserID: "u1"}); err != nil {
		t.Fatalf("create conv: %v", err)
	}

	err := repo.UpdateExtraField(ctx, "conv-empty", func(extra *model.ConversationExtra) {
		extra.ToolExposure = &model.ToolExposure{SessionStartMsgID: "w2", Tools: []string{"bookkeeping"}}
	})
	if err != nil {
		t.Fatalf("UpdateExtraField error: %v", err)
	}

	got, err := repo.GetExtra(ctx, "conv-empty")
	if err != nil {
		t.Fatalf("GetExtra error: %v", err)
	}
	if got.ToolExposure == nil || got.ToolExposure.SessionStartMsgID != "w2" {
		t.Errorf("ToolExposure not written: %+v", got.ToolExposure)
	}
}

func TestUpdateExtraField_NonexistentConversation(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	err := repo.UpdateExtraField(ctx, "no-such-conv", func(extra *model.ConversationExtra) {})
	if err == nil {
		t.Error("expected error for nonexistent conversation")
	}
}

func TestListSubConversations(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
		os.RemoveAll(tmpDir)
	}()
	repo := NewConversationRepo(db)
	ctx := context.Background()

	parentID := "main-conv"
	for i := 1; i <= 3; i++ {
		if err := repo.Create(ctx, &model.Conversation{ID: "sub-" + string(rune('0'+i)), UserID: "u1", Title: "调研", ParentID: &parentID}); err != nil {
			t.Fatalf("create sub: %v", err)
		}
	}
	// 普通会话（不应出现）
	if err := repo.Create(ctx, &model.Conversation{ID: "normal", UserID: "u1"}); err != nil {
		t.Fatalf("create normal: %v", err)
	}
	// sub-1 带 2 条消息
	for _, c := range []string{"m1", "m2"} {
		if err := db.Create(&model.Message{ID: c, ConversationID: "sub-1", UserID: "u1", Role: "user", Content: "x"}).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	// 第一页 limit=2 → hasMore=true，倒序：sub-3, sub-2
	rows, hasMore, err := repo.ListSubConversations(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListSubConversations: %v", err)
	}
	if !hasMore || len(rows) != 2 {
		t.Fatalf("page1: want hasMore=true, 2 rows; got hasMore=%v rows=%d", hasMore, len(rows))
	}
	if rows[0].ID != "sub-3" || rows[1].ID != "sub-2" {
		t.Fatalf("page1 order wrong: %s, %s", rows[0].ID, rows[1].ID)
	}
	if rows[1].MsgCount != 0 {
		t.Fatalf("sub-2 msg_count should be 0, got %d", rows[1].MsgCount)
	}

	// 第二页 offset=2 → 剩余 1 条，hasMore=false
	rows, hasMore, err = repo.ListSubConversations(ctx, 2, 2)
	if err != nil {
		t.Fatalf("ListSubConversations page2: %v", err)
	}
	if hasMore || len(rows) != 1 {
		t.Fatalf("page2: want hasMore=false, 1 row; got hasMore=%v rows=%d", hasMore, len(rows))
	}
	if rows[0].ID != "sub-1" || rows[0].MsgCount != 2 {
		t.Fatalf("sub-1 wrong: id=%s msg_count=%d", rows[0].ID, rows[0].MsgCount)
	}
}

// ---- 无痕会话销毁 ----

func TestIsEphemeral(t *testing.T) {
	repo, db, cleanup := setupPurgeRepo(t)
	defer cleanup()
	ctx := context.Background()

	extra, _ := json.Marshal(&model.ConversationExtra{Ephemeral: true})
	db.Create(&model.Conversation{ID: "c-eph", UserID: "u1", Extra: string(extra)})
	db.Create(&model.Conversation{ID: "c-normal", UserID: "u1"})

	if got, err := repo.IsEphemeral(ctx, "c-eph"); err != nil || !got {
		t.Fatalf("expected ephemeral=true, got %v err=%v", got, err)
	}
	if got, err := repo.IsEphemeral(ctx, "c-normal"); err != nil || got {
		t.Fatalf("expected ephemeral=false, got %v err=%v", got, err)
	}
	if got, err := repo.IsEphemeral(ctx, "missing"); err != nil || got {
		t.Fatalf("expected false for missing conv, got %v err=%v", got, err)
	}
}

func TestBeginPurge_HidesAndCleansSideTables(t *testing.T) {
	repo, db, cleanup := setupPurgeRepo(t)
	defer cleanup()
	ctx := context.Background()

	parent := "conv-main"
	db.Create(&model.Conversation{ID: "conv-main", UserID: "u1"})
	db.Create(&model.Conversation{ID: "conv-sub", UserID: "u1", ParentID: &parent})
	db.Create(&model.Conversation{ID: "conv-other", UserID: "u1"})

	db.Create(&model.Message{ID: "m1", ConversationID: "conv-main", UserID: "u1", Role: "user", Content: "a", Status: 1})
	db.Create(&model.Message{ID: "m2", ConversationID: "conv-sub", UserID: "u1", Role: "user", Content: "b", Status: 1})
	db.Create(&model.Message{ID: "m3", ConversationID: "conv-other", UserID: "u1", Role: "user", Content: "c", Status: 1})

	db.Create(&model.ConversationSummary{ID: "s1", ConversationID: "conv-main", UserID: "u1", SummaryText: "x", Status: "active"})
	db.Create(&model.ConversationPlan{ID: "p1", UserID: "u1", ConversationID: "conv-main", Goal: "g"})
	db.Create(&model.MessageShare{ID: "sh1", Token: "t1", ConversationID: "conv-main", MessageIDs: "[]", CreatedBy: "u1"})

	scope, found, err := repo.BeginPurge(ctx, "conv-main")
	if err != nil || !found {
		t.Fatalf("BeginPurge: found=%v err=%v", found, err)
	}
	if len(scope) != 2 {
		t.Fatalf("expected scope of 2 (main+sub), got %v", scope)
	}

	var conv model.Conversation
	db.First(&conv, "id = ?", "conv-main")
	if !conv.IsDeleted {
		t.Error("main conv should be soft-deleted")
	}
	var m model.Message
	db.First(&m, "id = ?", "m1")
	if m.Status != model.MsgStatusDeleted {
		t.Errorf("m1 should be status=2, got %d", m.Status)
	}

	var n int64
	db.Model(&model.ConversationSummary{}).Where("conversation_id = ?", "conv-main").Count(&n)
	if n != 0 {
		t.Error("summary not deleted")
	}
	db.Model(&model.ConversationPlan{}).Where("conversation_id = ?", "conv-main").Count(&n)
	if n != 0 {
		t.Error("plan not deleted")
	}
	db.Model(&model.MessageShare{}).Where("conversation_id = ?", "conv-main").Count(&n)
	if n != 0 {
		t.Error("share not deleted")
	}

	// 无关会话不受影响
	var otherConv model.Conversation
	db.Where("id = ?", "conv-other").First(&otherConv)
	if otherConv.IsDeleted {
		t.Error("unrelated conv should not be deleted")
	}
	var otherMsg model.Message
	db.Where("id = ?", "m3").First(&otherMsg)
	if otherMsg.Status != 1 {
		t.Error("unrelated msg should not be touched")
	}
}

func TestBeginPurge_Nonexistent_NoOp(t *testing.T) {
	repo, _, cleanup := setupPurgeRepo(t)
	defer cleanup()
	scope, found, err := repo.BeginPurge(context.Background(), "nope")
	if err != nil || found || scope != nil {
		t.Fatalf("expected no-op, got scope=%v found=%v err=%v", scope, found, err)
	}
}

func TestPurgeMessageBatch_BatchedAndIdempotent(t *testing.T) {
	repo, db, cleanup := setupPurgeRepo(t)
	defer cleanup()
	ctx := context.Background()
	db.Create(&model.Conversation{ID: "conv-main", UserID: "u1"})
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("m%d", i)
		db.Create(&model.Message{ID: id, ConversationID: "conv-main", UserID: "u1", Role: "user", Content: "x", Status: model.MsgStatusDeleted})
		db.Create(&model.Embedding{ID: id, EntityType: model.EntityTypeMessage, EntityID: id, UserID: "u1", Model: "test"})
	}

	scope := []string{"conv-main"}
	total := 0
	for {
		n, err := repo.PurgeMessageBatch(ctx, scope, 2)
		if err != nil {
			t.Fatalf("batch err: %v", err)
		}
		total += n
		if n == 0 {
			break
		}
	}
	if total != 5 {
		t.Fatalf("expected 5 messages deleted, got %d", total)
	}
	var n int64
	db.Model(&model.Message{}).Where("conversation_id = ?", "conv-main").Count(&n)
	if n != 0 {
		t.Errorf("messages remain: %d", n)
	}
	db.Model(&model.Embedding{}).Where("entity_type = ?", model.EntityTypeMessage).Count(&n)
	if n != 0 {
		t.Errorf("embeddings remain: %d", n)
	}
}

func TestFinishPurge_HardDeletesConversation(t *testing.T) {
	repo, db, cleanup := setupPurgeRepo(t)
	defer cleanup()
	ctx := context.Background()
	db.Create(&model.Conversation{ID: "conv-main", UserID: "u1"})

	if err := repo.FinishPurge(ctx, []string{"conv-main"}); err != nil {
		t.Fatalf("FinishPurge: %v", err)
	}
	var n int64
	db.Model(&model.Conversation{}).Where("id = ?", "conv-main").Count(&n)
	if n != 0 {
		t.Errorf("conversation should be hard-deleted, count=%d", n)
	}
}

func TestArchiveStale_OnlyStaleNonDefaultNonPinned(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)

	pinOld := now.Add(-time.Hour)
	mk := func(id string, updated time.Time, isDefault bool, pinned *time.Time) {
		t.Helper()
		if err := repo.Create(ctx, &model.Conversation{ID: id, UserID: "u1", IsDefault: isDefault, PinnedAt: pinned}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if err := repo.DB().Model(&model.Conversation{}).Where("id = ?", id).
			UpdateColumn("updated_at", updated).Error; err != nil {
			t.Fatalf("set updated_at %s: %v", id, err)
		}
	}
	mk("stale", old, false, nil)
	mk("fresh", now, false, nil)
	mk("default-old", old, true, nil)
	mk("pinned-old", old, false, &pinOld)

	cutoff := now.AddDate(0, 0, -14)
	n, err := repo.ArchiveStale(ctx, cutoff)
	if err != nil {
		t.Fatalf("ArchiveStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 archived, got %d", n)
	}
	for _, id := range []string{"stale"} {
		c, _ := repo.FindByID(ctx, id)
		if c == nil || !c.IsDeleted {
			t.Errorf("%s should be archived", id)
		}
	}
	for _, id := range []string{"fresh", "default-old", "pinned-old"} {
		c, _ := repo.FindByID(ctx, id)
		if c == nil || c.IsDeleted {
			t.Errorf("%s should NOT be archived", id)
		}
	}
}

func TestRestore_ReappearsInActiveList(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()
	ctx := context.Background()
	if err := repo.Create(ctx, &model.Conversation{ID: "c1", UserID: "u1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.SoftDelete(ctx, "c1"); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	archived, _, err := repo.ListArchivedByUser(ctx, "u1", 10, 0)
	if err != nil || len(archived) != 1 {
		t.Fatalf("expected 1 archived, got %d err=%v", len(archived), err)
	}

	if err := repo.Restore(ctx, "c1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	active, _, err := repo.ListByUser(ctx, "u1", 10, 0)
	if err != nil || len(active) != 1 || active[0].ID != "c1" {
		t.Fatalf("restored conv should be active, got %+v err=%v", active, err)
	}
	archived, _, err = repo.ListArchivedByUser(ctx, "u1", 10, 0)
	if err != nil || len(archived) != 0 {
		t.Fatalf("archived list should be empty after restore, got %d err=%v", len(archived), err)
	}
}
