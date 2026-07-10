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
	msgRepo     *service.MessageRepo
}

// NewSummaryHandler 创建 SummaryHandler
func NewSummaryHandler(summaryRepo *service.ConversationSummaryRepo, msgRepo *service.MessageRepo) *SummaryHandler {
	return &SummaryHandler{summaryRepo: summaryRepo, msgRepo: msgRepo}
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

// GetSummaryMessages GET /api/summaries/:id/messages
func (h *SummaryHandler) GetSummaryMessages(c *gin.Context) {
	userID := c.GetString("user_id")
	summaryID := c.Param("id")

	s, err := h.summaryRepo.GetByID(c.Request.Context(), summaryID, userID)
	if err != nil {
		log.Printf("[summary] GetByID error: %v", err)
		view.JSONError(c, 500, "query summary failed")
		return
	}
	if s == nil {
		view.JSONError(c, 404, "summary not found")
		return
	}

	if s.CoverFromMsgID == "" || s.CoverToMsgID == "" {
		view.JSONOK(c, gin.H{"summary": s, "messages": []interface{}{}})
		return
	}

	msgs, err := h.msgRepo.ListMessagesBetweenMsgIDs(c.Request.Context(), s.ConversationID, s.CoverFromMsgID, s.CoverToMsgID, 0)
	if err != nil {
		log.Printf("[summary] ListMessagesBetweenMsgIDs error: %v", err)
		view.JSONError(c, 500, "query messages failed")
		return
	}

	filtered := make([]interface{}, 0, len(msgs))
	for i := range msgs {
		if msgs[i].Role != "tool" {
			filtered = append(filtered, msgs[i])
		}
	}

	view.JSONOK(c, gin.H{"summary": s, "messages": filtered})
}
