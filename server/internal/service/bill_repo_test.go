package service

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupBillRepo(t *testing.T) (*BillRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.Bill{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return NewBillRepo(db), func() {}
}

func seedTestBills(t *testing.T, repo *BillRepo, userID string) {
	t.Helper()
	bills := []model.Bill{
		{ID: "b1", UserID: userID, Item: "啤酒", Amount: 50, Category1: "餐饮", Category2: "啤酒", Date: "2026-05-01", Status: model.BillStatusNormal},
		{ID: "b2", UserID: userID, Item: "咖啡", Amount: 30, Category1: "餐饮", Category2: "咖啡奶茶", Date: "2026-05-05", Status: model.BillStatusNormal},
		{ID: "b3", UserID: userID, Item: "地铁", Amount: 10, Category1: "交通", Category2: "地铁", Date: "2026-06-10", Status: model.BillStatusNormal},
		{ID: "b4", UserID: userID, Item: "精酿啤酒", Amount: 80, Category1: "餐饮", Category2: "啤酒", Date: "2026-06-15", Status: model.BillStatusNormal},
		{ID: "b5", UserID: userID, Item: "外卖", Amount: 40, Category1: "餐饮", Category2: "外卖", Date: "2026-07-01", Remark: "和朋友一起点的", Status: model.BillStatusNormal},
		{ID: "b6", UserID: userID, Item: "已取消", Amount: 100, Category1: "娱乐", Category2: "电影", Date: "2026-05-10", Status: model.BillStatusCancel},
	}
	for _, b := range bills {
		if err := repo.Create(context.Background(), &b); err != nil {
			t.Fatalf("seed bill %s: %v", b.ID, err)
		}
	}
}

func TestBillStats(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	count, sum, err := repo.Stats(context.Background(), "u1", BillFilter{})
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	// b1~b5 normal = 50+30+10+80+40=210，b6 作废不计
	if count != 5 || sum != 210 {
		t.Errorf("expected count=5 sum=210, got count=%d sum=%v", count, sum)
	}

	count, sum, err = repo.Stats(context.Background(), "u1", BillFilter{Category1List: []string{"餐饮"}})
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if count != 4 || sum != 200 {
		t.Errorf("expected count=4 sum=200 for 餐饮, got count=%d sum=%v", count, sum)
	}

	count, sum, err = repo.Stats(context.Background(), "u1", BillFilter{Keyword: "不存在"})
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if count != 0 || sum != 0 {
		t.Errorf("expected count=0 sum=0 on empty result, got count=%d sum=%v", count, sum)
	}
}

func TestListFilteredCategory1List(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	bills, total, err := repo.ListFiltered(context.Background(), "u1", BillFilter{Category1List: []string{"餐饮", "交通"}}, 50, 0)
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}
	if total != 5 {
		t.Errorf("expected 5 bills in 餐饮+交通, got %d", total)
	}
	for _, b := range bills {
		if b.Category1 != "餐饮" && b.Category1 != "交通" {
			t.Errorf("unexpected category1: %s", b.Category1)
		}
	}
}

func TestListFilteredCategory2(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	bills, total, err := repo.ListFiltered(context.Background(), "u1", BillFilter{Category2: "啤酒"}, 50, 0)
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 bills with category2=啤酒, got %d", total)
	}
	for _, b := range bills {
		if b.Category2 != "啤酒" {
			t.Errorf("expected category2=啤酒, got %s", b.Category2)
		}
	}
}

func TestListFilteredKeyword(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	bills, total, err := repo.ListFiltered(context.Background(), "u1", BillFilter{Keyword: "啤酒"}, 50, 0)
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 bills matching '啤酒', got %d", total)
	}
	for _, b := range bills {
		if b.Item != "啤酒" && b.Item != "精酿啤酒" {
			t.Errorf("unexpected item: %s", b.Item)
		}
	}
}

func TestListFilteredKeywordRemark(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	bills, total, err := repo.ListFiltered(context.Background(), "u1", BillFilter{Keyword: "朋友"}, 50, 0)
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 bill with remark matching '朋友', got %d", total)
	}
	if len(bills) > 0 && bills[0].Item != "外卖" {
		t.Errorf("expected 外卖 bill, got %s", bills[0].Item)
	}
}

func TestListFilteredDateRange(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	_, total, err := repo.ListFiltered(context.Background(), "u1", BillFilter{StartDate: "2026-05-01", EndDate: "2026-05-31"}, 50, 0)
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}
	// May: 啤酒(b1), 咖啡(b2) = 2 normal bills (b6 is cancelled)
	if total != 2 {
		t.Errorf("expected 2 bills in May, got %d", total)
	}
}

func TestListFilteredExcludesCancelled(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	_, total, _ := repo.ListFiltered(context.Background(), "u1", BillFilter{}, 50, 0)
	// 5 normal bills (b6 is cancelled)
	if total != 5 {
		t.Errorf("expected 5 normal bills, got %d", total)
	}
}

func TestGetBill(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	bill, err := repo.GetBill(context.Background(), "u1", "b1")
	if err != nil {
		t.Fatalf("GetBill failed: %v", err)
	}
	if bill.Item != "啤酒" {
		t.Errorf("expected '啤酒', got %s", bill.Item)
	}
	if bill.Amount != 50 {
		t.Errorf("expected 50, got %f", bill.Amount)
	}
}

func TestGetBillNotFound(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()

	_, err := repo.GetBill(context.Background(), "u1", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent bill")
	}
}

func TestGetBillWrongUser(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	_, err := repo.GetBill(context.Background(), "u2", "b1")
	if err == nil {
		t.Error("expected error for wrong user")
	}
}

func TestCategoryStats(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	rows, err := repo.CategoryStats(context.Background(), "u1", "", "", "")
	if err != nil {
		t.Fatalf("CategoryStats failed: %v", err)
	}

	// 3 categories: 餐饮(4笔=200), 交通(1笔=10), 娱乐 cancelled excluded
	if len(rows) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(rows))
	}

	// 餐饮 should be first (ORDER BY amount DESC)
	if rows[0].Category1 != "餐饮" {
		t.Errorf("expected 餐饮 first, got %s", rows[0].Category1)
	}
	if rows[0].Amount != 200 {
		t.Errorf("expected 餐饮 total 200, got %f", rows[0].Amount)
	}
	if rows[0].Count != 4 {
		t.Errorf("expected 餐饮 count 4, got %d", rows[0].Count)
	}

	if rows[1].Category1 != "交通" {
		t.Errorf("expected 交通 second, got %s", rows[1].Category1)
	}
	if rows[1].Amount != 10 {
		t.Errorf("expected 交通 total 10, got %f", rows[1].Amount)
	}
}

func TestCategoryStatsDateRange(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	// Only May
	rows, err := repo.CategoryStats(context.Background(), "u1", "2026-05-01", "2026-05-31", "")
	if err != nil {
		t.Fatalf("CategoryStats failed: %v", err)
	}
	if len(rows) != 1 || rows[0].Category1 != "餐饮" {
		t.Errorf("expected only 餐饮 in May, got %d categories", len(rows))
	}
	if rows[0].Count != 2 {
		t.Errorf("expected 2 餐饮 bills in May, got %d", rows[0].Count)
	}
}

func TestMonthlyStats(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	rows, err := repo.MonthlyStats(context.Background(), "u1", "2026-01-01", "2026-12-31", "", "", "")
	if err != nil {
		t.Fatalf("MonthlyStats failed: %v", err)
	}

	if len(rows) != 3 {
		t.Fatalf("expected 3 months, got %d", len(rows))
	}

	// May: 2 bills (b1=50, b2=30)
	if rows[0].Month != "2026-05" {
		t.Errorf("expected 2026-05, got %s", rows[0].Month)
	}
	if rows[0].Amount != 80 {
		t.Errorf("expected May total 80, got %f", rows[0].Amount)
	}
	if rows[0].Count != 2 {
		t.Errorf("expected May count 2, got %d", rows[0].Count)
	}

	// June: 2 bills (b3=10, b4=80)
	if rows[1].Month != "2026-06" {
		t.Errorf("expected 2026-06, got %s", rows[1].Month)
	}
	if rows[1].Amount != 90 {
		t.Errorf("expected June total 90, got %f", rows[1].Amount)
	}

	// July: 1 bill (b5=40)
	if rows[2].Month != "2026-07" {
		t.Errorf("expected 2026-07, got %s", rows[2].Month)
	}
	if rows[2].Amount != 40 {
		t.Errorf("expected July total 40, got %f", rows[2].Amount)
	}
}

func TestMonthlyStatsCategoryFilter(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	rows, err := repo.MonthlyStats(context.Background(), "u1", "2026-01-01", "2026-12-31", "交通", "", "")
	if err != nil {
		t.Fatalf("MonthlyStats failed: %v", err)
	}

	if len(rows) != 1 || rows[0].Month != "2026-06" {
		t.Errorf("expected only 2026-06 for 交通, got %d months", len(rows))
	}
	if rows[0].Amount != 10 {
		t.Errorf("expected 交通 total 10, got %f", rows[0].Amount)
	}
}

func TestMonthlyStatsCategory2Filter(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	rows, err := repo.MonthlyStats(context.Background(), "u1", "2026-01-01", "2026-12-31", "", "啤酒", "")
	if err != nil {
		t.Fatalf("MonthlyStats failed: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 months for 啤酒, got %d", len(rows))
	}
	// May: b1=50, June: b4=80
	if rows[0].Amount != 50 {
		t.Errorf("expected May 啤酒=50, got %f", rows[0].Amount)
	}
	if rows[1].Amount != 80 {
		t.Errorf("expected June 啤酒=80, got %f", rows[1].Amount)
	}
}

func TestMonthlyStatsEmptyYear(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	rows, err := repo.MonthlyStats(context.Background(), "u1", "2025-01-01", "2025-12-31", "", "", "")
	if err != nil {
		t.Fatalf("MonthlyStats failed: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 months for 2025, got %d", len(rows))
	}
}

func TestCategoryStatsExcludesCancelled(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	rows, err := repo.CategoryStats(context.Background(), "u1", "", "", "")
	if err != nil {
		t.Fatalf("CategoryStats failed: %v", err)
	}
	for _, r := range rows {
		if r.Category1 == "娱乐" {
			t.Error("cancelled 娱乐 bill should be excluded from stats")
		}
	}
}

func TestMonthlyStatsExcludesCancelled(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	rows, err := repo.MonthlyStats(context.Background(), "u1", "2026-01-01", "2026-12-31", "", "", "")
	if err != nil {
		t.Fatalf("MonthlyStats failed: %v", err)
	}
	// May should only have 2 bills (b1=50, b2=30), cancelled b6 (娱乐,100) excluded
	for _, r := range rows {
		if r.Month == "2026-05" && r.Amount != 80 {
			t.Errorf("May amount should be 80 (excl. cancelled), got %f", r.Amount)
		}
	}
}

func TestGetBillExcludesCancelled(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	_, err := repo.GetBill(context.Background(), "u1", "b6")
	if err == nil {
		t.Error("cancelled bill b6 should not be retrievable")
	}
}

func TestBatchUpdateOwner(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	affected, err := repo.BatchUpdate(context.Background(), "u1",
		BillFilter{Category2: "啤酒"},
		BatchUpdateFields{Owner: "小王"},
	)
	if err != nil {
		t.Fatalf("BatchUpdate failed: %v", err)
	}
	if affected != 2 {
		t.Errorf("expected 2 affected, got %d", affected)
	}

	b, _ := repo.GetBill(context.Background(), "u1", "b1")
	if b.Owner != "小王" {
		t.Errorf("expected b1 owner=小王, got %s", b.Owner)
	}

	b, _ = repo.GetBill(context.Background(), "u1", "b4")
	if b.Owner != "小王" {
		t.Errorf("expected b4 owner=小王, got %s", b.Owner)
	}

	// b2 should be unaffected (not 啤酒)
	b, _ = repo.GetBill(context.Background(), "u1", "b2")
	if b.Owner != "" {
		t.Errorf("expected b2 owner unchanged, got %s", b.Owner)
	}
}

func TestBatchUpdateMultipleFields(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	affected, err := repo.BatchUpdate(context.Background(), "u1",
		BillFilter{Category1: "餐饮"},
		BatchUpdateFields{Owner: "小李", Category2: "堂食"},
	)
	if err != nil {
		t.Fatalf("BatchUpdate failed: %v", err)
	}
	if affected != 4 {
		t.Errorf("expected 4 affected, got %d", affected)
	}

	for _, id := range []string{"b1", "b2", "b4", "b5"} {
		b, _ := repo.GetBill(context.Background(), "u1", id)
		if b.Owner != "小李" {
			t.Errorf("expected %s owner=小李, got %s", id, b.Owner)
		}
		if b.Category2 != "堂食" {
			t.Errorf("expected %s category2=堂食, got %s", id, b.Category2)
		}
	}
}

func TestBatchUpdateNoFields(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	_, err := repo.BatchUpdate(context.Background(), "u1",
		BillFilter{Category1: "餐饮"},
		BatchUpdateFields{},
	)
	if err == nil {
		t.Error("expected error for no fields to update")
	}
}

func TestBatchUpdateUserIsolation(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	affected, err := repo.BatchUpdate(context.Background(), "u2",
		BillFilter{},
		BatchUpdateFields{Owner: "hacker"},
	)
	if err != nil {
		t.Fatalf("BatchUpdate failed: %v", err)
	}
	if affected != 0 {
		t.Errorf("expected 0 affected for u2, got %d", affected)
	}

	// u1's bills should be unchanged
	b, _ := repo.GetBill(context.Background(), "u1", "b1")
	if b.Owner != "" {
		t.Errorf("u1 bills should be unchanged, got owner=%s", b.Owner)
	}
}

func TestBatchUpdateStartDate(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()
	seedTestBills(t, repo, "u1")

	affected, err := repo.BatchUpdate(context.Background(), "u1",
		BillFilter{StartDate: "2026-06-01"},
		BatchUpdateFields{Remark: "6月之后"},
	)
	if err != nil {
		t.Fatalf("BatchUpdate failed: %v", err)
	}
	if affected != 3 {
		t.Errorf("expected 3 affected (June+ = b3,b4,b5), got %d", affected)
	}
}

func TestListFilteredOwnerEmpty(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()

	// add bills with empty owner
	emptyBills := []model.Bill{
		{ID: "b7", UserID: "u1", Item: "测试", Amount: 10, Category1: "餐饮", Category2: "外卖", Date: "2026-07-01", Owner: "", Status: model.BillStatusNormal},
		{ID: "b8", UserID: "u1", Item: "测试2", Amount: 20, Category1: "交通", Category2: "地铁", Date: "2026-07-02", Owner: "", Status: model.BillStatusNormal},
	}
	for _, b := range emptyBills {
		repo.Create(context.Background(), &b)
	}

	// query with OwnerEmpty
	bills, total, err := repo.ListFiltered(context.Background(), "u1", BillFilter{OwnerEmpty: true}, 50, 0)
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 empty-owner bills, got %d", total)
	}
	for _, b := range bills {
		if b.Owner != "" {
			t.Errorf("expected empty owner, got %s for %s", b.Owner, b.ID)
		}
	}

	// query with specific owner should only match non-empty
	bills, total, _ = repo.ListFiltered(context.Background(), "u1", BillFilter{Owner: "test"}, 50, 0)
	if total != 0 {
		t.Errorf("expected 0 bills with owner=test, got %d", total)
	}
}

func TestBatchUpdateOwnerEmpty(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()

	emptyBills := []model.Bill{
		{ID: "b7", UserID: "u1", Item: "测试", Amount: 10, Category1: "餐饮", Category2: "外卖", Date: "2026-07-01", Owner: "", Status: model.BillStatusNormal},
	}
	for _, b := range emptyBills {
		repo.Create(context.Background(), &b)
	}

	affected, err := repo.BatchUpdate(context.Background(), "u1",
		BillFilter{OwnerEmpty: true},
		BatchUpdateFields{Owner: "补全"},
	)
	if err != nil {
		t.Fatalf("BatchUpdate failed: %v", err)
	}
	if affected != 1 {
		t.Errorf("expected 1 affected, got %d", affected)
	}

	b, _ := repo.GetBill(context.Background(), "u1", "b7")
	if b.Owner != "补全" {
		t.Errorf("expected owner=补全 after batch update, got %s", b.Owner)
	}
}

func TestBillRemarkLongText(t *testing.T) {
	repo, cleanup := setupBillRepo(t)
	defer cleanup()

	remark := strings.Repeat("超市小票明细", 100)
	bill := model.Bill{
		ID: "b-long", UserID: "u1", Item: "超市采购", Amount: 321.5,
		Category1: "日用", Category2: "生活用品", Date: "2026-09-01",
		Remark: remark, Status: model.BillStatusNormal,
	}
	if err := repo.Create(context.Background(), &bill); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetBill(context.Background(), "u1", "b-long")
	if err != nil {
		t.Fatalf("GetBill failed: %v", err)
	}
	if got == nil || got.Remark != remark {
		t.Fatalf("long remark round-trip failed: got %d chars, want %d", len(got.Remark), len(remark))
	}
}
