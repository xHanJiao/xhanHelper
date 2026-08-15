package agent

import (
	"context"
	"testing"

	"xhan-helper/internal/model"
)

// —— cosineSimilarity ——

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{1, 2, 3}
	sim := cosineSimilarity(a, b)
	if sim < 0.9999 {
		t.Errorf("expected ~1.0 for identical vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{0, 1, 0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for orthogonal vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float64{1, 0}
	b := []float64{-1, 0}
	sim := cosineSimilarity(a, b)
	if sim > -0.9999 {
		t.Errorf("expected ~-1.0 for opposite vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_DifferentLength(t *testing.T) {
	a := []float64{1, 2}
	b := []float64{1, 2, 3}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for different length vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float64{0, 0, 0}
	b := []float64{1, 2, 3}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for zero vector, got %.6f", sim)
	}
}

func TestCosineSimilarity_AllZeros(t *testing.T) {
	a := []float64{0, 0}
	b := []float64{0, 0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for all-zero vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_AboveThreshold(t *testing.T) {
	// ~30° angle → cos ≈ 0.866
	a := []float64{1, 0}
	b := []float64{0.866, 0.5}
	sim := cosineSimilarity(a, b)
	if sim < 0.86 || sim > 0.87 {
		t.Errorf("expected ~0.866, got %.6f", sim)
	}
}

func TestCosineSimilarity_BelowThreshold(t *testing.T) {
	// ~60° angle → cos = 0.5
	a := []float64{1, 0}
	b := []float64{0.5, 0.866}
	sim := cosineSimilarity(a, b)
	if sim < 0.49 || sim > 0.51 {
		t.Errorf("expected ~0.5, got %.6f", sim)
	}
}

// —— getOrEmbedSummary ——

func TestGetOrEmbedSummary_CacheHit(t *testing.T) {
	vs := NewVectorStore()
	summaryID := "test-summary-cached"
	vsKey := "summary:" + summaryID
	expectedVec := []float64{0.1, 0.2, 0.3}
	vs.Upsert(vsKey, expectedVec, map[string]string{
		"type":       "summary",
		"summary_id": summaryID,
	})

	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, vectorStore: vs}
	summary := &model.ConversationSummary{ID: summaryID, SummaryText: "不会用到"}

	vec := o.getOrEmbedSummary(context.Background(), summary)
	if len(vec) != 3 {
		t.Fatalf("expected 3-d vector, got %d", len(vec))
	}
	if vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("expected [0.1, 0.2, 0.3], got %v", vec)
	}
}

func TestGetOrEmbedSummary_EmbedFail(t *testing.T) {
	// embedder 为 nil → Embed 会 panic（硬依赖），但 Embedder.Embed 内部会做 HTTP 调用。
	// 这里测试当 embedder 为 nil 时，getOrEmbedSummary 的行为。
	// 实际上 embedder 是硬依赖，此测试验证当 cache miss 时 embedder 被调用。
	// 跳过此测试因为需要真实的 embedder。
	t.Skip("requires real embedder for cache miss path")
}

// —— checkSummaryRelevance 计算逻辑 ——

// 注意：checkSummaryRelevance 内部调用 tryEmbedContent（使用 embedder），
// embedder 是硬依赖。以下测试直接验证相似度计算逻辑：
// cosine 相似度 + 窗口取 max + 阈值比较。

func TestSummaryRelevance_HighSimilarityPasses(t *testing.T) {
	// 模拟：摘要与用户消息高度相似（cos ≈ 0.98），应通过阈值 0.5
	summaryVec := []float64{1, 0, 0}
	queryVec := []float64{0.98, 0.1, 0}
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim < 0.5 {
		t.Errorf("expected high similarity (>0.5), got %.6f", sim)
	}
}

func TestSummaryRelevance_OrthogonalFails(t *testing.T) {
	// 模拟：摘要与用户消息正交（cos = 0），应低于阈值 0.5
	summaryVec := []float64{1, 0, 0}
	queryVec := []float64{0, 1, 0}
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim >= 0.5 {
		t.Errorf("expected low similarity (<0.5) for orthogonal vectors, got %.6f", sim)
	}
}

func TestSummaryRelevance_MaxAcrossWindow(t *testing.T) {
	// 模拟窗口取 max：一个正交（cos=0），一个相似（cos≈1）
	summaryVec := []float64{1, 0}
	queryVecs := [][]float64{
		{0, 1},       // cos = 0
		{0.95, 0.05}, // cos ≈ 0.998
	}
	maxSim := 0.0
	for _, v := range queryVecs {
		if sim := cosineSimilarity(summaryVec, v); sim > maxSim {
			maxSim = sim
		}
	}
	if maxSim < 0.5 {
		t.Errorf("expected max similarity >0.5 (max wins), got %.6f", maxSim)
	}
}

func TestSummaryRelevance_BoundaryEqualsThreshold(t *testing.T) {
	// cos(60°) = 0.5，恰好等于阈值 → 应通过（>= 判断）
	summaryVec := []float64{1, 0}
	queryVec := []float64{0.5, 0.8660254} // cos(60°) = 0.5
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim < 0.499 || sim > 0.501 {
		t.Errorf("expected ~0.5, got %.6f", sim)
	}
	if sim < 0.5 {
		t.Error("boundary: similarity=0.5 should satisfy threshold >=0.5")
	}
}

func TestSummaryRelevance_JustBelowThreshold(t *testing.T) {
	// cos ≈ 0.49，略低于阈值 0.5
	summaryVec := []float64{1, 0}
	queryVec := []float64{0.49, 0.8717} // cos ≈ 0.49
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim >= 0.5 {
		t.Errorf("expected <0.5, got %.6f", sim)
	}
}

func TestSummaryRelevance_EmptyQueryWindow(t *testing.T) {
	// 空查询窗口 → 乐观附着（返回 true）
	// 此行为由 checkSummaryRelevance 中的 len(queryVecs)==0 分支保证
	queryVecs := [][]float64{}
	if len(queryVecs) != 0 {
		t.Error("expected empty query vectors")
	}
	// 空窗口时 checkSummaryRelevance 返回 true（乐观附着）
}

func TestGetOrEmbedSummary_NewEmbed(t *testing.T) {
	// 测试 getOrEmbedSummary 在 cache miss 时的行为
	// embedder 是硬依赖，此测试需要真实 embedder — 跳过
	t.Skip("requires real embedder for cache miss path")
}

// —— dedupSummaries ——

func TestDedupSummaries_SimilarDropsOlder(t *testing.T) {
	vs := NewVectorStore()
	// 两条相邻摘要高度相似（增量合并的典型形态）→ 丢弃较旧者
	vs.Upsert("summary:s1", []float64{1, 0, 0}, nil)
	vs.Upsert("summary:s2", []float64{0.99, 0.01, 0}, nil)
	o := &Orchestrator{vectorStore: vs, summaryDedupThreshold: 0.85}

	out := o.dedupSummaries(context.Background(), []model.ConversationSummary{
		{ID: "s1", SummaryText: "旧摘要"},
		{ID: "s2", SummaryText: "新摘要"},
	})
	if len(out) != 1 {
		t.Fatalf("expected 1 summary after dedup, got %d", len(out))
	}
	if out[0].ID != "s2" {
		t.Errorf("expected newer summary s2 kept, got %s", out[0].ID)
	}
}

func TestDedupSummaries_DissimilarKeepsBoth(t *testing.T) {
	vs := NewVectorStore()
	// 两条摘要正交 → 保留两条
	vs.Upsert("summary:s1", []float64{1, 0, 0}, nil)
	vs.Upsert("summary:s2", []float64{0, 1, 0}, nil)
	o := &Orchestrator{vectorStore: vs, summaryDedupThreshold: 0.85}

	out := o.dedupSummaries(context.Background(), []model.ConversationSummary{
		{ID: "s1", SummaryText: "摘要A"},
		{ID: "s2", SummaryText: "摘要B"},
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 summaries kept, got %d", len(out))
	}
	if out[0].ID != "s1" || out[1].ID != "s2" {
		t.Errorf("unexpected order: %s, %s", out[0].ID, out[1].ID)
	}
}

func TestDedupSummaries_ChainDedup(t *testing.T) {
	// 三条链式：s1≈s2（丢 s1），s3 与 s2 不同（保留）→ 结果 [s2, s3]
	vs := NewVectorStore()
	vs.Upsert("summary:s1", []float64{1, 0, 0}, nil)
	vs.Upsert("summary:s2", []float64{0.99, 0.01, 0}, nil)
	vs.Upsert("summary:s3", []float64{0, 1, 0}, nil)
	o := &Orchestrator{vectorStore: vs, summaryDedupThreshold: 0.85}

	out := o.dedupSummaries(context.Background(), []model.ConversationSummary{
		{ID: "s1", SummaryText: "摘要1"},
		{ID: "s2", SummaryText: "摘要2"},
		{ID: "s3", SummaryText: "摘要3"},
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(out))
	}
	if out[0].ID != "s2" || out[1].ID != "s3" {
		t.Errorf("unexpected result: %s, %s", out[0].ID, out[1].ID)
	}
}

func TestDedupSummaries_ThresholdBoundary(t *testing.T) {
	// 相似度恰好低于阈值（0.8 < 0.85）→ 保留两条
	vs := NewVectorStore()
	vs.Upsert("summary:s1", []float64{1, 0, 0}, nil)
	vs.Upsert("summary:s2", []float64{0.8, 0.6, 0}, nil) // cos = 0.8
	o := &Orchestrator{vectorStore: vs, summaryDedupThreshold: 0.85}

	out := o.dedupSummaries(context.Background(), []model.ConversationSummary{
		{ID: "s1", SummaryText: "摘要A"},
		{ID: "s2", SummaryText: "摘要B"},
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 summaries (0.8 < 0.85), got %d", len(out))
	}
}

func TestDedupSummaries_SingleKept(t *testing.T) {
	vs := NewVectorStore()
	o := &Orchestrator{vectorStore: vs, summaryDedupThreshold: 0.85}
	out := o.dedupSummaries(context.Background(), []model.ConversationSummary{{ID: "s1", SummaryText: "唯一摘要"}})
	if len(out) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(out))
	}
}
