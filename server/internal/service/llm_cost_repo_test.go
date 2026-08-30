package service

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/config"
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

func TestTotalsByConversations(t *testing.T) {
	db := newCostTestDB(t)
	rows := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", SubCategory: "sub_agent_run", ConvID: "conv-a", Model: "flash",
			PromptTokens: 100, PromptCacheHitTokens: 60, PromptCacheMissTokens: 40, CompletionTokens: 20, TotalTokens: 120},
		{Date: "2026-08-11", UserID: "u1", Category: "main_chat", SubCategory: "sub_agent_run", ConvID: "conv-a", Model: "flash",
			PromptTokens: 200, PromptCacheHitTokens: 0, PromptCacheMissTokens: 200, CompletionTokens: 30, TotalTokens: 230},
		{Date: "2026-08-11", UserID: "u2", Category: "main_chat", SubCategory: "sub_agent_run", ConvID: "conv-b", Model: "flash",
			PromptTokens: 300, PromptCacheHitTokens: 150, PromptCacheMissTokens: 150, CompletionTokens: 40, TotalTokens: 340},
		{Date: "2026-08-11", UserID: "u2", Category: "reflection", SubCategory: "mission_run", ConvID: "conv-a", Model: "flash",
			PromptTokens: 1, PromptCacheHitTokens: 1, PromptCacheMissTokens: 0, CompletionTokens: 1, TotalTokens: 2},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewLlmCostRepo(db)
	totals, err := repo.TotalsByConversations(context.Background(), []string{"conv-a", "conv-b", "conv-none"})
	if err != nil {
		t.Fatalf("TotalsByConversations error: %v", err)
	}
	a, ok := totals["conv-a"]
	if !ok {
		t.Fatal("conv-a missing from totals")
	}
	if a.CallCount != 3 || a.TotalTokens != 352 {
		t.Fatalf("conv-a unexpected totals: %+v", a)
	}
	if _, ok := totals["conv-none"]; ok {
		t.Fatal("conv-none should be absent")
	}
	if len(totals) != 2 {
		t.Fatalf("expected 2 convs, got %d", len(totals))
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
	if res.Groups[1].Key != "2026-08-11" || res.Groups[1].TotalTokens != 790 {
		t.Errorf("group[0] mismatch: %+v", res.Groups[1])
	}
	if res.Groups[0].Key != "2026-08-10" || res.Groups[0].TotalTokens != 350 {
		t.Errorf("group[1] mismatch: %+v", res.Groups[0])
	}
	// 08-11 hit = 150+400 = 550, miss = 150, rate = 0.786
	if res.Groups[1].CacheHitRate < 0.785 || res.Groups[1].CacheHitRate > 0.787 {
		t.Errorf("group hit rate mismatch: %f", res.Groups[1].CacheHitRate)
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

	sum, err := repo.Summary(context.Background(), "2026-08-10", "2026-08-11", nil, nil, "")
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

func TestExpenseQuery_PriceSegments(t *testing.T) {
	db := newCostTestDB(t)
	if err := db.AutoMigrate(&model.LlmModelPrice{}); err != nil {
		t.Fatal(err)
	}
	// 百万级 token，让 元/1M 单价折算后数字干净。时间为北京时间时刻。
	rows := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 10, 0, 0, 0, config.BJ), // 峰段内
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 500_000, CompletionTokens: 250_000},
		{Date: "2026-08-11", UserID: "u2", Category: "reflection", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 11, 2, 0, 0, 0, config.BJ), // 峰段外
			PromptCacheHitTokens: 800_000, PromptCacheMissTokens: 0, CompletionTokens: 100_000},
		{Date: "2026-08-11", UserID: "u2", Category: "main_chat", Model: "pro",
			CreatedAt:            time.Date(2026, 8, 11, 10, 0, 0, 0, config.BJ), // 峰段内
			PromptCacheHitTokens: 300_000, PromptCacheMissTokens: 200_000, CompletionTokens: 50_000},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewLlmCostRepo(db)

	segs, _ := NormalizePeakSegments("09:00-11:00")
	prices := []model.LlmModelPrice{
		{Model: "flash", EffectiveDate: "2026-08-01", PeakSegments: segs,
			PriceHit: 1, PriceMiss: 2, PriceOutput: 3, PriceHitOff: 0.5, PriceMissOff: 1, PriceOutputOff: 1.5},
		{Model: "pro", EffectiveDate: "2026-08-05", PeakSegments: segs,
			PriceHit: 0.2, PriceMiss: 0.4, PriceOutput: 0.6, PriceHitOff: 0.1, PriceMissOff: 0.2, PriceOutputOff: 0.3},
	}

	res, err := repo.ExpenseQuery(context.Background(), QueryParams{Start: "2026-08-10", End: "2026-08-11"}, prices)
	if err != nil {
		t.Fatal(err)
	}

	// flash 08-10 10:00(峰): 1*1 + 0.5*2 + 0.25*3 = 1 + 1 + 0.75 = 2.75
	// flash 08-11 02:00(谷): 0.8*0.5 + 0 + 0.1*1.5 = 0.4 + 0.15 = 0.55
	// pro   08-11 10:00(峰): 0.3*0.2 + 0.2*0.4 + 0.05*0.6 = 0.06 + 0.08 + 0.03 = 0.17
	wantTotal := 2.75 + 0.55 + 0.17
	if absFloat(res.Total-wantTotal) > 1e-9 {
		t.Errorf("total want %.4f got %.4f", wantTotal, res.Total)
	}
	if absFloat(res.TotalPeak-2.92) > 1e-9 || absFloat(res.TotalOff-0.55) > 1e-9 {
		t.Errorf("peak/off totals mismatch: peak=%v off=%v", res.TotalPeak, res.TotalOff)
	}

	if len(res.Days) != 2 {
		t.Fatalf("expected 2 days, got %d", len(res.Days))
	}
	if absFloat(res.Days[0].Cost-2.75) > 1e-9 || absFloat(res.Days[0].CostPeak-2.75) > 1e-9 {
		t.Errorf("day[0] mismatch: %+v", res.Days[0])
	}
	if absFloat(res.Days[1].Cost-0.72) > 1e-9 || absFloat(res.Days[1].CostPeak-0.17) > 1e-9 || absFloat(res.Days[1].CostOff-0.55) > 1e-9 {
		t.Errorf("day[1] mismatch: %+v", res.Days[1])
	}
	// day[1] 拆分：hit=0.4+0.06=0.46, miss=0.08, output=0.15+0.03=0.18
	if absFloat(res.Days[1].CostHit-0.46) > 1e-9 || absFloat(res.Days[1].CostMiss-0.08) > 1e-9 || absFloat(res.Days[1].CostOutput-0.18) > 1e-9 {
		t.Errorf("day[1] split mismatch: %+v", res.Days[1])
	}

	// 分类聚合：main_chat = 2.75 + 0.17 = 2.92 > reflection = 0.55
	if len(res.Categories) != 2 {
		t.Fatalf("expected 2 categories, got %d: %+v", len(res.Categories), res.Categories)
	}
	if res.Categories[0].Key != "main_chat" || absFloat(res.Categories[0].Cost-2.92) > 1e-9 {
		t.Errorf("top category mismatch: %+v", res.Categories[0])
	}

	// 模型聚合：flash = 3.3 > pro = 0.17
	if len(res.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(res.Models))
	}
	if res.Models[0].Key != "flash" || absFloat(res.Models[0].Cost-3.3) > 1e-9 {
		t.Errorf("top model mismatch: %+v", res.Models[0])
	}

	if len(res.UnpricedModels) != 0 {
		t.Errorf("expected no unpriced models, got %v", res.UnpricedModels)
	}
}

func TestExpenseQuery_UnpricedModel(t *testing.T) {
	db := newCostTestDB(t)
	if err := db.AutoMigrate(&model.LlmModelPrice{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 10, 0, 0, 0, config.BJ),
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 0, CompletionTokens: 0},
		{Date: "2026-08-11", UserID: "u2", Category: "main_chat", Model: "pro",
			CreatedAt:            time.Date(2026, 8, 11, 10, 0, 0, 0, config.BJ),
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 0, CompletionTokens: 0},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewLlmCostRepo(db)

	// 只有 flash 有价格，pro 无价格 → pro 消耗按 0，列入 unpriced
	segs, _ := NormalizePeakSegments("09:00-11:00")
	prices := []model.LlmModelPrice{
		{Model: "flash", EffectiveDate: "2026-08-01", PeakSegments: segs,
			PriceHit: 1, PriceMiss: 2, PriceOutput: 3, PriceHitOff: 1, PriceMissOff: 2, PriceOutputOff: 3},
	}

	res, err := repo.ExpenseQuery(context.Background(), QueryParams{Start: "2026-08-10", End: "2026-08-11"}, prices)
	if err != nil {
		t.Fatal(err)
	}

	if absFloat(res.Total-1.0) > 1e-9 {
		t.Errorf("total want 1.0 got %v", res.Total)
	}
	if len(res.UnpricedModels) != 1 || res.UnpricedModels[0] != "pro" {
		t.Errorf("expected ['pro'] unpriced, got %v", res.UnpricedModels)
	}
	// pro 不计入模型聚合
	if len(res.Models) != 1 || res.Models[0].Key != "flash" {
		t.Errorf("models should only contain flash: %+v", res.Models)
	}
}

func TestExpenseQuery_CategoryFilter(t *testing.T) {
	db := newCostTestDB(t)
	seedCostRows(t, db)
	repo := NewLlmCostRepo(db)

	// 百万级单价让 token 折算直接可见（token/1e6 * price）；峰谷同价，时段无关
	prices := []model.LlmModelPrice{
		{Model: "flash", EffectiveDate: "2026-08-01",
			PriceHit: 1e6, PriceMiss: 1e6, PriceOutput: 1e6, PriceHitOff: 1e6, PriceMissOff: 1e6, PriceOutputOff: 1e6},
		{Model: "pro", EffectiveDate: "2026-08-05",
			PriceHit: 1e6, PriceMiss: 1e6, PriceOutput: 1e6, PriceHitOff: 1e6, PriceMissOff: 1e6, PriceOutputOff: 1e6},
	}

	// seed: 08-10 main_chat flash(120t) + post_chat flash(230t)；08-11 main_chat pro(340t) + reflection flash(450t)
	res, err := repo.ExpenseQuery(context.Background(), QueryParams{Start: "2026-08-10", End: "2026-08-11", Category: "main_chat"}, prices)
	if err != nil {
		t.Fatal(err)
	}
	// main_chat token = 120 + 340 = 460，单价 1e6 → 折算 460
	if absFloat(res.Total-460) > 1e-6 {
		t.Errorf("category-filtered total want 460 got %v", res.Total)
	}
	if len(res.Categories) != 1 || res.Categories[0].Key != "main_chat" {
		t.Errorf("filtered categories mismatch: %+v", res.Categories)
	}
}

func TestExpenseQuery_TimezoneBeijing(t *testing.T) {
	db := newCostTestDB(t)
	if err := db.AutoMigrate(&model.LlmModelPrice{}); err != nil {
		t.Fatal(err)
	}
	// 用 UTC instant 构造，验证按北京时间判峰谷：
	// 01:00 UTC = 09:00 北京（峰段 09:00-11:00 内）
	// 00:00 UTC = 08:00 北京（峰段外）
	rows := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC),
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 0, CompletionTokens: 0},
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 0, CompletionTokens: 0},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewLlmCostRepo(db)

	segs, _ := NormalizePeakSegments("09:00-11:00")
	prices := []model.LlmModelPrice{
		{Model: "flash", EffectiveDate: "2026-08-01", PeakSegments: segs,
			PriceHit: 2, PriceMiss: 2, PriceOutput: 2, PriceHitOff: 1, PriceMissOff: 1, PriceOutputOff: 1},
	}

	res, err := repo.ExpenseQuery(context.Background(), QueryParams{Start: "2026-08-10", End: "2026-08-10"}, prices)
	if err != nil {
		t.Fatal(err)
	}
	// 峰=2（09:00 北京），谷=1（08:00 北京），各 1M hit → total = 3
	if absFloat(res.Total-3.0) > 1e-9 {
		t.Errorf("total want 3.0 got %v", res.Total)
	}
	if absFloat(res.TotalPeak-2.0) > 1e-9 || absFloat(res.TotalOff-1.0) > 1e-9 {
		t.Errorf("peak/off mismatch: peak=%v off=%v", res.TotalPeak, res.TotalOff)
	}
}

func TestPeakSegments_ParseAndMatch(t *testing.T) {
	// 解析与规范化
	norm, err := NormalizePeakSegments("08:30-12:00, 14:00-17:00")
	if err != nil {
		t.Fatal(err)
	}
	want := `[["08:30","12:00"],["14:00","17:00"]]`
	if norm != want {
		t.Errorf("normalize want %s got %s", want, norm)
	}
	if got, _ := NormalizePeakSegments(""); got != "" {
		t.Errorf("empty should normalize to empty, got %q", got)
	}
	if got, _ := NormalizePeakSegments("   "); got != "" {
		t.Errorf("blank should normalize to empty, got %q", got)
	}
	// 非法输入
	bad := []string{"8:30-12:00", "08:30", "08:30-", "12:00-08:00", "08:30-12:00,xx", "aa-bb"}
	for _, s := range bad {
		if _, err := NormalizePeakSegments(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}

	// 判峰谷（段开始含、结束不含）
	segs, _ := parsePeakSegmentsJSON(norm)
	cases := []struct {
		hm   string
		want bool
	}{
		{"08:29", false}, {"08:30", true}, {"11:59", true}, {"12:00", false},
		{"13:59", false}, {"14:00", true}, {"17:00", false}, {"00:00", false}, {"23:59", false},
	}
	for _, c := range cases {
		if got := isPeak(c.hm, segs); got != c.want {
			t.Errorf("isPeak(%s) want %v got %v", c.hm, c.want, got)
		}
	}
	// 无峰段 = 全天谷
	if isPeak("10:00", nil) {
		t.Error("nil segments should never be peak")
	}
	// 存储空串
	if segs2, err := parsePeakSegmentsJSON(""); err != nil || len(segs2) != 0 {
		t.Errorf("empty json should parse to nil segs, got %v err %v", segs2, err)
	}
}

func TestHourlyQuery_BucketsAndPeakOff(t *testing.T) {
	db := newCostTestDB(t)
	if err := db.AutoMigrate(&model.LlmModelPrice{}); err != nil {
		t.Fatal(err)
	}
	// 峰段 09:00-11:00；三行：10:00 峰、02:00 谷、08:59 谷（边界前）
	rows := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 10, 30, 0, 0, config.BJ),
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 500_000, CompletionTokens: 250_000},
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 2, 0, 0, 0, config.BJ),
			PromptCacheHitTokens: 400_000, PromptCacheMissTokens: 0, CompletionTokens: 100_000},
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 8, 59, 0, 0, config.BJ),
			PromptCacheHitTokens: 200_000, PromptCacheMissTokens: 0, CompletionTokens: 0},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewLlmCostRepo(db)

	segs, _ := NormalizePeakSegments("09:00-11:00")
	prices := []model.LlmModelPrice{
		{Model: "flash", EffectiveDate: "2026-08-01", PeakSegments: segs,
			PriceHit: 2, PriceMiss: 2, PriceOutput: 2, PriceHitOff: 1, PriceMissOff: 1, PriceOutputOff: 1},
	}

	res, err := repo.HourlyQuery(context.Background(), "2026-08-10", QueryParams{}, prices)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hours) != 24 {
		t.Fatalf("expected 24 hours, got %d", len(res.Hours))
	}
	// 小时桶命中
	if res.Hours[10].Hit != 1_000_000 || res.Hours[10].Miss != 500_000 || res.Hours[10].Output != 250_000 {
		t.Errorf("hour[10] tokens mismatch: %+v", res.Hours[10])
	}
	if res.Hours[2].Hit != 400_000 || res.Hours[2].Output != 100_000 {
		t.Errorf("hour[2] tokens mismatch: %+v", res.Hours[2])
	}
	if res.Hours[8].Hit != 200_000 {
		t.Errorf("hour[8] tokens mismatch: %+v", res.Hours[8])
	}
	// 小时花销（峰价 2/2/2，谷价 1/1/1）
	// 10:30 峰: 1*2 + 0.5*2 + 0.25*2 = 3.5
	if absFloat(res.Hours[10].Cost-3.5) > 1e-9 {
		t.Errorf("hour[10] cost want 3.5 got %v", res.Hours[10].Cost)
	}
	// 02:00 谷: 0.4*1 + 0.1*1 = 0.5
	if absFloat(res.Hours[2].Cost-0.5) > 1e-9 {
		t.Errorf("hour[2] cost want 0.5 got %v", res.Hours[2].Cost)
	}
	// 08:59 谷: 0.2*1 = 0.2
	if absFloat(res.Hours[8].Cost-0.2) > 1e-9 {
		t.Errorf("hour[8] cost want 0.2 got %v", res.Hours[8].Cost)
	}
	// 总量：cost = 4.2，峰 3.5，谷 0.7
	if absFloat(res.Totals.Cost-4.2) > 1e-9 || absFloat(res.Totals.CostPeak-3.5) > 1e-9 || absFloat(res.Totals.CostOff-0.7) > 1e-9 {
		t.Errorf("totals mismatch: %+v", res.Totals)
	}
	if len(res.UnpricedModels) != 0 {
		t.Errorf("expected no unpriced, got %v", res.UnpricedModels)
	}
}

func TestHourlyQuery_FilterAndUnpriced(t *testing.T) {
	db := newCostTestDB(t)
	if err := db.AutoMigrate(&model.LlmModelPrice{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash",
			CreatedAt:            time.Date(2026, 8, 10, 10, 0, 0, 0, config.BJ),
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 0, CompletionTokens: 0},
		{Date: "2026-08-10", UserID: "u2", Category: "post_chat", Model: "pro",
			CreatedAt:            time.Date(2026, 8, 10, 10, 0, 0, 0, config.BJ),
			PromptCacheHitTokens: 1_000_000, PromptCacheMissTokens: 0, CompletionTokens: 0},
	}
	for _, r := range rows {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewLlmCostRepo(db)

	segs, _ := NormalizePeakSegments("09:00-11:00")
	prices := []model.LlmModelPrice{
		{Model: "flash", EffectiveDate: "2026-08-01", PeakSegments: segs,
			PriceHit: 2, PriceMiss: 2, PriceOutput: 2, PriceHitOff: 2, PriceMissOff: 2, PriceOutputOff: 2},
	}

	// 分类筛选：只看 main_chat
	res, err := repo.HourlyQuery(context.Background(), "2026-08-10", QueryParams{Category: "main_chat"}, prices)
	if err != nil {
		t.Fatal(err)
	}
	if res.Totals.Hit != 1_000_000 || absFloat(res.Totals.Cost-2.0) > 1e-9 {
		t.Errorf("category filter mismatch: %+v", res.Totals)
	}

	// 用户筛选：只看 u2（pro 无价格 → unpriced）
	res2, err := repo.HourlyQuery(context.Background(), "2026-08-10", QueryParams{UserIDs: []string{"u2"}}, prices)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Totals.Hit != 1_000_000 || res2.Totals.Cost != 0 {
		t.Errorf("u2 filter mismatch: %+v", res2.Totals)
	}
	if len(res2.UnpricedModels) != 1 || res2.UnpricedModels[0] != "pro" {
		t.Errorf("expected ['pro'] unpriced, got %v", res2.UnpricedModels)
	}
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
