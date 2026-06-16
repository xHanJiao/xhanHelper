package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"path/filepath"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"xhan-helper/internal/eval"
	"xhan-helper/internal/view"
)

// EvalRunHandler 评测任务 API handler
type EvalRunHandler struct {
	evalDir string
}

func NewEvalRunHandler(evalDir string) *EvalRunHandler {
	return &EvalRunHandler{evalDir: evalDir}
}

func (h *EvalRunHandler) dbPath(project string) string {
	return filepath.Join(h.evalDir, project, "eval.db")
}

// ListRuns GET /api/admin/eval/projects/:project/runs
func (h *EvalRunHandler) ListRuns(c *gin.Context) {
	project := c.Param("project")
	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_run] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	list, err := r.ListRuns(project)
	if err != nil {
		log.Printf("[eval_run] ListRuns: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}
	if list == nil {
		list = []eval.RunRecord{}
	}
	view.JSONOK(c, gin.H{"runs": list})
}

// CreateRun POST /api/admin/eval/projects/:project/runs
func (h *EvalRunHandler) CreateRun(c *gin.Context) {
	project := c.Param("project")

	var req struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "请求格式错误")
		return
	}

	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_run] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	run, err := r.CreateRun(project, req.Name, string(req.Config))
	if err != nil {
		log.Printf("[eval_run] CreateRun: %v", err)
		view.JSONError(c, 500, "创建失败")
		return
	}

	view.JSONOK(c, run)
}

// GetRun GET /api/admin/eval/projects/:project/runs/:id
func (h *EvalRunHandler) GetRun(c *gin.Context) {
	project := c.Param("project")
	runID := c.Param("id")

	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_run] open project %s: %v", project, err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	run, err := r.GetRun(runID)
	if err != nil {
		view.JSONError(c, 404, "任务不存在")
		return
	}

	// 解析 report 和 config 为 JSON 对象
	var reportJSON, configJSON json.RawMessage
	if run.Report != "" {
		reportJSON = json.RawMessage(run.Report)
	}
	if run.Config != "" {
		configJSON = json.RawMessage(run.Config)
	}

	view.JSONOK(c, gin.H{
		"id":          run.ID,
		"project_id":  run.ProjectID,
		"name":        run.Name,
		"config":      configJSON,
		"status":      run.Status,
		"report":      reportJSON,
		"error_msg":   run.ErrorMsg,
		"created_at":  run.CreatedAt,
		"started_at":  run.StartedAt,
		"finished_at": run.FinishedAt,
	})
}

// ExecuteRun POST /api/admin/eval/projects/:project/runs/:id/execute
func (h *EvalRunHandler) ExecuteRun(c *gin.Context) {
	project := c.Param("project")
	runID := c.Param("id")

	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_run] open: %v", err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	if err := r.UpdateRunStatus(runID, "running", ""); err != nil {
		log.Printf("[eval_run] update status: %v", err)
		view.JSONError(c, 500, "更新状态失败")
		return
	}

	// 异步执行
	go func() {
		db, err := sql.Open("sqlite3", h.dbPath(project))
		if err != nil {
			h.failRun(runID, project, err.Error())
			return
		}
		defer db.Close()

		runRec, _ := r.GetRun(runID)
		if runRec == nil {
			return
		}

		var config struct {
			Engine struct {
				Type  string `json:"type"`
				Model string `json:"model"`
			} `json:"engine"`
		}
		if err := json.Unmarshal([]byte(runRec.Config), &config); err != nil || config.Engine.Type == "" {
			config.Engine.Type = "vector"
			config.Engine.Model = "doubao-embedding-vision-251215"
		}

		var engine eval.SearchEngine
		switch config.Engine.Type {
		case "vector":
			engine = eval.NewVectorEngine(config.Engine.Model)
		default:
			h.failRun(runID, project, "不支持的引擎类型: "+config.Engine.Type)
			return
		}

		evaluator := eval.NewEvaluator(db, engine)
		report, err := evaluator.Run(context.Background())
		if err != nil {
			h.failRun(runID, project, err.Error())
			log.Printf("[eval_run] evaluator %s: %v", runID, err)
			return
		}

		summaryJSON, _ := json.Marshal(report.Summary)
		detailMap := make(map[string]json.RawMessage)
		for _, q := range report.Queries {
			b, _ := json.Marshal(q)
			detailMap[q.QueryID] = b
		}
		detailJSON, _ := json.Marshal(detailMap)

		r2, err := eval.OpenRepo(h.dbPath(project))
		if err != nil {
			log.Printf("[eval_run] save open: %v", err)
			return
		}
		defer r2.Close()
		if err := r2.SaveRunResult(runID, string(summaryJSON), string(detailJSON)); err != nil {
			log.Printf("[eval_run] save result %s: %v", runID, err)
		}
	}()

	view.JSONOK(c, gin.H{"status": "running"})
}

func (h *EvalRunHandler) failRun(runID, project, errMsg string) {
	r2, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		return
	}
	defer r2.Close()
	r2.UpdateRunStatus(runID, "error", errMsg)
}

// DeleteRun DELETE /api/admin/eval/projects/:project/runs/:id
func (h *EvalRunHandler) DeleteRun(c *gin.Context) {
	project := c.Param("project")
	runID := c.Param("id")

	r, err := eval.OpenRepo(h.dbPath(project))
	if err != nil {
		log.Printf("[eval_run] open: %v", err)
		view.JSONError(c, 500, "打开项目失败")
		return
	}
	defer r.Close()

	if err := r.DeleteRun(runID); err != nil {
		log.Printf("[eval_run] DeleteRun: %v", err)
		view.JSONError(c, 500, "删除失败")
		return
	}

	view.JSONOK(c, nil)
}
