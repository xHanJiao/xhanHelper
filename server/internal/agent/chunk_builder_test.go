package agent

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// mockVSForChunk 记录 Upsert/Delete 调用，用于验证 chunk 的写入行为。
// mockEmbedderForChunk 见 concept_embedder_test.go（同包共用）。
type mockVSForChunk struct {
	upserted map[string]bool
	deleted  map[string]bool
}

func (m *mockVSForChunk) Upsert(id string, vec []float64, metadata map[string]string) {
	m.upserted[id] = true
}
func (m *mockVSForChunk) Search(query []float64, topK int, filter app.VectorFilter) []app.VectorMatch {
	return nil
}
func (m *mockVSForChunk) GetVector(id string) ([]float64, bool) { return nil, false }
func (m *mockVSForChunk) Delete(id string)                      { m.deleted[id] = true }
func (m *mockVSForChunk) Size() int                             { return 0 }

type chunkBuilderFixture struct {
	builder *ChunkBuilder
	vs      *mockVSForChunk
}

// setupChunkBuilder 初始化测试环境：SQLite 内存库 + mock embedder + mock 向量库
func setupChunkBuilder(t *testing.T) *chunkBuilderFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.EmbeddingChunk{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	chunkRepo := service.NewEmbeddingChunkRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	vs := &mockVSForChunk{upserted: make(map[string]bool), deleted: make(map[string]bool)}

	builder := &ChunkBuilder{
		embedder:      &mockEmbedderForChunk{},
		chunkRepo:     chunkRepo,
		embeddingRepo: embeddingRepo,
		vectorStore:   vs,
	}

	return &chunkBuilderFixture{builder: builder, vs: vs}
}

// TestBuildChunks_DisabledParent：禁用父类型直接短路，不嵌入、不写库、不写向量库。
func TestBuildChunks_DisabledParent(t *testing.T) {
	fx := setupChunkBuilder(t)
	fx.builder.disabledParents = map[string]bool{"memory": true}

	n, err := fx.builder.BuildChunks(context.Background(), "u0000001", "memory", "m0000001", "第一句。第二句。", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 chunks for disabled parent, got %d", n)
	}
	if len(fx.vs.upserted) != 0 {
		t.Fatalf("expected no vector upserts, got %d", len(fx.vs.upserted))
	}
}
