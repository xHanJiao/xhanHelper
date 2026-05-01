package controller

import (
	"bufio"
	"fmt"
	"time"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// ImportBills POST /api/bills/import
func (h *BillHandler) ImportBills(c *gin.Context) {
	dev := c.MustGet("device").(*model.Device)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		view.JSONError(c, 400, "read body failed")
		return
	}

	imported := 0
	skipped := 0
	var errs []string

	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		// 跳过标题行
		if lineNum == 1 && strings.Contains(line, "项目") {
			continue
		}

		fields := strings.Split(line, ",")
		if len(fields) < 8 {
			skipped++
			errs = append(errs, fmt.Sprintf("第%d行: 字段不足 (需要至少8列，实际%d列)", lineNum, len(fields)))
			continue
		}

		amount, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil || amount == 0 {
			skipped++
			errs = append(errs, fmt.Sprintf("第%d行: 金额解析失败 '%s'", lineNum, fields[1]))
			continue
		}

		item := strings.TrimSpace(fields[0])
		category1 := strings.TrimSpace(fields[2])
		category2 := strings.TrimSpace(fields[3])
		consumeType := strings.TrimSpace(fields[4])
		date := strings.TrimSpace(fields[5])
		date = strings.ReplaceAll(date, "/", "-") // 2026/01/10 → 2026-01-10
		payType := strings.TrimSpace(fields[6])
		owner := strings.TrimSpace(fields[7])
		remark := ""
		if len(fields) > 8 {
			remark = strings.TrimSpace(fields[8])
		}

		// 校验枚举值
		if !contains(model.Category1List, category1) {
			skipped++
			errs = append(errs, fmt.Sprintf("第%d行: 无效一级分类 '%s'", lineNum, category1))
			continue
		}
		if !contains(model.Category2List, category2) {
			skipped++
			errs = append(errs, fmt.Sprintf("第%d行: 无效二级分类 '%s'", lineNum, category2))
			continue
		}

		bill := &model.Bill{
			ID:          uuid.New().String(),
			DeviceID:    dev.ID,
			Item:        item,
			Amount:      amount,
			Category1:   category1,
			Category2:   category2,
			ConsumeType: consumeType,
			Date:        date,
			PayType:     payType,
			Owner:       owner,
			Remark:      remark,
			Status:      model.BillStatusNormal,
		}
		if err := h.billRepo.Create(c.Request.Context(), bill); err != nil {
			skipped++
			errs = append(errs, fmt.Sprintf("第%d行: 写入失败 - %s", lineNum, err.Error()))
			continue
		}
		imported++
	}

	view.JSONOK(c, gin.H{
		"imported": imported,
		"skipped":  skipped,
		"total":    imported + skipped,
		"errors":   errs,
	})
}

// ExportBills GET /api/bills/export
func (h *BillHandler) ExportBills(c *gin.Context) {
	dev := c.MustGet("device").(*model.Device)

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	category := c.Query("category")

	// 默认最近7天
	if startDate == "" && endDate == "" {
		endDate = time.Now().Format("2006-01-02")
		startDate = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	}

	bills, err := h.billRepo.ListFiltered(c.Request.Context(), dev.ID, startDate, endDate, category)
	if err != nil {
		view.JSONError(c, 500, "query bills failed")
		return
	}

	var sb strings.Builder
	// CSV 标题（与 import 格式一致）
	sb.WriteString("项目,实际消费,一级分类,二级分类,消费类别,日期,消费类型,人员,备注\n")
	for _, b := range bills {
		sb.WriteString(fmt.Sprintf("%s,%.2f,%s,%s,%s,%s,%s,%s,%s\n",
			b.Item, b.Amount, b.Category1, b.Category2,
			b.ConsumeType, b.Date, b.PayType, b.Owner, b.Remark))
	}

	filename := fmt.Sprintf("xhan-helper-bills-%s.csv", time.Now().Format("2006-01-02"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.String(200, sb.String())
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
