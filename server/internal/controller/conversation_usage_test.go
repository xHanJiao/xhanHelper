package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// TestGetConversationUsage 端到端验证 usage handler：上下文 token、窗口上限、窗口/会话金额。
func TestGetConversationUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.Message{}, &model.LlmCostDetail{}, &model.LlmModelPrice{}); err != nil {
		t.Fatal(err)
	}

	convRepo := service.NewConversationRepo(db)
	msgRepo := service.NewMessageRepo(db)
	costRepo := service.NewLlmCostRepo(db)
	priceRepo := service.NewLlmPriceRepo(db)
	ctx := context.Background()

	if err := convRepo.Create(ctx, &model.Conversation{ID: "c1", UserID: "u1", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	// 子会话（subAgent）：parent_id 指向 c1
	parentID := "c1"
	if err := convRepo.Create(ctx, &model.Conversation{ID: "sub1", UserID: "u1", ParentID: &parentID}); err != nil {
		t.Fatal(err)
	}

	startAt := time.Date(2026, 8, 10, 11, 0, 0, 0, config.BJ)
	startExtra := `{"window_size":1}`
	userExtra := `{"window_size":3,"session_start_msg_id":"m-start"}`
	assistantExtra := `{"usage":{"prompt_tokens":12345,"completion_tokens":100,"total_tokens":12445}}`
	msgs := []*model.Message{
		{ID: "m-start", UserID: "u1", ConversationID: "c1", Role: "user", Content: "start", Extra: &startExtra, CreatedAt: startAt},
		{ID: "m-user", UserID: "u1", ConversationID: "c1", Role: "user", Content: "hi", Extra: &userExtra, CreatedAt: startAt.Add(time.Minute)},
		{ID: "m-assist", UserID: "u1", ConversationID: "c1", Role: "assistant", Content: "hello", Extra: &assistantExtra, CreatedAt: startAt.Add(2 * time.Minute)},
	}
	for _, m := range msgs {
		if err := msgRepo.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	// 窗口前 1M + 窗口内 2M（单价 1 元/1M）
	costs := []model.LlmCostDetail{
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash", ConvID: "c1",
			CreatedAt: startAt.Add(-time.Hour), PromptCacheHitTokens: 1_000_000},
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash", ConvID: "c1",
			CreatedAt: startAt.Add(time.Hour), PromptCacheHitTokens: 2_000_000},
		// 子会话成本 0.5 元
		{Date: "2026-08-10", UserID: "u1", Category: "main_chat", Model: "flash", ConvID: "sub1",
			CreatedAt: startAt.Add(3 * time.Minute), PromptCacheHitTokens: 500_000},
	}
	for i := range costs {
		if err := costRepo.Insert(ctx, &costs[i]); err != nil {
			t.Fatal(err)
		}
	}
	segs, _ := service.NormalizePeakSegments("")
	if err := priceRepo.Save(ctx, &model.LlmModelPrice{
		Model: "flash", EffectiveDate: "2026-08-01", PeakSegments: segs,
		PriceHit: 1, PriceMiss: 1, PriceOutput: 1, PriceHitOff: 1, PriceMissOff: 1, PriceOutputOff: 1,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.LLM.Model = "deepseek-v4-flash"
	cfg.LLM.StrongModel = "deepseek-v4-pro"
	cfg.LLM.ContextWindow = 1_000_000
	cfg.LLM.StrongContextWindow = 2_000_000

	r := gin.New()
	r.GET("/api/conversations/:id/usage", func(c *gin.Context) {
		c.Set("user_id", "u1")
		getConversationUsage(convRepo, msgRepo, costRepo, priceRepo, cfg)(c)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/conversations/c1/usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			ContextTokens    int     `json:"context_tokens"`
			ContextWindow    int     `json:"context_window"`
			WindowCost       float64 `json:"window_cost"`
			ConversationCost float64 `json:"conversation_cost"`
			SubagentCost     float64 `json:"subagent_cost"`
			SubagentCount    int     `json:"subagent_count"`
			TotalCost        float64 `json:"total_cost"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.ContextTokens != 12345 {
		t.Errorf("context_tokens want 12345 got %d", resp.Data.ContextTokens)
	}
	if resp.Data.ContextWindow != 1_000_000 {
		t.Errorf("context_window want 1000000 got %d", resp.Data.ContextWindow)
	}
	if resp.Data.ConversationCost < 2.99 || resp.Data.ConversationCost > 3.01 {
		t.Errorf("conversation_cost want ~3 got %v", resp.Data.ConversationCost)
	}
	if resp.Data.WindowCost < 1.99 || resp.Data.WindowCost > 2.01 {
		t.Errorf("window_cost want ~2 got %v", resp.Data.WindowCost)
	}
	if resp.Data.SubagentCount != 1 {
		t.Errorf("subagent_count want 1 got %d", resp.Data.SubagentCount)
	}
	if resp.Data.SubagentCost < 0.49 || resp.Data.SubagentCost > 0.51 {
		t.Errorf("subagent_cost want ~0.5 got %v", resp.Data.SubagentCost)
	}
	if resp.Data.TotalCost < 3.49 || resp.Data.TotalCost > 3.51 {
		t.Errorf("total_cost want ~3.5 got %v", resp.Data.TotalCost)
	}
}
