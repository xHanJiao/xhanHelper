package eval

import (
	"context"
	"database/sql"
)

// SearchResult 一条排序后的语料匹配结果
type SearchResult struct {
	CorpusID string  `json:"corpus_id"`
	Score    float64 `json:"score"`
}

// SearchEngine 检索评测的核心抽象。
// 不同实现可互相替换以对比不同搜索策略（向量、关键词、混合），无需改动评测管线。
//
// 后续引擎（KeywordEngine、HybridEngineRRF）实现此接口。
// 基于 chunk 的搜索由引擎内部的 Index/Search 逻辑自行处理，
// 通过检测 eval_corpus_chunks 表是否存在来判断。
type SearchEngine interface {
	Name() string

	// Index 从 eval.db 预加载语料数据（向量、FTS5 表等）。
	// 在对多条 query 执行 Search 之前调用一次。
	Index(ctx context.Context, db *sql.DB) error

	// Search 返回全部语料按与 query 相关度降序排列的结果，
	// 不截断 top-K。指标层在计算 Recall@K / NDCG@K 时自行截断。
	Search(ctx context.Context, db *sql.DB, query string) ([]SearchResult, error)
}
