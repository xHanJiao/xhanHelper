package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// FactHandler — 事实（facts）只读 REST API。
// 只读：事实的增删改一律由后台决策段（agent.FactWorker）产生，无对外写入口。
type FactHandler struct {
	factRepo *service.FactRepo
}

// NewFactHandler 创建 FactHandler
func NewFactHandler(factRepo *service.FactRepo) *FactHandler {
	return &FactHandler{factRepo: factRepo}
}

// ListFacts GET /api/facts?limit=50&offset=0
func (h *FactHandler) ListFacts(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	facts, total, err := h.factRepo.ListByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Printf("[fact] List error: %v", err)
		view.JSONError(c, 500, "query facts failed")
		return
	}
	// nil 切片会 marshal 成 null，客户端按 [Fact] 解码会抛错 —— 归一成空数组。
	if facts == nil {
		facts = []model.Fact{}
	}
	view.JSONOK(c, gin.H{"items": facts, "total": total})
}
