package app

// VectorMatch 向量检索命中结果，统一替代各包自定义的 VectorHit / SearchResult / VectorSearchHit。
type VectorMatch struct {
	ID       string
	Score    float64
	Metadata map[string]string
}

// VectorFilter 向量检索过滤函数，可基于 id / 向量 / metadata 进行过滤。
type VectorFilter func(id string, vec []float64, metadata map[string]string) bool

// VectorStore 向量存储与检索接口。
// 由 agent.VectorStore（通过 VectorSearchAdapter）实现。
// 合并了原 tool.VectorSearcher / tool.VectorStorer / tool.VectorStore / agent.VectorStorer 四套接口。
type VectorStore interface {
	// Upsert 写入或更新向量
	Upsert(id string, vec []float64, metadata map[string]string)

	// Search 余弦相似度检索，返回 topK 条命中
	Search(query []float64, topK int, filter VectorFilter) []VectorMatch

	// GetVector 按 ID 获取已缓存的向量
	GetVector(id string) ([]float64, bool)

	// Delete 按 ID 删除向量
	Delete(id string)

	// Size 返回当前向量条目数
	Size() int
}
