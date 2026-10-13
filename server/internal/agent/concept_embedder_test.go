package agent

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// GetOrEmbed 的自愈：概念行在、向量行缺失时**补嵌并写回**，不再返回空向量。
// 这是线上 `embed existing topic "房产投资" error: <nil> (skip)` 的根因
// ——孤儿概念行永久用不了，见 docs/bugs/037。
func TestGetOrEmbed_SelfHealsMissingVector(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ConceptEmbedding{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conceptRepo := service.NewConceptEmbeddingRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	svc := &ConceptEmbedService{conceptRepo: conceptRepo, embeddingRepo: embeddingRepo, embedder: &mockEmbedder{}}

	// 造一条「有概念行、无向量行」的孤儿——正是线上那条 "房产投资" 的状态。
	if err := conceptRepo.Create(context.Background(), &model.ConceptEmbedding{
		ID: "c1", Type: "topic", Value: "房产投资", UserID: "u1",
	}); err != nil {
		t.Fatalf("create concept: %v", err)
	}

	vec, err := svc.GetOrEmbed(context.Background(), "topic", "房产投资", "u1")
	if err != nil {
		t.Fatalf("孤儿概念行必须自愈，不该报错: %v", err)
	}
	if len(vec) == 0 {
		t.Fatal("孤儿概念行必须补嵌，不能返回空向量")
	}

	// 向量必须写回（否则下次还是孤儿）。
	emb, err := embeddingRepo.GetByEntity(context.Background(), model.EntityTypeConcept, "c1")
	if err != nil {
		t.Fatalf("get embedding: %v", err)
	}
	if emb == nil || emb.Embedding == nil || *emb.Embedding == "" {
		t.Fatalf("补嵌后向量必须写回: %+v", emb)
	}

	// 写回后第二次调用走缓存：把 embedder 换成会失败的，仍应拿到向量。
	svc.embedder = failingEmbedder{}
	if vec2, err := svc.GetOrEmbed(context.Background(), "topic", "房产投资", "u1"); err != nil || len(vec2) == 0 {
		t.Fatalf("第二次应命中缓存、不再调 embedder: vec=%v err=%v", vec2, err)
	}
}

// failingEmbedder 任何嵌入都报错，用来证明"没有落在嵌入调用上"。
type failingEmbedder struct{}

func (failingEmbedder) Embed(context.Context, string) ([]float64, error) {
	return nil, gorm.ErrInvalidDB
}
func (failingEmbedder) ModelName() string { return "failing" }
