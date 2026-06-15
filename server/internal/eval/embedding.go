package eval

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
)

// EmbeddingRecord holds a single embedding vector from the MySQL embeddings table.
type embeddingRecord struct {
	EntityType string
	EntityID   string
	Model      string
	Embedding  string // JSON float64 array
}

// preloadEmbeddings reads full-content embeddings from MySQL and writes them into the
// eval SQLite's eval_embedding_cache table. Only entity_type IN ('message','note','memory')
// are loaded. 'message' embeddings are mapped to source_type='query', others to 'corpus'.
func preloadEmbeddings(srcDB, sqliteDB *sql.DB) error {
	rows, err := srcDB.Query(
		`SELECT entity_type, entity_id, model, embedding
		 FROM embeddings
		 WHERE entity_type IN ('message','note','memory')
		   AND embedding IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query embeddings: %w", err)
	}
	defer rows.Close()

	stmt, err := sqliteDB.Prepare(
		`INSERT OR IGNORE INTO eval_embedding_cache (source_type, source_id, model_name, embedding, dimension)
		 VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert cache: %w", err)
	}
	defer stmt.Close()

	var total, skipped int
	for rows.Next() {
		var rec embeddingRecord
		if err := rows.Scan(&rec.EntityType, &rec.EntityID, &rec.Model, &rec.Embedding); err != nil {
			return fmt.Errorf("scan embedding: %w", err)
		}

		sourceType := mapSourceType(rec.EntityType)
		dim, err := embeddingDim(rec.Embedding)
		if err != nil {
			log.Printf("[eval] skip embedding %s/%s: bad json: %v", rec.EntityType, rec.EntityID, err)
			skipped++
			continue
		}

		if _, err := stmt.Exec(sourceType, rec.EntityID, rec.Model, rec.Embedding, dim); err != nil {
			log.Printf("[eval] skip duplicate embedding %s/%s: %v", rec.EntityType, rec.EntityID, err)
			skipped++
			continue
		}
		total++
	}

	if rows.Err() != nil {
		return fmt.Errorf("iterate embeddings: %w", rows.Err())
	}

	log.Printf("[eval] preloaded %d embeddings into cache (skipped %d)", total, skipped)
	return nil
}

func mapSourceType(entityType string) string {
	if entityType == "message" {
		return "query"
	}
	return "corpus"
}

func embeddingDim(raw string) (int, error) {
	var vec []float64
	if err := json.Unmarshal([]byte(raw), &vec); err != nil {
		return 0, err
	}
	return len(vec), nil
}
