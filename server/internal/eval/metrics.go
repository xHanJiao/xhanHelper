package eval

import (
	"math"
	"sort"
)

// QueryMetrics 单条 query 的评测指标
type QueryMetrics struct {
	ReciprocalRank float64 `json:"reciprocal_rank"`
	NDCGAt5        float64 `json:"ndcg_at5"`
	NDCGAt10       float64 `json:"ndcg_at10"`
	RecallAt5      float64 `json:"recall_at5"`
	RecallAt10     float64 `json:"recall_at10"`
	PrecisionAt5   float64 `json:"precision_at5"`
	PrecisionAt10  float64 `json:"precision_at10"`
}

// SummaryMetrics 全部 query 汇总指标（各 query 指标的平均值）
type SummaryMetrics struct {
	RecallAt5     float64 `json:"recall_at5"`
	RecallAt10    float64 `json:"recall_at10"`
	PrecisionAt5  float64 `json:"precision_at5"`
	PrecisionAt10 float64 `json:"precision_at10"`
	NDCGAt5       float64 `json:"ndcg_at5"`
	NDCGAt10      float64 `json:"ndcg_at10"`
	MRR           float64 `json:"mrr"`
}

// computeQueryMetrics 对一条 query 的完整检索排名计算各项指标。
// gt 是 ground_truth，key 为 corpusID，value 为 0~4 的相关度分数。
func computeQueryMetrics(ranked []SearchResult, gt map[string]int) QueryMetrics {
	m := QueryMetrics{}

	// MRR: 第一个 GT≥3 的排名倒数
	for i, r := range ranked {
		if gt[r.CorpusID] >= 3 {
			m.ReciprocalRank = 1.0 / float64(i+1)
			break
		}
	}

	m.NDCGAt5 = computeNDCG(ranked, gt, 5)
	m.NDCGAt10 = computeNDCG(ranked, gt, 10)
	m.RecallAt5, m.PrecisionAt5 = computeRecallPrecision(ranked, gt, 5)
	m.RecallAt10, m.PrecisionAt10 = computeRecallPrecision(ranked, gt, 10)

	return m
}

// computeNDCG 计算 NDCG@K。五档权重：0→0, 1→1, 2→3, 3→7, 4→15。
func computeNDCG(ranked []SearchResult, gt map[string]int, k int) float64 {
	gainTable := []float64{0, 1, 3, 7, 15}

	dcg := 0.0
	for i := 0; i < k && i < len(ranked); i++ {
		rel := gt[ranked[i].CorpusID]
		if rel < 0 || rel > 4 {
			rel = 0
		}
		gain := gainTable[rel]
		dcg += gain / math.Log2(float64(i)+2)
	}

	// ideal DCG：取 GT 中所有 corpus 的相关度，降序排列，取前 k 个
	sorted := make([]int, 0, len(gt))
	for _, rel := range gt {
		sorted = append(sorted, rel)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	if k > len(sorted) {
		k = len(sorted)
	}

	idcg := 0.0
	for i := 0; i < k; i++ {
		if sorted[i] <= 0 {
			break
		}
		gain := gainTable[sorted[i]]
		idcg += gain / math.Log2(float64(i)+2)
	}

	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// computeRecallPrecision 计算 Recall@K 和 Precision@K。
// 相关定义为 GT ≥ 3。
func computeRecallPrecision(ranked []SearchResult, gt map[string]int, k int) (float64, float64) {
	totalRelevant := 0
	for _, rel := range gt {
		if rel >= 3 {
			totalRelevant++
		}
	}

	hit := 0
	for i := 0; i < k && i < len(ranked); i++ {
		if gt[ranked[i].CorpusID] >= 3 {
			hit++
		}
	}

	recall := float64(hit) / float64(totalRelevant)
	if totalRelevant == 0 {
		recall = 1.0 // 无相关 corpus 时，recall 定义为 1（无漏检）
	}

	precision := float64(hit) / float64(k)

	return recall, precision
}
