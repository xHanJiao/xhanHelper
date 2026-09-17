package service

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupDocumentRepo(t *testing.T) (*DocumentRepo, *gorm.DB, func()) {
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
	return NewDocumentRepo(db), db, cleanup
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
	repo, _, cleanup := setupDocumentRepo(t)
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
	repo, _, cleanup := setupDocumentRepo(t)
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

func TestDocumentRepo_DeleteReturnsRowsAffected(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()
	ctx := context.Background()

	seedDoc(t, repo, "d1", "u1", "")
	rows, err := repo.Delete(ctx, "d1", "u1")
	if err != nil || rows != 1 {
		t.Fatalf("delete existing: rows=%d err=%v, want 1/nil", rows, err)
	}
	rows, err = repo.Delete(ctx, "d1", "u1")
	if err != nil || rows != 0 {
		t.Errorf("delete missing: rows=%d err=%v, want 0/nil", rows, err)
	}
	// 别人的文档删不动，也不该报成功
	seedDoc(t, repo, "d2", "u1", "")
	if rows, _ = repo.Delete(ctx, "d2", "u2"); rows != 0 {
		t.Errorf("delete other user's doc: rows=%d, want 0", rows)
	}
}

func TestDocumentRepo_ListLimitZero(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()

	seedDoc(t, repo, "d1", "u1", "")
	seedDoc(t, repo, "d2", "u1", "")

	list, total, err := repo.List(context.Background(), "u1", "", "", 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("limit=0 should return no docs, got %v", ids(list))
	}
	if total != 2 {
		t.Errorf("limit=0 should still count, got total=%d", total)
	}
}

func TestDocumentRepo_ListStableOrderOnTie(t *testing.T) {
	repo, db, cleanup := setupDocumentRepo(t)
	defer cleanup()

	// 三篇文档 updated_at 完全相同，只有 id 不同
	for _, id := range []string{"0003", "0001", "0002"} {
		seedDoc(t, repo, id, "u1", "")
	}
	if err := db.Exec("UPDATE documents SET updated_at = ?", "2026-01-01 00:00:00").Error; err != nil {
		t.Fatalf("force tie: %v", err)
	}

	full, total, err := repo.List(context.Background(), "u1", "", "", 10, 0)
	if err != nil || total != 3 {
		t.Fatalf("full list: total=%d err=%v", total, err)
	}
	// tie-break 固定为 id 降序
	if got := ids(full); got[0] != "0003" || got[1] != "0002" || got[2] != "0001" {
		t.Errorf("tie order should be id DESC, got %v", got)
	}

	// 分页拼接必须与整表顺序一致（不同 limit 不能改变并列项顺序）
	page1, _, _ := repo.List(context.Background(), "u1", "", "", 2, 0)
	page2, _, _ := repo.List(context.Background(), "u1", "", "", 2, 2)
	one, _, _ := repo.List(context.Background(), "u1", "", "", 1, 0)
	if got := append(ids(page1), ids(page2)...); len(got) != 3 || got[0] != "0003" || got[1] != "0002" || got[2] != "0001" {
		t.Errorf("paged ids should match full order, got %v (page1=%v page2=%v)", got, ids(page1), ids(page2))
	}
	if ids(one)[0] != "0003" {
		t.Errorf("limit=1 first item should match full order first item, got %v", ids(one))
	}
}

func TestDocumentRepo_ListTagCountsFullwidthComma(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()

	// 存量可能带全角逗号，读取侧也要能拆开
	seedDoc(t, repo, "d1", "u1", "回归R，回归R")
	counts, err := repo.ListTagCounts(context.Background(), "u1")
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	got := map[string]int{}
	for _, tc := range counts {
		got[tc.Name] = tc.Count
	}
	if len(counts) != 1 || got["回归R"] != 2 {
		t.Errorf("fullwidth comma should split into 1 tag x2, got %v", counts)
	}
}

func TestNormalizeTags(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"Go", "Go"},
		{"Go, 后端", "Go,后端"},
		{"a， a , b,", "a,b"},
		{"技术,Go,后端,技术", "技术,Go,后端"},
		{"，，", ""},
	} {
		if got := NormalizeTags(tc.in); got != tc.want {
			t.Errorf("NormalizeTags(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
