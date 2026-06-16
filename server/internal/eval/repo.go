package eval

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Repo provides query methods for an eval SQLite database.
type Repo struct {
	db *sql.DB
}

// OpenRepo opens an eval.db at the given path.
func OpenRepo(dbPath string) (*Repo, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open eval db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping eval db: %w", err)
	}

	// 自动升级 schema：新增的表幂等创建
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Repo{db: db}, nil
}

// Close closes the underlying database connection.
func (r *Repo) Close() error {
	return r.db.Close()
}

// ListProjectDirs scans data/eval/ for directories containing an eval.db.
func ListProjectDirs(evalDir string) ([]string, error) {
	entries, err := os.ReadDir(evalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read eval dir: %w", err)
	}
	var projects []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dbPath := filepath.Join(evalDir, e.Name(), "eval.db")
		if _, err := os.Stat(dbPath); err == nil {
			projects = append(projects, e.Name())
		}
	}
	return projects, nil
}

// ProjectInfo holds summary info for one eval project.
type ProjectInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	QueryCount   int    `json:"query_count"`
	CorpusCount  int    `json:"corpus_count"`
	SessionCount int    `json:"session_count"`
}

// GetProjectInfo returns summary info for the project.
func (r *Repo) GetProjectInfo() (*ProjectInfo, error) {
	p := &ProjectInfo{}
	if err := r.db.QueryRow(`SELECT id, name, status FROM eval_projects`).Scan(&p.ID, &p.Name, &p.Status); err != nil {
		return nil, fmt.Errorf("query project: %w", err)
	}
	_ = r.db.QueryRow(`SELECT COUNT(*) FROM raw_queries`).Scan(&p.QueryCount)
	_ = r.db.QueryRow(`SELECT COUNT(*) FROM eval_corpus`).Scan(&p.CorpusCount)
	_ = r.db.QueryRow(`SELECT COUNT(*) FROM annotation_sessions`).Scan(&p.SessionCount)
	return p, nil
}

// Stats holds per-project statistics.
type Stats struct {
	TotalQueries     int           `json:"total_queries"`
	LabeledQueries   int           `json:"labeled_queries"`
	ReviewedQueries  int           `json:"reviewed_queries"`
	UnlabeledQueries int           `json:"unlabeled_queries"`
	RelevanceDist    map[int]int   `json:"relevance_distribution"`
	Sessions         []SessionInfo `json:"sessions"`
}

// SessionInfo holds summary info for one annotation session.
type SessionInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	QueryCount int    `json:"query_count"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

// GetStats returns project statistics.
func (r *Repo) GetStats() (*Stats, error) {
	s := &Stats{}
	_ = r.db.QueryRow(`SELECT COUNT(*) FROM raw_queries`).Scan(&s.TotalQueries)
	_ = r.db.QueryRow(`SELECT COUNT(DISTINCT query_id) FROM eval_ground_truth`).Scan(&s.LabeledQueries)
	_ = r.db.QueryRow(`SELECT COUNT(DISTINCT query_id) FROM annotation_session_queries WHERE label_status IN ('reviewed','finalized')`).Scan(&s.ReviewedQueries)
	s.UnlabeledQueries = s.TotalQueries - s.LabeledQueries

	rows, err := r.db.Query(`SELECT relevance, COUNT(*) FROM eval_ground_truth GROUP BY relevance`)
	if err != nil {
		return nil, fmt.Errorf("query dist: %w", err)
	}
	defer rows.Close()
	s.RelevanceDist = make(map[int]int)
	for rows.Next() {
		var rel, cnt int
		if err := rows.Scan(&rel, &cnt); err != nil {
			return nil, err
		}
		s.RelevanceDist[rel] = cnt
	}
	_ = rows.Err()

	sessRows, err := r.db.Query(`SELECT id, COALESCE(name,''), query_count, status, created_at FROM annotation_sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer sessRows.Close()
	for sessRows.Next() {
		var si SessionInfo
		if err := sessRows.Scan(&si.ID, &si.Name, &si.QueryCount, &si.Status, &si.CreatedAt); err != nil {
			return nil, err
		}
		s.Sessions = append(s.Sessions, si)
	}
	_ = sessRows.Err()
	return s, nil
}

// QueryInfo holds a row for the query list.
type QueryInfo struct {
	ID           string `json:"id"`
	Content      string `json:"content"`
	SourceConvID string `json:"source_conv_id"`
	CreatedAt    string `json:"created_at"`
	LabeledCount int    `json:"labeled_count"`
	Reviewed     bool   `json:"reviewed"`
}

// ListQueries returns a paginated, filterable list of raw_queries.
func (r *Repo) ListQueries(keyword, status, sessionID string, limit, offset int) ([]QueryInfo, int, error) {
	var args []interface{}
	where := "WHERE 1=1"

	if keyword != "" {
		where += " AND q.content LIKE ?"
		args = append(args, "%"+keyword+"%")
	}

	// SQL-level status filter
	switch status {
	case "unselected":
		where += ` AND NOT EXISTS(SELECT 1 FROM annotation_session_queries sq WHERE sq.query_id=q.id)`
	case "selected":
		where += ` AND EXISTS(SELECT 1 FROM annotation_session_queries sq WHERE sq.query_id=q.id AND sq.label_status='selected')`
	case "prelabeled":
		where += ` AND EXISTS(SELECT 1 FROM annotation_session_queries sq WHERE sq.query_id=q.id AND sq.label_status='prelabeled')`
	case "reviewed":
		where += ` AND EXISTS(SELECT 1 FROM annotation_session_queries sq WHERE sq.query_id=q.id AND sq.label_status IN ('reviewed','finalized'))`
	}

	if sessionID != "" {
		where += ` AND q.id IN (SELECT query_id FROM annotation_session_queries WHERE session_id=?)`
		args = append(args, sessionID)
	}

	var total int
	totalSQL := "SELECT COUNT(*) FROM raw_queries q " + where
	if err := r.db.QueryRow(totalSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sql := fmt.Sprintf(
		`SELECT q.id, q.content, COALESCE(q.source_conv_id,''), COALESCE(q.created_at,''),
		        COALESCE(gt_cnt.c,0),
		        EXISTS(SELECT 1 FROM annotation_session_queries sq WHERE sq.query_id=q.id AND sq.label_status IN ('reviewed','finalized'))
		 FROM raw_queries q
		 LEFT JOIN (SELECT query_id, COUNT(*) AS c FROM eval_ground_truth GROUP BY query_id) gt_cnt ON gt_cnt.query_id=q.id
		 %s
		 ORDER BY q.created_at ASC
		 LIMIT ? OFFSET ?`, where)

	args = append(args, limit, offset)
	rows, err := r.db.Query(sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []QueryInfo
	for rows.Next() {
		var qi QueryInfo
		if err := rows.Scan(&qi.ID, &qi.Content, &qi.SourceConvID, &qi.CreatedAt, &qi.LabeledCount, &qi.Reviewed); err != nil {
			return nil, 0, err
		}
		out = append(out, qi)
	}
	return out, total, rows.Err()
}

// ListPlacedQueries 返回有 ground_truth 标注的 query 列表，供评测任务手动选择。
func (r *Repo) ListPlacedQueries(keyword string, limit, offset int) ([]QueryInfo, int, error) {
	var args []interface{}
	where := "WHERE EXISTS(SELECT 1 FROM eval_ground_truth gt WHERE gt.query_id=q.id)"

	if keyword != "" {
		where += " AND q.content LIKE ?"
		args = append(args, "%"+keyword+"%")
	}

	var total int
	totalSQL := "SELECT COUNT(*) FROM raw_queries q " + where
	if err := r.db.QueryRow(totalSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sql := fmt.Sprintf(
		`SELECT q.id, q.content, COALESCE(q.source_conv_id,''), COALESCE(q.created_at,''),
		        COALESCE(gt_cnt.c,0), 0
		 FROM raw_queries q
		 JOIN (SELECT query_id, COUNT(*) AS c FROM eval_ground_truth GROUP BY query_id) gt_cnt ON gt_cnt.query_id=q.id
		 %s
		 ORDER BY q.created_at ASC
		 LIMIT ? OFFSET ?`, where)

	args = append(args, limit, offset)
	rows, err := r.db.Query(sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []QueryInfo
	for rows.Next() {
		var qi QueryInfo
		if err := rows.Scan(&qi.ID, &qi.Content, &qi.SourceConvID, &qi.CreatedAt, &qi.LabeledCount, &qi.Reviewed); err != nil {
			return nil, 0, err
		}
		out = append(out, qi)
	}
	return out, total, rows.Err()
}

// AnnotationRecord holds one ground_truth row with joined query/corpus content.
type AnnotationRecord struct {
	QueryID       string `json:"query_id"`
	QueryContent  string `json:"query_content"`
	CorpusID      string `json:"corpus_id"`
	CorpusContent string `json:"corpus_content"`
	Relevance     int    `json:"relevance"`
	LabelSource   string `json:"label_source"`
	LabeledAt     string `json:"labeled_at"`
}

// QueryDetail holds full detail of a query with its annotations.
type QueryDetail struct {
	Query        QueryInfo          `json:"query"`
	Annotations  []AnnotationRecord `json:"annotations"`
	ReviewStatus string             `json:"review_status"`
}

func (r *Repo) GetQueryDetail(queryID string) (*QueryDetail, error) {
	d := &QueryDetail{}
	if err := r.db.QueryRow(
		`SELECT id, content, COALESCE(source_conv_id,''), COALESCE(created_at,'')
		 FROM raw_queries WHERE id=?`, queryID,
	).Scan(&d.Query.ID, &d.Query.Content, &d.Query.SourceConvID, &d.Query.CreatedAt); err != nil {
		return nil, fmt.Errorf("query raw_queries: %w", err)
	}

	// Check if human-reviewed via annotation_session_queries
	var revStatus string
	_ = r.db.QueryRow(
		`SELECT COALESCE(label_status,'') FROM annotation_session_queries WHERE query_id=? ORDER BY selected_at DESC LIMIT 1`,
		queryID,
	).Scan(&revStatus)
	d.Query.Reviewed = revStatus == "reviewed" || revStatus == "finalized"

	// Safety net: if label_status is still 'selected' but ground_truth has records,
	// the labeling completed without updating the status. Treat as prelabeled.
	var gtHasRecords bool
	_ = r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM eval_ground_truth WHERE query_id=?)`, queryID).Scan(&gtHasRecords)
	if revStatus == "selected" && gtHasRecords {
		revStatus = "prelabeled"
	}

	rows, err := r.db.Query(
		`SELECT gt.query_id, q.content, gt.corpus_id, c.content, gt.relevance, gt.label_source, gt.labeled_at
		 FROM eval_ground_truth gt
		 JOIN raw_queries q ON q.id=gt.query_id
		 JOIN eval_corpus c ON c.id=gt.corpus_id
		 WHERE gt.query_id=?
		 ORDER BY gt.relevance DESC`, queryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ar AnnotationRecord
		if err := rows.Scan(&ar.QueryID, &ar.QueryContent, &ar.CorpusID, &ar.CorpusContent, &ar.Relevance, &ar.LabelSource, &ar.LabeledAt); err != nil {
			return nil, err
		}
		d.Annotations = append(d.Annotations, ar)
	}
	_ = rows.Err()

	d.Query.LabeledCount = len(d.Annotations)
	if d.Query.Reviewed {
		d.ReviewStatus = "reviewed"
	} else if len(d.Annotations) > 0 {
		d.ReviewStatus = "prelabeled"
	} else {
		d.ReviewStatus = "unlabeled"
	}

	return d, nil
}

// AnnotationUpdate represents a single annotation change from the PATCH request.
type AnnotationUpdate struct {
	CorpusID  string `json:"corpus_id"`
	Relevance int    `json:"relevance"`
}

// UpdateAnnotations applies annotation changes and sets review status.
func (r *Repo) UpdateAnnotations(queryID string, anns []AnnotationUpdate, reviewStatus string, notes string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, a := range anns {
		_, err := tx.Exec(
			`INSERT INTO eval_ground_truth (query_id, corpus_id, relevance, label_source, labeled_at, notes)
			 VALUES (?, ?, ?, 'corrected', datetime('now'), ?)
			 ON CONFLICT(query_id, corpus_id) DO UPDATE SET relevance=excluded.relevance, label_source='corrected', labeled_at=datetime('now'), notes=COALESCE(excluded.notes, eval_ground_truth.notes)`,
			queryID, a.CorpusID, a.Relevance, notes,
		)
		if err != nil {
			return fmt.Errorf("upsert annotation %s/%s: %w", queryID, a.CorpusID, err)
		}
	}

	if reviewStatus != "" {
		_, err := tx.Exec(
			`UPDATE annotation_session_queries SET label_status=? WHERE query_id=?`,
			reviewStatus, queryID,
		)
		if err != nil {
			return fmt.Errorf("update review status: %w", err)
		}
	}

	return tx.Commit()
}

// CorpusInfo holds a row for the corpus list.
type CorpusInfo struct {
	ID              string `json:"id"`
	Content         string `json:"content"`
	SourceType      string `json:"source_type"`
	Topic           string `json:"topic"`
	Tags            string `json:"tags"`
	CreatedAt       string `json:"created_at"`
	AnnotationCount int    `json:"annotation_count"`
}

// ListCorpus returns a paginated, filterable list of eval_corpus entries.
func (r *Repo) ListCorpus(keyword, sourceType, topic string, limit, offset int) ([]CorpusInfo, int, error) {
	var args []interface{}
	where := "WHERE 1=1"

	if keyword != "" {
		where += " AND content LIKE ?"
		args = append(args, "%"+keyword+"%")
	}
	if sourceType != "" && sourceType != "all" {
		where += " AND source_type = ?"
		args = append(args, sourceType)
	}
	if topic != "" {
		where += " AND topic = ?"
		args = append(args, topic)
	}

	var total int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM eval_corpus "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sql := fmt.Sprintf(
		`SELECT c.id, c.content, c.source_type, COALESCE(c.topic,''), COALESCE(c.tags,''), COALESCE(c.created_at,''),
		        COALESCE(cnt.c,0)
		 FROM eval_corpus c
		 LEFT JOIN (SELECT corpus_id, COUNT(*) AS c FROM eval_ground_truth GROUP BY corpus_id) cnt ON cnt.corpus_id=c.id
		 %s
		 ORDER BY c.source_type, c.created_at ASC
		 LIMIT ? OFFSET ?`, where)

	args = append(args, limit, offset)
	rows, err := r.db.Query(sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []CorpusInfo
	for rows.Next() {
		var ci CorpusInfo
		if err := rows.Scan(&ci.ID, &ci.Content, &ci.SourceType, &ci.Topic, &ci.Tags, &ci.CreatedAt, &ci.AnnotationCount); err != nil {
			return nil, 0, err
		}
		out = append(out, ci)
	}
	return out, total, rows.Err()
}

// ListAnnotations returns a paginated, filterable list of annotation records.
func (r *Repo) ListAnnotations(queryKeyword, labelSource string, relevance int, limit, offset int) ([]AnnotationRecord, int, error) {
	var args []interface{}
	where := "WHERE 1=1"

	if queryKeyword != "" {
		where += " AND q.content LIKE ?"
		args = append(args, "%"+queryKeyword+"%")
	}
	if labelSource != "" && labelSource != "all" {
		where += " AND gt.label_source = ?"
		args = append(args, labelSource)
	}
	if relevance >= 0 {
		where += " AND gt.relevance = ?"
		args = append(args, relevance)
	}

	var total int
	countSQL := "SELECT COUNT(*) FROM eval_ground_truth gt JOIN raw_queries q ON q.id=gt.query_id " + where
	if err := r.db.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sql := fmt.Sprintf(
		`SELECT gt.query_id, q.content, gt.corpus_id, c.content, gt.relevance, gt.label_source, gt.labeled_at
		 FROM eval_ground_truth gt
		 JOIN raw_queries q ON q.id=gt.query_id
		 JOIN eval_corpus c ON c.id=gt.corpus_id
		 %s
		 ORDER BY gt.labeled_at DESC
		 LIMIT ? OFFSET ?`, where)

	args = append(args, limit, offset)
	rows, err := r.db.Query(sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []AnnotationRecord
	for rows.Next() {
		var ar AnnotationRecord
		if err := rows.Scan(&ar.QueryID, &ar.QueryContent, &ar.CorpusID, &ar.CorpusContent, &ar.Relevance, &ar.LabelSource, &ar.LabeledAt); err != nil {
			return nil, 0, err
		}
		out = append(out, ar)
	}
	return out, total, rows.Err()
}

// QueryWithAnnotations groups annotations under their parent query.
type QueryWithAnnotations struct {
	QueryID      string             `json:"query_id"`
	QueryContent string             `json:"query_content"`
	Annotations  []AnnotationRecord `json:"annotations"`
}

// ListQueryAnnotations returns paginated queries with their annotations grouped.
// Pagination is per-query, not per-annotation-row.
func (r *Repo) ListQueryAnnotations(queryKeyword, labelSource string, relevance int, limit, offset int) ([]QueryWithAnnotations, int, error) {
	var args []interface{}
	where := "WHERE 1=1"

	if queryKeyword != "" {
		where += " AND q.content LIKE ?"
		args = append(args, "%"+queryKeyword+"%")
	}
	if labelSource != "" && labelSource != "all" {
		where += " AND gt.label_source = ?"
		args = append(args, labelSource)
	}
	if relevance >= 0 {
		where += " AND gt.relevance = ?"
		args = append(args, relevance)
	}

	// Count distinct queries matching filter
	var total int
	countSQL := fmt.Sprintf("SELECT COUNT(DISTINCT gt.query_id) FROM eval_ground_truth gt JOIN raw_queries q ON q.id=gt.query_id %s", where)
	if err := r.db.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Get paginated query IDs (ordered by latest annotation time)
	qidSQL := fmt.Sprintf(
		`SELECT DISTINCT gt.query_id
		 FROM eval_ground_truth gt
		 JOIN raw_queries q ON q.id=gt.query_id
		 %s
		 ORDER BY (SELECT MAX(labeled_at) FROM eval_ground_truth WHERE query_id=gt.query_id) DESC
		 LIMIT ? OFFSET ?`, where)

	args2 := append([]interface{}{}, args...)
	args2 = append(args2, limit, offset)
	rows, err := r.db.Query(qidSQL, args2...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var queryIDs []string
	for rows.Next() {
		var qid string
		if err := rows.Scan(&qid); err != nil {
			return nil, 0, err
		}
		queryIDs = append(queryIDs, qid)
	}
	_ = rows.Err()
	if len(queryIDs) == 0 {
		return nil, total, nil
	}

	// Bulk-load all annotations for these query IDs
	annMap := make(map[string][]AnnotationRecord)
	annRows, err := r.db.Query(
		`SELECT gt.query_id, q.content, gt.corpus_id, c.content, gt.relevance, gt.label_source, gt.labeled_at
		 FROM eval_ground_truth gt
		 JOIN raw_queries q ON q.id=gt.query_id
		 JOIN eval_corpus c ON c.id=gt.corpus_id
		 WHERE gt.query_id IN (`+placeholders(len(queryIDs))+`)
		 ORDER BY gt.query_id, gt.relevance DESC`,
		strsToIface(queryIDs)...)
	if err != nil {
		return nil, 0, err
	}
	defer annRows.Close()

	for annRows.Next() {
		var ar AnnotationRecord
		if err := annRows.Scan(&ar.QueryID, &ar.QueryContent, &ar.CorpusID, &ar.CorpusContent, &ar.Relevance, &ar.LabelSource, &ar.LabeledAt); err != nil {
			return nil, 0, err
		}
		annMap[ar.QueryID] = append(annMap[ar.QueryID], ar)
	}
	_ = annRows.Err()

	// Build ordered output
	out := make([]QueryWithAnnotations, 0, len(queryIDs))
	for _, qid := range queryIDs {
		anns, ok := annMap[qid]
		if !ok {
			anns = []AnnotationRecord{}
		}
		content := ""
		if len(anns) > 0 {
			content = anns[0].QueryContent
		}
		out = append(out, QueryWithAnnotations{
			QueryID:      qid,
			QueryContent: content,
			Annotations:  anns,
		})
	}

	return out, total, nil
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	p := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			p = append(p, ',')
		}
		p = append(p, '?')
	}
	return string(p)
}

func strsToIface(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// SessionDetail holds progress info for one annotation session.
type SessionDetail struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	TotalCount int      `json:"total_count"`
	DoneCount  int      `json:"done_count"`
	QueryIDs   []string `json:"query_ids"`
	CreatedAt  string   `json:"created_at"`
}

// GetSessionDetail returns progress info for a session.
func (r *Repo) GetSessionDetail(sessionID string) (*SessionDetail, error) {
	d := &SessionDetail{}
	if err := r.db.QueryRow(
		`SELECT id, status, query_count, created_at FROM annotation_sessions WHERE id=?`, sessionID,
	).Scan(&d.ID, &d.Status, &d.TotalCount, &d.CreatedAt); err != nil {
		return nil, err
	}

	_ = r.db.QueryRow(
		`SELECT COUNT(*) FROM annotation_session_queries WHERE session_id=? AND label_status IN ('prelabeled','reviewed','finalized')`,
		sessionID,
	).Scan(&d.DoneCount)

	rows, err := r.db.Query(`SELECT query_id FROM annotation_session_queries WHERE session_id=? ORDER BY selected_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var qid string
		if err := rows.Scan(&qid); err != nil {
			return nil, err
		}
		d.QueryIDs = append(d.QueryIDs, qid)
	}
	_ = rows.Err()

	return d, nil
}

func (r *Repo) DistinctTopics() ([]string, error) {
	rows, err := r.db.Query(`SELECT DISTINCT topic FROM eval_corpus WHERE topic IS NOT NULL AND topic != '' ORDER BY topic`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var topics []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

// GetQueryContent returns the content of a single query by ID.
func (r *Repo) GetQueryContent(queryID string) (string, error) {
	var content string
	err := r.db.QueryRow(`SELECT content FROM raw_queries WHERE id=?`, queryID).Scan(&content)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content), nil
}

// BatchGetCorpusContent 批量获取 corpus 内容，返回 corpusID → content 映射。
func (r *Repo) BatchGetCorpusContent(corpusIDs []string) map[string]string {
	if len(corpusIDs) == 0 {
		return nil
	}
	// 构建 IN 查询
	placeholders := make([]byte, 0, len(corpusIDs)*2)
	args := make([]interface{}, len(corpusIDs))
	for i, id := range corpusIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	rows, err := r.db.Query(
		fmt.Sprintf("SELECT id, content FROM eval_corpus WHERE id IN (%s)", string(placeholders)),
		args...,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make(map[string]string, len(corpusIDs))
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			continue
		}
		out[id] = content
	}
	return out
}
