package service

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupVersionRepo(t *testing.T) (*VersionRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.ContentVersion{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return NewVersionRepo(db), func() {}
}

type testDoc struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Tags    string `json:"tags"`
}

func TestCreateSnapshot(t *testing.T) {
	repo, cleanup := setupVersionRepo(t)
	defer cleanup()
	ctx := context.Background()

	err := repo.CreateSnapshot(ctx, "document", "doc-1", testDoc{
		Title: "My Doc", Content: "hello world", Tags: "go,tech",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	versions, err := repo.GetVersions(ctx, "document", "doc-1")
	if err != nil {
		t.Fatalf("GetVersions failed: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].Version != 1 {
		t.Errorf("expected version 1, got %d", versions[0].Version)
	}
}

func TestVersionIncrement(t *testing.T) {
	repo, cleanup := setupVersionRepo(t)
	defer cleanup()
	ctx := context.Background()

	repo.CreateSnapshot(ctx, "document", "doc-1", testDoc{Title: "v1", Content: "a"})
	repo.CreateSnapshot(ctx, "document", "doc-1", testDoc{Title: "v2", Content: "b"})
	repo.CreateSnapshot(ctx, "document", "doc-1", testDoc{Title: "v3", Content: "c"})

	versions, _ := repo.GetVersions(ctx, "document", "doc-1")
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	// 按 version DESC 排序
	if versions[0].Version != 3 {
		t.Errorf("expected version 3 first, got %d", versions[0].Version)
	}
	if versions[2].Version != 1 {
		t.Errorf("expected version 1 last, got %d", versions[2].Version)
	}
}

func TestGetSnapshot(t *testing.T) {
	repo, cleanup := setupVersionRepo(t)
	defer cleanup()
	ctx := context.Background()

	repo.CreateSnapshot(ctx, "note", "note-1", map[string]interface{}{
		"topic": "健康", "content": "今天跑步5公里",
	})

	snap, err := repo.GetSnapshot(ctx, "note", "note-1", 1)
	if err != nil {
		t.Fatalf("GetSnapshot failed: %v", err)
	}
	if snap.Snapshot != `{"content":"今天跑步5公里","topic":"健康"}` {
		t.Errorf("unexpected snapshot: %s", snap.Snapshot)
	}
}

func TestEntityIsolation(t *testing.T) {
	repo, cleanup := setupVersionRepo(t)
	defer cleanup()
	ctx := context.Background()

	repo.CreateSnapshot(ctx, "document", "doc-1", testDoc{Title: "Doc1"})
	repo.CreateSnapshot(ctx, "document", "doc-2", testDoc{Title: "Doc2"})
	repo.CreateSnapshot(ctx, "note", "note-1", map[string]string{"topic": "Note1"})

	// doc-1 有 1 个版本
	v1, _ := repo.GetVersions(ctx, "document", "doc-1")
	if len(v1) != 1 {
		t.Errorf("doc-1: expected 1 version, got %d", len(v1))
	}

	// doc-2 有 1 个版本
	v2, _ := repo.GetVersions(ctx, "document", "doc-2")
	if len(v2) != 1 {
		t.Errorf("doc-2: expected 1 version, got %d", len(v2))
	}

	// note-1 有 1 个版本
	v3, _ := repo.GetVersions(ctx, "note", "note-1")
	if len(v3) != 1 {
		t.Errorf("note-1: expected 1 version, got %d", len(v3))
	}
}

func TestDeleteByEntity(t *testing.T) {
	repo, cleanup := setupVersionRepo(t)
	defer cleanup()
	ctx := context.Background()

	repo.CreateSnapshot(ctx, "document", "doc-1", testDoc{Title: "v1"})
	repo.CreateSnapshot(ctx, "document", "doc-1", testDoc{Title: "v2"})
	repo.CreateSnapshot(ctx, "document", "doc-2", testDoc{Title: "other"})

	if err := repo.DeleteByEntity(ctx, "document", "doc-1"); err != nil {
		t.Fatalf("DeleteByEntity failed: %v", err)
	}

	// doc-1 版本全部删除
	v1, _ := repo.GetVersions(ctx, "document", "doc-1")
	if len(v1) != 0 {
		t.Errorf("doc-1: expected 0 versions after delete, got %d", len(v1))
	}

	// doc-2 不受影响
	v2, _ := repo.GetVersions(ctx, "document", "doc-2")
	if len(v2) != 1 {
		t.Errorf("doc-2: expected 1 version, got %d", len(v2))
	}
}

func TestEmptyVersions(t *testing.T) {
	repo, cleanup := setupVersionRepo(t)
	defer cleanup()
	ctx := context.Background()

	versions, err := repo.GetVersions(ctx, "document", "nonexistent")
	if err != nil {
		t.Fatalf("GetVersions failed: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("expected 0 versions for nonexistent entity, got %d", len(versions))
	}
}
