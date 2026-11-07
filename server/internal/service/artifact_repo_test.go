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

// 老库升级：artifacts 已在、没有 source_html。SQLite 不接受「ADD COLUMN NOT NULL 无默认值」，
// 所以这列必须是可空的，且旧行读出来是空串（get 据此判断「没有原文」）。
func TestArtifactAutoMigrateAddsSourceHTMLOnExistingTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}()

	// 升级前的表结构（没有 source_html）
	if err := db.Exec(`CREATE TABLE artifacts (
		id text PRIMARY KEY,
		user_id text NOT NULL,
		conversation_id text NOT NULL,
		title text NOT NULL,
		summary text NOT NULL,
		html text NOT NULL,
		share_token text,
		created_at datetime,
		deleted_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(`INSERT INTO artifacts (id, user_id, conversation_id, title, summary, html)
		VALUES ('old-1', 'u1', 'c1', '旧页', '旧摘要', '<html><body>旧</body></html>')`).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := db.AutoMigrate(&model.Artifact{}); err != nil {
		t.Fatalf("AutoMigrate 应能在已有表上加列: %v", err)
	}

	var a model.Artifact
	if err := db.First(&a, "id = ?", "old-1").Error; err != nil {
		t.Fatalf("读旧行: %v", err)
	}
	if a.SourceHTML != "" {
		t.Fatalf("旧行的 source_html 应为空串，实际 %q", a.SourceHTML)
	}
	if a.Tags != "" {
		t.Fatalf("旧行的 tags 应为空串，实际 %q", a.Tags)
	}
	if n, err := NewArtifactRepo(db).CountUntagged(context.Background(), "u1"); err != nil || n != 1 {
		t.Fatalf("NULL tags 的老行也要算作未打标签，实际 n=%d err=%v", n, err)
	}
	if a.HTML == "" {
		t.Fatal("旧行正文不该丢")
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

	ok, err := repo.Update(ctx, "u1", "c1", "新标题", "新摘要", "标签A,标签B", "<html>新</html>", "<html>源</html>")
	if err != nil || !ok {
		t.Fatalf("Update 应成功，ok=%v err=%v", ok, err)
	}
	a, err := repo.GetByID(ctx, "u1", "c1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if a.Title != "新标题" || a.Summary != "新摘要" || a.Tags != "标签A,标签B" || a.HTML != "<html>新</html>" {
		t.Fatalf("覆盖没生效：%+v", a)
	}
	if a.SourceHTML != "<html>源</html>" {
		t.Fatalf("原文也要一起覆盖（get 读的是它）：%q", a.SourceHTML)
	}

	// 内容完全相同再改一次：MySQL 的 RowsAffected 会是 0，不能拿它判存在
	if ok, err := repo.Update(ctx, "u1", "c1", "新标题", "新摘要", "标签A,标签B", "<html>新</html>", "<html>源</html>"); err != nil || !ok {
		t.Fatalf("同值更新也应返回 true，ok=%v err=%v", ok, err)
	}
	// 越权 / 不存在
	if ok, err := repo.Update(ctx, "u1", "c2", "t", "s", "t", "<html>y</html>", "<html>y</html>"); err != nil || ok {
		t.Fatalf("越权更新应返回 false，ok=%v err=%v", ok, err)
	}
	if ok, err := repo.Update(ctx, "u1", "nope", "t", "s", "t", "<html>y</html>", "<html>y</html>"); err != nil || ok {
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

// 乐观锁：只有「读到的原文」仍是库里那份时才写入；被另一处改过 → 0 行，不覆盖。
func TestArtifactRepoUpdateIfSourceUnchanged(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	a := &model.Artifact{
		ID: "a1", UserID: "u1", ConversationID: "c1",
		Title: "t", Summary: "s", HTML: "<html>v1</html>", SourceHTML: "v1",
	}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	tags := "标签X"
	newTitle, newSummary := "t2", "s2"
	ok, err := repo.UpdateIfSourceUnchanged(ctx, "u1", "a1", "v1", "<html>v2</html>", "v2", &newTitle, &newSummary, &tags)
	if err != nil || !ok {
		t.Fatalf("原文匹配应写入: ok=%v err=%v", ok, err)
	}
	var got model.Artifact
	if err := db.First(&got, "id = ?", "a1").Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SourceHTML != "v2" || got.HTML != "<html>v2</html>" || got.Tags != "标签X" || got.Title != "t2" || got.Summary != "s2" {
		t.Fatalf("更新不对（正文与标题/摘要/标签应同一条 UPDATE 落库）: %+v", got)
	}

	// 拿旧原文再写一次 → 拒绝，库不动
	ok, err = repo.UpdateIfSourceUnchanged(ctx, "u1", "a1", "v1", "<html>v3</html>", "v3", nil, nil, &tags)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("旧原文不应写入")
	}
	if err := db.First(&got, "id = ?", "a1").Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SourceHTML != "v2" {
		t.Fatalf("被拒后库不该变: %q", got.SourceHTML)
	}
}

// 标签筛选：多标签交集、逗号边界（Go ≠ Golang）、关键词、limit=0 只出 total。
func TestArtifactRepoListByTags(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Now().Add(-time.Hour)
	seed := []model.Artifact{
		{ID: "t1", UserID: "u1", ConversationID: "c1", Title: "东京手账", Summary: "地铁", Tags: "手账,旅行", HTML: "<html>1</html>", CreatedAt: base},
		{ID: "t2", UserID: "u1", ConversationID: "c1", Title: "月度理财", Summary: "账单", Tags: "手账,理财", HTML: "<html>2</html>", CreatedAt: base.Add(time.Minute)},
		{ID: "t3", UserID: "u1", ConversationID: "c1", Title: "Go 笔记", Summary: "并发", Tags: "Golang", HTML: "<html>3</html>", CreatedAt: base.Add(2 * time.Minute)},
		{ID: "t4", UserID: "u1", ConversationID: "c1", Title: "老页", Summary: "无标签", HTML: "<html>4</html>", CreatedAt: base.Add(3 * time.Minute)},
		{ID: "t5", UserID: "u2", ConversationID: "c2", Title: "别人的", Summary: "s", Tags: "手账", HTML: "<html>5</html>", CreatedAt: base.Add(4 * time.Minute)},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	// 单标签
	rows, total, err := repo.ListByTags(ctx, "u1", []string{"手账"}, "", 10, 0)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("『手账』应命中 t1,t2，实际 total=%d len=%d err=%v", total, len(rows), err)
	}
	if rows[0].ID != "t2" || rows[1].ID != "t1" {
		t.Fatalf("应按时间倒序，实际 %+v", rows)
	}
	if rows[0].HTML != "" {
		t.Fatal("列表不应取正文")
	}
	if rows[0].Tags != "手账,理财" {
		t.Fatalf("列表应带标签，实际 %q", rows[0].Tags)
	}

	// 多标签交集
	rows, total, err = repo.ListByTags(ctx, "u1", []string{"手账", "旅行"}, "", 10, 0)
	if err != nil || total != 1 || rows[0].ID != "t1" {
		t.Fatalf("交集应只命中 t1，实际 total=%d rows=%+v err=%v", total, rows, err)
	}

	// 标签是逗号边界匹配：Go 不该命中 Golang
	if _, total, err = repo.ListByTags(ctx, "u1", []string{"Go"}, "", 10, 0); err != nil || total != 0 {
		t.Fatalf("Go 不应命中 Golang，实际 total=%d err=%v", total, err)
	}

	// 关键词搜标题/摘要
	rows, total, err = repo.ListByTags(ctx, "u1", nil, "理财", 10, 0)
	if err != nil || total != 1 || rows[0].ID != "t2" {
		t.Fatalf("关键词应命中 t2，实际 total=%d rows=%+v err=%v", total, rows, err)
	}
	if _, total, err = repo.ListByTags(ctx, "u1", nil, "账单", 10, 0); err != nil || total != 1 {
		t.Fatalf("摘要也应参与关键词，实际 total=%d err=%v", total, err)
	}

	// limit=0：只出 total，不返回列表
	rows, total, err = repo.ListByTags(ctx, "u1", []string{"手账"}, "", 0, 0)
	if err != nil || total != 2 || len(rows) != 0 {
		t.Fatalf("limit=0 应只出 total=2，实际 total=%d len=%d err=%v", total, len(rows), err)
	}
}

// 标签图 / 标签名 / 未打标签计数 / 只改标签 / 批量查存活 id。
func TestArtifactRepoTagsGraphAndHelpers(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	seed := []model.Artifact{
		{ID: "g1", UserID: "u1", ConversationID: "c1", Title: "a", Summary: "s", Tags: "手账,旅行", HTML: "<html>1</html>"},
		{ID: "g2", UserID: "u1", ConversationID: "c1", Title: "b", Summary: "s", Tags: "手账,理财", HTML: "<html>2</html>"},
		{ID: "g3", UserID: "u1", ConversationID: "c1", Title: "老页", Summary: "s", HTML: "<html>3</html>"},
		{ID: "g4", UserID: "u2", ConversationID: "c2", Title: "别人的", Summary: "s", Tags: "手账", HTML: "<html>4</html>"},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	tags, edges, err := repo.TagGraph(ctx, "u1")
	if err != nil {
		t.Fatalf("TagGraph: %v", err)
	}
	if len(tags) != 3 || tags[0].Name != "手账" || tags[0].Count != 2 {
		t.Fatalf("频次应手账=2 且排第一，实际 %+v", tags)
	}
	// 手账 × 旅行 / 手账 × 理财
	if len(edges) != 2 {
		t.Fatalf("应有 2 条共现边，实际 %+v", edges)
	}

	names, err := repo.ListTags(ctx, "u1")
	if err != nil || len(names) != 3 || names[0] != "手账" {
		t.Fatalf("ListTags 应返回 3 个且手账在前，实际 %+v err=%v", names, err)
	}

	if n, err := repo.CountUntagged(ctx, "u1"); err != nil || n != 1 {
		t.Fatalf("未打标签应为 1（g3），实际 n=%d err=%v", n, err)
	}

	newTags := "手账,旅行,日本"
	ok, err := repo.SetMeta(ctx, "u1", "g1", nil, nil, &newTags)
	if err != nil || !ok {
		t.Fatalf("SetMeta 应成功，ok=%v err=%v", ok, err)
	}
	a, err := repo.GetMeta(ctx, "u1", "g1")
	if err != nil || a.Tags != "手账,旅行,日本" || a.Title != "a" {
		t.Fatalf("只应改标签，实际 %+v err=%v", a, err)
	}
	badTags := "x"
	if ok, err := repo.SetMeta(ctx, "u1", "g4", nil, nil, &badTags); err != nil || ok {
		t.Fatalf("越权改标签应 false，ok=%v err=%v", ok, err)
	}

	got, err := repo.ExistingIDs(ctx, "u1", []string{"g1", "g4", "nope"})
	if err != nil {
		t.Fatalf("ExistingIDs: %v", err)
	}
	if len(got) != 1 || got[0] != "g1" {
		t.Fatalf("只应返回属于 u1 的 g1，实际 %+v", got)
	}
}

// 不带 tags 的正文写不能把「并发改的标签」回滚（tags=nil 时不写该列）。
func TestArtifactRepoUpdateIfSourceUnchangedKeepsTagsWhenNil(t *testing.T) {
	repo, db, cleanup := setupArtifactRepo(t)
	defer cleanup()
	ctx := context.Background()

	a := &model.Artifact{
		ID: "a1", UserID: "u1", ConversationID: "c1",
		Title: "t", Summary: "s", Tags: "手账", HTML: "<html>v1</html>", SourceHTML: "v1",
	}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 并发里的另一条：只改标签（不动 source_html）
	concurrentTags := "旅行,日本"
	if ok, err := repo.SetMeta(ctx, "u1", "a1", nil, nil, &concurrentTags); err != nil || !ok {
		t.Fatalf("SetMeta: ok=%v err=%v", ok, err)
	}

	// 正文 patch 不带 tags：照常按乐观锁写正文，但 tags 列必须原样保留
	ok, err := repo.UpdateIfSourceUnchanged(ctx, "u1", "a1", "v1", "<html>v2</html>", "v2", nil, nil, nil)
	if err != nil || !ok {
		t.Fatalf("正文写应成功: ok=%v err=%v", ok, err)
	}
	var got model.Artifact
	if err := db.First(&got, "id = ?", "a1").Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Tags != "旅行,日本" {
		t.Fatalf("并发改的标签被回滚了: %q", got.Tags)
	}
	if got.SourceHTML != "v2" {
		t.Fatalf("正文应更新: %q", got.SourceHTML)
	}
}
