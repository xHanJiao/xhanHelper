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

func setupArtifactRepo(t *testing.T) (*ArtifactRepo, *gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "a.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Artifact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewArtifactRepo(db), db, func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}
}

// 列表按时间倒序、分页、只取五列（不带正文）、软删不出现、越权看不到。
func TestArtifactRepoListByUser(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Now().Add(-time.Hour)
	seed := []model.Artifact{
		{ID: "a1", UserID: "u1", ConversationID: "c1", Title: "老", Summary: "s1", HTML: "<html>老</html>", CreatedAt: base},
		{ID: "a2", UserID: "u1", ConversationID: "c1", Title: "新", Summary: "s2", HTML: "<html>新</html>", CreatedAt: base.Add(time.Minute)},
		{ID: "a3", UserID: "u2", ConversationID: "c2", Title: "别人的", Summary: "s3", HTML: "<html>x</html>", CreatedAt: base.Add(2 * time.Minute)},
		{ID: "a4", UserID: "u1", ConversationID: "c1", Title: "已删", Summary: "s4", HTML: "<html>y</html>", CreatedAt: base.Add(3 * time.Minute)},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}
	if err := db.Delete(&model.Artifact{ID: "a4"}).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	rows, total, err := repo.ListByUser(ctx, "u1", 2, 0)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if total != 2 {
		t.Fatalf("total 应为 2（软删与别人都不算），实际 %d", total)
	}
	if len(rows) != 2 || rows[0].ID != "a2" || rows[1].ID != "a1" {
		t.Fatalf("应按时间倒序返回 a2,a1，实际 %+v", rows)
	}
	if rows[0].HTML != "" {
		t.Fatal("列表不应取正文（html 列）")
	}
	if rows[0].Title == "" || rows[0].Summary == "" {
		t.Fatal("标题/摘要应正常返回")
	}

	// 第二页
	rows2, _, err := repo.ListByUser(ctx, "u1", 2, 2)
	if err != nil {
		t.Fatalf("ListByUser page2: %v", err)
	}
	if len(rows2) != 0 {
		t.Fatalf("第二页应为空，实际 %d 条", len(rows2))
	}
}
