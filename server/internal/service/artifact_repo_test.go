package service

import (
	"context"
	"errors"
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

// 原地覆盖：同 id 改内容；越权/不存在返回 false；内容相同也不能被判成不存在。
func TestArtifactRepoUpdateInPlace(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	seed := []model.Artifact{
		{ID: "c1", UserID: "u1", ConversationID: "c1", Title: "旧", Summary: "旧", HTML: "<html>旧</html>"},
		{ID: "c2", UserID: "u2", ConversationID: "c2", Title: "别人的", Summary: "s", HTML: "<html>x</html>"},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	ok, err := repo.Update(ctx, "u1", "c1", "新标题", "新摘要", "<html>新</html>")
	if err != nil || !ok {
		t.Fatalf("Update 应成功，ok=%v err=%v", ok, err)
	}
	a, err := repo.GetByID(ctx, "u1", "c1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if a.Title != "新标题" || a.Summary != "新摘要" || a.HTML != "<html>新</html>" {
		t.Fatalf("覆盖没生效：%+v", a)
	}

	// 内容完全相同再改一次：MySQL 的 RowsAffected 会是 0，不能拿它判存在
	if ok, err := repo.Update(ctx, "u1", "c1", "新标题", "新摘要", "<html>新</html>"); err != nil || !ok {
		t.Fatalf("同值更新也应返回 true，ok=%v err=%v", ok, err)
	}
	// 越权 / 不存在
	if ok, err := repo.Update(ctx, "u1", "c2", "t", "s", "<html>y</html>"); err != nil || ok {
		t.Fatalf("越权更新应返回 false，ok=%v err=%v", ok, err)
	}
	if ok, err := repo.Update(ctx, "u1", "nope", "t", "s", "<html>y</html>"); err != nil || ok {
		t.Fatalf("不存在应返回 false，ok=%v err=%v", ok, err)
	}
}

// 批量删除：混入别人的 id / 空串只删自己的；空入参不打 DB。
func TestArtifactRepoDeleteMany(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	seed := []model.Artifact{
		{ID: "d1", UserID: "u1", ConversationID: "c1", Title: "a", Summary: "s", HTML: "<html>1</html>"},
		{ID: "d2", UserID: "u1", ConversationID: "c1", Title: "b", Summary: "s", HTML: "<html>2</html>"},
		{ID: "d3", UserID: "u2", ConversationID: "c2", Title: "别人的", Summary: "s", HTML: "<html>3</html>"},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	rows, err := repo.DeleteMany(ctx, "u1", []string{"d1", "d2", "d3", ""})
	if err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}
	if rows != 2 {
		t.Fatalf("只应删掉自己的 2 条，实际 %d", rows)
	}
	if _, err := repo.GetByID(ctx, "u2", "d3"); err != nil {
		t.Fatalf("别人的页面不该被删: %v", err)
	}
	if _, err := repo.GetByID(ctx, "u1", "d1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("d1 应已软删，err=%v", err)
	}

	if rows, err := repo.DeleteMany(ctx, "u1", nil); err != nil || rows != 0 {
		t.Fatalf("空入参应返回 0 且不报错，rows=%d err=%v", rows, err)
	}
}

// 删除与分享：越权删不动、软删后列表/分享都取不到、token 可设可撤。
func TestArtifactRepoDeleteAndShare(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	seed := []model.Artifact{
		{ID: "b1", UserID: "u1", ConversationID: "c1", Title: "我的", Summary: "s", HTML: "<html>1</html>"},
		{ID: "b2", UserID: "u2", ConversationID: "c2", Title: "别人的", Summary: "s", HTML: "<html>2</html>"},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	// 越权删：行数 0，别人的那条还在
	if rows, err := repo.Delete(ctx, "u1", "b2"); err != nil || rows != 0 {
		t.Fatalf("越权删除应影响 0 行，实际 rows=%d err=%v", rows, err)
	}
	if _, err := repo.GetByID(ctx, "u2", "b2"); err != nil {
		t.Fatalf("别人的产物不该被删掉: %v", err)
	}

	// 分享：设置后可只取 token，也可按 token 取正文
	if _, err := repo.SetShareToken(ctx, "u1", "b1", "tok-1"); err != nil {
		t.Fatalf("SetShareToken: %v", err)
	}
	if tok, err := repo.GetShareToken(ctx, "u1", "b1"); err != nil || tok != "tok-1" {
		t.Fatalf("GetShareToken 应为 tok-1，实际 %q err=%v", tok, err)
	}
	if a, err := repo.GetByShareToken(ctx, "tok-1"); err != nil || a.ID != "b1" {
		t.Fatalf("按 token 取不到产物: %v", err)
	}

	// 删除自己的：列表、按 token 都取不到（软删过滤）
	if rows, err := repo.Delete(ctx, "u1", "b1"); err != nil || rows != 1 {
		t.Fatalf("删除应影响 1 行，实际 rows=%d err=%v", rows, err)
	}
	if _, total, err := repo.ListByUser(ctx, "u1", 10, 0); err != nil || total != 0 {
		t.Fatalf("删除后列表应为空，实际 total=%d err=%v", total, err)
	}
	if _, err := repo.GetByShareToken(ctx, "tok-1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("删除后分享链接应失效，实际 err=%v", err)
	}

	// 撤销分享：token 清空，旧 token 取不到
	if _, err := repo.SetShareToken(ctx, "u2", "b2", "tok-2"); err != nil {
		t.Fatalf("SetShareToken b2: %v", err)
	}
	if _, err := repo.ClearShareToken(ctx, "u2", "b2"); err != nil {
		t.Fatalf("ClearShareToken: %v", err)
	}
	if tok, _ := repo.GetShareToken(ctx, "u2", "b2"); tok != "" {
		t.Fatalf("撤销后 token 应为空，实际 %q", tok)
	}
	if _, err := repo.GetByShareToken(ctx, "tok-2"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("撤销后旧 token 应失效，实际 err=%v", err)
	}
}
