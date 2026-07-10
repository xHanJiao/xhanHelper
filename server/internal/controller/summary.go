package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// SummaryHandler — 摘要 REST API
type SummaryHandler struct {
	summaryRepo *service.ConversationSummaryRepo
}

// NewSummaryHandler 创建 SummaryHandler
func NewSummaryHandler(summaryRepo *service.ConversationSummaryRepo) *SummaryHandler {
	return &SummaryHandler{summaryRepo: summaryRepo}
}

// ListSummaries GET /api/summaries?limit=50&offset=0&keyword=
func (h *SummaryHandler) ListSummaries(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	summaries, total, err := h.summaryRepo.ListByUser(c.Request.Context(), userID,
		c.Query("keyword"), limit, offset)
	if err != nil {
		log.Printf("[summary] List error: %v", err)
		view.JSONError(c, 500, "query summaries failed")
		return
	}
	view.JSONOK(c, gin.H{"summaries": summaries, "total": total})
}
