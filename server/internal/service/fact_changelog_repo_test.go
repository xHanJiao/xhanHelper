package service

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
)

func setupFactChangelogRepo(t *testing.T) *FactChangelogRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.FactChangelog{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return NewFactChangelogRepo(db)
}

func TestFactChangelogRepo_CreateAndList(t *testing.T) {
	repo := setupFactChangelogRepo(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		c := &model.FactChangelog{
			ID:          idgen.Next(),
			UserID:      "u1",
			FactID:      "fact-1",
			Op:          model.FactOpUpdate,
			OldContent:  "old",
			NewContent:  "new",
			SourceMsgID: "msg-1",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	// 另一事实的变更不应混入
	if err := repo.Create(ctx, &model.FactChangelog{
		ID: idgen.Next(), UserID: "u1", FactID: "fact-2", Op: model.FactOpDelete, OldContent: "gone",
	}); err != nil {
		t.Fatalf("create other: %v", err)
	}

	list, total, err := repo.ListByFact(ctx, "u1", "fact-1", 2, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(list) != 2 {
		t.Fatalf("page 1: total=%d len=%d", total, len(list))
	}

	list, total, err = repo.ListByFact(ctx, "u1", "fact-1", 2, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if total != 3 || len(list) != 1 {
		t.Fatalf("page 2: total=%d len=%d", total, len(list))
	}
}
