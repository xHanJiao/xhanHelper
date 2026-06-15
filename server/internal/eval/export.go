package eval

import (
	"context"
	"database/sql"
	"fmt"
	"time"
	"unicode/utf8"
)

// QueryExport represents a single user query extracted for the evaluation dataset.
type QueryExport struct {
	ID           string
	Content      string
	SourceConvID string
	CreatedAt    time.Time
}

// CorpusExport represents a single corpus entry (note or memory) for the evaluation dataset.
type CorpusExport struct {
	ID         string
	Content    string
	SourceType string
	SourceID   string
	Topic      string
	Tags       string
	CreatedAt  time.Time
}

// ExportStrategy defines how to extract queries and corpus from a source database (typically MySQL).
// Different implementations can support different export policies (time range, user filtering, etc).
type ExportStrategy interface {
	Name() string
	ExportQueries(ctx context.Context, db *sql.DB) ([]QueryExport, error)
	ExportCorpus(ctx context.Context, db *sql.DB) ([]CorpusExport, error)
}

// AllPastDataStrategy exports all historical data, filtering out queries that would
// not trigger retrieval in the current pipeline (i.e., very short non-question messages
// without intent keywords).
type AllPastDataStrategy struct{}

func (s *AllPastDataStrategy) Name() string { return "all-past-data" }

func (s *AllPastDataStrategy) ExportQueries(ctx context.Context, db *sql.DB) ([]QueryExport, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, content, COALESCE(conversation_id,''), created_at
		 FROM messages
		 WHERE role='user' AND status=1
		 ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var out []QueryExport
	for rows.Next() {
		var q QueryExport
		if err := rows.Scan(&q.ID, &q.Content, &q.SourceConvID, &q.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if s.shouldSkip(q.Content) {
			continue
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// shouldSkip mirrors the pipeline's short-input check logic (step_short_input.go)
// but implemented independently to avoid package coupling.
func (s *AllPastDataStrategy) shouldSkip(content string) bool {
	charLen := utf8.RuneCountInString(content)
	byteLen := len(content)
	return charLen <= 5 || (charLen <= 10 && byteLen <= 15)
}

func (s *AllPastDataStrategy) ExportCorpus(ctx context.Context, db *sql.DB) ([]CorpusExport, error) {
	var out []CorpusExport

	notes, err := s.exportCorpusFromTable(ctx, db, "note",
		`SELECT id, content, COALESCE(topic,''), COALESCE(tags,''), created_at
		 FROM notes WHERE status=0`)
	if err != nil {
		return nil, err
	}
	out = append(out, notes...)

	memories, err := s.exportCorpusFromTable(ctx, db, "memory",
		`SELECT id, memory_value AS content, COALESCE(topic,''), COALESCE(tags,''), created_at
		 FROM user_memories`)
	if err != nil {
		return nil, err
	}
	out = append(out, memories...)

	return out, nil
}

func (s *AllPastDataStrategy) exportCorpusFromTable(ctx context.Context, db *sql.DB, sourceType, query string) ([]CorpusExport, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", sourceType, err)
	}
	defer rows.Close()

	var out []CorpusExport
	for rows.Next() {
		var c CorpusExport
		if err := rows.Scan(&c.SourceID, &c.Content, &c.Topic, &c.Tags, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan %s: %w", sourceType, err)
		}
		c.ID = c.SourceID
		c.SourceType = sourceType
		out = append(out, c)
	}
	return out, rows.Err()
}
