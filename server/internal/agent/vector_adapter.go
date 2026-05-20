package agent

import "xhan-helper/internal/app"

// VectorSearchAdapter 将 agent.VectorStore 适配为 app.VectorStore 接口。
type VectorSearchAdapter struct {
	store *VectorStore
}

func NewVectorSearchAdapter(store *VectorStore) *VectorSearchAdapter {
	return &VectorSearchAdapter{store: store}
}

func (a *VectorSearchAdapter) Upsert(id string, vec []float64, metadata map[string]string) {
	a.store.Upsert(id, vec, metadata)
}

func (a *VectorSearchAdapter) Search(query []float64, topK int, filter app.VectorFilter) []app.VectorMatch {
	results := a.store.Search(query, topK, func(e vectorEntry) bool {
		return filter(e.ID, e.Vec, e.Metadata)
	})
	matches := make([]app.VectorMatch, len(results))
	for i, r := range results {
		matches[i] = app.VectorMatch{ID: r.ID, Score: r.Score, Metadata: r.Metadata}
	}
	return matches
}

func (a *VectorSearchAdapter) GetVector(id string) ([]float64, bool) {
	if a.store == nil {
		return nil, false
	}
	return a.store.GetVector(id)
}

func (a *VectorSearchAdapter) Delete(id string) {
	if a.store != nil {
		a.store.Delete(id)
	}
}

func (a *VectorSearchAdapter) Size() int {
	if a.store == nil {
		return 0
	}
	return a.store.Size()
}
