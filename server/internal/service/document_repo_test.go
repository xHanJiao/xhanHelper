package service

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupDocumentRepo(t *testing.T) (*DocumentRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Document{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}
	return NewDocumentRepo(db), cleanup
}

func seedDoc(t *testing.T, repo *DocumentRepo, id, userID, tags string) {
	t.Helper()
	if err := repo.Create(context.Background(), &model.Document{
		ID: id, UserID: userID, Title: id, Content: "c", Tags: tags,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestDocumentRepo_ListTagCounts(t *testing.T) {
	repo, cleanup := setupDocumentRepo(t)
	defer cleanup()

	seedDoc(t, repo, "d1", "u1", "Go,Web")
	seedDoc(t, repo, "d2", "u1", "Go, 后端")
	seedDoc(t, repo, "d3", "u1", "Go")
	seedDoc(t, repo, "d4", "u1", "")
	seedDoc(t, repo, "d5", "u2", "Go")

	counts, err := repo.ListTagCounts(context.Background(), "u1")
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	got := map[string]int{}
	for _, tc := range counts {
		got[tc.Name] = tc.Count
	}
	if got["Go"] != 3 {
		t.Errorf("Go count: got %d, want 3", got["Go"])
	}
	if got["Web"] != 1 || got["后端"] != 1 {
		t.Errorf("minor tags: got %v", got)
	}
	if len(counts) != 3 {
		t.Errorf("tag kinds: got %d, want 3", len(counts))
	}
	if counts[0].Name != "Go" {
		t.Errorf("first should be most frequent Go, got %q", counts[0].Name)
	}
}

func TestDocumentRepo_List_TagBoundary(t *testing.T) {
	repo, cleanup := setupDocumentRepo(t)
	defer cleanup()

	seedDoc(t, repo, "d1", "u1", "Go,Web")
	seedDoc(t, repo, "d2", "u1", "Golang")
	seedDoc(t, repo, "d3", "u1", "Django")
	seedDoc(t, repo, "d4", "u1", "a, 后端, b")

	list, total, err := repo.List(context.Background(), "u1", "Go", "", 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != "d1" {
		t.Errorf("Go should match only d1, got total=%d ids=%v", total, ids(list))
	}

	list, total, _ = repo.List(context.Background(), "u1", "后端", "", 10, 0)
	if total != 1 || len(list) != 1 || list[0].ID != "d4" {
		t.Errorf("spaced tag should match d4, got total=%d ids=%v", total, ids(list))
	}

	for _, tag := range []string{"Golang", "Django"} {
		_, total, _ := repo.List(context.Background(), "u1", tag, "", 10, 0)
		if total != 1 {
			t.Errorf("%s: got total=%d, want 1", tag, total)
		}
	}
}

func ids(list []model.Document) []string {
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.ID
	}
	return out
}
