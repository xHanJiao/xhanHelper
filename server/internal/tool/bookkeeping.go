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
			Display:     "记账",
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
				"required": []interface{}{"item", "amount", "category1", "category2", "consume_type", "owner"},
			},
		},
		{
			Name:        "update_bill",
			Display:     "修改账单",
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
			Name:        "batch_update_bills",
			Display:     "批量修改账单",
			Description: "根据筛选条件批量修改账单的归属人、分类、消费/支付类型或备注。筛选条件与 query_bills 相同（需至少指定一个筛选条件，防止误改全量）。最多更新 500 条",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_date":       map[string]interface{}{"type": "string", "description": "筛选-开始日期，格式 YYYY-MM-DD"},
					"end_date":         map[string]interface{}{"type": "string", "description": "筛选-结束日期，格式 YYYY-MM-DD"},
					"category":         map[string]interface{}{"type": "string", "description": "筛选-按一级分类"},
					"category2":        map[string]interface{}{"type": "string", "description": "筛选-按二级分类"},
					"keyword":          map[string]interface{}{"type": "string", "description": "筛选-按项目名称或备注模糊搜索"},
					"owner":            map[string]interface{}{"type": "string", "description": "筛选-按归属人"},
					"owner_is_empty":   map[string]interface{}{"type": "boolean", "description": "筛选-归属人为空字符串的账单。设为 true 时忽略 owner 参数"},
					"amount_min":       map[string]interface{}{"type": "number", "description": "筛选-金额下限（含）"},
					"amount_max":       map[string]interface{}{"type": "number", "description": "筛选-金额上限（含）"},
					"consume_type":     map[string]interface{}{"type": "string", "enum": []interface{}{"长期", "一次性", "周期"}, "description": "筛选-按消费类型"},
					"pay_type":         map[string]interface{}{"type": "string", "enum": []interface{}{"现金", "公积金"}, "description": "筛选-按支付方式"},
					"set_owner":        map[string]interface{}{"type": "string", "description": "修改-归属人，设为指定值"},
					"set_category1":    category1Field(),
					"set_category2":    category2Field(),
					"set_consume_type": consumeTypeField(),
					"set_pay_type":     payTypeField(),
					"set_remark":       map[string]interface{}{"type": "string", "description": "修改-备注，设为指定值"},
				},
				"required": []interface{}{},
			},
		},
		{
			Name:        "cancel_bill",
			Display:     "删除账单",
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
			Display:     "查询账单",
			Description: "查询账单列表，支持按时间范围、分类、归属人、金额范围、消费/支付类型和关键词筛选，最多返回 200 条",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_date":     map[string]interface{}{"type": "string", "description": "开始日期，格式 YYYY-MM-DD，例如 2026-01-01"},
					"end_date":       map[string]interface{}{"type": "string", "description": "结束日期，格式 YYYY-MM-DD，例如 2026-12-31"},
					"category":       map[string]interface{}{"type": "string", "description": "按一级分类筛选，可选"},
					"category2":      map[string]interface{}{"type": "string", "description": "按二级分类筛选，可选"},
					"keyword":        map[string]interface{}{"type": "string", "description": "按项目名称或备注模糊搜索，可选"},
					"owner":          map[string]interface{}{"type": "string", "description": "按归属人筛选，可选"},
					"owner_is_empty": map[string]interface{}{"type": "boolean", "description": "筛选归属人为空字符串的账单，可选。设为 true 时忽略 owner 参数"},
					"amount_min":     map[string]interface{}{"type": "number", "description": "金额下限（含），可选"},
					"amount_max":     map[string]interface{}{"type": "number", "description": "金额上限（含），可选"},
					"consume_type":   map[string]interface{}{"type": "string", "enum": []interface{}{"长期", "一次性", "周期"}, "description": "按消费类型筛选，可选"},
					"pay_type":       map[string]interface{}{"type": "string", "enum": []interface{}{"现金", "公积金"}, "description": "按支付方式筛选，可选"},
					"sort_by":        map[string]interface{}{"type": "string", "enum": []interface{}{"date", "amount"}, "description": "排序字段，可选 date 或 amount，默认 date"},
					"sort_order":     map[string]interface{}{"type": "string", "enum": []interface{}{"asc", "desc"}, "description": "排序方向，asc 升序或 desc 降序，默认 desc"},
				},
			},
		},
		{
			Name:        "get_bill_by_id",
			Display:     "账单详情",
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
			Display:     "月度统计",
			Description: "按月汇总账单金额和数量，用于月度分析和对比。可筛选一级/二级分类和归属人",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"year":      map[string]interface{}{"type": "integer", "description": "年份，如2026。不传默认当前年"},
					"category":  map[string]interface{}{"type": "string", "description": "按一级分类筛选，可选"},
					"category2": map[string]interface{}{"type": "string", "description": "按二级分类筛选，可选"},
					"owner":     map[string]interface{}{"type": "string", "description": "按归属人筛选，可选"},
				},
			},
		},
		{
			Name:        "get_category_stats",
			Display:     "分类统计",
			Description: "按一级分类汇总账单金额，用于消费结构分析。可筛选日期范围和归属人",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_date": map[string]interface{}{"type": "string", "description": "开始日期，格式 YYYY-MM-DD，例如 2026-01-01"},
					"end_date":   map[string]interface{}{"type": "string", "description": "结束日期，格式 YYYY-MM-DD，例如 2026-12-31"},
					"owner":      map[string]interface{}{"type": "string", "description": "按归属人筛选，可选"},
				},
			},
		},
		{
			Name:        "get_category_options",
			Display:     "分类选项",
			Description: "获取当前用户账单中使用过的一级/二级分类列表及映射关系",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "get_owner_options",
			Display:     "归属人选项",
			Description: "获取当前用户所有账单的归属人列表（去重），用于了解有哪些归属人可供筛选",
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
	case "batch_update_bills":
		return t.batchUpdateBills(ec, args)
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
	case "get_owner_options":
		return t.getOwnerOptions(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("bookkeeping: unknown function %s", funcName)}
	}
}

func (t *BookkeepingTool) createBill(ec *ExecuteContext, args json.RawMessage) *Result {
	var a billArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.Item == "" || a.Amount == 0 || a.Owner == "" {
		return &Result{Success: false, Error: "item, amount, and owner are required"}
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

func (t *BookkeepingTool) batchUpdateBills(ec *ExecuteContext, args json.RawMessage) *Result {
	var a batchUpdateBillsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}

	fields := service.BatchUpdateFields{
		Owner:       a.SetOwner,
		Category1:   a.SetCategory1,
		Category2:   a.SetCategory2,
		ConsumeType: a.SetConsumeType,
		PayType:     a.SetPayType,
		Remark:      a.SetRemark,
	}
	if fields == (service.BatchUpdateFields{}) {
		return &Result{Success: false, Error: "至少需要指定一个要修改的字段（set_owner/set_category1/set_category2/set_consume_type/set_pay_type/set_remark）"}
	}

	f := service.BillFilter{
		StartDate:   a.StartDate,
		EndDate:     a.EndDate,
		Category1:   a.Category,
		Category2:   a.Category2,
		Keyword:     a.Keyword,
		Owner:       a.Owner,
		OwnerEmpty:  a.OwnerIsEmpty,
		AmountMin:   a.AmountMin,
		AmountMax:   a.AmountMax,
		ConsumeType: a.ConsumeType,
		PayType:     a.PayType,
	}

	affected, err := t.billRepo.BatchUpdate(ec.Ctx, ec.UserID, f, fields)
	if err != nil {
		return &Result{Success: false, Error: "batch update failed: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{"updated": affected})
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
	StartDate    string  `json:"start_date,omitempty"`
	EndDate      string  `json:"end_date,omitempty"`
	Category     string  `json:"category,omitempty"`
	Category2    string  `json:"category2,omitempty"`
	Keyword      string  `json:"keyword,omitempty"`
	Owner        string  `json:"owner,omitempty"`
	OwnerIsEmpty bool    `json:"owner_is_empty,omitempty"`
	AmountMin    float64 `json:"amount_min,omitempty"`
	AmountMax    float64 `json:"amount_max,omitempty"`
	ConsumeType  string  `json:"consume_type,omitempty"`
	PayType      string  `json:"pay_type,omitempty"`
	SortBy       string  `json:"sort_by,omitempty"`
	SortOrder    string  `json:"sort_order,omitempty"`
}

type batchUpdateBillsArgs struct {
	StartDate      string  `json:"start_date,omitempty"`
	EndDate        string  `json:"end_date,omitempty"`
	Category       string  `json:"category,omitempty"`
	Category2      string  `json:"category2,omitempty"`
	Keyword        string  `json:"keyword,omitempty"`
	Owner          string  `json:"owner,omitempty"`
	OwnerIsEmpty   bool    `json:"owner_is_empty,omitempty"`
	AmountMin      float64 `json:"amount_min,omitempty"`
	AmountMax      float64 `json:"amount_max,omitempty"`
	ConsumeType    string  `json:"consume_type,omitempty"`
	PayType        string  `json:"pay_type,omitempty"`
	SetOwner       string  `json:"set_owner,omitempty"`
	SetCategory1   string  `json:"set_category1,omitempty"`
	SetCategory2   string  `json:"set_category2,omitempty"`
	SetConsumeType string  `json:"set_consume_type,omitempty"`
	SetPayType     string  `json:"set_pay_type,omitempty"`
	SetRemark      string  `json:"set_remark,omitempty"`
}

type getBillArgs struct {
	ID string `json:"id,omitempty"`
}

// billSummary — 返回给 LLM 的精简账单字段
type billSummary struct {
	ID          string  `json:"id"`
	Item        string  `json:"item"`
	Amount      float64 `json:"amount"`
	Category1   string  `json:"category1"`
	Category2   string  `json:"category2"`
	Date        string  `json:"date"`
	PayType     string  `json:"pay_type"`
	Owner       string  `json:"owner"`
	ConsumeType string  `json:"consume_type"`
	Remark      string  `json:"remark"`
}

func toBillSummaries(bills []model.Bill) []billSummary {
	result := make([]billSummary, len(bills))
	for i, b := range bills {
		result[i] = billSummary{
			ID:          b.ID,
			Item:        b.Item,
			Amount:      b.Amount,
			Category1:   b.Category1,
			Category2:   b.Category2,
			Date:        b.Date,
			PayType:     b.PayType,
			Owner:       b.Owner,
			ConsumeType: b.ConsumeType,
			Remark:      b.Remark,
		}
	}
	return result
}

func (t *BookkeepingTool) queryBills(ec *ExecuteContext, args json.RawMessage) *Result {
	var a queryBillsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		log.Printf("[bookkeeping] parse args error: %v", err)
	}

	bills, total, err := t.billRepo.ListFiltered(ec.Ctx, ec.UserID, service.BillFilter{
		StartDate:   a.StartDate,
		EndDate:     a.EndDate,
		Category1:   a.Category,
		Category2:   a.Category2,
		Keyword:     a.Keyword,
		Owner:       a.Owner,
		OwnerEmpty:  a.OwnerIsEmpty,
		AmountMin:   a.AmountMin,
		AmountMax:   a.AmountMax,
		ConsumeType: a.ConsumeType,
		PayType:     a.PayType,
		SortBy:      a.SortBy,
		SortOrder:   a.SortOrder,
	}, 200, 0)
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
	Owner     string `json:"owner,omitempty"`
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

	rows, err := t.billRepo.MonthlyStats(ec.Ctx, ec.UserID, startDate, endDate, a.Category, a.Category2, a.Owner)
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
	opts, err := t.billRepo.CategoryOptions(ec.Ctx, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "query category options failed: " + err.Error()}
	}
	data, _ := json.Marshal(opts)
	return &Result{Success: true, Data: data}
}

func (t *BookkeepingTool) getOwnerOptions(ec *ExecuteContext, args json.RawMessage) *Result {
	owners, err := t.billRepo.DistinctOwners(ec.Ctx, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "query owner options failed: " + err.Error()}
	}
	data, _ := json.Marshal(map[string]interface{}{"owners": owners})
	return &Result{Success: true, Data: data}
}

type dateRangeArgs struct {
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
	Owner     string `json:"owner,omitempty"`
}

func (t *BookkeepingTool) getCategoryStats(ec *ExecuteContext, args json.RawMessage) *Result {
	var a dateRangeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		log.Printf("[bookkeeping] parse args error: %v", err)
	}

	rows, err := t.billRepo.CategoryStats(ec.Ctx, ec.UserID, a.StartDate, a.EndDate, a.Owner)
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
