package agent

import "xhan-helper/internal/tool"

// VectorSearchAdapter — 将 agent.VectorStore 适配为 tool.VectorSearcher 接口 + agent.VectorStorer 接口
type VectorSearchAdapter struct {
	store *VectorStore
}

func NewVectorSearchAdapter(store *VectorStore) *VectorSearchAdapter {
	return &VectorSearchAdapter{store: store}
}

// ===== tool.VectorSearcher =====

func (a *VectorSearchAdapter) Search(query []float64, topK int, filter func(id string, vec []float64, metadata map[string]string) bool) []tool.VectorHit {
	results := a.store.Search(query, topK, func(e vectorEntry) bool {
		return filter(e.ID, e.Vec, e.Metadata)
	})
	hits := make([]tool.VectorHit, len(results))
	for i, r := range results {
		hits[i] = tool.VectorHit{ID: r.ID, Score: r.Score}
	}
	return hits
}

func (a *VectorSearchAdapter) Size() int {
	return a.store.Size()
}

// ===== agent.VectorStorer (for ThemeDiscoverer dedup) =====

func (a *VectorSearchAdapter) Upsert(id string, vec []float64, metadata map[string]string) {
	a.store.Upsert(id, vec, metadata)
}

// VecSearch 实现 VectorStorer.Search（返回 VectorSearchHit）
func (a *VectorSearchAdapter) VecSearch(query []float64, topK int, filter func(id string, vec []float64, metadata map[string]string) bool) []VectorSearchHit {
	results := a.store.Search(query, topK, func(e vectorEntry) bool {
		return filter(e.ID, e.Vec, e.Metadata)
	})
	hits := make([]VectorSearchHit, len(results))
	for i, r := range results {
		hits[i] = VectorSearchHit{ID: r.ID, Score: r.Score}
	}
	return hits
}
