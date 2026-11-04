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

type memoryFixture struct {
	src       *EntitySource[model.UserMemory]
	db        *gorm.DB
	memRepo   *service.UserMemoryRepo
	chunkRepo *service.EmbeddingChunkRepo
	cleanup   func()
}

func setupMemorySource(t *testing.T) *memoryFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}, &model.EmbeddingChunk{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	memRepo := service.NewUserMemoryRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	return &memoryFixture{
		src: &EntitySource[model.UserMemory]{
			name:          model.EntityTypeMemory,
			entityType:    model.EntityTypeMemory,
			fullType:      model.VectorTypeMemory,
			idPrefix:      model.VectorPrefixMemory,
			decayer:       NewTimeDecay(0),
			repo:          memRepo,
			embeddingRepo: embeddingRepo,
			chunkRepo:     chunkRepo,
			chunkBuilder:  &stubChunkBuilder{},
			configPicker:  func(o SearchOptions) SourceConfig { return o.Memory },
		},
		memRepo:   memRepo,
		db:        db,
		chunkRepo: chunkRepo,
		cleanup:   func() {},
	}
}

func memChunkResult(chunkID, memID string, score float64) SearchResult {
	return SearchResult{
		ID:    chunkID,
		Score: score,
		Metadata: map[string]string{
			"type":        model.VectorTypeChunk,
			"parent_type": model.EntityTypeMemory,
			"parent_id":   memID,
			"user_id":     "u1",
		},
	}
}

func fullMemResult(memID string, score float64) SearchResult {
	return SearchResult{
		ID:    model.VectorPrefixMemory + memID,
		Score: score,
		Metadata: map[string]string{
			"type":    model.VectorTypeMemory,
			"user_id": "u1",
		},
	}
}

func seedMemories(t *testing.T, repo *service.UserMemoryRepo) {
	t.Helper()
	memories := []model.UserMemory{
		{ID: "m1", UserID: "u1", MemoryKey: "饮酒偏好", ContentHash: "h1", MemoryValue: "用户喜欢喝精酿啤酒，每周约2-3次"},
		{ID: "m2", UserID: "u1", MemoryKey: "健身习惯", ContentHash: "h2", MemoryValue: "用户每周跑步3次，坚持了半年"},
		{ID: "m3", UserID: "u1", MemoryKey: "预算管理", ContentHash: "h3", MemoryValue: "用户每月餐饮预算2000元"},
	}
	for _, m := range memories {
		if err := repo.Create(context.Background(), &m); err != nil {
			t.Fatalf("seed memory %s: %v", m.ID, err)
		}
	}
}

func seedMemChunks(t *testing.T, repo *service.EmbeddingChunkRepo) {
	t.Helper()
	chunks := []model.EmbeddingChunk{
		{ID: "chunk:memory:m1/0", ParentType: "memory", ParentID: "m1", ChunkIndex: 0, Content: "喜欢喝精酿啤酒"},
		{ID: "chunk:memory:m1/1", ParentType: "memory", ParentID: "m1", ChunkIndex: 1, Content: "每周约2-3次"},
		{ID: "chunk:memory:m2/0", ParentType: "memory", ParentID: "m2", ChunkIndex: 0, Content: "每周跑步3次"},
	}
	if err := repo.BatchCreate(context.Background(), chunks); err != nil {
		t.Fatalf("seed mem chunks: %v", err)
	}
}

// ---- 测试用例 ----

func TestMemorySource_ChunkDedup_SameMemory(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	seedMemChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		memChunkResult("chunk:memory:m1/1", "m1", 0.82),
	}}

	packets, nearMiss, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("去重后应返回 1 条记忆，实际 %d 条", len(packets))
	}
	if packets[0].ID != "m1" {
		t.Errorf("期望命中 m1，实际 %s", packets[0].ID)
	}
	if !strings.Contains(packets[0].Text, "精酿啤酒") {
		t.Error("返回文本应包含记忆内容")
	}
	if len(nearMiss) != 0 {
		t.Errorf("不应有 nearMiss，实际 %d 条", len(nearMiss))
	}
}

func TestMemorySource_ChunkHits_DifferentMemories(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	seedMemChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		memChunkResult("chunk:memory:m2/0", "m2", 0.78),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望返回 2 条记忆，实际 %d 条", len(packets))
	}
}

func TestMemorySource_ChunkBelowThreshold_DedupedNearMiss(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	seedMemChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.55),
		memChunkResult("chunk:memory:m1/1", "m1", 0.52),
	}}

	packets, nearMiss, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.7, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("低于阈值不应有命中，实际 %d 条", len(packets))
	}
	if len(nearMiss) != 1 {
		t.Errorf("同记忆的 2 个低分 chunk 去重后应只产生 1 条 nearMiss，实际 %d 条", len(nearMiss))
	}
	if nearMiss[0].ID != "m1" {
		t.Errorf("nearMiss ID 应为 m1，实际 %s", nearMiss[0].ID)
	}
}

func TestMemorySource_Fallback_FullMemoryEmbedding(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		fullMemResult("m1", 0.75),
		fullMemResult("m2", 0.65),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("回退路径应返回 2 条记忆，实际 %d 条", len(packets))
	}
}

func TestMemorySource_ChunkPlusFallback(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	seedMemChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		fullMemResult("m2", 0.72),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望 2 条记忆（chunk+fallback），实际 %d 条", len(packets))
	}
}

func TestMemorySource_NoDuplicate_ChunkAndFullHit(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	seedMemChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		fullMemResult("m1", 0.60),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("m1 不应重复返回，期望 1 条，实际 %d 条", len(packets))
	}
}

func TestMemorySource_EmptyResults(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()

	f.src.vs = &mockVectorStore{results: nil}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "任意查询", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("空结果应返回 0 条，实际 %d 条", len(packets))
	}
}

func TestMemorySource_FilterOtherUsers(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	seedMemChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		{ID: "chunk:memory:m_other/0", Score: 0.90, Metadata: map[string]string{
			"type": model.VectorTypeChunk, "parent_type": model.EntityTypeMemory, "parent_id": "m_other", "user_id": "u2",
		}},
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "查询", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 5}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("应只返回 u1 的记忆，实际 %d 条", len(packets))
	}
}

func TestMemorySource_LimitTruncation(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	seedMemChunks(t, f.chunkRepo)

	f.src.vs = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.90),
		memChunkResult("chunk:memory:m2/0", "m2", 0.85),
		memChunkResult("chunk:memory:m3/0", "m3", 0.80),
	}}
	f.memRepo.Create(context.Background(), &model.UserMemory{
		ID: "m3", UserID: "u1", MemoryKey: "m3", ContentHash: "h3", MemoryValue: "m3",
	})

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "查询", UserID: "u1", ConversationID: "c1", Memory: SourceConfig{Threshold: 0.5, Limit: 2}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("limit=2 应返回 2 条，实际 %d 条", len(packets))
	}
}

func TestMemorySource_ExcludesEventOwnedMemories(t *testing.T) {
	f := setupMemorySource(t)
	defer f.cleanup()
	seedMemories(t, f.memRepo)
	// 把 m1 挂到事件上：普通记忆块必须跳过它（只经事件块注入）
	eid := "e1"
	if err := f.db.Model(&model.UserMemory{ID: "m1"}).Update("event_id", eid).Error; err != nil {
		t.Fatalf("set event id: %v", err)
	}
	f.src.filter = func(m model.UserMemory) bool { return m.EventID == nil }

	f.src.vs = &mockVectorStore{results: []SearchResult{
		fullMemResult("m1", 0.9),
		fullMemResult("m2", 0.8),
	}}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Memory: SourceConfig{Threshold: 0.5, Limit: 5},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 1 || packets[0].ID != "m2" {
		t.Fatalf("event-owned memory must be filtered out, got %+v", packets)
	}
}
