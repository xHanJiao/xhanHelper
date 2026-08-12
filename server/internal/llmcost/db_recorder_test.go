package llmcost

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// :memory: 库每个连接独立，限单连接保证 worker goroutine 与测试共享同一库
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

func TestDBRecorder_InsertsDetail(t *testing.T) {
	db := newTestDB(t)
	rec := NewDBRecorder(service.NewLlmCostRepo(db))

	rec.Record(UsageRecord{
		Date:                  time.Date(2026, 8, 12, 10, 0, 0, 0, time.Local),
		UserID:                "user-1",
		Category:              "main_chat",
		SubCategory:           "re_act_loop",
		Model:                 "deepseek-v4-flash",
		ConvID:                "conv-1",
		PromptTokens:          100,
		PromptCacheHitTokens:  60,
		PromptCacheMissTokens: 40,
		CompletionTokens:      50,
		TotalTokens:           150,
		Meta:                  map[string]interface{}{"round": 2},
	})

	var detail model.LlmCostDetail
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.First(&detail).Error; err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if detail.ID == 0 {
		t.Fatal("row not inserted within timeout")
	}

	// SQLite driver 可能把 date 列读回 RFC3339（"2026-08-12T00:00:00Z"），MySQL 返回纯日期串
	if len(detail.Date) < 10 || detail.Date[:10] != "2026-08-12" {
		t.Errorf("date mismatch: %s", detail.Date)
	}
	if detail.UserID != "user-1" || detail.Category != "main_chat" || detail.SubCategory != "re_act_loop" {
		t.Errorf("dimensions mismatch: %+v", detail)
	}
	if detail.Model != "deepseek-v4-flash" || detail.ConvID != "conv-1" {
		t.Errorf("model/conv mismatch: %+v", detail)
	}
	if detail.PromptTokens != 100 || detail.PromptCacheHitTokens != 60 || detail.PromptCacheMissTokens != 40 {
		t.Errorf("prompt tokens mismatch: %+v", detail)
	}
	if detail.CompletionTokens != 50 || detail.TotalTokens != 150 {
		t.Errorf("tokens mismatch: %+v", detail)
	}
	if detail.MetaJSON != `{"round":2}` {
		t.Errorf("meta mismatch: %s", detail.MetaJSON)
	}
	if detail.RefType != "" || detail.RefID != "" {
		t.Errorf("ref should be empty: %+v", detail)
	}
}

func TestDBRecorder_InsertEmptyMeta(t *testing.T) {
	db := newTestDB(t)
	rec := NewDBRecorder(service.NewLlmCostRepo(db))

	rec.Record(UsageRecord{
		Date:        time.Now(),
		UserID:      "",
		TotalTokens: 1,
	})

	var detail model.LlmCostDetail
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.First(&detail).Error; err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if detail.ID == 0 {
		t.Fatal("row not inserted within timeout")
	}
	if detail.UserID != "" {
		t.Errorf("user should be empty: %s", detail.UserID)
	}
	if detail.MetaJSON != "" {
		t.Errorf("meta should be empty: %s", detail.MetaJSON)
	}
}
