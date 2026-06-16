package eval

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// RunLabel processes one annotation session using the given label strategy.
func RunLabel(dbPath string, sessionID, projectID string, queryLimit, candidateLimit int, retrySession bool, strategy LabelStrategy, llm ChatLLM, embedder Embedder) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open eval db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Step 0: Prepare session
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM annotation_sessions WHERE id=?`, sessionID).Scan(&count)
	if err != nil {
		return fmt.Errorf("check session: %w", err)
	}
	sessionExists := count > 0

	if sessionExists && !retrySession {
		return fmt.Errorf("session %q already exists, use --retry-session to re-run", sessionID)
	}
	if sessionExists && retrySession {
		log.Printf("[eval] retrying session %q: clearing previous state", sessionID)
		if _, err := db.Exec(`DELETE FROM annotation_session_queries WHERE session_id=?`, sessionID); err != nil {
			return fmt.Errorf("clear session queries: %w", err)
		}
	}

	if !sessionExists {
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := db.Exec(
			`INSERT INTO annotation_sessions (id, project_id, status, query_count, created_at) VALUES (?, ?, 'draft', ?, ?)`,
			sessionID, projectID, queryLimit, now,
		); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	}

	// Step 1: Find unlabeled query candidates
	unlabeledRows, err := db.Query(
		`SELECT id, content FROM raw_queries
		 WHERE id NOT IN (SELECT DISTINCT query_id FROM eval_ground_truth)
		 ORDER BY created_at ASC`)
	if err != nil {
		return fmt.Errorf("query unlabeled: %w", err)
	}
	defer unlabeledRows.Close()

	var candidates []QueryRecord
	for unlabeledRows.Next() {
		var qr QueryRecord
		if err := unlabeledRows.Scan(&qr.ID, &qr.Content); err != nil {
			return fmt.Errorf("scan unlabeled: %w", err)
		}
		candidates = append(candidates, qr)
	}
	if unlabeledRows.Err() != nil {
		return fmt.Errorf("iterate unlabeled: %w", unlabeledRows.Err())
	}
	log.Printf("[eval] unlabeled candidates: %d / %d total", len(candidates), len(candidates)+queryLimit)

	// Step 2: Randomly select queries
	actualLimit := queryLimit
	if actualLimit > len(candidates) {
		actualLimit = len(candidates)
	}
	if actualLimit == 0 {
		log.Printf("[eval] no unlabeled queries left, nothing to do")
		_ = updateSessionStatus(db, sessionID, "done")
		return nil
	}

	log.Printf("[eval] randomly selecting %d queries from %d candidates", actualLimit, len(candidates))
	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})
	selectedIDs := make([]string, 0, actualLimit)
	for i := 0; i < actualLimit; i++ {
		selectedIDs = append(selectedIDs, candidates[i].ID)
	}

	// Register selected queries in session
	for _, qid := range selectedIDs {
		_, err := db.Exec(
			`INSERT OR IGNORE INTO annotation_session_queries (session_id, query_id) VALUES (?, ?)`,
			sessionID, qid,
		)
		if err != nil {
			return fmt.Errorf("register query %s: %w", qid, err)
		}
	}

	// Determine embedding model (use the first corpus cache entry)
	var defaultModel string
	if err := db.QueryRow(
		`SELECT model_name FROM eval_embedding_cache WHERE source_type='corpus' LIMIT 1`,
	).Scan(&defaultModel); err != nil {
		log.Printf("[eval] no corpus embedding cache, using default model")
		defaultModel = "doubao-embedding-vision-251215"
	}
	log.Printf("[eval] using embedding model: %s", defaultModel)

	// Step 3: Load corpus vectors once
	corpusVecs, err := loadCorpusVectors(db, defaultModel)
	if err != nil {
		return fmt.Errorf("load corpus vectors: %w", err)
	}
	log.Printf("[eval] loaded %d corpus vectors", len(corpusVecs))

	// Step 4: For each selected query, pre-filter + pre-label
	totalLabeled := 0
	for i, qid := range selectedIDs {
		log.Printf("[eval] [%d/%d] processing query %s", i+1, len(selectedIDs), qid)

		// Get query content
		var queryContent string
		_ = db.QueryRow(`SELECT content FROM raw_queries WHERE id=?`, qid).Scan(&queryContent)
		if queryContent == "" {
			log.Printf("[eval] skip query %s: no content", qid)
			continue
		}

		queryVec, err := loadQueryVector(db, qid, defaultModel)
		if err != nil {
			log.Printf("[eval] query %s: cache miss, computing embedding", qid)
			if embedder == nil {
				log.Printf("[eval] skip query %s: no embedder available", qid)
				continue
			}
			vec, embErr := embedder.Embed(context.Background(), queryContent)
			if embErr != nil {
				log.Printf("[eval] skip query %s: embed failed: %v", qid, embErr)
				continue
			}
			saveEmbedding(db, "query", qid, embedder.ModelName(), vec)
			queryVec = vec
		}

		scored := cosineFilter(queryVec, corpusVecs, candidateLimit)
		if len(scored) == 0 {
			log.Printf("[eval] no corpus candidates for %s", qid)
			continue
		}

		candidates := loadCorpusRecords(db, scored)

		results, err := strategy.PreLabel(context.Background(), llm, queryContent, candidates)
		if err != nil {
			log.Printf("[eval] prelabel %s: %v", qid, err)
			continue
		}

		now := time.Now().UTC().Format(time.RFC3339)
		for _, r := range results {
			if r.Relevance < 0 || r.Relevance > 4 {
				continue
			}
			_, err := db.Exec(
				`INSERT OR REPLACE INTO eval_ground_truth (query_id, corpus_id, relevance, label_source, labeled_at)
				 VALUES (?, ?, ?, 'llm', ?)`,
				qid, r.CorpusID, r.Relevance, now,
			)
			if err != nil {
				log.Printf("[eval] insert ground_truth %s/%s: %v", qid, r.CorpusID, err)
			}
		}
		totalLabeled++
		log.Printf("[eval] labeled %d results for %s", len(results), qid)
	}

	// Step 5: Update session status
	_ = updateSessionStatus(db, sessionID, "prelabeled")
	_ = insertMetadata(db, fmt.Sprintf("session_%s_count", sessionID), fmt.Sprintf("%d", totalLabeled))
	_ = insertMetadata(db, fmt.Sprintf("session_%s_date", sessionID), time.Now().UTC().Format(time.RFC3339))

	log.Printf("[eval] session %q complete: %d queries labeled", sessionID, totalLabeled)
	return nil
}

func loadCorpusRecords(db *sql.DB, topK []scoredCandidate) []CorpusRecord {
	var out []CorpusRecord
	for _, sc := range topK {
		var content string
		if err := db.QueryRow(`SELECT content FROM eval_corpus WHERE id=?`, sc.ID).Scan(&content); err != nil {
			continue
		}
		out = append(out, CorpusRecord{ID: sc.ID, Content: content})
	}
	return out
}

func updateSessionStatus(db *sql.DB, sessionID, status string) error {
	if sessionID == "" {
		return nil
	}
	_, err := db.Exec(`UPDATE annotation_sessions SET status=? WHERE id=?`, status, sessionID)
	return err
}
