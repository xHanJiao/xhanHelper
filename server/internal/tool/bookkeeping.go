package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// BookkeepingTool — 记账工具
type BookkeepingTool struct {
	billRepo *service.BillRepo
}

// NewBookkeepingTool 创建记账工具
func NewBookkeepingTool(billRepo *service.BillRepo) *BookkeepingTool {
	return &BookkeepingTool{billRepo: billRepo}
}

func (t *BookkeepingTool) Name() string { return "bookkeeping" }
func (t *BookkeepingTool) Description() string {
	return "记账工具，可以新增、修改、删除、查询账单，并且查看账单的统计数据"
}

func (t *BookkeepingTool) Functions() []FuncSchema {
	// 共享的字段定义（有枚举约束）
	category1Field := func() map[string]interface{} {
		enums := make([]interface{}, len(model.Category1List))
		for i, v := range model.Category1List {
			enums[i] = v
		}
		return map[string]interface{}{"type": "string", "enum": enums, "description": "一级分类，尽量从列表中选，如果实在选不出来，可以询问用户后自定义输入"}
	}
	category2Field := func() map[string]interface{} {
		enums := make([]interface{}, len(model.Category2List))
		for i, v := range model.Category2List {
			enums[i] = v
		}
		return map[string]interface{}{"type": "string", "enum": enums, "description": "二级分类，尽量从列表中选，如果实在选不出来，可以询问用户后自定义输入"}
	}
	consumeTypeField := func() map[string]interface{} {
		return map[string]interface{}{"type": "string", "enum": []interface{}{"长期", "一次性", "周期"}, "description": "消费类型，可以根据账单的持续性选择，长期表示会持续很久的（比如房租），一次性表示只发生过一次的（比如买个东西），周期表示会定期发生的（比如每个月交水电费）"}
	}
	payTypeField := func() map[string]interface{} {
		return map[string]interface{}{"type": "string", "enum": []interface{}{"现金", "公积金"}, "description": "支付方式，默认现金"}
	}

	return []FuncSchema{
		{
			Name:        "create_bill",
			Description: "新增一笔账单",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"item":         map[string]interface{}{"type": "string", "description": "项目名称"},
					"amount":       map[string]interface{}{"type": "number", "description": "金额"},
					"category1":    category1Field(),
					"category2":    category2Field(),
					"consume_type": consumeTypeField(),
					"date":         map[string]interface{}{"type": "string", "description": "日期，格式必须为 YYYY-MM-DD（如 2026-05-28），默认今天"},
					"pay_type":     payTypeField(),
					"owner":        map[string]interface{}{"type": "string", "description": "归属人"},
					"remark":       map[string]interface{}{"type": "string", "description": "备注"},
				},
				"required": []interface{}{"item", "amount", "category1", "category2", "consume_type"},
			},
		},
		{
			Name:        "update_bill",
			Description: "修改已有账单，id 必填",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":           map[string]interface{}{"type": "string", "description": "账单ID (必填)"},
					"item":         map[string]interface{}{"type": "string", "description": "项目名称"},
					"amount":       map[string]interface{}{"type": "number", "description": "金额"},
					"category1":    category1Field(),
					"category2":    category2Field(),
					"consume_type": consumeTypeField(),
					"date":         map[string]interface{}{"type": "string", "description": "日期，格式必须为 YYYY-MM-DD（如 2026-05-28）"},
					"pay_type":     payTypeField(),
					"owner":        map[string]interface{}{"type": "string", "description": "归属人"},
					"remark":       map[string]interface{}{"type": "string", "description": "备注"},
				},
				"required": []interface{}{"id"},
			},
		},
		{
			Name:        "cancel_bill",
			Description: "删除一笔账单（软删除），id 必填",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "账单ID (必填)"},
				},
				"required": []interface{}{"id"},
			},
		},
		{
			Name:        "query_bills",
			Description: "查询账单列表，可按时间范围、分类和关键词筛选。最多返回 200 条",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_date": map[string]interface{}{"type": "string", "description": "开始日期，格式 YYYY-MM-DD，例如 2026-01-01"},
					"end_date":   map[string]interface{}{"type": "string", "description": "结束日期，格式 YYYY-MM-DD，例如 2026-12-31"},
					"category":   map[string]interface{}{"type": "string", "description": "按一级分类筛选，可选"},
					"category2":  map[string]interface{}{"type": "string", "description": "按二级分类筛选，可选"},
					"keyword":    map[string]interface{}{"type": "string", "description": "按项目名称或备注模糊搜索，可选"},
				},
			},
		},
		{
			Name:        "get_bill_by_id",
			Description: "根据 ID 查询单条账单详情",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "账单ID（必填）"},
				},
				"required": []interface{}{"id"},
			},
		},
		{
			Name:        "get_monthly_stats",
			Description: "按月汇总账单金额和数量，用于月度分析和对比。可筛选一级/二级分类",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"year":      map[string]interface{}{"type": "integer", "description": "年份，如2026。不传默认当前年"},
					"category":  map[string]interface{}{"type": "string", "description": "按一级分类筛选，可选"},
					"category2": map[string]interface{}{"type": "string", "description": "按二级分类筛选，可选"},
				},
			},
		},
		{
			Name:        "get_category_stats",
			Description: "按一级分类汇总账单金额，用于消费结构分析",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_date": map[string]interface{}{"type": "string", "description": "开始日期，格式 YYYY-MM-DD，例如 2026-01-01"},
					"end_date":   map[string]interface{}{"type": "string", "description": "结束日期，格式 YYYY-MM-DD，例如 2026-12-31"},
				},
			},
		},
		{
			Name:        "get_category_options",
			Description: "获取一级分类和对应二级分类的映射关系，用于了解可用的分类选项",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

// --- 参数类型 ---

type billArgs struct {
	ID          string  `json:"id,omitempty"`
	Item        string  `json:"item,omitempty"`
	Amount      float64 `json:"amount,omitempty"`
	Category1   string  `json:"category1,omitempty"`
	Category2   string  `json:"category2,omitempty"`
	ConsumeType string  `json:"consume_type,omitempty"`
	Date        string  `json:"date,omitempty"`
	PayType     string  `json:"pay_type,omitempty"`
	Owner       string  `json:"owner,omitempty"`
	Remark      string  `json:"remark,omitempty"`
}

// --- Execute ---

func (t *BookkeepingTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_bill":
		return t.createBill(ec, args)
	case "update_bill":
		return t.updateBill(ec, args)
	case "cancel_bill":
		return t.deleteBill(ec, args)
	case "query_bills":
		return t.queryBills(ec, args)
	case "get_bill_by_id":
		return t.getBillByID(ec, args)
	case "get_monthly_stats":
		return t.getMonthlyStats(ec, args)
	case "get_category_stats":
		return t.getCategoryStats(ec, args)
	case "get_category_options":
		return t.getCategoryOptions(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("bookkeeping: unknown function %s", funcName)}
	}
}

func (t *BookkeepingTool) createBill(ec *ExecuteContext, args json.RawMessage) *Result {
	var a billArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.Item == "" || a.Amount == 0 {
		return &Result{Success: false, Error: "item and amount are required"}
	}
	if a.Date == "" {
		a.Date = time.Now().In(config.BJ).Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", a.Date); err != nil {
		return &Result{Success: false, Error: "日期格式错误，请使用 YYYY-MM-DD 格式，例如 2026-05-28"}
	}
	if a.PayType == "" {
		a.PayType = "现金"
	}

	bill := &model.Bill{
		ID:          uuid.New().String(),
		UserID:      ec.UserID,
		Item:        a.Item,
		Amount:      a.Amount,
		Category1:   a.Category1,
		Category2:   a.Category2,
		ConsumeType: a.ConsumeType,
		Date:        a.Date,
		PayType:     a.PayType,
		Owner:       a.Owner,
		Remark:      a.Remark,
		Status:      model.BillStatusNormal,
	}
	if err := t.billRepo.Create(ec.Ctx, bill); err != nil {
		return &Result{Success: false, Error: "create bill failed: " + err.Error()}
	}

	data, _ := json.Marshal(bill)
	return &Result{Success: true, Data: data}
}

func (t *BookkeepingTool) updateBill(ec *ExecuteContext, args json.RawMessage) *Result {
	var a billArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.ID == "" {
		return &Result{Success: false, Error: "id is required for update"}
	}

	existing, err := t.billRepo.FindByID(ec.Ctx, a.ID)
	if err != nil || existing == nil {
		return &Result{Success: false, Error: "bill not found: " + a.ID}
	}
	if existing.UserID != ec.UserID {
		return &Result{Success: false, Error: "access denied"}
	}

	// 只更新传入的非零值字段
	if a.Item != "" {
		existing.Item = a.Item
	}
	if a.Amount != 0 {
		existing.Amount = a.Amount
	}
	if a.Category1 != "" {
		existing.Category1 = a.Category1
	}
	if a.Category2 != "" {
		existing.Category2 = a.Category2
	}
	if a.ConsumeType != "" {
		existing.ConsumeType = a.ConsumeType
	}
	if a.Date != "" {
		if _, err := time.Parse("2006-01-02", a.Date); err != nil {
			return &Result{Success: false, Error: "日期格式错误，请使用 YYYY-MM-DD 格式，例如 2026-05-28"}
		}
		existing.Date = a.Date
	}
	if a.PayType != "" {
		existing.PayType = a.PayType
	}
	if a.Owner != "" {
		existing.Owner = a.Owner
	}
	if a.Remark != "" {
		existing.Remark = a.Remark
	}

	if err := t.billRepo.Update(ec.Ctx, existing); err != nil {
		return &Result{Success: false, Error: "update bill failed: " + err.Error()}
	}

	data, _ := json.Marshal(existing)
	return &Result{Success: true, Data: data}
}

func (t *BookkeepingTool) deleteBill(ec *ExecuteContext, args json.RawMessage) *Result {
	var a billArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.ID == "" {
		return &Result{Success: false, Error: "id is required for delete"}
	}

	existing, err := t.billRepo.FindByID(ec.Ctx, a.ID)
	if err != nil || existing == nil {
		return &Result{Success: false, Error: "bill not found: " + a.ID}
	}
	if existing.UserID != ec.UserID {
		return &Result{Success: false, Error: "access denied"}
	}

	if err := t.billRepo.SoftDelete(ec.Ctx, a.ID); err != nil {
		return &Result{Success: false, Error: "delete bill failed: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]string{"deleted_id": a.ID})
	return &Result{Success: true, Data: data}
}

type queryBillsArgs struct {
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
	Category  string `json:"category,omitempty"`
	Category2 string `json:"category2,omitempty"`
	Keyword   string `json:"keyword,omitempty"`
}

type getBillArgs struct {
	ID string `json:"id,omitempty"`
}

// billSummary — 返回给 LLM 的精简账单字段
type billSummary struct {
	Item      string  `json:"item"`
	Amount    float64 `json:"amount"`
	Category1 string  `json:"category1"`
	Category2 string  `json:"category2"`
	Date      string  `json:"date"`
	PayType   string  `json:"pay_type"`
	Remark    string  `json:"remark"`
}

func toBillSummaries(bills []model.Bill) []billSummary {
	result := make([]billSummary, len(bills))
	for i, b := range bills {
		result[i] = billSummary{
			Item:      b.Item,
			Amount:    b.Amount,
			Category1: b.Category1,
			Category2: b.Category2,
			Date:      b.Date,
			PayType:   b.PayType,
			Remark:    b.Remark,
		}
	}
	return result
}

func (t *BookkeepingTool) queryBills(ec *ExecuteContext, args json.RawMessage) *Result {
	var a queryBillsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		log.Printf("[bookkeeping] parse args error: %v", err)
	}

	bills, total, err := t.billRepo.ListFiltered(ec.Ctx, ec.UserID, a.StartDate, a.EndDate, a.Category, a.Category2, a.Keyword, 200, 0)
	if err != nil {
		return &Result{Success: false, Error: "query bills failed: " + err.Error()}
	}

	summaries := toBillSummaries(bills)
	result := map[string]interface{}{
		"bills": summaries,
		"total": total,
	}
	if total > 200 {
		result["notice"] = fmt.Sprintf("共 %d 条记录，仅返回前 200 条，请缩小筛选范围", total)
	}
	data, _ := json.Marshal(result)
	return &Result{Success: true, Data: data}
}

func (t *BookkeepingTool) getBillByID(ec *ExecuteContext, args json.RawMessage) *Result {
	var a getBillArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.ID == "" {
		return &Result{Success: false, Error: "id is required"}
	}

	bill, err := t.billRepo.GetBill(ec.Ctx, ec.UserID, a.ID)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	data, _ := json.Marshal(bill)
	return &Result{Success: true, Data: data}
}

type statsArgs struct {
	Year      int    `json:"year,omitempty"`
	Category  string `json:"category,omitempty"`
	Category2 string `json:"category2,omitempty"`
}

func (t *BookkeepingTool) getMonthlyStats(ec *ExecuteContext, args json.RawMessage) *Result {
	var a statsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		log.Printf("[bookkeeping] parse args error: %v", err)
	}
	if a.Year == 0 {
		a.Year = time.Now().Year()
	}

	startDate := fmt.Sprintf("%d-01-01", a.Year)
	endDate := fmt.Sprintf("%d-12-31", a.Year)

	rows, err := t.billRepo.MonthlyStats(ec.Ctx, ec.UserID, startDate, endDate, a.Category, a.Category2)
	if err != nil {
		return &Result{Success: false, Error: "monthly stats failed: " + err.Error()}
	}

	result := make(map[string]map[string]interface{})
	for _, r := range rows {
		result[r.Month] = map[string]interface{}{
			"amount": r.Amount,
			"count":  r.Count,
		}
	}

	data, _ := json.Marshal(result)
	return &Result{Success: true, Data: data}
}

func (t *BookkeepingTool) getCategoryOptions(ec *ExecuteContext, args json.RawMessage) *Result {
	mapping, err := t.billRepo.CategoryOptions(ec.Ctx, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "query category options failed: " + err.Error()}
	}
	data, _ := json.Marshal(mapping)
	return &Result{Success: true, Data: data}
}

type dateRangeArgs struct {
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

func (t *BookkeepingTool) getCategoryStats(ec *ExecuteContext, args json.RawMessage) *Result {
	var a dateRangeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		log.Printf("[bookkeeping] parse args error: %v", err)
	}

	rows, err := t.billRepo.CategoryStats(ec.Ctx, ec.UserID, a.StartDate, a.EndDate)
	if err != nil {
		return &Result{Success: false, Error: "category stats failed: " + err.Error()}
	}

	result := make(map[string]map[string]interface{})
	var totalAmount float64
	var totalCount int64
	for _, r := range rows {
		result[r.Category1] = map[string]interface{}{
			"amount": r.Amount,
			"count":  r.Count,
		}
		totalAmount += r.Amount
		totalCount += r.Count
	}
	result["_total"] = map[string]interface{}{
		"amount": totalAmount,
		"count":  totalCount,
	}

	data, _ := json.Marshal(result)
	return &Result{Success: true, Data: data}
}
