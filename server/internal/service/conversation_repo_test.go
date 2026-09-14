package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
		&model.ConversationSummary{}, &model.ConversationPlan{}, &model.MessageShare{},
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

func TestSoftDelete_RejectsDefaultConversation(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-default", UserID: "u1", IsDefault: true}); err != nil {
		t.Fatalf("create default: %v", err)
	}

	err := repo.SoftDelete(ctx, "conv-default")
	if !errors.Is(err, ErrDefaultConversation) {
		t.Fatalf("expected ErrDefaultConversation, got %v", err)
	}

	// 默认会话应仍然可用（未被软删）
	conv, err := repo.FindByID(ctx, "conv-default")
	if err != nil {
		t.Fatalf("FindByID error: %v", err)
	}
	if conv == nil || conv.IsDeleted {
		t.Fatalf("default conversation should not be deleted, got %+v", conv)
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

func TestListByUser_DefaultFirst(t *testing.T) {
	repo, cleanup := setupConversationRepo(t)
	defer cleanup()

	ctx := context.Background()
	// 先建默认会话（updated_at 最早），再建 3 个普通会话
	if err := repo.Create(ctx, &model.Conversation{ID: "conv-default", UserID: "u1", IsDefault: true}); err != nil {
		t.Fatalf("create default: %v", err)
	}
	for _, id := range []string{"conv-a", "conv-b", "conv-c"} {
		if err := repo.Create(ctx, &model.Conversation{ID: id, UserID: "u1", IsDefault: false}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	list, total, err := repo.ListByUser(ctx, "u1", 10, 0)
	if err != nil {
		t.Fatalf("ListByUser error: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected total 4, got %d", total)
	}
	if len(list) != 4 {
		t.Fatalf("expected 4 conversations, got %d", len(list))
	}
	// 默认会话必须置顶
	if list[0].ID != "conv-default" {
		t.Fatalf("expected default conversation first, got %s", list[0].ID)
	}
	if !list[0].IsDefault {
		t.Fatalf("expected list[0] IsDefault=true, got false")
	}
	// 其余按 created_at（即 updated_at 顺序）倒序（从第 2 个普通会话开始比较，
	// list[0] 是默认会话必然最早，与它比较无意义）
	for i := 2; i < len(list); i++ {
		if list[i].IsDefault {
			t.Errorf("only first item should be default, got index %d", i)
		}
		if list[i].CreatedAt.After(list[i-1].CreatedAt) {
			t.Errorf("expected DESC order, got[%d]=%v after got[%d]=%v",
				i, list[i].CreatedAt, i-1, list[i-1].CreatedAt)
		}
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
