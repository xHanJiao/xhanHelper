package agent

import (
	"log"
	"math"
	"sync"
)

// vectorEntry — 向量存储条目
type vectorEntry struct {
	ID       string // 外部 ID (memoryID / messageID / noteID)
	Vec      []float64
	Score    float64           // 检索时使用
	Metadata map[string]string // 业务元数据 (type, conv_id 等)
}

// FilterFunc — 向量检索时的过滤函数，返回 true 表示该条目纳入候选
type FilterFunc func(entry vectorEntry) bool

// VectorStore — 内存向量存储 + 余弦检索
type VectorStore struct {
	mu      sync.RWMutex
	vectors []vectorEntry // O(n) 暴力搜索，n < 5000 时够用
}

// NewVectorStore 创建向量存储
func NewVectorStore() *VectorStore {
	return &VectorStore{}
}

// Upsert 插入或更新向量，metadata 可选（传 nil 则初始化为空 map）
func (vs *VectorStore) Upsert(id string, vec []float64, metadata map[string]string) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	for i, e := range vs.vectors {
		if e.ID == id {
			vs.vectors[i].Vec = vec
			if metadata != nil {
				vs.vectors[i].Metadata = metadata
			}
			return
		}
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	vs.vectors = append(vs.vectors, vectorEntry{ID: id, Vec: vec, Metadata: metadata})
}

// Search 余弦相似度检索，从通过 filter 的条目中返回 topK
// filter 为 nil 时不做过滤
func (vs *VectorStore) Search(query []float64, topK int, filter FilterFunc) []struct {
	ID    string
	Score float64
} {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if len(vs.vectors) == 0 {
		return nil
	}

	// 计算余弦相似度，同时过滤
	type candidate struct {
		idx   int
		score float64
	}
	var eligible []candidate
	for i, e := range vs.vectors {
		if filter != nil && !filter(e) {
			continue
		}
		eligible = append(eligible, candidate{idx: i, score: cosineSimilarity(query, e.Vec)})
	}

	if len(eligible) == 0 {
		return nil
	}

	// 部分排序取 topK
	topN := topK
	if topN > len(eligible) {
		topN = len(eligible)
	}
	for i := 0; i < topN; i++ {
		best := i
		for j := i + 1; j < len(eligible); j++ {
			if eligible[j].score > eligible[best].score {
				best = j
			}
		}
		eligible[i], eligible[best] = eligible[best], eligible[i]
	}

	out := make([]struct {
		ID    string
		Score float64
	}, topN)
	for i := 0; i < topN; i++ {
		out[i].ID = vs.vectors[eligible[i].idx].ID
		out[i].Score = eligible[i].score
	}

	log.Printf("[vector] search: topK=%d/%d/%d, best=%.4f", topK, len(eligible), len(vs.vectors), out[0].Score)
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
