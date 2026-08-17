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
	if len(filtered) > topK {
		filtered = filtered[:topK]
	}
	return filtered
}

func (m *mockVectorStore) Upsert(id string, vec []float64, metadata map[string]string) {}
func (m *mockVectorStore) Size() int                                                   { return 0 }

// stubChunkBuilder 存根，单测中不实际建 chunk
type stubChunkBuilder struct{}

func (s *stubChunkBuilder) BuildChunks(ctx context.Context, userID, parentType, parentID, text string, meta map[string]string) (int, error) {
	return 0, nil
}

type noteFixture struct {
	src       *EntitySource[model.Note]
	noteRepo  *service.NoteRepo
	chunkRepo *service.EmbeddingChunkRepo
	cleanup   func()
}

func setupNoteSource(t *testing.T) *noteFixture {
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
	return &noteFixture{
		src: &EntitySource[model.Note]{
			name:          "note",
			entityType:    model.EntityTypeNote,
			fullType:      "note",
			idPrefix:      "note:",
			decayer:       NewTimeDecay(0),
			repo:          noteRepo,
			embeddingRepo: embeddingRepo,
			chunkRepo:     chunkRepo,
			chunkBuilder:  &stubChunkBuilder{},
			configPicker:  func(o SearchOptions) SourceConfig { return o.Note },
		},
		noteRepo:  noteRepo,
		chunkRepo: chunkRepo,
		cleanup:   func() {},
	}
}

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

func TestNoteSource_ChunkDedup_SameNote(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	seedNotes(t, f.noteRepo)
	seedChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		chunkResult("chunk:note:n1/1", "n1", 0.82),
	}}

	packets, nearMiss, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒消费", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
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

func TestNoteSource_ChunkHits_DifferentNotes(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	seedNotes(t, f.noteRepo)
	seedChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		chunkResult("chunk:note:n2/0", "n2", 0.78),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望返回 2 条笔记，实际 %d 条", len(packets))
	}
}

func TestNoteSource_ChunkBelowThreshold_DedupedNearMiss(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	seedNotes(t, f.noteRepo)
	seedChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.55),
		chunkResult("chunk:note:n1/1", "n1", 0.52),
	}}

	packets, nearMiss, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.7, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("低于阈值不应有命中，实际 %d 条", len(packets))
	}
	if len(nearMiss) != 1 {
		t.Errorf("同笔记的 2 个低分 chunk 去重后应只产生 1 条 nearMiss，实际 %d 条", len(nearMiss))
	}
	if nearMiss[0].ID != "n1" {
		t.Errorf("nearMiss ID 应为 n1，实际 %s", nearMiss[0].ID)
	}
}

func TestNoteSource_Fallback_FullNoteEmbedding(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	seedNotes(t, f.noteRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		fullNoteResult("n1", 0.75),
		fullNoteResult("n2", 0.65),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("回退路径应返回 2 条笔记，实际 %d 条", len(packets))
	}
}

func TestNoteSource_ChunkPlusFallback(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	seedNotes(t, f.noteRepo)
	seedChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		fullNoteResult("n2", 0.72),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "消费", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望 2 条笔记（chunk+fallback），实际 %d 条", len(packets))
	}
}

func TestNoteSource_NoDuplicate_ChunkAndFullHit(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	seedNotes(t, f.noteRepo)
	seedChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		fullNoteResult("n1", 0.60),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("n1 不应重复返回，期望 1 条，实际 %d 条", len(packets))
	}
}

func TestNoteSource_EmptyResults(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()

	f.src.vs = &mockVectorStore{results: nil}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "任意查询", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("空结果应返回 0 条，实际 %d 条", len(packets))
	}
}

func TestNoteSource_FilterOtherUsers(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	seedNotes(t, f.noteRepo)
	seedChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		chunkResult("chunk:note:n1/0", "n1", 0.85),
		{ID: "chunk:note:n_other/0", Score: 0.90, Metadata: map[string]string{
			"type": "chunk", "parent_type": "note", "parent_id": "n_other", "user_id": "u2",
		}},
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "查询", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("应只返回 u1 的笔记，实际 %d 条", len(packets))
	}
}

func TestNoteSource_LongNote_TruncatedInjection(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	longContent := strings.Repeat("这是一段很长的笔记细节内容，包含日期金额决定等大量信息。", 10)
	note := model.Note{ID: "n-long", UserID: "u1", Topic: "长笔记", Content: longContent}
	if err := f.noteRepo.Create(context.Background(), &note); err != nil {
		t.Fatalf("create long note: %v", err)
	}

	f.src.vs = &mockVectorStore{results: []SearchResult{
		fullNoteResult("n-long", 0.75),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "长笔记", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("应返回 1 条笔记，实际 %d 条", len(packets))
	}

	// 注入文本应被中间截断并提示可调 get_note
	if !strings.Contains(packets[0].Text, "…") {
		t.Errorf("注入文本应被中间截断（含 …），实际: %.80s", packets[0].Text)
	}
	if !strings.Contains(packets[0].Text, "get_note") {
		t.Errorf("注入文本应提示可调 get_note 获取全文，实际: %.80s", packets[0].Text)
	}
	if strings.Contains(packets[0].Text, longContent) {
		t.Error("注入文本不应包含完整超长内容")
	}

	// Meta.Content 必须保留全文（供后续处理/取全量）
	meta, ok := packets[0].Meta.(EntityMeta)
	if !ok {
		t.Fatalf("Meta should be EntityMeta, got %T", packets[0].Meta)
	}
	if meta.Content != longContent {
		t.Error("Meta.Content 应保留完整内容")
	}
}

func TestNoteSource_ShortNote_NotTruncated(t *testing.T) {
	f := setupNoteSource(t)
	defer f.cleanup()
	note := model.Note{ID: "n-short", UserID: "u1", Topic: "短笔记", Content: "这是条短笔记"}
	if err := f.noteRepo.Create(context.Background(), &note); err != nil {
		t.Fatalf("create short note: %v", err)
	}

	f.src.vs = &mockVectorStore{results: []SearchResult{
		fullNoteResult("n-short", 0.75),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "短笔记", UserID: "u1", ConversationID: "c1", Note: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("应返回 1 条笔记，实际 %d 条", len(packets))
	}
	if !strings.Contains(packets[0].Text, "这是条短笔记") {
		t.Errorf("短笔记不应被截断，实际: %s", packets[0].Text)
	}
	if strings.Contains(packets[0].Text, "已截断") {
		t.Error("短笔记不应出现截断提示")
	}
}
