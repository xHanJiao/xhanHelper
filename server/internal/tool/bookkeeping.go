package tool

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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

func (t *BookkeepingTool) Name() string        { return "bookkeeping" }
func (t *BookkeepingTool) Description() string { return "记账工具，可以新增、修改、删除、查询账单" }

func (t *BookkeepingTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_bill",
			Description: "新增一笔账单",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"item":         map[string]interface{}{"type": "string", "description": "项目名称"},
					"amount":       map[string]interface{}{"type": "number", "description": "金额"},
					"category1":    map[string]interface{}{"type": "string", "description": "一级分类"},
					"category2":    map[string]interface{}{"type": "string", "description": "二级分类"},
					"consume_type": map[string]interface{}{"type": "string", "description": "消费类型: 长期/一次性/周期"},
					"date":         map[string]interface{}{"type": "string", "description": "日期 YYYY-MM-DD，默认今天"},
					"pay_type":     map[string]interface{}{"type": "string", "description": "支付方式: 现金/公积金，默认现金"},
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
					"category1":    map[string]interface{}{"type": "string", "description": "一级分类"},
					"category2":    map[string]interface{}{"type": "string", "description": "二级分类"},
					"consume_type": map[string]interface{}{"type": "string", "description": "消费类型"},
					"date":         map[string]interface{}{"type": "string", "description": "日期"},
					"pay_type":     map[string]interface{}{"type": "string", "description": "支付方式"},
					"owner":        map[string]interface{}{"type": "string", "description": "归属人"},
					"remark":       map[string]interface{}{"type": "string", "description": "备注"},
				},
				"required": []interface{}{"id"},
			},
		},
		{
			Name:        "delete_bill",
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
			Description: "查询当前所有正常状态账单",
			Parameters: map[string]interface{}{
				"type": "object",
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
	case "delete_bill":
		return t.deleteBill(ec, args)
	case "query_bills":
		return t.queryBills(ec, args)
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
		a.Date = time.Now().Format("2006-01-02")
	}
	if a.PayType == "" {
		a.PayType = "现金"
	}

	bill := &model.Bill{
		ID:          uuid.New().String(),
		DeviceID:    ec.DeviceID,
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

	if err := t.billRepo.SoftDelete(ec.Ctx, a.ID); err != nil {
		return &Result{Success: false, Error: "delete bill failed: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]string{"deleted_id": a.ID})
	return &Result{Success: true, Data: data}
}

func (t *BookkeepingTool) queryBills(ec *ExecuteContext, args json.RawMessage) *Result {
	bills, err := t.billRepo.ListByDevice(ec.Ctx, ec.DeviceID)
	if err != nil {
		return &Result{Success: false, Error: "query bills failed: " + err.Error()}
	}
	data, _ := json.Marshal(bills)
	return &Result{Success: true, Data: data}
}
