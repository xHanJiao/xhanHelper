package eval

import (
	"database/sql"
	"fmt"
	"time"
)

const schemaDDL = `
CREATE TABLE IF NOT EXISTS eval_projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'draft',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    locked_at   TEXT
);

CREATE TABLE IF NOT EXISTS raw_queries (
    id               TEXT PRIMARY KEY,
    content          TEXT NOT NULL,
    source_msg_id    TEXT NOT NULL,
    source_conv_id   TEXT,
    created_at       TEXT
);

CREATE TABLE IF NOT EXISTS eval_corpus (
    id          TEXT PRIMARY KEY,
    content     TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_id   TEXT NOT NULL,
    topic       TEXT,
    tags        TEXT,
    created_at  TEXT
);

CREATE TABLE IF NOT EXISTS annotation_sessions (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    name         TEXT,
    status       TEXT NOT NULL DEFAULT 'draft',
    query_count  INTEGER NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS annotation_session_queries (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id    TEXT NOT NULL,
    query_id      TEXT NOT NULL,
    label_status  TEXT NOT NULL DEFAULT 'selected',
    selected_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(session_id, query_id)
);

CREATE TABLE IF NOT EXISTS eval_ground_truth (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    query_id      TEXT NOT NULL,
    corpus_id     TEXT NOT NULL,
    relevance     INTEGER NOT NULL,
    label_source  TEXT DEFAULT 'llm',
    labeled_at    TEXT NOT NULL DEFAULT (datetime('now')),
    notes         TEXT,
    UNIQUE(query_id, corpus_id)
);

CREATE TABLE IF NOT EXISTS eval_embedding_cache (
    source_type TEXT NOT NULL,
    source_id   TEXT NOT NULL,
    model_name  TEXT NOT NULL,
    embedding   TEXT NOT NULL,
    dimension   INTEGER NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, model_name)
);

CREATE TABLE IF NOT EXISTS eval_metadata (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS eval_runs (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    name         TEXT NOT NULL,
    config       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    report       TEXT,
    detail       TEXT,
    error_msg    TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    started_at   TEXT,
    finished_at  TEXT
);
`

func createSchema(db *sql.DB) error {
	if _, err := db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

func insertProject(db *sql.DB, name string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO eval_projects (id, name, status, created_at) VALUES (?, ?, 'draft', ?)`,
		name, name, now,
	)
	if err != nil {
		return fmt.Errorf("insert project %q: %w", name, err)
	}
	return nil
}

func insertRawQueries(db *sql.DB, queries []QueryExport) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO raw_queries (id, content, source_msg_id, source_conv_id, created_at)
		 VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert raw_queries: %w", err)
	}
	defer stmt.Close()

	for _, q := range queries {
		createdAt := q.CreatedAt.Format(time.RFC3339)
		if _, err := stmt.Exec(q.ID, q.Content, q.ID, q.SourceConvID, createdAt); err != nil {
			return fmt.Errorf("insert query %q: %w", q.ID, err)
		}
	}

	return tx.Commit()
}

func insertCorpus(db *sql.DB, corpus []CorpusExport) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO eval_corpus (id, content, source_type, source_id, topic, tags, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert corpus: %w", err)
	}
	defer stmt.Close()

	for _, c := range corpus {
		createdAt := c.CreatedAt.Format(time.RFC3339)
		if _, err := stmt.Exec(c.ID, c.Content, c.SourceType, c.SourceID, c.Topic, c.Tags, createdAt); err != nil {
			return fmt.Errorf("insert corpus %q: %w", c.ID, err)
		}
	}

	return tx.Commit()
}

func insertMetadata(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		`INSERT OR REPLACE INTO eval_metadata (key, value) VALUES (?, ?)`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("insert metadata %q: %w", key, err)
	}
	return nil
}
