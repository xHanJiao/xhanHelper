package controller

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"time"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// BillHandler — 账单 REST API
type BillHandler struct {
	billRepo  *service.BillRepo
	extractor *agent.MemoryExtractor
}

// NewBillHandler 创建 BillHandler
func NewBillHandler(billRepo *service.BillRepo, extractor *agent.MemoryExtractor) *BillHandler {
	return &BillHandler{billRepo: billRepo, extractor: extractor}
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

	// 如果请求了提取记忆，异步分析全部账单
	if c.Query("extract_memories") == "true" && h.extractor != nil {
		bills, err := h.billRepo.ListByDevice(c.Request.Context(), dev.ID)
		if err == nil && len(bills) > 0 {
			go func() {
				log.Printf("[memory] CSV import: extracting memories from %d bills...", len(bills))
				h.extractBillMemories(dev.ID, bills)
			}()
		}
	}

	view.JSONOK(c, gin.H{
		"imported": imported,
		"skipped":  skipped,
		"total":    imported + skipped,
		"errors":   errs,
	})
}

// extractBillMemories 从账单数据中批量提取聚合记忆
func (h *BillHandler) extractBillMemories(deviceID string, bills []model.Bill) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("以下是用户的%d条账单记录:\n", len(bills)))
	for _, b := range bills[:min(len(bills), 500)] {
		sb.WriteString(fmt.Sprintf("%s | %.2f元 | %s/%s | %s | %s | %s\n",
			b.Date, b.Amount, b.Category1, b.Category2, b.ConsumeType, b.PayType, b.Item))
	}

	prompt := fmt.Sprintf(`%s
请从以上账单中提取用户的消费习惯、偏好和规律。
关注: 常去的地点、消费水平、分类偏好、周期性的规律、变化趋势。
用 "key: value" 格式，每条一句。没有值得提取的就回复"无"。`, sb.String())

	h.extractor.ExtractFromPrompt(context.Background(), deviceID, prompt, "csv_import")
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
	sb.WriteString("项目,实际消费,一级分类,二级分类,消费类别,日期,消费类型,人员,备注\n")
	// 按日期正序排列（旧→新）
	for i := len(bills) - 1; i >= 0; i-- {
		b := bills[i]
		sb.WriteString(fmt.Sprintf("%s,%.2f,%s,%s,%s,%s,%s,%s,%s\n",
			b.Item, b.Amount, b.Category1, b.Category2,
			b.ConsumeType, strings.ReplaceAll(b.Date, "-", "/"), b.PayType, b.Owner, b.Remark))
	}

	filename := fmt.Sprintf("xhan-helper-bills-%s.csv", time.Now().Format("2006-01-02"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.String(200, sb.String())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
