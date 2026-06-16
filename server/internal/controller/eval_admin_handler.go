package controller

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"xhan-helper/internal/eval"
	"xhan-helper/internal/view"
)

// EvalAdminHandler handles admin API endpoints for retrieval evaluation datasets.
type EvalAdminHandler struct {
	evalDir  string
	labelLLM eval.ChatLLM
	embedder eval.Embedder
}

// NewEvalAdminHandler creates a new EvalAdminHandler.
func NewEvalAdminHandler(evalDir string, labelLLM eval.ChatLLM, embedder eval.Embedder) *EvalAdminHandler {
	return &EvalAdminHandler{evalDir: evalDir, labelLLM: labelLLM, embedder: embedder}
}

func (h *EvalAdminHandler) dbPath(project string) string {
	return filepath.Join(h.evalDir, project, "eval.db")
}

// ListProjects GET /api/admin/eval/projects
func (h *EvalAdminHandler) ListProjects(c *gin.Context) {
	projects, err := eval.ListProjectDirs(h.evalDir)
	if err != nil {
		log.Printf("[eval_admin] ListProjects error: %v", err)
		view.JSONError(c, 500, "读取项目列表失败")
		return
	}

	var out []eval.ProjectInfo
	for _, name := range projects {
		r, err := eval.OpenRepo(h.dbPath(name))
		if err != nil {
			log.Printf("[eval_admin] open project %s: %v", name, err)
			continue
		}
		info, err := r.GetProjectInfo()
		r.Close()
		if err != nil {
			log.Printf("[eval_admin] project info %s: %v", name, err)
			continue
		}
		out = append(out, *info)
	}

	view.JSONOK(c, gin.H{"projects": out})
}

// GetStats GET /api/admin/eval/projects/:project/stats
func (h *EvalAdminHandler) GetStats(c *gin.Context) {
	project := c.Param("project")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	stats, err := r.GetStats()
	if err != nil {
		log.Printf("[eval_admin] GetStats error: %v", err)
		view.JSONError(c, 500, "查询统计失败")
		return
	}

	view.JSONOK(c, stats)
}

// ListQueries GET /api/admin/eval/projects/:project/queries
func (h *EvalAdminHandler) ListQueries(c *gin.Context) {
	project := c.Param("project")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	keyword := c.Query("keyword")
	status := c.Query("status")
	sessionID := c.Query("session_id")

	list, total, err := r.ListQueries(keyword, status, sessionID, limit, offset)
	if err != nil {
		log.Printf("[eval_admin] ListQueries error: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}

	if list == nil {
		list = []eval.QueryInfo{}
	}
	view.JSONOK(c, gin.H{"queries": list, "total": total})
}

// ListPlacedQueries GET /api/admin/eval/projects/:project/queries/placed
func (h *EvalAdminHandler) ListPlacedQueries(c *gin.Context) {
	project := c.Param("project")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	keyword := c.Query("keyword")

	list, total, err := r.ListPlacedQueries(keyword, limit, offset)
	if err != nil {
		log.Printf("[eval_admin] ListPlacedQueries: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}

	if list == nil {
		list = []eval.QueryInfo{}
	}
	view.JSONOK(c, gin.H{"queries": list, "total": total})
}

// GetQueryDetail GET /api/admin/eval/projects/:project/queries/:id
func (h *EvalAdminHandler) GetQueryDetail(c *gin.Context) {
	project := c.Param("project")
	queryID := c.Param("id")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	detail, err := r.GetQueryDetail(queryID)
	if err != nil {
		log.Printf("[eval_admin] GetQueryDetail %s: %v", queryID, err)
		view.JSONError(c, 404, "查询不存在")
		return
	}

	view.JSONOK(c, detail)
}

// UpdateAnnotations PATCH /api/admin/eval/projects/:project/queries/:id
func (h *EvalAdminHandler) UpdateAnnotations(c *gin.Context) {
	project := c.Param("project")
	queryID := c.Param("id")

	var req struct {
		Annotations  []eval.AnnotationUpdate `json:"annotations"`
		ReviewStatus string                  `json:"review_status"`
		Notes        string                  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "请求格式错误")
		return
	}

	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	if err := r.UpdateAnnotations(queryID, req.Annotations, req.ReviewStatus, req.Notes); err != nil {
		log.Printf("[eval_admin] UpdateAnnotations %s: %v", queryID, err)
		view.JSONError(c, 500, "保存失败")
		return
	}

	view.JSONOK(c, nil)
}

// ListCorpus GET /api/admin/eval/projects/:project/corpus
func (h *EvalAdminHandler) ListCorpus(c *gin.Context) {
	project := c.Param("project")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	keyword := c.Query("keyword")
	sourceType := c.Query("type")
	topic := c.Query("topic")

	list, total, err := r.ListCorpus(keyword, sourceType, topic, limit, offset)
	if err != nil {
		log.Printf("[eval_admin] ListCorpus error: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}

	if list == nil {
		list = []eval.CorpusInfo{}
	}
	view.JSONOK(c, gin.H{"corpus": list, "total": total})
}

// ListQueryAnnotations GET /api/admin/eval/projects/:project/query-annotations
// Returns paginated queries with their annotations grouped by query.
func (h *EvalAdminHandler) ListQueryAnnotations(c *gin.Context) {
	project := c.Param("project")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	queryKeyword := c.Query("keyword")
	labelSource := c.Query("label_source")
	relevance, _ := strconv.Atoi(c.DefaultQuery("relevance", "-1"))

	list, total, err := r.ListQueryAnnotations(queryKeyword, labelSource, relevance, limit, offset)
	if err != nil {
		log.Printf("[eval_admin] ListQueryAnnotations error: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}

	if list == nil {
		list = []eval.QueryWithAnnotations{}
	}
	view.JSONOK(c, gin.H{"queries": list, "total": total})
}

// ListAnnotations GET /api/admin/eval/projects/:project/annotations
func (h *EvalAdminHandler) ListAnnotations(c *gin.Context) {
	project := c.Param("project")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	queryKeyword := c.Query("query_keyword")
	labelSource := c.Query("label_source")
	relevance, _ := strconv.Atoi(c.DefaultQuery("relevance", "-1"))

	list, total, err := r.ListAnnotations(queryKeyword, labelSource, relevance, limit, offset)
	if err != nil {
		log.Printf("[eval_admin] ListAnnotations error: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}

	if list == nil {
		list = []eval.AnnotationRecord{}
	}
	view.JSONOK(c, gin.H{"annotations": list, "total": total})
}

// LabelBatch POST /api/admin/eval/projects/:project/label
func (h *EvalAdminHandler) LabelBatch(c *gin.Context) {
	project := c.Param("project")

	var req struct {
		QueryIDs       []string `json:"query_ids"`
		CandidateLimit int      `json:"candidate_limit"`
		SessionName    string   `json:"session_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "请求格式错误")
		return
	}
	if req.CandidateLimit <= 0 {
		req.CandidateLimit = 20
	}
	if req.SessionName == "" {
		req.SessionName = fmt.Sprintf("batch-%s", time.Now().Format("20060102-150405"))
	}
	if len(req.QueryIDs) > 10 {
		view.JSONError(c, 400, "单批次最多选择 10 条 query")
		return
	}

	// Create session synchronously
	sqlDB, err := sql.Open("sqlite3", h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open sqlite: %v", err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)

	labeler := eval.NewLabeler(sqlDB, h.embedder, h.labelLLM, &eval.DefaultLabelStrategy{})
	if err := labeler.CreateSession(req.SessionName, project, req.QueryIDs); err != nil {
		log.Printf("[eval_admin] CreateSession: %v", err)
		view.JSONError(c, 500, "创建批次失败")
		return
	}
	_, _ = sqlDB.Exec(`UPDATE annotation_sessions SET status='labeling' WHERE id=?`, req.SessionName)

	// Label asynchronously with 10-minute timeout
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		asyncDB, err := sql.Open("sqlite3", h.dbPath(project))
		if err != nil {
			log.Printf("[eval_admin] open sqlite async: %v", err)
			return
		}
		defer asyncDB.Close()
		asyncDB.SetMaxOpenConns(1)
		asyncLabeler := eval.NewLabeler(asyncDB, h.embedder, h.labelLLM, &eval.DefaultLabelStrategy{})

		success := 0
		for _, qid := range req.QueryIDs {
			select {
			case <-ctx.Done():
				log.Printf("[eval_admin] batch %s: context cancelled after %d/%d", req.SessionName, success, len(req.QueryIDs))
				_, _ = asyncDB.Exec(`UPDATE annotation_sessions SET status='prelabeled' WHERE id=?`, req.SessionName)
				return
			default:
			}
			if err := asyncLabeler.LabelQuery(ctx, qid, req.CandidateLimit); err != nil {
				log.Printf("[eval_admin] label %s: %v", qid, err)
				continue
			}
			success++
		}
		_, _ = asyncDB.Exec(`UPDATE annotation_sessions SET status='prelabeled' WHERE id=?`, req.SessionName)
		log.Printf("[eval_admin] batch %s done: %d/%d labeled", req.SessionName, success, len(req.QueryIDs))
	}()

	view.JSONOK(c, gin.H{"session_id": req.SessionName, "status": "started", "total": len(req.QueryIDs)})
}

// GetSession GET /api/admin/eval/projects/:project/sessions/:id
func (h *EvalAdminHandler) GetSession(c *gin.Context) {
	project := c.Param("project")
	sessionID := c.Param("id")

	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_admin] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	detail, err := r.GetSessionDetail(sessionID)
	if err != nil {
		log.Printf("[eval_admin] GetSessionDetail: %v", err)
		view.JSONError(c, 404, "批次不存在")
		return
	}

	view.JSONOK(c, detail)
}
