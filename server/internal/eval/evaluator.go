package eval

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// Evaluator 编排完整的评测流程：加载 ground_truth → 遍历有标注的 query → 调 SearchEngine 检索 → 算指标。
type Evaluator struct {
	db     *sql.DB
	engine SearchEngine
}

// NewEvaluator 创建评测编排器。db 是已打开的 eval.db 连接。
func NewEvaluator(db *sql.DB, engine SearchEngine) *Evaluator {
	return &Evaluator{db: db, engine: engine}
}

// RankedResult 单条 query 的完整检索排名结果
type RankedResult struct {
	QueryID string       `json:"query_id"`
	Query   string       `json:"query"`
	Results []RankedRow  `json:"results"`
	Metrics QueryMetrics `json:"metrics"`
}

// RankedRow 检索排名中的一行（含 COS 分数和 ground_truth 分数）
type RankedRow struct {
	Rank     int     `json:"rank"`
	CorpusID string  `json:"corpus_id"`
	Score    float64 `json:"score"`
	GT       int     `json:"gt"` // -1 表示该 corpus 未被标注
}

// FullReport 完整评测报告
type FullReport struct {
	Summary SummaryMetrics `json:"summary"`
	Queries []RankedResult `json:"queries"`
	Total   int            `json:"total"`
}

// Run 执行评测，可选 filter 限定评测哪些 query（nil=全部有 GT 的）。
func (e *Evaluator) Run(ctx context.Context, filter []string) (*FullReport, error) {
	log.Printf("[evaluator] indexing corpus with engine=%q", e.engine.Name())
	if err := e.engine.Index(ctx, e.db); err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}

	// 加载 ground_truth，按 query 分组
	gtByQuery := make(map[string]map[string]int)
	rows, err := e.db.Query(`SELECT query_id, corpus_id, relevance FROM eval_ground_truth`)
	if err != nil {
		return nil, fmt.Errorf("query ground_truth: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var qid, cid string
		var rel int
		if err := rows.Scan(&qid, &cid, &rel); err != nil {
			return nil, err
		}
		if gtByQuery[qid] == nil {
			gtByQuery[qid] = make(map[string]int)
		}
		gtByQuery[qid][cid] = rel
	}
	_ = rows.Err()

	totalGTQueries := len(gtByQuery)
	log.Printf("[evaluator] loaded %d queries with ground_truth", totalGTQueries)
	if totalGTQueries == 0 {
		return nil, fmt.Errorf("no queries with ground_truth")
	}

	// 预加载有标注的 query（避免遍历时打开新查询导致死锁）
	type qRow struct {
		ID      string
		Content string
	}
	var queries []qRow
	qRows, err := e.db.Query(
		`SELECT id, content FROM raw_queries
		 WHERE id IN (SELECT DISTINCT query_id FROM eval_ground_truth)
		 ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query labeled queries: %w", err)
	}
	for qRows.Next() {
		var qr qRow
		if err := qRows.Scan(&qr.ID, &qr.Content); err != nil {
			qRows.Close()
			return nil, err
		}
		queries = append(queries, qr)
	}
	qRows.Close()

	// 如果指定了 filter，只保留 filter 中的 query
	if len(filter) > 0 {
		filterSet := make(map[string]bool, len(filter))
		for _, id := range filter {
			filterSet[id] = true
		}
		var filtered []qRow
		for _, q := range queries {
			if filterSet[q.ID] {
				filtered = append(filtered, q)
			}
		}
		queries = filtered
	}

	var allQueries []RankedResult
	var sumReciprocalRank, sumNDCG5, sumNDCG10, sumRecall5, sumRecall10, sumPrecision5, sumPrecision10 float64
	count := 0

	for _, q := range queries {
		qid := q.ID
		content := q.Content

		ranked, err := e.engine.Search(ctx, e.db, content)
		if err != nil {
			log.Printf("[evaluator] search %s: %v", qid, err)
			continue
		}

		gt := gtByQuery[qid]
		if gt == nil {
			gt = map[string]int{}
		}

		// 构建完整排名行（带 GT）
		rows := make([]RankedRow, len(ranked))
		for i, r := range ranked {
			gtVal, ok := gt[r.CorpusID]
			if !ok {
				gtVal = -1
			}
			rows[i] = RankedRow{
				Rank:     i,
				CorpusID: r.CorpusID,
				Score:    r.Score,
				GT:       gtVal,
			}
		}

		m := computeQueryMetrics(ranked, gt)
		allQueries = append(allQueries, RankedResult{
			QueryID: qid,
			Query:   content,
			Results: rows,
			Metrics: m,
		})

		sumReciprocalRank += m.ReciprocalRank
		sumNDCG5 += m.NDCGAt5
		sumNDCG10 += m.NDCGAt10
		sumRecall5 += m.RecallAt5
		sumRecall10 += m.RecallAt10
		sumPrecision5 += m.PrecisionAt5
		sumPrecision10 += m.PrecisionAt10
		count++
	}
	_ = qRows.Err()

	if count == 0 {
		return nil, fmt.Errorf("no queries successfully evaluated")
	}

	n := float64(count)
	report := &FullReport{
		Total:   count,
		Queries: allQueries,
		Summary: SummaryMetrics{
			MRR:           sumReciprocalRank / n,
			NDCGAt5:       sumNDCG5 / n,
			NDCGAt10:      sumNDCG10 / n,
			RecallAt5:     sumRecall5 / n,
			RecallAt10:    sumRecall10 / n,
			PrecisionAt5:  sumPrecision5 / n,
			PrecisionAt10: sumPrecision10 / n,
		},
	}

	log.Printf("[evaluator] done: %d queries, MRR=%.4f, NDCG@5=%.4f, Recall@10=%.4f",
		count, report.Summary.MRR, report.Summary.NDCGAt5, report.Summary.RecallAt10)

	return report, nil
}
