package retrieval

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// mockVectorStore 用于注入可控的向量检索结果
type mockVectorStore struct {
	results []SearchResult
}

func (m *mockVectorStore) Search(query []float64, topK int, filter func(Entry) bool) []SearchResult {
	var filtered []SearchResult
	for _, r := range m.results {
		e := Entry{
			ID:       r.ID,
			Metadata: r.Metadata,
		}
		if filter(e) {
			filtered = append(filtered, r)
		}
	}
	// 模拟 topK 截断
	if len(filtered) > topK {
		filtered = filtered[:topK]
	}
	return filtered
}

func (m *mockVectorStore) Upsert(id string, vec []float64, metadata map[string]string) {}
func (m *mockVectorStore) Size() int                                                   { return 0 }

// stubChunkBuilder 存根，单测中不实际建 chunk
type stubChunkBuilder struct{}

func (s *stubChunkBuilder) BuildChunks(ctx context.Context, userID, parentType, parentID, text string) (int, error) {
	return 0, nil
}

func setupNoteSource(t *testing.T) (*NoteSource, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Note{}, &model.EmbeddingChunk{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	noteRepo := service.NewNoteRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	return &NoteSource{
		noteRepo:      noteRepo,
		chunkRepo:     chunkRepo,
		embeddingRepo: embeddingRepo,
		chunkBuilder:  &stubChunkBuilder{},
		ranker:        DefaultRanker(),
	}, func() {}
}

// 构造一个 chunk SearchResult
func chunkResult(chunkID, noteID string, score float64) SearchResult {
	return SearchResult{
		ID:    chunkID,
		Score: score,
		Metadata: map[string]string{
			"type":        "chunk",
			"parent_type": "note",
			"parent_id":   noteID,
			"user_id":     "u1",
		},
	}
}

// 构造一个全量 note 嵌入 SearchResult
func fullNoteResult(noteID string, score float64) SearchResult {
	return SearchResult{
		ID:    "note:" + noteID,
		Score: score,
		Metadata: map[string]string{
			"type":    "note",
			"user_id": "u1",
		},
	}
}

func seedNotes(t *testing.T, repo *service.NoteRepo) {
	t.Helper()
	notes := []model.Note{
		{ID: "n1", UserID: "u1", Topic: "饮酒记录", Content: "5月喝了3次啤酒，每次约50元"},
		{ID: "n2", UserID: "u1", Topic: "健身计划", Content: "每周跑步3次，每次5公里"},
		{ID: "n3", UserID: "u1", Topic: "饮食预算", Content: "每月餐饮预算2000元，实际花费约1800"},
	}
	for _, n := range notes {
		if err := repo.Create(context.Background(), &n); err != nil {
			t.Fatalf("seed note %s: %v", n.ID, err)
		}
	}
}

func seedChunks(t *testing.T, repo *service.EmbeddingChunkRepo) {
	t.Helper()
	chunks := []model.EmbeddingChunk{
		{ID: "chunk:note:n1/0", ParentType: "note", ParentID: "n1", ChunkIndex: 0, Content: "5月喝了3次啤酒"},
		{ID: "chunk:note:n1/1", ParentType: "note", ParentID: "n1", ChunkIndex: 1, Content: "每次约50元"},
		{ID: "chunk:note:n2/0", ParentType: "note", ParentID: "n2", ChunkIndex: 0, Content: "每周跑步3次"},
		{ID: "chunk:note:n2/1", ParentType: "note", ParentID: "n2", ChunkIndex: 1, Content: "每次5公里"},
	}
	if err := repo.BatchCreate(context.Background(), chunks); err != nil {
		t.Fatalf("seed chunks: %v", err)
	}
}

// ---- 测试用例 ----

// 用例1：chunk 命中，同一笔记多 chunk 被正确去重，只返回 1 个 note
func TestNoteSource_ChunkDedup_SameNote(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)
	seedChunks(t, src.chunkRepo)

	// n1 的两个 chunk 都高分命中，期望去重后只返回 n1 一条笔记
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		chunkResult("chunk:note:n1/1", "n1", 0.82),
	}}

	packets, nearMiss, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒消费", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("去重后应返回 1 条笔记，实际 %d 条", len(packets))
	}
	if packets[0].ID != "n1" {
		t.Errorf("期望命中 n1，实际 %s", packets[0].ID)
	}
	if len(nearMiss) != 0 {
		t.Errorf("不应有 nearMiss，实际 %d 条", len(nearMiss))
	}
}

// 用例2：多个不同笔记的 chunk 命中，返回多条笔记
func TestNoteSource_ChunkHits_DifferentNotes(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)
	seedChunks(t, src.chunkRepo)

	src.vectorStore = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		chunkResult("chunk:note:n2/0", "n2", 0.78),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望返回 2 条笔记，实际 %d 条", len(packets))
	}
}

// 用例3：部分 chunk 低于阈值，去重后只产生一条 nearMiss
func TestNoteSource_ChunkBelowThreshold_DedupedNearMiss(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)
	seedChunks(t, src.chunkRepo)

	// n1 的 2 个 chunk 都低于阈值 0.7
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.55),
		chunkResult("chunk:note:n1/1", "n1", 0.52),
	}}

	packets, nearMiss, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.7, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("低于阈值不应有命中，实际 %d 条", len(packets))
	}
	// 去重后 n1 只产生 1 条 nearMiss，不是 2 条
	if len(nearMiss) != 1 {
		t.Errorf("同笔记的 2 个低分 chunk 去重后应只产生 1 条 nearMiss，实际 %d 条", len(nearMiss))
	}
	if nearMiss[0].ID != "n1" {
		t.Errorf("nearMiss ID 应为 n1，实际 %s", nearMiss[0].ID)
	}
}

// 用例4：chunk 无命中，回退到全量 note 嵌入
func TestNoteSource_Fallback_FullNoteEmbedding(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)

	// 没有 chunk 结果，只有全量 note 嵌入
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		fullNoteResult("n1", 0.75),
		fullNoteResult("n2", 0.65),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("回退路径应返回 2 条笔记，实际 %d 条", len(packets))
	}
}

// 用例5：chunk 部分命中 + 全量嵌入补充不足的数量
func TestNoteSource_ChunkPlusFallback(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)
	seedChunks(t, src.chunkRepo)

	// n1 通过 chunk 命中，n2 通过全量嵌入补充
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		fullNoteResult("n2", 0.72),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "消费", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望 2 条笔记（chunk+fallback），实际 %d 条", len(packets))
	}
}

// 用例6：chunk 和全量嵌入命中同一个笔记，不重复返回
func TestNoteSource_NoDuplicate_ChunkAndFullHit(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)
	seedChunks(t, src.chunkRepo)

	// n1 同时被 chunk 和全量嵌入命中，只返回一次
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		fullNoteResult("n1", 0.60),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("n1 不应重复返回，期望 1 条，实际 %d 条", len(packets))
	}
}

// 用例7：返回结果附带匹配 chunk 片段
func TestNoteSource_MatchedChunkSnippets(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)
	seedChunks(t, src.chunkRepo)

	src.vectorStore = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// 应包含 "匹配片段" 字样
	if !strings.Contains(packets[0].Text, "匹配片段") {
		t.Error("返回文本应包含匹配的 chunk 片段")
	}
	if !strings.Contains(packets[0].Text, "5月喝了3次啤酒") {
		t.Error("返回文本应包含 chunk 内容")
	}
}

// 用例8：向量库空结果
func TestNoteSource_EmptyResults(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()

	src.vectorStore = &mockVectorStore{results: nil}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "任意查询", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("空结果应返回 0 条，实际 %d 条", len(packets))
	}
}

// 用例9：其他用户的 chunk 被过滤掉
func TestNoteSource_FilterOtherUsers(t *testing.T) {
	src, cleanup := setupNoteSource(t)
	defer cleanup()
	seedNotes(t, src.noteRepo)
	seedChunks(t, src.chunkRepo)

	// 包含 u2 的 chunk，不应被返回
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		{ID: "chunk:note:n_other/0", Score: 0.90, Metadata: map[string]string{
			"type": "chunk", "parent_type": "note", "parent_id": "n_other", "user_id": "u2",
		}},
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "查询", UserID: "u1", ConversationID: "c1", NoteThreshold: 0.5, NoteLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("应只返回 u1 的笔记，实际 %d 条", len(packets))
	}
}
