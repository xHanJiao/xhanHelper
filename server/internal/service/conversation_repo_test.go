package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

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
	extraJSON, _ := json.Marshal(&model.ConversationExtra{SubAgentType: "explore"})
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
