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

// mockEmbedderForChunk 返回固定向量，模拟 embedding 调用
type mockEmbedderForChunk struct{}

func (m *mockEmbedderForChunk) Embed(ctx context.Context, text string) ([]float64, error) {
	// 返回固定三维向量，实际嵌入操作在单测中不调用真实 API
	return []float64{0.1, 0.2, 0.3}, nil
}
func (m *mockEmbedderForChunk) ModelName() string { return "mock" }

// mockVSForChunk 记录 Upsert 和 Delete 调用，用于验证 chunk 的增删行为
type mockVSForChunk struct {
	upserted map[string]bool // 新创建或更新的 chunk id 集合
	deleted  map[string]bool // 被删除的 chunk id 集合
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

// TestDiffAndBuildChunks_EmptyText 空文本：0 保留 0 新增，不触发嵌入
func TestDiffAndBuildChunks_EmptyText(t *testing.T) {
	fx := setupChunkBuilder(t)
	kept, added, err := fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kept != 0 || added != 0 {
		t.Errorf("expected 0/0, got kept=%d added=%d", kept, added)
	}
}

// TestDiffAndBuildChunks_FirstTime 首次嵌入：0 保留，N 句全部新增嵌入
func TestDiffAndBuildChunks_FirstTime(t *testing.T) {
	fx := setupChunkBuilder(t)

	kept, added, err := fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "第一句。第二句。第三句。", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 首次调用无旧 chunk，3 句全部新增
	if kept != 0 {
		t.Errorf("first time: kept should be 0, got %d", kept)
	}
	if added != 3 {
		t.Errorf("first time: added should be 3, got %d", added)
	}
	if len(fx.vs.upserted) != 3 {
		t.Errorf("first time: expected 3 upserted, got %d", len(fx.vs.upserted))
	}
}

// TestDiffAndBuildChunks_SameText 相同文本再次调用：全部保留，0 新增嵌入
func TestDiffAndBuildChunks_SameText(t *testing.T) {
	fx := setupChunkBuilder(t)

	text := "第一句。第二句。第三句。"

	// first pass: build initial chunks
	fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", text, nil)

	// 重置计数器，模拟第二次调用
	fx.vs = &mockVSForChunk{upserted: make(map[string]bool), deleted: make(map[string]bool)}
	fx.builder.vectorStore = fx.vs

	// 相同文本再次调用：旧 chunk 文本完全匹配，不应触发任何嵌入
	kept, added, err := fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", text, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kept != 3 {
		t.Errorf("same text: kept should be 3, got %d", kept)
	}
	if added != 0 {
		t.Errorf("same text: added should be 0 (zero embeddings), got %d", added)
	}
	if len(fx.vs.upserted) > 0 {
		t.Errorf("same text: expected 0 upserted (all kept), got %d", len(fx.vs.upserted))
	}
	if len(fx.vs.deleted) > 0 {
		t.Errorf("same text: expected 0 deleted, got %d", len(fx.vs.deleted))
	}
}

// TestDiffAndBuildChunks_OneSentenceChanged 修改 1 句：2 句保留，1 句重新嵌入
func TestDiffAndBuildChunks_OneSentenceChanged(t *testing.T) {
	fx := setupChunkBuilder(t)

	fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "第一句。第二句。第三句。", nil)

	fx.vs = &mockVSForChunk{upserted: make(map[string]bool), deleted: make(map[string]bool)}
	fx.builder.vectorStore = fx.vs

	// 修改中间句子，第一句和第三句不变
	kept, added, err := fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "第一句。修改后的第二句。第三句。", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 句与旧文本完全一致 → 保留；1 句变更 → 重新嵌入
	if kept != 2 {
		t.Errorf("kept should be 2, got %d", kept)
	}
	if added != 1 {
		t.Errorf("added should be 1, got %d", added)
	}
}

// TestDiffAndBuildChunks_SentenceAdded 新增 1 句：旧句保留，新句嵌入
func TestDiffAndBuildChunks_SentenceAdded(t *testing.T) {
	fx := setupChunkBuilder(t)

	fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "第一句。第二句。", nil)

	fx.vs = &mockVSForChunk{upserted: make(map[string]bool), deleted: make(map[string]bool)}
	fx.builder.vectorStore = fx.vs

	// 新文本比旧文本多一句
	kept, added, err := fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "第一句。第二句。新增的第三句。", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 句保留，1 句新增嵌入
	if kept != 2 {
		t.Errorf("kept should be 2, got %d", kept)
	}
	if added != 1 {
		t.Errorf("added should be 1, got %d", added)
	}
}

// TestDiffAndBuildChunks_SentenceRemoved 删除 1 句：保留句不动，删掉的句子从向量库移除
func TestDiffAndBuildChunks_SentenceRemoved(t *testing.T) {
	fx := setupChunkBuilder(t)

	fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "第一句。第二句。第三句。", nil)

	fx.vs = &mockVSForChunk{upserted: make(map[string]bool), deleted: make(map[string]bool)}
	fx.builder.vectorStore = fx.vs

	// 新文本删掉第二句
	kept, added, err := fx.builder.DiffAndBuildChunks(context.Background(), "u0000001", "theme", "t0000001", "第一句。第三句。", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 句保留，0 句新增，被删句子从向量库移除
	if kept != 2 {
		t.Errorf("kept should be 2, got %d", kept)
	}
	if added != 0 {
		t.Errorf("added should be 0, got %d", added)
	}
	if len(fx.vs.deleted) == 0 {
		t.Error("deleted vec should contain removed sentence")
	}
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
