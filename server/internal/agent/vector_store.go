package agent

import (
	"math"
	"sync"

	"xhan-helper/internal/agent/retrieval"
)

// vectorEntry — 向量存储条目
type vectorEntry struct {
	ID       string
	Vec      []float64
	Score    float64
	Metadata map[string]string
}

// candidate — 检索候选条目
type candidate struct {
	id       string
	score    float64
	metadata map[string]string
}

// VectorStore — 内存向量存储 + 余弦检索
type VectorStore struct {
	mu      sync.RWMutex
	vectors []vectorEntry
	byID    map[string]int
}

func NewVectorStore() *VectorStore {
	return &VectorStore{byID: make(map[string]int)}
}

func (vs *VectorStore) Upsert(id string, vec []float64, metadata map[string]string) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if idx, ok := vs.byID[id]; ok {
		vs.vectors[idx].Vec = vec
		if metadata != nil {
			vs.vectors[idx].Metadata = metadata
		}
		return
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	vs.byID[id] = len(vs.vectors)
	vs.vectors = append(vs.vectors, vectorEntry{ID: id, Vec: vec, Metadata: metadata})
}

// Search 余弦相似度检索
func (vs *VectorStore) Search(query []float64, topK int, filter func(retrieval.Entry) bool) []retrieval.SearchResult {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if len(vs.vectors) == 0 {
		return nil
	}

	var eligible []candidate
	for _, e := range vs.vectors {
		if filter != nil && !filter(retrieval.Entry{ID: e.ID, Vec: e.Vec, Metadata: e.Metadata}) {
			continue
		}
		eligible = append(eligible, candidate{
			id:       e.ID,
			score:    cosineSimilarity(query, e.Vec),
			metadata: e.Metadata,
		})
	}

	if len(eligible) == 0 || topK <= 0 {
		return nil
	}

	topN := topK
	if topN > len(eligible) {
		topN = len(eligible)
	}

	result := heapTopK(eligible, topN)
	// log.Printf("[vector] search: topK=%d/%d/%d, best=%.4f", topK, len(eligible), len(vs.vectors), result[0].Score)
	return result
}

func heapTopK(candidates []candidate, k int) []retrieval.SearchResult {
	if k <= 0 || len(candidates) == 0 {
		return nil
	}

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

	out := make([]retrieval.SearchResult, len(heap))
	for i := len(heap) - 1; i >= 0; i-- {
		out[i] = retrieval.SearchResult{
			ID:       heap[0].id,
			Score:    heap[0].score,
			Metadata: heap[0].metadata,
		}
		last := len(heap) - 1
		heap[0] = heap[last]
		heap = heap[:last]
		if len(heap) > 0 {
			heapDown(heap, 0, len(heap))
		}
	}
	return out
}

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

func (vs *VectorStore) GetVector(id string) ([]float64, bool) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	idx, ok := vs.byID[id]
	if !ok {
		return nil, false
	}
	e := vs.vectors[idx]
	vec := make([]float64, len(e.Vec))
	copy(vec, e.Vec)
	return vec, true
}

func (vs *VectorStore) Size() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.vectors)
}

func (vs *VectorStore) Delete(id string) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.deleteLocked(id)
}

func (vs *VectorStore) deleteLocked(id string) {
	idx, ok := vs.byID[id]
	if !ok {
		return
	}
	last := len(vs.vectors) - 1
	if idx < last {
		vs.vectors[idx] = vs.vectors[last]
		vs.byID[vs.vectors[idx].ID] = idx
	}
	vs.vectors = vs.vectors[:last]
	delete(vs.byID, id)
}

// DeleteByMetadata 删除所有 metadata[key] == value 的向量，返回删除数量。
// 先收集待删 ID 再逐个移除（不能边遍历边 swap-remove，索引会错位）。
func (vs *VectorStore) DeleteByMetadata(key, value string) int {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	var ids []string
	for _, e := range vs.vectors {
		if e.Metadata[key] == value {
			ids = append(ids, e.ID)
		}
	}
	for _, id := range ids {
		vs.deleteLocked(id)
	}
	return len(ids)
}

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

func (vs *VectorStore) Stats() map[string]interface{} {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	byType := map[string]int{}
	totalBytes := 0
	for _, e := range vs.vectors {
		t := e.Metadata["type"]
		if t == "" {
			t = "unknown"
		}
		byType[t]++
		totalBytes += len(e.Vec) * 8
	}

	return map[string]interface{}{
		"total_entries":   len(vs.vectors),
		"by_type":         byType,
		"estimated_bytes": totalBytes,
	}
}
