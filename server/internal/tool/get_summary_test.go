package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupGetSummaryTool(t *testing.T) (*GetSummaryTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ConversationSummary{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewConversationSummaryRepo(db)
	return NewGetSummaryTool(repo, nil, nil), func() {}
}

func seedSummaries(t *testing.T, repo *service.ConversationSummaryRepo, userID string, n int) {
	t.Helper()
	now := time.Now()
	for i := 0; i < n; i++ {
		s := model.ConversationSummary{
			ID:             "sum-" + string(rune('a'+i)),
			ConversationID: "conv-1",
			UserID:         userID,
			SummaryText:    "摘要内容 " + string(rune('a'+i)),
			Status:         "active",
			CreatedAt:      now.Add(-time.Duration(i) * time.Hour),
		}
		if err := repo.Create(context.Background(), &s); err != nil {
			t.Fatalf("seed summary: %v", err)
		}
	}
}

func TestGetSummary_QueryByTimeDefaultLimit5(t *testing.T) {
	tool, cleanup := setupGetSummaryTool(t)
	defer cleanup()
	seedSummaries(t, tool.summaryRepo, "u1", 8)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "query_by_time", mustMarshal(t, map[string]string{
		"start_time": "2026-01-01",
		"end_time":   "2026-12-31",
	}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	items := out["summaries"].([]interface{})
	if len(items) > 5 {
		t.Errorf("default limit should be 5, got %d", len(items))
	}
}
