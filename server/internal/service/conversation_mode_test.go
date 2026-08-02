package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// newConvModeTestDB 内存 SQLite + conversations 表迁移
func newConvModeTestDB(t *testing.T) (*ConversationRepo, func()) {
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

func TestConversationRepo_SetMode(t *testing.T) {
	repo, cleanup := newConvModeTestDB(t)
	defer cleanup()
	ctx := context.Background()

	if err := repo.Create(ctx, &model.Conversation{ID: "c1", UserID: "u1", Title: "t"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 默认 work
	conv, err := repo.FindByID(ctx, "c1")
	if err != nil || conv == nil {
		t.Fatalf("find: %v", err)
	}
	if conv.Mode != model.ConvModeWork {
		t.Errorf("默认模式应为 work, got %q", conv.Mode)
	}

	// 切 plan
	if err := repo.SetMode(ctx, "c1", model.ConvModePlan); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	conv, _ = repo.FindByID(ctx, "c1")
	if conv.Mode != model.ConvModePlan {
		t.Errorf("切换后应为 plan, got %q", conv.Mode)
	}

	// 切回 work
	if err := repo.SetMode(ctx, "c1", model.ConvModeWork); err != nil {
		t.Fatalf("set work: %v", err)
	}
	conv, _ = repo.FindByID(ctx, "c1")
	if conv.Mode != model.ConvModeWork {
		t.Errorf("切回后应为 work, got %q", conv.Mode)
	}
}
