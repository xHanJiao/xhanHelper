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

func setupMemorySource(t *testing.T) (*MemorySource, func()) {
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
	return &MemorySource{
		memRepo:       memRepo,
		chunkRepo:     chunkRepo,
		embeddingRepo: embeddingRepo,
		chunkBuilder:  &stubChunkBuilder{},
		ranker:        DefaultRanker(),
	}, func() {}
}

// 构造一个 memory chunk SearchResult
func memChunkResult(chunkID, memID string, score float64) SearchResult {
	return SearchResult{
		ID:    chunkID,
		Score: score,
		Metadata: map[string]string{
			"type":        "chunk",
			"parent_type": "memory",
			"parent_id":   memID,
			"user_id":     "u1",
		},
	}
}

// 构造一个全量 memory 嵌入 SearchResult
func fullMemResult(memID string, score float64) SearchResult {
	return SearchResult{
		ID:    "mem:" + memID,
		Score: score,
		Metadata: map[string]string{
			"type":    "mem",
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

// 用例1：同一记忆多 chunk 命中，去重后只返回 1 条记忆
func TestMemorySource_ChunkDedup_SameMemory(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	// m1 的两个 chunk 都高分命中
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		memChunkResult("chunk:memory:m1/1", "m1", 0.82),
	}}

	packets, nearMiss, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
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

// 用例2：多个不同记忆的 chunk 命中
func TestMemorySource_ChunkHits_DifferentMemories(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		memChunkResult("chunk:memory:m2/0", "m2", 0.78),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望返回 2 条记忆，实际 %d 条", len(packets))
	}
}

// 用例3：部分 chunk 低于阈值，去重后只产生一条 nearMiss
func TestMemorySource_ChunkBelowThreshold_DedupedNearMiss(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	// m1 的两个 chunk 都低于阈值 0.7
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.55),
		memChunkResult("chunk:memory:m1/1", "m1", 0.52),
	}}

	packets, nearMiss, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.7, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("低于阈值不应有命中，实际 %d 条", len(packets))
	}
	// 去重后 m1 只产生 1 条 nearMiss
	if len(nearMiss) != 1 {
		t.Errorf("同记忆的 2 个低分 chunk 去重后应只产生 1 条 nearMiss，实际 %d 条", len(nearMiss))
	}
	if nearMiss[0].ID != "m1" {
		t.Errorf("nearMiss ID 应为 m1，实际 %s", nearMiss[0].ID)
	}
}

// 用例4：chunk 无命中，回退到全量 memory 嵌入
func TestMemorySource_Fallback_FullMemoryEmbedding(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)

	// 没有 chunk 结果，只有全量 memory 嵌入
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		fullMemResult("m1", 0.75),
		fullMemResult("m2", 0.65),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("回退路径应返回 2 条记忆，实际 %d 条", len(packets))
	}
}

// 用例5：chunk 部分命中 + 全量嵌入补充不足的数量
func TestMemorySource_ChunkPlusFallback(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	// m1 通过 chunk 命中，m2 通过全量嵌入补充
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		fullMemResult("m2", 0.72),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "运动", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("期望 2 条记忆（chunk+fallback），实际 %d 条", len(packets))
	}
}

// 用例6：chunk 和全量嵌入命中同一个记忆，不重复返回
func TestMemorySource_NoDuplicate_ChunkAndFullHit(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	// m1 同时被 chunk 和全量嵌入命中，只返回一次
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		fullMemResult("m1", 0.60),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("m1 不应重复返回，期望 1 条，实际 %d 条", len(packets))
	}
}

// 用例7：返回结果附带匹配 chunk 片段
func TestMemorySource_MatchedChunkSnippets(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "啤酒", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(packets[0].Text, "匹配片段") {
		t.Error("返回文本应包含匹配的 chunk 片段")
	}
	if !strings.Contains(packets[0].Text, "精酿啤酒") {
		t.Error("返回文本应包含 chunk 内容")
	}
}

// 用例8：向量库空结果
func TestMemorySource_EmptyResults(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()

	src.vectorStore = &mockVectorStore{results: nil}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "任意查询", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Errorf("空结果应返回 0 条，实际 %d 条", len(packets))
	}
}

// 用例9：其他用户的 chunk 被过滤掉
func TestMemorySource_FilterOtherUsers(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	// 包含 u2 的 chunk，不应被返回
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.85),
		{ID: "chunk:memory:m_other/0", Score: 0.90, Metadata: map[string]string{
			"type": "chunk", "parent_type": "memory", "parent_id": "m_other", "user_id": "u2",
		}},
	}}

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "查询", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 5})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Errorf("应只返回 u1 的记忆，实际 %d 条", len(packets))
	}
}

// 用例10：limit 限制生效，命中数超过 limit 时截断
func TestMemorySource_LimitTruncation(t *testing.T) {
	src, cleanup := setupMemorySource(t)
	defer cleanup()
	seedMemories(t, src.memRepo)
	seedMemChunks(t, src.chunkRepo)

	// 3 个不同记忆的 chunk 命中，但 limit=2
	src.vectorStore = &mockVectorStore{results: []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.90),
		memChunkResult("chunk:memory:m2/0", "m2", 0.85),
		memChunkResult("chunk:memory:m3/0", "m3", 0.80),
	}}
	// seed m3 too
	src.memRepo.Create(context.Background(), &model.UserMemory{
		ID: "m3", UserID: "u1", MemoryKey: "m3", ContentHash: "h3", MemoryValue: "m3",
	})

	packets, _, err := src.Fetch(context.Background(), SearchOptions{Embedding: nil, Query: "查询", UserID: "u1", ConversationID: "c1", MemoryThreshold: 0.5, MemoryLimit: 2})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(packets) != 2 {
		t.Errorf("limit=2 应返回 2 条，实际 %d 条", len(packets))
	}
}
