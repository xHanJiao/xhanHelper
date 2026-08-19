package controller

import (
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// CostRoutesConfig LLM 成本统计 API 路由依赖
type CostRoutesConfig struct {
	R               gin.IRouter
	AuthMiddleware  gin.HandlerFunc
	AdminMiddleware gin.HandlerFunc
	Repo            *service.LlmCostRepo
	PriceRepo       *service.LlmPriceRepo
}

// RegisterCostRoutes 注册成本统计 API 路由（Admin）
func RegisterCostRoutes(rc CostRoutesConfig) {
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	{
		cost := admin.Group("/cost")
		{
			h := &CostHandler{repo: rc.Repo, priceRepo: rc.PriceRepo}
			cost.GET("/stats", h.Stats)
			cost.GET("/summary", h.Summary)
			cost.GET("/details", h.Details)
			cost.GET("/categories", h.Categories)
			cost.GET("/users", h.Users)
			cost.GET("/models", h.Models)
			cost.GET("/expense", h.Expense)
			cost.GET("/hourly", h.Hourly)
			cost.GET("/prices", h.Prices)
			cost.POST("/prices", h.SavePrice)
			cost.DELETE("/prices/:id", h.DeletePrice)
		}
	}
}

// CostHandler LLM 成本统计
type CostHandler struct {
	repo      *service.LlmCostRepo
	priceRepo *service.LlmPriceRepo
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

// Models GET /api/admin/cost/models — 模型下拉列表（定价表单用）
func (h *CostHandler) Models(c *gin.Context) {
	models, err := h.repo.ListModels(c.Request.Context())
	if err != nil {
		log.Printf("[cost] models error: %v", err)
		view.JSONError(c, 500, "query cost models failed")
		return
	}
	view.JSONOK(c, models)
}

// Expense GET /api/admin/cost/expense — 花销聚合（每日/分类/模型，按价格表折算）
func (h *CostHandler) Expense(c *gin.Context) {
	prices, err := h.priceRepo.List(c.Request.Context())
	if err != nil {
		log.Printf("[cost] expense prices error: %v", err)
		view.JSONError(c, 500, "query model prices failed")
		return
	}
	res, err := h.repo.ExpenseQuery(c.Request.Context(), service.QueryParams{
		Start:    c.Query("start"),
		End:      c.Query("end"),
		UserIDs:  queryUserIDs(c),
		Category: c.Query("category"),
	}, prices)
	if err != nil {
		log.Printf("[cost] expense error: %v", err)
		view.JSONError(c, 500, "query cost expense failed")
		return
	}
	view.JSONOK(c, res)
}

// Hourly GET /api/admin/cost/hourly — 单日分时（24 小时）token 与花销
func (h *CostHandler) Hourly(c *gin.Context) {
	date := c.Query("date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		view.JSONError(c, 400, "date required, format YYYY-MM-DD")
		return
	}
	prices, err := h.priceRepo.List(c.Request.Context())
	if err != nil {
		log.Printf("[cost] hourly prices error: %v", err)
		view.JSONError(c, 500, "query model prices failed")
		return
	}
	res, err := h.repo.HourlyQuery(c.Request.Context(), date, service.QueryParams{
		UserIDs:  queryUserIDs(c),
		Category: c.Query("category"),
	}, prices)
	if err != nil {
		log.Printf("[cost] hourly error: %v", err)
		view.JSONError(c, 500, "query cost hourly failed")
		return
	}
	view.JSONOK(c, res)
}

// Prices GET /api/admin/cost/prices — 价格记录列表
func (h *CostHandler) Prices(c *gin.Context) {
	rows, err := h.priceRepo.List(c.Request.Context())
	if err != nil {
		log.Printf("[cost] prices error: %v", err)
		view.JSONError(c, 500, "query model prices failed")
		return
	}
	view.JSONOK(c, rows)
}

// SavePrice POST /api/admin/cost/prices — 新增/更新价格（同模型同日期覆盖）
func (h *CostHandler) SavePrice(c *gin.Context) {
	var p model.LlmModelPrice
	if err := c.ShouldBindJSON(&p); err != nil {
		view.JSONError(c, 400, "invalid price payload: "+err.Error())
		return
	}
	if p.Model == "" {
		view.JSONError(c, 400, "model required")
		return
	}
	if _, err := time.Parse("2006-01-02", p.EffectiveDate); err != nil {
		view.JSONError(c, 400, "effective_date must be YYYY-MM-DD")
		return
	}
	if p.PriceHit < 0 || p.PriceMiss < 0 || p.PriceOutput < 0 ||
		p.PriceHitOff < 0 || p.PriceMissOff < 0 || p.PriceOutputOff < 0 {
		view.JSONError(c, 400, "prices must be >= 0")
		return
	}
	norm, err := service.NormalizePeakSegments(p.PeakSegments)
	if err != nil {
		view.JSONError(c, 400, "peak_segments: "+err.Error())
		return
	}
	p.PeakSegments = norm
	if err := h.priceRepo.Save(c.Request.Context(), &p); err != nil {
		log.Printf("[cost] save price error: %v", err)
		view.JSONError(c, 500, "save model price failed")
		return
	}
	view.JSONOK(c, p)
}

// DeletePrice DELETE /api/admin/cost/prices/:id — 删除价格记录
func (h *CostHandler) DeletePrice(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		view.JSONError(c, 400, "invalid price id")
		return
	}
	if err := h.priceRepo.Delete(c.Request.Context(), uint(id)); err != nil {
		log.Printf("[cost] delete price error: %v", err)
		view.JSONError(c, 500, "delete model price failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": id})
}
