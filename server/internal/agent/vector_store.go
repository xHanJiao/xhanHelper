package agent

import (
	"log"
	"math"
	"sync"
)

// vectorEntry — 向量存储条目
type vectorEntry struct {
	ID    string    // 外部 ID (memoryID / messageID / noteID)
	Vec   []float64
	Score float64 // 检索时使用
}

// VectorStore — 内存向量存储 + 余弦检索
type VectorStore struct {
	mu      sync.RWMutex
	vectors []vectorEntry // O(n) 暴力搜索，n < 5000 时够用
}

// NewVectorStore 创建向量存储
func NewVectorStore() *VectorStore {
	return &VectorStore{}
}

// Upsert 插入或更新向量
func (vs *VectorStore) Upsert(id string, vec []float64) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	for i, e := range vs.vectors {
		if e.ID == id {
			vs.vectors[i].Vec = vec
			return
		}
	}
	vs.vectors = append(vs.vectors, vectorEntry{ID: id, Vec: vec})
}

// Search 余弦相似度检索，返回 topK 的 ID 和 score
func (vs *VectorStore) Search(query []float64, topK int) []struct {
	ID    string
	Score float64
} {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if len(vs.vectors) == 0 {
		return nil
	}

	// 计算余弦相似度
	results := make([]vectorEntry, len(vs.vectors))
	for i, e := range vs.vectors {
		results[i] = vectorEntry{ID: e.ID, Score: cosineSimilarity(query, e.Vec)}
	}

	// 部分排序取 topK
	topN := topK
	if topN > len(results) {
		topN = len(results)
	}
	for i := 0; i < topN; i++ {
		best := i
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[best].Score {
				best = j
			}
		}
		results[i], results[best] = results[best], results[i]
	}

	out := make([]struct {
		ID    string
		Score float64
	}, topN)
	for i := 0; i < topN; i++ {
		out[i].ID = results[i].ID
		out[i].Score = results[i].Score
	}

	log.Printf("[vector] search: topK=%d/%d, best=%.4f", topK, len(vs.vectors), out[0].Score)
	return out
}

// Size 返回向量数量
func (vs *VectorStore) Size() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.vectors)
}

// cosineSimilarity 余弦相似度
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
