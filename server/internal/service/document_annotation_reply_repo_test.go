package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupReplyRepo(t *testing.T) (*DocumentAnnotationReplyRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.DocumentAnnotationReply{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}
	return NewDocumentAnnotationReplyRepo(db), cleanup
}

// 造一批回复：annotation a1 有 3 条（时间递增）、a2 有 1 条。
func seedReplies(t *testing.T, repo *DocumentAnnotationReplyRepo) {
	t.Helper()
	ctx := context.Background()
	mk := func(id, anno, role, content string) model.DocumentAnnotationReply {
		return model.DocumentAnnotationReply{ID: id, AnnotationID: anno, DocumentID: "doc1", UserID: "u1", Role: role, Content: content, Source: "button"}
	}
	// 逐条创建以拿到递增的 created_at（CreateBatch 同批时间相同，无法验排序）
	for _, r := range []model.DocumentAnnotationReply{
		mk("r1", "a1", "assistant", "one"),
		mk("r2", "a1", "user", "two"),
		mk("r3", "a1", "assistant", "three"),
		mk("r4", "a2", "assistant", "other"),
	} {
		rr := r
		if err := repo.CreateBatch(ctx, []model.DocumentAnnotationReply{rr}); err != nil {
			t.Fatalf("seed %s: %v", rr.ID, err)
		}
	}
}

func TestReplyRepoListAndCount(t *testing.T) {
	repo, cleanup := setupReplyRepo(t)
	defer cleanup()
	seedReplies(t, repo)
	ctx := context.Background()

	list, total, err := repo.ListByAnnotationID(ctx, "a1", "u1", 0, 0)
	if err != nil || total != 3 || len(list) != 0 {
		t.Fatalf("limit=0: total=%d len=%d err=%v", total, len(list), err)
	}

	list, total, err = repo.ListByAnnotationID(ctx, "a1", "u1", -1, 0)
	if err != nil || total != 3 || len(list) != 3 {
		t.Fatalf("list: total=%d len=%d err=%v", total, len(list), err)
	}
	if list[0].Content != "one" || list[2].Content != "three" {
		t.Fatalf("顺序应为时间正序，得到 %s..%s", list[0].Content, list[2].Content)
	}
	// 越权：别的用户看不到
	if _, total, _ := repo.ListByAnnotationID(ctx, "a1", "u2", -1, 0); total != 0 {
		t.Fatalf("cross-user total=%d, want 0", total)
	}

	counts, err := repo.CountsByAnnotationIDs(ctx, []string{"a1", "a2", "missing"}, "u1")
	if err != nil || counts["a1"] != 3 || counts["a2"] != 1 || counts["missing"] != 0 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}

	latest, err := repo.LatestByAnnotationIDs(ctx, []string{"a1", "a2"}, "u1")
	if err != nil || latest["a1"].Content != "three" || latest["a2"].Content != "other" {
		t.Fatalf("latest=%v err=%v", latest, err)
	}
}

func TestReplyRepoListRecentPerAnnotation(t *testing.T) {
	repo, cleanup := setupReplyRepo(t)
	defer cleanup()
	ctx := context.Background()
	// a1 造 5 条，取最近 2 条应为 r4/r5（时间正序）
	for i := 1; i <= 5; i++ {
		r := model.DocumentAnnotationReply{ID: fmt.Sprintf("a1r%d", i), AnnotationID: "a1", DocumentID: "doc1", UserID: "u1", Role: "assistant", Content: fmt.Sprintf("m%d", i)}
		if err := repo.CreateBatch(ctx, []model.DocumentAnnotationReply{r}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := repo.ListRecentByAnnotationIDs(ctx, []string{"a1"}, "u1", 2)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got["a1"]) != 2 || got["a1"][0].Content != "m4" || got["a1"][1].Content != "m5" {
		t.Fatalf("recent=%v, want [m4 m5]", got["a1"])
	}
}

func TestReplyRepoDelete(t *testing.T) {
	repo, cleanup := setupReplyRepo(t)
	defer cleanup()
	seedReplies(t, repo)
	ctx := context.Background()

	n, err := repo.DeleteByAnnotation(ctx, "a1", "u1")
	if err != nil || n != 3 {
		t.Fatalf("delete by annotation: n=%d err=%v", n, err)
	}
	if _, total, _ := repo.ListByAnnotationID(ctx, "a1", "u1", -1, 0); total != 0 {
		t.Fatalf("a1 应清空，剩 %d", total)
	}
	if _, total, _ := repo.ListByAnnotationID(ctx, "a2", "u1", -1, 0); total != 1 {
		t.Fatalf("a2 不应受影响")
	}

	if n, err := repo.DeleteByDocument(ctx, "doc1", "u1"); err != nil || n != 1 {
		t.Fatalf("delete by document: n=%d err=%v", n, err)
	}
}
