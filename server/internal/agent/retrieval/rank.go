package retrieval

import (
	"math"
	"sort"
	"time"
)

// scoreInput — 检索打分输入
type scoreInput struct {
	ID          string
	Content     string
	CreatedAt   time.Time
	VectorScore float64
}

// scoreResult — 检索打分结果
type scoreResult struct {
	ID           string
	VectorScore  float64
	KeywordScore float64
	Freshness    float64
	FinalScore   float64
}

// Ranker — 检索结果排序器，支持自定义权重和时间衰减
type Ranker struct {
	VectorWeight    float64 // 向量相似度权重，默认 0.5
	KeywordWeight   float64 // 关键词匹配权重，默认 0.3
	FreshnessWeight float64 // 新鲜度权重，默认 0.2
	FreshnessDecay  float64 // 时间衰减系数，>0 时启用，默认 0
}

// DefaultRanker 返回默认权重的 Ranker（不含时间衰减）
func DefaultRanker() *Ranker {
	return &Ranker{VectorWeight: 0.5, KeywordWeight: 0.3, FreshnessWeight: 0.2, FreshnessDecay: 0}
}

// Score 对候选项打分并降序排列
func (r *Ranker) Score(query string, items []scoreInput) []scoreResult {
	queryBigrams := bigramSet(query)
	now := time.Now()

	results := make([]scoreResult, len(items))
	for i, item := range items {
		ks := bigramOverlap(queryBigrams, item.Content)
		freshness := 1.0
		if r.FreshnessDecay > 0 {
			daysAgo := now.Sub(item.CreatedAt).Hours() / 24
			freshness = 1.0 / (1.0 + daysAgo*r.FreshnessDecay)
		}
		finalScore := item.VectorScore*r.VectorWeight + ks*r.KeywordWeight + freshness*r.FreshnessWeight
		results[i] = scoreResult{
			ID:           item.ID,
			VectorScore:  item.VectorScore,
			KeywordScore: math.Round(ks*10000) / 10000,
			Freshness:    math.Round(freshness*10000) / 10000,
			FinalScore:   math.Round(finalScore*10000) / 10000,
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].FinalScore > results[j].FinalScore
	})

	return results
}
