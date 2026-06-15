package eval

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// corpusVec holds a pre-loaded corpus vector for cosine similarity computation.
type corpusVec struct {
	ID  string
	Vec []float64
}

// loadCorpusVectors reads all corpus embeddings from eval_embedding_cache for a given model.
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

// loadQueryVector reads a single query embedding from eval_embedding_cache.
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

// scoredCandidate pairs a corpus ID with a similarity score.
type scoredCandidate struct {
	ID    string
	Score float64
}

// cosineFilter computes cosine similarity between the query vector and all corpus vectors,
// returning the top K corpus IDs sorted by descending score.
func cosineFilter(queryVec []float64, corpusVecs []corpusVec, topK int) []scoredCandidate {
	results := make([]scoredCandidate, 0, len(corpusVecs))

	for _, cv := range corpusVecs {
		score := cosine(queryVec, cv.Vec)
		results = append(results, scoredCandidate{ID: cv.ID, Score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if topK > len(results) {
		topK = len(results)
	}
	return results[:topK]
}

// cosine computes cosine similarity between two float64 vectors.
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
