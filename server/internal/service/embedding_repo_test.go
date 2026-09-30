package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupEmbeddingRepo(t *testing.T) (*EmbeddingRepo, *gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}
	return NewEmbeddingRepo(db), db, cleanup
}

// TestDeleteByTypeBefore — 只删指定类型且早于 cutoff 的行，其它类型/近期行不受影响。
func TestDeleteByTypeBefore(t *testing.T) {
	repo, db, cleanup := setupEmbeddingRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-time.Hour)

	seed := func(id, typ string, at time.Time) {
		t.Helper()
		e := &model.Embedding{ID: id, EntityType: typ, EntityID: id, UserID: "u1", Model: "x", CreatedAt: at}
		if err := db.Create(e).Error; err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("m-old", model.EntityTypeMessage, old)
	seed("m-new", model.EntityTypeMessage, recent)
	seed("s-old", model.EntityTypeSummary, old)
	seed("n-old", model.EntityTypeNote, old)

	n, err := repo.DeleteByTypeBefore(ctx, model.EntityTypeMessage, now.Add(-14*24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteByTypeBefore error: %v", err)
	}
	if n != 1 {
		t.Errorf("应只删 1 行旧 message，got %d", n)
	}

	count := func(id string) int64 {
		var c int64
		db.Model(&model.Embedding{}).Where("id = ?", id).Count(&c)
		return c
	}
	if count("m-old") != 0 {
		t.Error("过期 message 行应被删除")
	}
	for _, id := range []string{"m-new", "s-old", "n-old"} {
		if count(id) != 1 {
			t.Errorf("%s 不应受影响", id)
		}
	}
}

// TestDeleteByTypeBefore_NoMatch — 无匹配返回 0，无错误。
func TestDeleteByTypeBefore_NoMatch(t *testing.T) {
	repo, _, cleanup := setupEmbeddingRepo(t)
	defer cleanup()
	n, err := repo.DeleteByTypeBefore(context.Background(), model.EntityTypeMessage, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("空库应返回 0, got %d", n)
	}
}
