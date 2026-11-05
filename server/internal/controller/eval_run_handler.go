package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"math/rand"
	"path/filepath"
	"strconv"

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

	limit := parseListLimit(c, 20)
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, err := r.ListRuns(project, limit, offset)
	if err != nil {
		log.Printf("[eval_run] ListRuns: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}
	if list == nil {
		list = []eval.RunRecord{}
	}
	items := make([]gin.H, 0, len(list))
	for _, rr := range list {
		items = append(items, gin.H{
			"id":          rr.ID,
			"name":        rr.Name,
			"status":      rr.Status,
			"config":      rr.Config,
			"report":      rr.Report,
			"error_msg":   rr.ErrorMsg,
			"created_at":  rr.CreatedAt,
			"started_at":  rr.StartedAt,
			"finished_at": rr.FinishedAt,
		})
	}
	view.JSONOK(c, gin.H{"runs": items})
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

	resp := gin.H{
		"id":          run.ID,
		"name":        run.Name,
		"status":      run.Status,
		"config":      json.RawMessage(run.Config),
		"report":      json.RawMessage(run.Report),
		"error_msg":   run.ErrorMsg,
		"created_at":  run.CreatedAt,
		"started_at":  run.StartedAt,
		"finished_at": run.FinishedAt,
	}

	// 解析 detail JSON，给每条 query 取前 30 条结果并补 corpus_content
	if run.Status == "done" && run.Detail != "" {
		var detailMap map[string]json.RawMessage
		if err := json.Unmarshal([]byte(run.Detail), &detailMap); err == nil {
			queries := make([]gin.H, 0)
			for qid, raw := range detailMap {
				var qr eval.RankedResult
				if err := json.Unmarshal(raw, &qr); err != nil {
					continue
				}
				results := qr.Results
				limit := 30
				if limit > len(results) {
					limit = len(results)
				}
				type PreviewRow struct {
					Rank     int     `json:"rank"`
					CorpusID string  `json:"corpus_id"`
					Content  string  `json:"corpus_content"`
					Score    float64 `json:"score"`
					GT       int     `json:"gt"`
				}
				preview := make([]PreviewRow, 0, limit)
				corpusIDs := make([]string, 0, limit)
				for i := 0; i < limit; i++ {
					r := results[i]
					corpusIDs = append(corpusIDs, r.CorpusID)
					preview = append(preview, PreviewRow{
						Rank: r.Rank, CorpusID: r.CorpusID,
						Score: r.Score, GT: r.GT,
					})
				}
				// 批量补 corpus content
				contentMap, err := r.BatchGetCorpusContent(corpusIDs)
				if err != nil {
					log.Printf("[eval] batch get corpus content: %v", err)
					view.JSONError(c, 500, "load corpus content failed")
					return
				}
				for i := range preview {
					if c, ok := contentMap[preview[i].CorpusID]; ok {
						preview[i].Content = c
					}
				}
				queries = append(queries, gin.H{
					"query_id": qid,
					"query":    qr.Query,
					"summary":  qr.Metrics,
					"total":    len(qr.Results),
					"results":  preview,
				})
			}
			resp["queries"] = queries
		}
	}

	view.JSONOK(c, resp)
}

// ExecuteRun POST /api/admin/eval/projects/:project/runs/:id/execute
func (h *EvalRunHandler) ExecuteRun(c *gin.Context) {
	project := c.Param("project")
	runID := c.Param("id")

	var req struct {
		QueryIDs    []string `json:"query_ids"`
		RandomCount int      `json:"random_count"`
	}
	_ = c.ShouldBindJSON(&req)

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

	// 异步执行前：在外层读取 run config（此时 r 尚未关闭）
	runRec, _ := r.GetRun(runID)
	var engineConfig struct {
		Engine struct {
			Type  string `json:"type"`
			Model string `json:"model"`
		} `json:"engine"`
	}
	if runRec != nil {
		_ = json.Unmarshal([]byte(runRec.Config), &engineConfig)
	}
	if engineConfig.Engine.Type == "" {
		engineConfig.Engine.Type = "vector"
		engineConfig.Engine.Model = "doubao-embedding-vision-251215"
	}

	// 异步执行
	go func() {
		db, err := sql.Open("sqlite3", h.dbPath(project))
		if err != nil {
			h.failRun(runID, project, err.Error())
			return
		}
		defer db.Close()

		var engine eval.SearchEngine
		switch engineConfig.Engine.Type {
		case "vector":
			engine = eval.NewVectorEngine(engineConfig.Engine.Model)
		default:
			h.failRun(runID, project, "不支持的引擎类型: "+engineConfig.Engine.Type)
			return
		}

		// 构建 query filter：手动指定 > 随机采样 > 全部
		queryFilter := req.QueryIDs
		if len(queryFilter) == 0 && req.RandomCount > 0 {
			idRows, err := db.Query(`SELECT id FROM raw_queries WHERE id IN (SELECT DISTINCT query_id FROM eval_ground_truth)`)
			if err == nil {
				defer idRows.Close()
				var all []string
				for idRows.Next() {
					var id string
					if idRows.Scan(&id) == nil {
						all = append(all, id)
					}
				}
				rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
				n := req.RandomCount
				if n > len(all) {
					n = len(all)
				}
				queryFilter = all[:n]
				log.Printf("[eval_run] random %d queries from %d labeled", n, len(all))
			}
		}

		evaluator := eval.NewEvaluator(db, engine)
		report, err := evaluator.Run(context.Background(), queryFilter)
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
