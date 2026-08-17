package controller

import (
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// CostRoutesConfig LLM 成本统计 API 路由依赖
type CostRoutesConfig struct {
	R               gin.IRouter
	AuthMiddleware  gin.HandlerFunc
	AdminMiddleware gin.HandlerFunc
	Repo            *service.LlmCostRepo
}

// RegisterCostRoutes 注册成本统计 API 路由（Admin）
func RegisterCostRoutes(rc CostRoutesConfig) {
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	{
		cost := admin.Group("/cost")
		{
			h := &CostHandler{repo: rc.Repo}
			cost.GET("/stats", h.Stats)
			cost.GET("/summary", h.Summary)
			cost.GET("/details", h.Details)
			cost.GET("/categories", h.Categories)
			cost.GET("/users", h.Users)
		}
	}
}

// CostHandler LLM 成本统计
type CostHandler struct {
	repo *service.LlmCostRepo
}

// queryUserIDs 解析多选用户参数（?user_id=a&user_id=b）
func queryUserIDs(c *gin.Context) []string {
	ids := c.QueryArray("user_id")
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// Stats GET /api/admin/cost/stats — 聚合查询（图表数据源）
func (h *CostHandler) Stats(c *gin.Context) {
	groupBy := c.Query("group_by")
	if groupBy == "" {
		view.JSONError(c, 400, "group_by required: date|category|model|user_id|sub_category")
		return
	}

	res, err := h.repo.Query(c.Request.Context(), service.QueryParams{
		Start:    c.Query("start"),
		End:      c.Query("end"),
		UserIDs:  queryUserIDs(c),
		Category: c.Query("category"),
		GroupBy:  groupBy,
	})
	if err != nil {
		log.Printf("[cost] stats error: %v", err)
		view.JSONError(c, 500, "query cost stats failed")
		return
	}
	view.JSONOK(c, res)
}

// Summary GET /api/admin/cost/summary — 概览卡片（今日/昨日/本月）
func (h *CostHandler) Summary(c *gin.Context) {
	userIDs := queryUserIDs(c)
	category := c.Query("category")
	ctx := c.Request.Context()
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	firstOfMonth := time.Now().AddDate(0, 0, -time.Now().Day()+1).Format("2006-01-02")

	todayTotal, err := h.repo.Summary(ctx, today, today, userIDs, category)
	if err != nil {
		log.Printf("[cost] summary today error: %v", err)
		view.JSONError(c, 500, "query cost summary failed")
		return
	}
	yesterdayTotal, err := h.repo.Summary(ctx, yesterday, yesterday, userIDs, category)
	if err != nil {
		log.Printf("[cost] summary yesterday error: %v", err)
		view.JSONError(c, 500, "query cost summary failed")
		return
	}
	monthTotal, err := h.repo.Summary(ctx, firstOfMonth, today, userIDs, category)
	if err != nil {
		log.Printf("[cost] summary month error: %v", err)
		view.JSONError(c, 500, "query cost summary failed")
		return
	}

	view.JSONOK(c, gin.H{
		"today":      todayTotal,
		"yesterday":  yesterdayTotal,
		"this_month": monthTotal,
	})
}

// Details GET /api/admin/cost/details — 调用明细分页
func (h *CostHandler) Details(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	rows, total, err := h.repo.ListDetails(c.Request.Context(), service.ListParams{
		Start:    c.Query("start"),
		End:      c.Query("end"),
		UserIDs:  queryUserIDs(c),
		Category: c.Query("category"),
		Date:     c.Query("date"),
	}, page, pageSize)
	if err != nil {
		log.Printf("[cost] details error: %v", err)
		view.JSONError(c, 500, "query cost details failed")
		return
	}
	view.JSONOK(c, gin.H{"total": total, "rows": rows})
}

// Categories GET /api/admin/cost/categories — 分类下拉列表
func (h *CostHandler) Categories(c *gin.Context) {
	view.JSONOK(c, []string{
		llmcost.CategoryMainChat,
		llmcost.CategoryPostChat,
		llmcost.CategoryReflection,
		llmcost.CategoryTool,
		llmcost.CategoryAdmin,
	})
}

// Users GET /api/admin/cost/users — 用户下拉列表（多选）
func (h *CostHandler) Users(c *gin.Context) {
	users, err := h.repo.ListUsers(c.Request.Context(), c.Query("start"), c.Query("end"))
	if err != nil {
		log.Printf("[cost] users error: %v", err)
		view.JSONError(c, 500, "query cost users failed")
		return
	}
	if users == nil {
		users = []service.UserCostSummary{}
	}
	view.JSONOK(c, users)
}
