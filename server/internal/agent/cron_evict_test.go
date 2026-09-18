package agent

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupEvictFixture(t *testing.T) (*service.EmbeddingRepo, *VectorStore, *gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
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
	// DB 行保留
	var n int64
	db.Model(&model.Embedding{}).Where("id = ?", "m-old").Count(&n)
	if n != 1 {
		t.Error("DB embedding row must be preserved")
	}
}
