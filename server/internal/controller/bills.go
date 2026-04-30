package controller

import (
	"github.com/gin-gonic/gin"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// BillHandler — 账单 REST API
type BillHandler struct {
	billRepo *service.BillRepo
}

// NewBillHandler 创建 BillHandler
func NewBillHandler(billRepo *service.BillRepo) *BillHandler {
	return &BillHandler{billRepo: billRepo}
}

// ListBills GET /api/bills
func (h *BillHandler) ListBills(c *gin.Context) {
	dev := c.MustGet("device").(*model.Device)

	bills, err := h.billRepo.ListByDevice(c.Request.Context(), dev.ID)
	if err != nil {
		view.JSONError(c, 500, "query bills failed")
		return
	}
	view.JSONOK(c, bills)
}
