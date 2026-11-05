package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ReflectionAdminHandler — 反思可观测性 API（管理端）
type ReflectionAdminHandler struct {
	traceRepo *service.ReflectionTraceRepo
}

func NewReflectionAdminHandler(repo *service.ReflectionTraceRepo) *ReflectionAdminHandler {
	return &ReflectionAdminHandler{traceRepo: repo}
}

// ListTraces GET /api/admin/reflection/traces
func (h *ReflectionAdminHandler) ListTraces(c *gin.Context) {
	userID := c.GetString("user_id")
	limit := parseListLimit(c, 20)
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, total, err := h.traceRepo.ListTraces(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Printf("[reflection_admin] ListTraces error: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}
	view.JSONOK(c, gin.H{"items": list, "total": total})
}

// GetTrace GET /api/admin/reflection/traces/:id
func (h *ReflectionAdminHandler) GetTrace(c *gin.Context) {
	traceID := c.Param("id")
	trace, err := h.traceRepo.GetTraceByID(c.Request.Context(), traceID)
	if err != nil || trace == nil {
		view.JSONError(c, 404, "trace 不存在")
		return
	}
	llmCalls, _ := h.traceRepo.GetLLMCallsByTrace(c.Request.Context(), traceID)
	decisions, _ := h.traceRepo.GetDecisionsByTrace(c.Request.Context(), traceID)
	view.JSONOK(c, gin.H{
		"trace":     trace,
		"llm_calls": llmCalls,
		"decisions": decisions,
	})
}

// ListDecisions GET /api/admin/reflection/decisions
func (h *ReflectionAdminHandler) ListDecisions(c *gin.Context) {
	userID := c.GetString("user_id")
	limit := parseListLimit(c, 50)
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	stage := c.Query("stage")
	verdict := c.Query("verdict")

	list, total, err := h.traceRepo.ListDecisions(c.Request.Context(), userID, stage, verdict, limit, offset)
	if err != nil {
		log.Printf("[reflection_admin] ListDecisions error: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}
	view.JSONOK(c, gin.H{"items": list, "total": total})
}

// GetLLMCall GET /api/admin/reflection/llm-calls/:id
func (h *ReflectionAdminHandler) GetLLMCall(c *gin.Context) {
	callID := c.Param("id")
	// 通过 trace 间接获取 LLM 调用（需要知道 traceID）
	// 简化处理：前端可通过 trace 详情查阅
	traceID := c.Query("trace_id")
	if traceID != "" {
		calls, err := h.traceRepo.GetLLMCallsByTrace(c.Request.Context(), traceID)
		if err != nil {
			log.Printf("[reflection_admin] GetLLMCallsByTrace error: %v", err)
			view.JSONError(c, 500, "查询失败")
			return
		}
		for _, call := range calls {
			if call.ID == callID {
				view.JSONOK(c, call)
				return
			}
		}
	}
	view.JSONError(c, 404, "LLM 调用记录不存在")
}
