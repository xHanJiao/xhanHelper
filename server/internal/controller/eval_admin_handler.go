package controller

import (
	"log"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/eval"
	"xhan-helper/internal/view"
)

// EvalAdminHandler handles admin API endpoints for retrieval evaluation datasets.
type EvalAdminHandler struct {
	evalDir string
}

// NewEvalAdminHandler creates a new EvalAdminHandler.
func NewEvalAdminHandler(evalDir string) *EvalAdminHandler {
	return &EvalAdminHandler{evalDir: evalDir}
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

	list, total, err := r.ListQueries(keyword, status, limit, offset)
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
