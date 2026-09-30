package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupEvictFixture(t *testing.T) (*service.EmbeddingRepo, *VectorStore, *gorm.DB, func()) {
	t.Helper()
	// 文件库：VACUUM 回收空闲页只对文件库生效（:memory: 不缩）
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evict.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return service.NewEmbeddingRepo(db), NewVectorStore(), db, cleanup
}

// TestEvictStaleVectors_OnlyExpired：只逐出超过保留窗的 message/summary，近期与其它类型不受影响。
func TestEvictStaleVectors_OnlyExpired(t *testing.T) {
	repo, vs, db, cleanup := setupEvictFixture(t)
	defer cleanup()
	ctx := context.Background()

	oldMsg := &model.Embedding{ID: "m-old", EntityType: model.EntityTypeMessage, EntityID: "m-old", UserID: "u1", Model: "x"}
	newMsg := &model.Embedding{ID: "m-new", EntityType: model.EntityTypeMessage, EntityID: "m-new", UserID: "u1", Model: "x"}
	oldSum := &model.Embedding{ID: "s-old", EntityType: model.EntityTypeSummary, EntityID: "s-old", UserID: "u1", Model: "x"}
	oldNote := &model.Embedding{ID: "n-old", EntityType: model.EntityTypeNote, EntityID: "n-old", UserID: "u1", Model: "x"}
	for _, e := range []*model.Embedding{oldMsg, newMsg, oldSum, oldNote} {
		db.Create(e)
	}
	db.Model(&model.Embedding{}).Where("id IN ?", []string{"m-old", "s-old", "n-old"}).
		UpdateColumn("created_at", time.Now().AddDate(0, 0, -40))

	for _, id := range []string{"msg:m-old", "msg:m-new", "summary:s-old", "note:n-old"} {
		vs.Upsert(id, []float64{1, 0}, nil)
	}

	cr := &CronRunner{embeddingRepo: repo, vectorStore: vs, msgRetentionDays: 14, sumRetentionDays: 30}
	cr.evictStaleVectors(ctx)

	if _, ok := vs.GetVector("msg:m-old"); ok {
		t.Error("expired message vector should be evicted")
	}
	if _, ok := vs.GetVector("summary:s-old"); ok {
		t.Error("expired summary vector should be evicted")
	}
	if _, ok := vs.GetVector("msg:m-new"); !ok {
		t.Error("recent message vector should stay")
	}
	if _, ok := vs.GetVector("note:n-old"); !ok {
		t.Error("note vectors are not governed by retention and should stay")
	}
	// DB 行同窗滚动删除
	countDB := func(id string) int64 {
		var n int64
		db.Model(&model.Embedding{}).Where("id = ?", id).Count(&n)
		return n
	}
	if countDB("m-old") != 0 {
		t.Error("expired message DB row must be deleted")
	}
	if countDB("s-old") != 0 {
		t.Error("expired summary DB row must be deleted")
	}
	if countDB("m-new") != 1 {
		t.Error("recent message DB row must stay")
	}
	if countDB("n-old") != 1 {
		t.Error("note DB row must stay (not governed by retention)")
	}
}

// TestEvictStaleVectors_TriggersVacuum — 真删了行且超阈值时，本轮触发一次 VACUUM。
func TestEvictStaleVectors_TriggersVacuum(t *testing.T) {
	repo, vs, db, cleanup := setupEvictFixture(t)
	defer cleanup()
	ctx := context.Background()

	// 灌入大行制造可回收空闲页；全部标为过期
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	s := string(big)
	for i := 0; i < 300; i++ {
		id := "big-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
		db.Create(&model.Embedding{ID: id, EntityType: model.EntityTypeMessage, EntityID: id,
			UserID: "u1", Model: "x", Embedding: &s})
	}
	db.Model(&model.Embedding{}).Where("entity_type = ?", model.EntityTypeMessage).
		UpdateColumn("created_at", time.Now().AddDate(0, 0, -40))

	// 阈值取 1 字节（>0 = 启用；0/负数才是关闭），确保必然触发
	cr := &CronRunner{embeddingRepo: repo, vectorStore: vs, msgRetentionDays: 14,
		db: db, vacuumThreshold: 1}
	cr.evictStaleVectors(ctx)

	free, _ := service.FreePageBytes(db)
	if free != 0 {
		t.Errorf("VACUUM 后空闲页应归零, got %d", free)
	}
}

// TestVacuumIfNeeded_NoOp — 阈值关闭或 db 为 nil 时静默跳过。
func TestVacuumIfNeeded_NoOp(t *testing.T) {
	// db nil
	cr := &CronRunner{vacuumThreshold: 1}
	cr.vacuumIfNeeded("test")
	// 阈值关闭
	_, _, db, cleanup := setupEvictFixture(t)
	defer cleanup()
	cr2 := &CronRunner{db: db, vacuumThreshold: 0}
	cr2.vacuumIfNeeded("test")
}
