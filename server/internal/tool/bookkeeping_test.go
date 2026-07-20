package tool

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// mustMarshal is shared with document_test.go

func setupBookkeepingTool(t *testing.T) (*BookkeepingTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Bill{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	billRepo := service.NewBillRepo(db)
	return NewBookkeepingTool(billRepo), func() {}
}

func seedBookkeepingBills(t *testing.T, tool *BookkeepingTool, userID string) {
	t.Helper()
	bills := []model.Bill{
		{ID: "b1", UserID: userID, Item: "啤酒", Amount: 50, Category1: "餐饮", Category2: "啤酒", ConsumeType: "一次性", Date: "2026-05-01", PayType: "现金", Owner: "张三", Status: model.BillStatusNormal},
		{ID: "b2", UserID: userID, Item: "咖啡", Amount: 30, Category1: "餐饮", Category2: "咖啡奶茶", ConsumeType: "一次性", Date: "2026-05-05", PayType: "现金", Owner: "张三", Status: model.BillStatusNormal},
		{ID: "b3", UserID: userID, Item: "地铁", Amount: 10, Category1: "交通", Category2: "地铁", ConsumeType: "周期", Date: "2026-06-10", PayType: "现金", Status: model.BillStatusNormal},
		{ID: "b4", UserID: userID, Item: "精酿啤酒", Amount: 80, Category1: "餐饮", Category2: "啤酒", ConsumeType: "一次性", Date: "2026-06-15", PayType: "现金", Owner: "张三", Remark: "朋友聚会AA", Status: model.BillStatusNormal},
	}
	for _, b := range bills {
		if err := tool.billRepo.Create(context.Background(), &b); err != nil {
			t.Fatalf("seed bill %s: %v", b.ID, err)
		}
	}
}

// --- Date Validation Tests ---

func TestCreateBillDateValidation(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	// Valid date
	res := tool.Execute(ec, "create_bill", mustMarshal(t, map[string]interface{}{
		"item": "测试", "amount": 100, "category1": "餐饮", "category2": "啤酒",
		"consume_type": "一次性", "date": "2026-05-28", "owner": "张三",
	}))
	if !res.Success {
		t.Errorf("valid date should succeed: %s", res.Error)
	}

	// Invalid date
	res = tool.Execute(ec, "create_bill", mustMarshal(t, map[string]interface{}{
		"item": "测试2", "amount": 200, "category1": "餐饮", "category2": "啤酒",
		"consume_type": "一次性", "date": "2026年5月28日", "owner": "张三",
	}))
	if res.Success {
		t.Error("invalid date format should fail")
	}

	// Empty date defaults to today (should work)
	res = tool.Execute(ec, "create_bill", mustMarshal(t, map[string]interface{}{
		"item": "测试3", "amount": 300, "category1": "餐饮", "category2": "啤酒",
		"consume_type": "一次性", "owner": "张三",
	}))
	if !res.Success {
		t.Errorf("empty date should default to today: %s", res.Error)
	}
}

func TestUpdateBillDateValidation(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	// Valid date update
	res := tool.Execute(ec, "update_bill", mustMarshal(t, map[string]interface{}{
		"id": "b1", "date": "2026-06-01",
	}))
	if !res.Success {
		t.Errorf("valid date update should succeed: %s", res.Error)
	}

	// Invalid date update
	res = tool.Execute(ec, "update_bill", mustMarshal(t, map[string]interface{}{
		"id": "b2", "date": "5月28日",
	}))
	if res.Success {
		t.Error("invalid date format should fail on update")
	}
}

// --- query_bills Tests ---

func TestQueryBillsKeyword(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	// 按 item 关键字搜索
	res := tool.Execute(ec, "query_bills", mustMarshal(t, map[string]interface{}{
		"keyword": "啤酒",
	}))
	if !res.Success {
		t.Fatalf("query_bills failed: %s", res.Error)
	}

	var data map[string]interface{}
	json.Unmarshal(res.Data, &data)
	bills := data["bills"].([]interface{})
	if len(bills) != 2 {
		t.Errorf("expected 2 bills matching '啤酒' in item, got %d", len(bills))
	}

	// 按 remark 关键字搜索
	res = tool.Execute(ec, "query_bills", mustMarshal(t, map[string]interface{}{
		"keyword": "聚会",
	}))
	if !res.Success {
		t.Fatalf("query_bills by remark failed: %s", res.Error)
	}
	json.Unmarshal(res.Data, &data)
	bills = data["bills"].([]interface{})
	if len(bills) != 1 {
		t.Errorf("expected 1 bill matching '聚会' in remark, got %d", len(bills))
	}

	// Verify no consume_type in response
	billJSON, _ := json.Marshal(bills[0])
	var b map[string]interface{}
	json.Unmarshal(billJSON, &b)
	if _, ok := b["consume_type"]; !ok {
		t.Error("consume_type should be in query_bills response")
	}
	if _, ok := b["owner"]; !ok {
		t.Error("owner should be in query_bills response")
	}
	if _, ok := b["id"]; !ok {
		t.Error("id should be in query_bills response")
	}
}

func TestQueryBillsDateRangeAndKeyword(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	// 精确匹配用户的 replay 场景：日期范围 + keyword
	res := tool.Execute(ec, "query_bills", mustMarshal(t, map[string]interface{}{
		"start_date": "2026-04-27",
		"end_date":   "2026-05-27",
		"keyword":    "啤酒",
	}))
	if !res.Success {
		t.Fatalf("query_bills failed: %s", res.Error)
	}

	var data map[string]interface{}
	json.Unmarshal(res.Data, &data)
	bills := data["bills"].([]interface{})
	// b1(啤酒, 2026-05-01) 在范围内，b4(精酿啤酒, 2026-06-15) 不在
	if len(bills) != 1 {
		t.Errorf("expected 1 bill in date range matching '啤酒', got %d", len(bills))
	}

	total := data["total"].(float64)
	if total != 1 {
		t.Errorf("expected total=1, got %v", total)
	}
}

func TestQueryBillsCategory2(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "query_bills", mustMarshal(t, map[string]interface{}{
		"category2": "咖啡奶茶",
	}))
	if !res.Success {
		t.Fatalf("query_bills failed: %s", res.Error)
	}

	var data map[string]interface{}
	json.Unmarshal(res.Data, &data)
	bills := data["bills"].([]interface{})
	if len(bills) != 1 {
		t.Errorf("expected 1 bill with category2=咖啡奶茶, got %d", len(bills))
	}
}

// --- get_bill Tests ---

func TestGetBill(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_bill_by_id", mustMarshal(t, map[string]string{
		"id": "b1",
	}))
	if !res.Success {
		t.Fatalf("get_bill failed: %s", res.Error)
	}

	var bill model.Bill
	json.Unmarshal(res.Data, &bill)
	if bill.Item != "啤酒" {
		t.Errorf("expected '啤酒', got %s", bill.Item)
	}
	if bill.Amount != 50 {
		t.Errorf("expected 50, got %f", bill.Amount)
	}
}

func TestGetBillNotFound(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_bill_by_id", mustMarshal(t, map[string]string{
		"id": "nonexistent",
	}))
	if res.Success {
		t.Error("expected failure for nonexistent bill")
	}
}

func TestGetBillMissingID(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_bill_by_id", mustMarshal(t, map[string]string{}))
	if res.Success {
		t.Error("expected failure for missing id")
	}
}

// --- get_category_stats Tests ---

func TestGetCategoryStats(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_category_stats", mustMarshal(t, map[string]string{}))
	if !res.Success {
		t.Fatalf("get_category_stats failed: %s", res.Error)
	}

	var data map[string]map[string]interface{}
	json.Unmarshal(res.Data, &data)

	// 餐饮 should be in results
	if cat, ok := data["餐饮"]; !ok {
		t.Error("expected 餐饮 in category stats")
	} else {
		if cat["amount"].(float64) != 160 {
			t.Errorf("expected 餐饮 amount=160, got %v", cat["amount"])
		}
		if cat["count"].(float64) != 3 {
			t.Errorf("expected 餐饮 count=3, got %v", cat["count"])
		}
	}

	// _total should exist
	if total, ok := data["_total"]; !ok {
		t.Error("expected _total in category stats")
	} else {
		if total["amount"].(float64) != 170 {
			t.Errorf("expected total amount=170, got %v", total["amount"])
		}
		if total["count"].(float64) != 4 {
			t.Errorf("expected total count=4, got %v", total["count"])
		}
	}
}

func TestGetCategoryStatsDateRange(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_category_stats", mustMarshal(t, map[string]string{
		"start_date": "2026-06-01",
		"end_date":   "2026-06-30",
	}))
	if !res.Success {
		t.Fatalf("get_category_stats failed: %s", res.Error)
	}

	var data map[string]map[string]interface{}
	json.Unmarshal(res.Data, &data)

	// June only: 交通(10) + 餐饮(80) = 2 categories
	if len(data) != 3 { // 交通 + 餐饮 + _total
		t.Errorf("expected 3 entries (2 categories + _total), got %d", len(data))
	}
	if data["_total"]["amount"].(float64) != 90 {
		t.Errorf("expected June total=90, got %v", data["_total"]["amount"])
	}
}

// --- get_monthly_stats Tests ---

func TestGetMonthlyStats(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_monthly_stats", mustMarshal(t, map[string]interface{}{
		"year": 2026,
	}))
	if !res.Success {
		t.Fatalf("get_monthly_stats failed: %s", res.Error)
	}

	var data map[string]map[string]interface{}
	json.Unmarshal(res.Data, &data)

	if len(data) != 2 {
		t.Errorf("expected 2 months, got %d", len(data))
	}

	may := data["2026-05"]
	if may["amount"].(float64) != 80 {
		t.Errorf("expected May amount=80, got %v", may["amount"])
	}
	if may["count"].(float64) != 2 {
		t.Errorf("expected May count=2, got %v", may["count"])
	}
}

func TestGetMonthlyStatsCategory2(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_monthly_stats", mustMarshal(t, map[string]interface{}{
		"year": 2026, "category2": "啤酒",
	}))
	if !res.Success {
		t.Fatalf("get_monthly_stats failed: %s", res.Error)
	}

	var data map[string]map[string]interface{}
	json.Unmarshal(res.Data, &data)

	if len(data) != 2 {
		t.Errorf("expected 2 months for 啤酒, got %d", len(data))
	}
}

func TestGetCategoryOptions(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_category_options", mustMarshal(t, map[string]interface{}{}))
	if !res.Success {
		t.Fatalf("get_category_options failed: %s", res.Error)
	}

	var data service.CategoryOptionsResult
	if err := json.Unmarshal(res.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := data.Mapping["餐饮"]; !ok {
		t.Error("expected 餐饮 in category options")
	}
	if _, ok := data.Mapping["交通"]; !ok {
		t.Error("expected 交通 in category options")
	}
	if len(data.Category1List) == 0 {
		t.Error("expected non-empty category1_list")
	}
	if len(data.Category2List) == 0 {
		t.Error("expected non-empty category2_list")
	}
}

func TestGetCategoryOptionsEmpty(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "get_category_options", mustMarshal(t, map[string]interface{}{}))
	if !res.Success {
		t.Fatalf("get_category_options failed: %s", res.Error)
	}

	var data service.CategoryOptionsResult
	json.Unmarshal(res.Data, &data)
	if len(data.Mapping) != 0 {
		t.Errorf("expected empty options for user with no bills, got %d categories", len(data.Mapping))
	}
}

func TestBatchUpdateBillsOwner(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "batch_update_bills", mustMarshal(t, map[string]interface{}{
		"category2": "啤酒",
		"set_owner": "李四",
	}))
	if !res.Success {
		t.Fatalf("batch_update_bills failed: %s", res.Error)
	}

	var data map[string]interface{}
	json.Unmarshal(res.Data, &data)
	affected := data["updated"].(float64)
	if affected != 2 {
		t.Errorf("expected 2 updated, got %v", affected)
	}

	// verify b1 and b4 changed, b2 unchanged
	checkRes := tool.Execute(ec, "get_bill_by_id", mustMarshal(t, map[string]string{"id": "b1"}))
	var b1 model.Bill
	json.Unmarshal(checkRes.Data, &b1)
	if b1.Owner != "李四" {
		t.Errorf("b1 owner expected 李四, got %s", b1.Owner)
	}

	checkRes = tool.Execute(ec, "get_bill_by_id", mustMarshal(t, map[string]string{"id": "b4"}))
	var b4 model.Bill
	json.Unmarshal(checkRes.Data, &b4)
	if b4.Owner != "李四" {
		t.Errorf("b4 owner expected 李四, got %s", b4.Owner)
	}

	checkRes = tool.Execute(ec, "get_bill_by_id", mustMarshal(t, map[string]string{"id": "b2"}))
	var b2 model.Bill
	json.Unmarshal(checkRes.Data, &b2)
	if b2.Owner != "张三" {
		t.Errorf("b2 owner expected unchanged 张三, got %s", b2.Owner)
	}
}

func TestBatchUpdateBillsNoFields(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "batch_update_bills", mustMarshal(t, map[string]interface{}{
		"category": "餐饮",
	}))
	if res.Success {
		t.Error("expected error when no set_* field specified")
	}
}

func TestBatchUpdateBillsMultipleFields(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()
	seedBookkeepingBills(t, tool, "u1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "batch_update_bills", mustMarshal(t, map[string]interface{}{
		"owner":            "张三",
		"set_consume_type": "周期",
		"set_remark":       "批量改的",
	}))
	if !res.Success {
		t.Fatalf("batch_update_bills failed: %s", res.Error)
	}

	var data map[string]interface{}
	json.Unmarshal(res.Data, &data)
	affected := data["updated"].(float64)
	if affected != 3 {
		t.Errorf("expected 3 updated (张三's bills), got %v", affected)
	}

	for _, id := range []string{"b1", "b2", "b4"} {
		checkRes := tool.Execute(ec, "get_bill_by_id", mustMarshal(t, map[string]string{"id": id}))
		var b model.Bill
		json.Unmarshal(checkRes.Data, &b)
		if b.ConsumeType != "周期" {
			t.Errorf("%s consume_type expected 周期, got %s", id, b.ConsumeType)
		}
		if b.Remark != "批量改的" {
			t.Errorf("%s remark expected 批量改的, got %s", id, b.Remark)
		}
	}
}

func TestQueryBillsOwnerEmpty(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	tool.billRepo.Create(context.Background(), &model.Bill{
		ID: "b-empty", UserID: "u1", Item: "无归属人账单", Amount: 100,
		Category1: "餐饮", Category2: "外卖", ConsumeType: "一次性",
		Date: "2026-07-01", PayType: "现金", Owner: "", Status: model.BillStatusNormal,
	})
	tool.billRepo.Create(context.Background(), &model.Bill{
		ID: "b-owner", UserID: "u1", Item: "有归属人账单", Amount: 200,
		Category1: "交通", Category2: "地铁", ConsumeType: "周期",
		Date: "2026-07-01", PayType: "现金", Owner: "张三", Status: model.BillStatusNormal,
	})

	res := tool.Execute(ec, "query_bills", mustMarshal(t, map[string]interface{}{
		"owner_is_empty": true,
	}))
	if !res.Success {
		t.Fatalf("query_bills failed: %s", res.Error)
	}

	var data map[string]interface{}
	json.Unmarshal(res.Data, &data)
	total := data["total"].(float64)
	if total != 1 {
		t.Errorf("expected 1 empty-owner bill, got %v", total)
	}
}

func TestBatchUpdateOwnerEmptyTool(t *testing.T) {
	tool, cleanup := setupBookkeepingTool(t)
	defer cleanup()

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	tool.billRepo.Create(context.Background(), &model.Bill{
		ID: "b-empty2", UserID: "u1", Item: "无归属人账单", Amount: 100,
		Category1: "餐饮", Category2: "外卖", ConsumeType: "一次性",
		Date: "2026-07-01", PayType: "现金", Owner: "", Status: model.BillStatusNormal,
	})

	res := tool.Execute(ec, "batch_update_bills", mustMarshal(t, map[string]interface{}{
		"owner_is_empty": true,
		"set_owner":      "补全",
	}))
	if !res.Success {
		t.Fatalf("batch_update_bills failed: %s", res.Error)
	}

	var data map[string]interface{}
	json.Unmarshal(res.Data, &data)
	affected := data["updated"].(float64)
	if affected != 1 {
		t.Errorf("expected 1 updated, got %v", affected)
	}

	res = tool.Execute(ec, "query_bills", mustMarshal(t, map[string]interface{}{
		"owner_is_empty": true,
	}))
	json.Unmarshal(res.Data, &data)
	if data["total"].(float64) != 0 {
		t.Error("expected 0 empty-owner bills after batch update")
	}
}
