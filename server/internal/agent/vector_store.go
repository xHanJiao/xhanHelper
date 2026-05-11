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

// candidate — 检索候选条目
type candidate struct {
	id    string
	score float64
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
	var eligible []candidate
	for _, e := range vs.vectors {
		if filter != nil && !filter(e) {
			continue
		}
		eligible = append(eligible, candidate{id: e.ID, score: cosineSimilarity(query, e.Vec)})
	}

	if len(eligible) == 0 || topK <= 0 {
		return nil
	}

	topN := topK
	if topN > len(eligible) {
		topN = len(eligible)
	}

	// 使用堆排序取 topK，时间复杂度 O(n log k)
	result := heapTopK(eligible, topN)

	log.Printf("[vector] search: topK=%d/%d/%d, best=%.4f", topK, len(eligible), len(vs.vectors), result[0].Score)
	return result
}

// heapTopK 使用最小堆获取 topK 个最大元素
func heapTopK(candidates []candidate, k int) []struct {
	ID    string
	Score float64
} {
	if k <= 0 || len(candidates) == 0 {
		return nil
	}

	// 最小堆，堆顶是当前 topK 中最小的元素
	heap := make([]candidate, 0, k)

	for _, c := range candidates {
		if len(heap) < k {
			heap = append(heap, c)
			heapUp(heap, len(heap)-1)
		} else if c.score > heap[0].score {
			heap[0] = c
			heapDown(heap, 0, k)
		}
	}

	// 从堆中取出结果并按分数降序排序
	out := make([]struct {
		ID    string
		Score float64
	}, len(heap))
	for i := len(heap) - 1; i >= 0; i-- {
		out[i].ID = heap[0].id
		out[i].Score = heap[0].score
		last := len(heap) - 1
		heap[0] = heap[last]
		heap = heap[:last]
		if len(heap) > 0 {
			heapDown(heap, 0, len(heap))
		}
	}
	return out
}

// heapUp 向上调整堆
func heapUp(heap []candidate, i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if heap[i].score >= heap[parent].score {
			break
		}
		heap[i], heap[parent] = heap[parent], heap[i]
		i = parent
	}
}

// heapDown 向下调整堆
func heapDown(heap []candidate, i, n int) {
	for {
		left := 2*i + 1
		right := 2*i + 2
		smallest := i
		if left < n && heap[left].score < heap[smallest].score {
			smallest = left
		}
		if right < n && heap[right].score < heap[smallest].score {
			smallest = right
		}
		if smallest == i {
			break
		}
		heap[i], heap[smallest] = heap[smallest], heap[i]
		i = smallest
	}
}

// Size 返回向量数量
func (vs *VectorStore) Size() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.vectors)
}

// Delete 从向量库中移除指定 id 的条目
func (vs *VectorStore) Delete(id string) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	for i, e := range vs.vectors {
		if e.ID == id {
			vs.vectors = append(vs.vectors[:i], vs.vectors[i+1:]...)
			return
		}
	}
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
