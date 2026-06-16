package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Labeler handles the labeling pipeline: cosine pre-filter + LLM scoring + writing results.
// It is shared between the HTTP handler and the CLI tool.
type Labeler struct {
	db           *sql.DB
	embedder     Embedder
	llm          ChatLLM
	strategy     LabelStrategy
	defaultModel string
}

// NewLabeler creates a new Labeler. The embedder is used as fallback when query vectors
// are missing from the embedding cache.
func NewLabeler(db *sql.DB, embedder Embedder, llm ChatLLM, strategy LabelStrategy) *Labeler {
	return &Labeler{
		db:       db,
		embedder: embedder,
		llm:      llm,
		strategy: strategy,
	}
}

// model returns the default embedding model from the cache, with fallback.
func (l *Labeler) model() string {
	if l.defaultModel == "" {
		if err := l.db.QueryRow(
			`SELECT model_name FROM eval_embedding_cache WHERE source_type='corpus' LIMIT 1`,
		).Scan(&l.defaultModel); err != nil {
			l.defaultModel = "doubao-embedding-vision-251215"
		}
	}
	return l.defaultModel
}

// LabelQuery pre-filters corpus candidates with cosine similarity, then calls the LLM
// to score each candidate 0-4, writing results to eval_ground_truth.
func (l *Labeler) LabelQuery(ctx context.Context, queryID string, candidateLimit int) error {
	// Get query content and vector
	var queryContent string
	if err := l.db.QueryRow(`SELECT content FROM raw_queries WHERE id=?`, queryID).Scan(&queryContent); err != nil {
		return fmt.Errorf("get query: %w", err)
	}

	queryVec, err := loadQueryVector(l.db, queryID, l.model())
	if err != nil {
		log.Printf("[labeler] query %s: cache miss, computing embedding", queryID)
		if l.embedder == nil {
			return fmt.Errorf("no embedder, cache miss for query %s", queryID)
		}
		vec, embErr := l.embedder.Embed(ctx, queryContent)
		if embErr != nil {
			return fmt.Errorf("embed query: %w", embErr)
		}
		saveEmbedding(l.db, "query", queryID, l.embedder.ModelName(), vec)
		queryVec = vec
	}

	// Load corpus vectors
	corpusVecs, err := loadCorpusVectors(l.db, l.model())
	if err != nil {
		return fmt.Errorf("load corpus vectors: %w", err)
	}
	log.Printf("[labeler] loaded %d corpus vectors for %s", len(corpusVecs), queryID)

	// Cosine pre-filter
	scored := cosineFilter(queryVec, corpusVecs, candidateLimit)
	if len(scored) == 0 {
		return fmt.Errorf("no corpus candidates for %s", queryID)
	}

	// LLM scoring
	candidates := loadCorpusRecords(l.db, scored)
	results, err := l.strategy.PreLabel(ctx, l.llm, queryContent, candidates)
	if err != nil {
		return fmt.Errorf("prelabel: %w", err)
	}

	// Write results
	now := time.Now().UTC().Format(time.RFC3339)
	for _, r := range results {
		if r.Relevance < 0 || r.Relevance > 4 {
			continue
		}
		_, err := l.db.Exec(
			`INSERT INTO eval_ground_truth (query_id, corpus_id, relevance, label_source, labeled_at)
			 VALUES (?, ?, ?, 'llm', ?)
			 ON CONFLICT(query_id, corpus_id) DO UPDATE SET relevance=excluded.relevance, label_source='llm', labeled_at=excluded.labeled_at`,
			queryID, r.CorpusID, r.Relevance, now,
		)
		if err != nil {
			log.Printf("[labeler] insert ground_truth %s/%s: %v", queryID, r.CorpusID, err)
		}
	}

	// Mark as prelabeled in session queries
	_, _ = l.db.Exec(
		`UPDATE annotation_session_queries SET label_status='prelabeled' WHERE query_id=? AND label_status='selected'`,
		queryID,
	)

	log.Printf("[labeler] labeled %d results for %s", len(results), queryID)
	return nil
}

// CreateSession creates a new annotation session and registers the given query IDs.
func (l *Labeler) CreateSession(sessionID, projectID string, queryIDs []string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := l.db.Exec(
		`INSERT OR IGNORE INTO annotation_sessions (id, project_id, status, query_count, created_at) VALUES (?, ?, 'draft', ?, ?)`,
		sessionID, projectID, len(queryIDs), now,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	for _, qid := range queryIDs {
		_, err := l.db.Exec(
			`INSERT OR IGNORE INTO annotation_session_queries (session_id, query_id, label_status)
			 VALUES (?, ?, 'selected')`,
			sessionID, qid,
		)
		if err != nil {
			log.Printf("[labeler] register query %s: %v", qid, err)
		}
	}
	return nil
}

func saveEmbedding(db *sql.DB, sourceType, sourceID, model string, vec []float64) {
	raw, _ := json.Marshal(vec)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT OR IGNORE INTO eval_embedding_cache (source_type, source_id, model_name, embedding, dimension, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sourceType, sourceID, model, string(raw), len(vec), now,
	)
	if err != nil {
		log.Printf("[eval] save embedding %s/%s: %v", sourceType, sourceID, err)
	}
}
