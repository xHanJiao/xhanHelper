package service

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func newCostTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.LlmCostDetail{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedCostRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	rows := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", SubCategory: "re_act_loop", Model: "flash",
			PromptTokens: 100, PromptCacheHitTokens: 60, PromptCacheMissTokens: 40, CompletionTokens: 20, TotalTokens: 120},
		{Date: "2026-08-10", UserID: "u1", Category: "post_chat", SubCategory: "summary", Model: "flash",
			PromptTokens: 200, PromptCacheHitTokens: 0, PromptCacheMissTokens: 200, CompletionTokens: 30, TotalTokens: 230},
		{Date: "2026-08-11", UserID: "u2", Category: "main_chat", SubCategory: "re_act_loop", Model: "pro",
			PromptTokens: 300, PromptCacheHitTokens: 150, PromptCacheMissTokens: 150, CompletionTokens: 40, TotalTokens: 340},
		{Date: "2026-08-11", UserID: "u2", Category: "reflection", SubCategory: "mission_run", Model: "flash",
			PromptTokens: 400, PromptCacheHitTokens: 400, PromptCacheMissTokens: 0, CompletionTokens: 50, TotalTokens: 450},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuery_GroupByDate_CacheSplit(t *testing.T) {
	db := newCostTestDB(t)
	seedCostRows(t, db)
	repo := NewLlmCostRepo(db)

	res, err := repo.Query(context.Background(), QueryParams{
		Start:   "2026-08-10",
		End:     "2026-08-11",
		GroupBy: "date",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 总额
	if res.Total.TotalTokens != 1140 || res.Total.CallCount != 4 {
		t.Errorf("total mismatch: %+v", res.Total)
	}
	if res.Total.PromptCacheHit != 610 || res.Total.PromptCacheMiss != 390 || res.Total.Completion != 140 {
		t.Errorf("cache split mismatch: %+v", res.Total)
	}
	// hit_rate = 610 / (610+390) = 0.61
	if res.Total.CacheHitRate < 0.609 || res.Total.CacheHitRate > 0.611 {
		t.Errorf("cache hit rate mismatch: %f", res.Total.CacheHitRate)
	}

	// 分组（按 total_tokens DESC：08-11=790 > 08-10=350）
	if len(res.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(res.Groups))
	}
	if res.Groups[0].Key != "2026-08-11" || res.Groups[0].TotalTokens != 790 {
		t.Errorf("group[0] mismatch: %+v", res.Groups[0])
	}
	if res.Groups[1].Key != "2026-08-10" || res.Groups[1].TotalTokens != 350 {
		t.Errorf("group[1] mismatch: %+v", res.Groups[1])
	}
	// 08-11 hit = 150+400 = 550, miss = 150, rate = 0.786
	if res.Groups[0].CacheHitRate < 0.785 || res.Groups[0].CacheHitRate > 0.787 {
		t.Errorf("group hit rate mismatch: %f", res.Groups[0].CacheHitRate)
	}
}

func TestQuery_MultiUserFilter(t *testing.T) {
	db := newCostTestDB(t)
	seedCostRows(t, db)
	repo := NewLlmCostRepo(db)

	res, err := repo.Query(context.Background(), QueryParams{
		Start:   "2026-08-10",
		End:     "2026-08-11",
		UserIDs: []string{"u1", "u2"},
		GroupBy: "date",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total.TotalTokens != 1140 {
		t.Errorf("all users should sum to 1140, got %d", res.Total.TotalTokens)
	}

	res2, err := repo.Query(context.Background(), QueryParams{
		Start:   "2026-08-10",
		End:     "2026-08-11",
		UserIDs: []string{"u1"},
		GroupBy: "date",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Total.TotalTokens != 350 || res2.Total.CallCount != 2 {
		t.Errorf("u1 only should be 350/2, got %+v", res2.Total)
	}

	res3, err := repo.Query(context.Background(), QueryParams{
		Start:   "2026-08-10",
		End:     "2026-08-11",
		UserIDs: []string{"nobody"},
		GroupBy: "date",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res3.Total.TotalTokens != 0 || len(res3.Groups) != 0 {
		t.Errorf("nobody should be empty, got %+v", res3.Total)
	}
}

func TestQuery_GroupByCategory_Filter(t *testing.T) {
	db := newCostTestDB(t)
	seedCostRows(t, db)
	repo := NewLlmCostRepo(db)

	res, err := repo.Query(context.Background(), QueryParams{
		Start:    "2026-08-10",
		End:      "2026-08-11",
		Category: "main_chat",
		GroupBy:  "category",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 1 || res.Groups[0].Key != "main_chat" {
		t.Fatalf("expected only main_chat group: %+v", res.Groups)
	}
	if res.Groups[0].TotalTokens != 460 {
		t.Errorf("main_chat should be 460 (120+340), got %d", res.Groups[0].TotalTokens)
	}
}

func TestQuery_InvalidGroupBy(t *testing.T) {
	db := newCostTestDB(t)
	repo := NewLlmCostRepo(db)
	if _, err := repo.Query(context.Background(), QueryParams{GroupBy: "evil; DROP TABLE"}); err == nil {
		t.Fatal("expected error for invalid group_by")
	}
}

func TestSummary_And_ListUsers(t *testing.T) {
	db := newCostTestDB(t)
	seedCostRows(t, db)
	repo := NewLlmCostRepo(db)

	sum, err := repo.Summary(context.Background(), "2026-08-10", "2026-08-11", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalTokens != 1140 || sum.CallCount != 4 {
		t.Errorf("summary mismatch: %+v", sum)
	}

	users, err := repo.ListUsers(context.Background(), "2026-08-10", "2026-08-11")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	// u2=790 > u1=350
	if users[0].UserID != "u2" || users[0].TotalTokens != 790 {
		t.Errorf("top user mismatch: %+v", users[0])
	}
}

func TestListDetails_Pagination(t *testing.T) {
	db := newCostTestDB(t)
	seedCostRows(t, db)
	repo := NewLlmCostRepo(db)

	rows, total, err := repo.ListDetails(context.Background(), ListParams{Start: "2026-08-10", End: "2026-08-11"}, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("total should be 4, got %d", total)
	}
	if len(rows) != 3 {
		t.Errorf("page size 3 should return 3 rows, got %d", len(rows))
	}

	// 按用户过滤
	rows2, total2, err := repo.ListDetails(context.Background(), ListParams{UserIDs: []string{"u1"}}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 2 || len(rows2) != 2 {
		t.Errorf("u1 should have 2 rows, got %d/%d", total2, len(rows2))
	}

	// 精确日期
	rows3, total3, err := repo.ListDetails(context.Background(), ListParams{Date: "2026-08-11"}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total3 != 2 || len(rows3) != 2 {
		t.Errorf("date filter should return 2 rows, got %d/%d", total3, len(rows3))
	}
}
