package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupFactRepoForTest(t *testing.T) *FactRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.Fact{}, &model.FactChangelog{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return NewFactRepo(db)
}

func newFact(userID, content string) *model.Fact {
	return &model.Fact{
		ID:          uuid.New().String(),
		UserID:      userID,
		Subject:     "项目K",
		Kind:        model.FactKindState,
		Content:     content,
		ContentHash: "hash-" + content,
		Status:      model.FactStatusActive,
	}
}

func TestFactRepo_CreateAndFindByID(t *testing.T) {
	repo := setupFactRepoForTest(t)
	ctx := context.Background()

	f := newFact("u1", "本人兼产品与开发")
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.FindByID(ctx, "u1", f.ID)
	if err != nil || got == nil {
		t.Fatalf("find by id: got=%v err=%v", got, err)
	}
	if got.Content != "本人兼产品与开发" || got.Kind != model.FactKindState {
		t.Fatalf("unexpected fact: %+v", got)
	}

	// 他人不可见
	if got, _ := repo.FindByID(ctx, "u2", f.ID); got != nil {
		t.Fatalf("expected nil for other user, got %+v", got)
	}
	// 不存在 → (nil, nil)
	if got, err := repo.FindByID(ctx, "u1", uuid.New().String()); got != nil || err != nil {
		t.Fatalf("expected (nil,nil), got=%v err=%v", got, err)
	}
}

func TestFactRepo_FindByContentHash_ActiveOnly(t *testing.T) {
	repo := setupFactRepoForTest(t)
	ctx := context.Background()

	f := newFact("u1", "计划为家里还债")
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.FindByContentHash(ctx, "u1", f.ContentHash)
	if err != nil || got == nil {
		t.Fatalf("find by hash: got=%v err=%v", got, err)
	}
	// 软删后不可见
	if err := repo.SoftDelete(ctx, f.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	got, err = repo.FindByContentHash(ctx, "u1", f.ContentHash)
	if err != nil {
		t.Fatalf("find after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after soft delete, got %+v", got)
	}
}

func TestFactRepo_UpdateContent(t *testing.T) {
	repo := setupFactRepoForTest(t)
	ctx := context.Background()

	f := newFact("u1", "旧内容")
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.UpdateContent(ctx, f.ID, "新内容", "hash-新内容", "新主体", model.FactKindEvent); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := repo.FindByID(ctx, "u1", f.ID)
	if got == nil || got.Content != "新内容" || got.ContentHash != "hash-新内容" ||
		got.Subject != "新主体" || got.Kind != model.FactKindEvent {
		t.Fatalf("unexpected after update: %+v", got)
	}

	// 空 subject/kind 不改
	if err := repo.UpdateContent(ctx, f.ID, "新内容2", "hash-新内容2", "", ""); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	got, _ = repo.FindByID(ctx, "u1", f.ID)
	if got.Subject != "新主体" || got.Kind != model.FactKindEvent {
		t.Fatalf("empty fields should not overwrite: %+v", got)
	}
}

func TestFactRepo_SoftDeleteStillReadableByID(t *testing.T) {
	repo := setupFactRepoForTest(t)
	ctx := context.Background()

	f := newFact("u1", "要被删的事实")
	if err := repo.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.SoftDelete(ctx, f.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	got, err := repo.FindByID(ctx, "u1", f.ID)
	if err != nil || got == nil {
		t.Fatalf("FindByID should still read deleted row: got=%v err=%v", got, err)
	}
	if got.Status != model.FactStatusDeleted {
		t.Fatalf("expected deleted status, got %s", got.Status)
	}

	// 列表不含
	list, total, err := repo.ListByUser(ctx, "u1", 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("deleted fact should not list: total=%d len=%d", total, len(list))
	}
	all, err := repo.ListAllActivePaginated(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("deleted fact should not warmup-load: %d", len(all))
	}
}

func TestFactRepo_ListByUserPagination(t *testing.T) {
	repo := setupFactRepoForTest(t)
	ctx := context.Background()

	for _, c := range []string{"f1", "f2", "f3"} {
		if err := repo.Create(ctx, newFact("u1", c)); err != nil {
			t.Fatalf("create %s: %v", c, err)
		}
	}
	if err := repo.Create(ctx, newFact("u2", "other")); err != nil {
		t.Fatalf("create u2: %v", err)
	}

	list, total, err := repo.ListByUser(ctx, "u1", 2, 0)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total != 3 || len(list) != 2 {
		t.Fatalf("page 1: total=%d len=%d", total, len(list))
	}

	list, total, err = repo.ListByUser(ctx, "u1", 2, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if total != 3 || len(list) != 1 {
		t.Fatalf("page 2: total=%d len=%d", total, len(list))
	}
}
