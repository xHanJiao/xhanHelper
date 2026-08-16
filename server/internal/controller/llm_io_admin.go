package controller

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// LLMIOAuditRoutesConfig — LLM 审计 API 路由依赖。
type LLMIOAuditRoutesConfig struct {
	R               gin.IRouter
	AuthMiddleware  gin.HandlerFunc
	AdminMiddleware gin.HandlerFunc
	Repo            *service.LLMCallIORepo
}

// RegisterLLMIOAuditRoutes 注册 LLM 审计 API 路由（Admin）。
func RegisterLLMIOAuditRoutes(rc LLMIOAuditRoutesConfig) {
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	h := NewLLMIOAuditHandler(rc.Repo)
	admin.GET("/llm-io", h.List)
	admin.GET("/llm-io/:id", h.Get)
}

// LLMIOAuditHandler — LLM 调用审计（Admin）。
type LLMIOAuditHandler struct {
	repo *service.LLMCallIORepo
}

// NewLLMIOAuditHandler 创建审计 handler。
func NewLLMIOAuditHandler(repo *service.LLMCallIORepo) *LLMIOAuditHandler {
	return &LLMIOAuditHandler{repo: repo}
}

// List GET /api/admin/llm-io — 分页列表（不含大字段）。
func (h *LLMIOAuditHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	rows, total, err := h.repo.List(c.Request.Context(), service.LLMIOListParams{
		Category:    c.Query("category"),
		SubCategory: c.Query("sub_category"),
		ConvID:      c.Query("conv_id"),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		log.Printf("[llm_io] List error: %v", err)
		view.JSONError(c, http.StatusInternalServerError, "list llm io failed")
		return
	}
	view.JSONOK(c, gin.H{"data": rows, "total": total})
}

// Get GET /api/admin/llm-io/:id — 详情（含大字段）。
func (h *LLMIOAuditHandler) Get(c *gin.Context) {
	row, err := h.repo.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		log.Printf("[llm_io] GetByID error: %v", err)
		view.JSONError(c, http.StatusInternalServerError, "get llm io failed")
		return
	}
	if row == nil {
		view.JSONError(c, http.StatusNotFound, "llm io record not found")
		return
	}
	view.JSONOK(c, row)
}
