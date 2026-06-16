package eval

import "sort"

// scoredCandidate 语料 ID 与相似度分数的配对，供 labeler 的余弦预筛使用。
type scoredCandidate struct {
	ID    string
	Score float64
}

// cosineFilter 计算 query 与全部语料向量的余弦相似度，返回 top K 结果。
// 供 labeler 在 LLM 评分前预筛候选语料使用。
func cosineFilter(queryVec []float64, corpusVecs []corpusVec, topK int) []scoredCandidate {
	results := make([]scoredCandidate, 0, len(corpusVecs))

	for _, cv := range corpusVecs {
		score := cosine(queryVec, cv.Vec)
		results = append(results, scoredCandidate{ID: cv.ID, Score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if topK > len(results) {
		topK = len(results)
	}
	return results[:topK]
}
