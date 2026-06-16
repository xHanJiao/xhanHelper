package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
)

// VectorEngine 使用从 eval_embedding_cache 预加载的 full-content 语料向量，
// 通过余弦相似度实现 SearchEngine 接口。
//
// 当前仅读取 source_type='corpus' 的向量，暂不支持 chunk 模式。
// 后续加入 chunk 支持时，Index 会额外加载 source_type='corpus_chunk' 的向量，
// Search 在排序后按 corpus_id（父级）去重。
type VectorEngine struct {
	model      string
	corpusVecs []corpusVec // Index 一次性加载
}

// NewVectorEngine 为指定嵌入模型创建 VectorEngine。
func NewVectorEngine(model string) *VectorEngine {
	return &VectorEngine{model: model}
}

func (e *VectorEngine) Name() string { return "vector" }

// Index 从 eval_embedding_cache 加载所有 corpus 向量到内存。
func (e *VectorEngine) Index(ctx context.Context, db *sql.DB) error {
	vecs, err := loadCorpusVectors(db, e.model)
	if err != nil {
		return fmt.Errorf("vector index: %w", err)
	}
	e.corpusVecs = vecs
	log.Printf("[vector] indexed %d corpus vectors (model=%s)", len(vecs), e.model)
	return nil
}

// Search 计算 query 嵌入与全部索引 corpus 向量的余弦相似度，
// 返回按分数降序的完整排名。
func (e *VectorEngine) Search(ctx context.Context, db *sql.DB, query string) ([]SearchResult, error) {
	if len(e.corpusVecs) == 0 {
		return nil, fmt.Errorf("vector engine not indexed")
	}

	// 通过 content 反查 query 在 raw_queries 中的 ID
	var queryID string
	if err := db.QueryRow(`SELECT id FROM raw_queries WHERE content=? LIMIT 1`, query).Scan(&queryID); err != nil {
		return nil, fmt.Errorf("lookup query: %w", err)
	}

	queryVec, err := loadQueryVector(db, queryID, e.model)
	if err != nil {
		return nil, fmt.Errorf("get query embedding (id=%s): %w", queryID, err)
	}

	results := make([]scoredCorpus, 0, len(e.corpusVecs))
	for _, cv := range e.corpusVecs {
		score := cosine(queryVec, cv.Vec)
		results = append(results, scoredCorpus{id: cv.ID, score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = SearchResult{CorpusID: r.id, Score: r.score}
	}
	return out, nil
}

type scoredCorpus struct {
	id    string
	score float64
}

// --- 可复用的向量工具函数（labeler 和 evaluator 共用） ---

// corpusVec 预加载的语料向量
type corpusVec struct {
	ID  string
	Vec []float64
}

// loadCorpusVectors 从 eval_embedding_cache 读取指定模型的全部 corpus 向量。
func loadCorpusVectors(db *sql.DB, model string) ([]corpusVec, error) {
	rows, err := db.Query(
		`SELECT source_id, embedding FROM eval_embedding_cache
		 WHERE source_type='corpus' AND model_name=?`, model)
	if err != nil {
		return nil, fmt.Errorf("query corpus cache: %w", err)
	}
	defer rows.Close()

	var out []corpusVec
	for rows.Next() {
		var cv corpusVec
		var raw string
		if err := rows.Scan(&cv.ID, &raw); err != nil {
			return nil, fmt.Errorf("scan corpus vec: %w", err)
		}
		var vec []float64
		if err := json.Unmarshal([]byte(raw), &vec); err != nil {
			return nil, fmt.Errorf("parse corpus vec %s: %w", cv.ID, err)
		}
		cv.Vec = vec
		out = append(out, cv)
	}
	return out, rows.Err()
}

// loadQueryVector 从 eval_embedding_cache 读取单条 query 的嵌入向量。
func loadQueryVector(db *sql.DB, queryID, model string) ([]float64, error) {
	var raw string
	err := db.QueryRow(
		`SELECT embedding FROM eval_embedding_cache
		 WHERE source_type='query' AND source_id=? AND model_name=?`,
		queryID, model,
	).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("query cache: %w", err)
	}
	var vec []float64
	if err := json.Unmarshal([]byte(raw), &vec); err != nil {
		return nil, fmt.Errorf("parse query vec %s: %w", queryID, err)
	}
	return vec, nil
}

// cosine 计算两个 float64 向量的余弦相似度。
func cosine(a, b []float64) float64 {
	var dot, normA, normB float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
