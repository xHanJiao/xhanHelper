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

// ── Interfaces ──

// QueryExporter extracts queries from a source database.
type QueryExporter interface {
	Name() string
	Export(ctx context.Context, db *sql.DB) ([]QueryExport, error)
}

// CorpusExporter extracts corpus entries from a source database.
type CorpusExporter interface {
	Name() string
	Export(ctx context.Context, db *sql.DB) ([]CorpusExport, error)
}

// ── Query Exporters ──

// RawQueryExporter exports user messages directly, filtering out very short messages.
type RawQueryExporter struct{}

func (e *RawQueryExporter) Name() string { return "raw" }

func (e *RawQueryExporter) Export(ctx context.Context, db *sql.DB) ([]QueryExport, error) {
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
		if shouldSkipQuery(q.Content) {
			continue
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// shouldSkipQuery mirrors the pipeline's short-input check logic (step_short_input.go)
// but implemented independently to avoid package coupling.
func shouldSkipQuery(content string) bool {
	charLen := utf8.RuneCountInString(content)
	byteLen := len(content)
	return charLen <= 5 || (charLen <= 10 && byteLen <= 15)
}

// ── Corpus Exporters ──

// FullContentCorpusExporter exports notes and memories as-is, without chunking.
type FullContentCorpusExporter struct{}

func (e *FullContentCorpusExporter) Name() string { return "full-content" }

func (e *FullContentCorpusExporter) Export(ctx context.Context, db *sql.DB) ([]CorpusExport, error) {
	var out []CorpusExport

	notes, err := exportCorpusFromTable(ctx, db, "note",
		`SELECT id, content, COALESCE(topic,''), COALESCE(tags,''), created_at
		 FROM notes WHERE status=0`)
	if err != nil {
		return nil, err
	}
	out = append(out, notes...)

	memories, err := exportCorpusFromTable(ctx, db, "memory",
		`SELECT id, memory_value AS content, COALESCE(topic,''), COALESCE(tags,''), created_at
		 FROM user_memories`)
	if err != nil {
		return nil, err
	}
	out = append(out, memories...)

	return out, nil
}

func exportCorpusFromTable(ctx context.Context, db *sql.DB, sourceType, query string) ([]CorpusExport, error) {
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
