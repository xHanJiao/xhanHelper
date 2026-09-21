package service

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

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
	if err := db.AutoMigrate(&model.Document{}, &model.DocumentAnnotation{}, &model.DocumentShare{}); err != nil {
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

	list, total, err := repo.List(context.Background(), "u1", "Go", "", nil, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != "d1" {
		t.Errorf("Go should match only d1, got total=%d ids=%v", total, ids(list))
	}

	list, total, _ = repo.List(context.Background(), "u1", "后端", "", nil, 10, 0)
	if total != 1 || len(list) != 1 || list[0].ID != "d4" {
		t.Errorf("spaced tag should match d4, got total=%d ids=%v", total, ids(list))
	}

	for _, tag := range []string{"Golang", "Django"} {
		_, total, _ := repo.List(context.Background(), "u1", tag, "", nil, 10, 0)
		if total != 1 {
			t.Errorf("%s: got total=%d, want 1", tag, total)
		}
	}
}

func TestDocumentRepo_ListByAllTagsIntersection(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()
	ctx := context.Background()

	seedDoc(t, repo, "d1", "u1", "A,B,C")
	seedDoc(t, repo, "d2", "u1", "A,C")
	seedDoc(t, repo, "d3", "u1", "A,B")
	seedDoc(t, repo, "d4", "u1", "A,D")
	seedDoc(t, repo, "d5", "u1", "E")

	// 交集：越选越少。{A}→d1..d4；{A,C}→d1,d2；{A,B,C}→d1；{A,C,D}→0
	for _, tc := range []struct {
		tags  []string
		total int64
		want  string
	}{
		{[]string{"A"}, 4, "d1,d2,d3,d4"},
		{[]string{"A", "C"}, 2, "d1,d2"},
		{[]string{"A", "B", "C"}, 1, "d1"},
		{[]string{"A", "C", "D"}, 0, ""},
	} {
		list, total, err := repo.ListByAllTags(ctx, "u1", tc.tags, "", nil, 10, 0)
		if err != nil {
			t.Fatalf("list %v: %v", tc.tags, err)
		}
		if total != tc.total {
			t.Errorf("tags=%v total: got %d, want %d", tc.tags, total, tc.total)
		}
		got := ids(list)
		sort.Strings(got)
		if strings.Join(got, ",") != tc.want {
			t.Errorf("tags=%v ids: got %v, want %q", tc.tags, got, tc.want)
		}
	}

	// 空 tags 等价于不过滤
	if _, total, _ := repo.ListByAllTags(ctx, "u1", nil, "", nil, 10, 0); total != 5 {
		t.Errorf("no tags total: got %d, want 5", total)
	}
}

func TestDocumentRepo_TagGraphEdges(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()

	// 需求原例：A/B/C、A/C、A/B、A/D
	seedDoc(t, repo, "d1", "u1", "A,B,C")
	seedDoc(t, repo, "d2", "u1", "A,C")
	seedDoc(t, repo, "d3", "u1", "A,B")
	seedDoc(t, repo, "d4", "u1", "A,D")

	_, edges, err := repo.TagGraph(context.Background(), "u1")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	got := map[string]int{}
	for _, e := range edges {
		got[e.A+","+e.B] = e.Count
	}
	for k, want := range map[string]int{"A,B": 2, "A,C": 2, "A,D": 1, "B,C": 1} {
		if got[k] != want {
			t.Errorf("edge %s: got %d, want %d", k, got[k], want)
		}
	}
	if len(edges) != 4 {
		t.Errorf("edge count: got %d, want 4 (%v)", len(edges), edges)
	}
}

func TestDocumentRepo_ListKeywordFields(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()
	ctx := context.Background()

	seed := func(id, title, content, tags string) {
		t.Helper()
		if err := repo.Create(ctx, &model.Document{ID: id, UserID: "u1", Title: title, Content: content, Tags: tags}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("t1", "alpha", "plain", "")
	seed("c1", "plain", "alpha", "")
	seed("g1", "plain", "plain", "Go")
	seed("g2", "plain", "plain", "Golang")
	seed("x1", "Go", "plain", "")

	cases := []struct {
		name    string
		keyword string
		fields  []string
		want    []string
	}{
		{"title only", "alpha", []string{"title"}, []string{"t1"}},
		{"content only", "alpha", []string{"content"}, []string{"c1"}},
		{"tag exact boundary", "Go", []string{"tag"}, []string{"g1"}},
		{"title+tag", "Go", []string{"title", "tag"}, []string{"g1", "x1"}},
		{"empty fields default title+content", "alpha", nil, []string{"t1", "c1"}},
	}
	for _, tc := range cases {
		list, total, err := repo.List(ctx, "u1", "", tc.keyword, tc.fields, 10, 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got, want := ids(list), append([]string(nil), tc.want...)
		sort.Strings(got)
		sort.Strings(want)
		if int(total) != len(want) || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got total=%d ids=%v, want %v", tc.name, total, got, want)
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

func TestDocumentRepo_SoftDeleteRestoreAndTrash(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()
	ctx := context.Background()

	seedDoc(t, repo, "d1", "u1", "Go")
	seedDoc(t, repo, "d2", "u1", "Go")

	if rows, err := repo.Delete(ctx, "d1", "u1"); err != nil || rows != 1 {
		t.Fatalf("soft delete: rows=%d err=%v, want 1/nil", rows, err)
	}

	// 已删文档从正常视图消失（单篇 / 列表 / 词云）
	if _, err := repo.GetByID(ctx, "d1", "u1"); err == nil {
		t.Error("GetByID should not see deleted doc")
	}
	list, total, _ := repo.List(ctx, "u1", "", "", nil, 10, 0)
	if total != 1 || len(list) != 1 || list[0].ID != "d2" {
		t.Errorf("active list should exclude deleted: total=%d ids=%v", total, ids(list))
	}
	counts, _ := repo.ListTagCounts(ctx, "u1")
	if len(counts) != 1 || counts[0].Count != 1 {
		t.Errorf("tag counts should exclude deleted, got %v", counts)
	}

	// 回收站可见，重复软删不再命中
	trash, trashTotal, err := repo.ListTrash(ctx, "u1", 10, 0)
	if err != nil || trashTotal != 1 || len(trash) != 1 || trash[0].ID != "d1" {
		t.Errorf("trash: total=%d ids=%v err=%v", trashTotal, ids(trash), err)
	}
	if rows, _ := repo.Delete(ctx, "d1", "u1"); rows != 0 {
		t.Errorf("re-delete: rows=%d, want 0", rows)
	}
	// 别人的回收站文档恢复不动
	if rows, _ := repo.Restore(ctx, "d1", "u2"); rows != 0 {
		t.Errorf("restore other user's doc: rows=%d, want 0", rows)
	}

	// 恢复后回到正常视图、离开回收站
	if rows, err := repo.Restore(ctx, "d1", "u1"); err != nil || rows != 1 {
		t.Fatalf("restore: rows=%d err=%v, want 1/nil", rows, err)
	}
	if _, err := repo.GetByID(ctx, "d1", "u1"); err != nil {
		t.Errorf("GetByID after restore: %v", err)
	}
	if _, trashTotal, _ := repo.ListTrash(ctx, "u1", 10, 0); trashTotal != 0 {
		t.Errorf("trash after restore: total=%d, want 0", trashTotal)
	}

	// 回收站按删除时间倒序：后删的在前（依赖软删 Update 刷新 updated_at）
	if _, err := repo.Delete(ctx, "d2", "u1"); err != nil {
		t.Fatalf("delete d2: %v", err)
	}
	if _, err := repo.Delete(ctx, "d1", "u1"); err != nil {
		t.Fatalf("delete d1: %v", err)
	}
	trash, _, _ = repo.ListTrash(ctx, "u1", 10, 0)
	if len(trash) != 2 || trash[0].ID != "d1" || trash[1].ID != "d2" {
		t.Errorf("trash should be ordered by deletion time desc, got %v", ids(trash))
	}
}

func TestDocumentRepo_RestoreAllAndPurgeAll(t *testing.T) {
	repo, db, cleanup := setupDocumentRepo(t)
	defer cleanup()
	ctx := context.Background()

	seedDoc(t, repo, "d1", "u1", "")
	seedDoc(t, repo, "d2", "u1", "")
	seedDoc(t, repo, "d3", "u1", "") // 未删除，不该受影响
	seedDoc(t, repo, "x1", "u2", "") // 别人的文档

	for _, id := range []string{"d1", "d2"} {
		if _, err := repo.Delete(ctx, id, "u1"); err != nil {
			t.Fatalf("delete %s: %v", id, err)
		}
	}
	if _, err := repo.Delete(ctx, "x1", "u2"); err != nil {
		t.Fatalf("delete x1: %v", err)
	}

	// 全部恢复：只动本用户回收站里的文档
	n, err := repo.RestoreAll(ctx, "u1")
	if err != nil || n != 2 {
		t.Fatalf("restore all: n=%d err=%v, want 2/nil", n, err)
	}
	if _, total, _ := repo.ListTrash(ctx, "u1", 10, 0); total != 0 {
		t.Errorf("trash after restore all: total=%d, want 0", total)
	}
	if _, err := repo.GetByID(ctx, "d1", "u1"); err != nil {
		t.Errorf("d1 should be active after restore all: %v", err)
	}
	// 别人的文档仍在回收站
	if _, total, _ := repo.ListTrash(ctx, "u2", 10, 0); total != 1 {
		t.Errorf("other user's trash should be untouched, total=%d", total)
	}

	// 清空回收站：只删本用户的
	if _, err := repo.Delete(ctx, "d1", "u1"); err != nil {
		t.Fatalf("re-delete d1: %v", err)
	}
	n, err = repo.PurgeAll(ctx, "u1")
	if err != nil || n != 1 {
		t.Fatalf("purge all: n=%d err=%v, want 1/nil", n, err)
	}
	var cnt int64
	db.Model(&model.Document{}).Where("id = ?", "d1").Count(&cnt)
	if cnt != 0 {
		t.Error("d1 should be physically gone after purge all")
	}
	db.Model(&model.Document{}).Where("id = ?", "d3").Count(&cnt)
	if cnt != 1 {
		t.Error("active doc d3 should survive purge all")
	}
	db.Model(&model.Document{}).Where("id = ?", "x1").Count(&cnt)
	if cnt != 1 {
		t.Error("other user's trashed doc should survive purge all")
	}
}

func TestDocumentRepo_PurgeExpired(t *testing.T) {
	repo, db, cleanup := setupDocumentRepo(t)
	defer cleanup()
	ctx := context.Background()

	seedDoc(t, repo, "old", "u1", "")
	seedDoc(t, repo, "fresh", "u1", "")
	seedDoc(t, repo, "live", "u1", "")

	if _, err := repo.Delete(ctx, "old", "u1"); err != nil {
		t.Fatalf("delete old: %v", err)
	}
	if _, err := repo.Delete(ctx, "fresh", "u1"); err != nil {
		t.Fatalf("delete fresh: %v", err)
	}
	// old 的删除时间（updated_at）推到 10 天前
	if err := db.Exec("UPDATE documents SET updated_at = ? WHERE id = ?", time.Now().AddDate(0, 0, -10), "old").Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// 批注 + 分享挂在 old 上，清理时一并物理删
	if err := db.Create(&model.DocumentAnnotation{ID: "a1", DocumentID: "old", UserID: "u1", Quote: "q"}).Error; err != nil {
		t.Fatalf("seed annotation: %v", err)
	}
	if err := db.Create(&model.DocumentShare{ID: "s1", DocumentID: "old", Token: "t1", ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatalf("seed share: %v", err)
	}

	n, err := repo.PurgeExpired(ctx, time.Now().AddDate(0, 0, -7))
	if err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v, want 1/nil", n, err)
	}

	for _, tc := range []struct {
		model interface{}
		where string
		arg   string
		want  int64
	}{
		{&model.Document{}, "id = ?", "old", 0},
		{&model.Document{}, "id = ?", "fresh", 1},
		{&model.Document{}, "id = ?", "live", 1},
		{&model.DocumentAnnotation{}, "document_id = ?", "old", 0},
		{&model.DocumentShare{}, "document_id = ?", "old", 0},
	} {
		var cnt int64
		if err := db.Model(tc.model).Where(tc.where, tc.arg).Count(&cnt).Error; err != nil {
			t.Fatalf("count %T %s: %v", tc.model, tc.arg, err)
		}
		if cnt != tc.want {
			t.Errorf("%T where %s=%s: got %d, want %d", tc.model, tc.where, tc.arg, cnt, tc.want)
		}
	}
}

func TestDocumentRepo_ListLimitZero(t *testing.T) {
	repo, _, cleanup := setupDocumentRepo(t)
	defer cleanup()

	seedDoc(t, repo, "d1", "u1", "")
	seedDoc(t, repo, "d2", "u1", "")

	list, total, err := repo.List(context.Background(), "u1", "", "", nil, 0, 0)
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

	full, total, err := repo.List(context.Background(), "u1", "", "", nil, 10, 0)
	if err != nil || total != 3 {
		t.Fatalf("full list: total=%d err=%v", total, err)
	}
	// tie-break 固定为 id 降序
	if got := ids(full); got[0] != "0003" || got[1] != "0002" || got[2] != "0001" {
		t.Errorf("tie order should be id DESC, got %v", got)
	}

	// 分页拼接必须与整表顺序一致（不同 limit 不能改变并列项顺序）
	page1, _, _ := repo.List(context.Background(), "u1", "", "", nil, 2, 0)
	page2, _, _ := repo.List(context.Background(), "u1", "", "", nil, 2, 2)
	one, _, _ := repo.List(context.Background(), "u1", "", "", nil, 1, 0)
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
