package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// GetBillStatsTool — 获取账单统计摘要
type GetBillStatsTool struct {
	billRepo *service.BillRepo
}

func NewGetBillStatsTool(billRepo *service.BillRepo) *GetBillStatsTool {
	return &GetBillStatsTool{billRepo: billRepo}
}

func (t *GetBillStatsTool) Name() string { return "get_bill_stats" }
func (t *GetBillStatsTool) Description() string {
	return "获取用户账单的结构化统计摘要：各类目消费趋势、购买周期、近期高频条目等"
}

func (t *GetBillStatsTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "get_bill_stats",
		Display:     "账单统计",
		Description: "获取账单统计摘要。返回近 90 天的消费统计：按一级分类汇总、近 30 天明细条数、高频条目（以帮助发现规律）。非账单类主题调用时返回空统计",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}}
}

func (t *GetBillStatsTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	now := time.Now().In(config.BJ)
	since := now.AddDate(0, -3, 0) // 近 90 天

	bills, _, err := t.billRepo.ListFiltered(ec.Ctx, ec.UserID,
		since.Format("2006-01-02"), now.Format("2006-01-02"), "", "", "", 200, 0)
	if err != nil {
		log.Printf("[get_bill_stats] ListFiltered error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询账单失败: %v", err)}
	}

	if len(bills) == 0 {
		data, _ := json.Marshal(map[string]interface{}{
			"summary":    "（近 90 天无账单记录）\n",
			"bill_count": 0,
		})
		return &Result{Success: true, Data: data}
	}

	// 按一级分类汇总
	categoryTotals := map[string]float64{}
	categoryItems := map[string]map[string]int{} // category → item → count
	recentBills := make([]model.Bill, 0)
	thirtyDaysAgo := now.AddDate(0, 0, -30)

	for _, b := range bills {
		categoryTotals[b.Category1] += b.Amount
		if categoryItems[b.Category1] == nil {
			categoryItems[b.Category1] = map[string]int{}
		}
		categoryItems[b.Category1][b.Item]++
		if b.Date >= thirtyDaysAgo.Format("2006-01-02") {
			recentBills = append(recentBills, b)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[bill_stats] 近90天共 %d 笔账单\n", len(bills)))
	sb.WriteString("## 按一级分类汇总\n")
	for cat, total := range categoryTotals {
		sb.WriteString(fmt.Sprintf("- %s: ¥%.2f\n", cat, total))
	}

	sb.WriteString("\n## 各类高频条目（可能有助于发现消耗周期）\n")
	for cat, items := range categoryItems {
		var topItem string
		topCount := 0
		for item, count := range items {
			if count > topCount {
				topCount = count
				topItem = item
			}
		}
		if topItem != "" && topCount >= 2 {
			sb.WriteString(fmt.Sprintf("- %s: 「%s」出现 %d 次\n", cat, topItem, topCount))
		}
	}

	sb.WriteString(fmt.Sprintf("\n## 近30天明细（%d 笔）\n", len(recentBills)))
	for _, b := range recentBills {
		sb.WriteString(fmt.Sprintf("- [%s] %s ¥%.2f (%s/%s)\n", b.Date, b.Item, b.Amount, b.Category1, b.Category2))
	}

	data, _ := json.Marshal(map[string]interface{}{
		"summary":    sb.String(),
		"bill_count": len(bills),
	})

	return &Result{Success: true, Data: data}
}
