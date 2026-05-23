package agent

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

// scoreItems 统一检索打分：向量 0.5 + 关键词 0.3 + 时间衰减 0.2
// decayRate=0 时 freshness 恒为 1.0（笔记等无时效需求的场景）
func scoreItems(query string, items []scoreInput, decayRate float64) []scoreResult {
	queryBigrams := bigramSet(query)
	now := time.Now()

	results := make([]scoreResult, len(items))
	for i, item := range items {
		ks := bigramOverlap(queryBigrams, item.Content)
		freshness := 1.0
		if decayRate > 0 {
			daysAgo := now.Sub(item.CreatedAt).Hours() / 24
			freshness = 1.0 / (1.0 + daysAgo*decayRate)
		}
		finalScore := item.VectorScore*0.5 + ks*0.3 + freshness*0.2
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
