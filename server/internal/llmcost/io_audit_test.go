package llmcost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func init() {
	idgen.Init(1)
}

// auditMockLLM 记录调用、返回预设响应。
type auditMockLLM struct {
	resp    llm.Message
	withErr bool
	reqs    []*llm.ChatRequest
}

func (m *auditMockLLM) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	m.reqs = append(m.reqs, req)
	if m.withErr {
		return llm.Message{}, errors.New("boom")
	}
	return m.resp, nil
}
func (m *auditMockLLM) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return nil, errors.New("not implemented")
}
func (m *auditMockLLM) ChatWithRawJSON(ctx context.Context, b json.RawMessage) (llm.Message, error) {
	return llm.Message{}, nil
}
func (m *auditMockLLM) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string { return base }
func (m *auditMockLLM) ModelName() string                                                { return "mock" }

func setupAuditRepo(t *testing.T) *service.LLMCallIORepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.LLMCallIO{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return service.NewLLMCallIORepo(db)
}

func runAuditedChatSync(t *testing.T, repo *service.LLMCallIORepo, scenario Scenario, mockErr bool) (context.Context, *service.LLMCallIORepo) {
	t.Helper()
	mock := &auditMockLLM{resp: llm.Message{Content: `{"职业领域":["5年Go后端"]}`, Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5}}, withErr: mockErr}
	w := WrapIOAudit(mock, repo)
	ctx := context.Background()
	if scenario.Category != "" {
		ctx = WithScenario(ctx, scenario)
	}
	_, _ = w.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "sys",
		Messages:     []llm.Message{{Role: "user", Content: "hi"}},
	})
	time.Sleep(50 * time.Millisecond) // 等异步落库
	return ctx, repo
}

func waitForRows(t *testing.T, repo *service.LLMCallIORepo) []model.LLMCallIO {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, _, err := repo.List(context.Background(), service.LLMIOListParams{Limit: 100})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) > 0 {
			return rows
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no audit rows written within timeout")
	return nil
}

func TestShouldAudit(t *testing.T) {
	cases := []struct {
		cat, sub string
		want     bool
	}{
		{CategoryPostChat, SubProfileExtract, true},
		{CategoryPostChat, SubMemoryExtract, true},
		{CategoryPostChat, SubSummary, true},
		{CategoryPostChat, SubProfileColdStart, true},
		{CategoryAdmin, SubAgentSummary, true},
		{CategoryTool, SubReminderGen, true},
		{CategoryReflection, SubThemeAnalyze, false}, // 反思已有追溯，跳过
		{CategoryMainChat, SubReActLoop, false},      // 主聊天已有 request_json
		{CategoryPostChat, SubReActLoop, false},      // 白名单外的子类
		{"", "", true},                               // 未标注 → uncategorized 兜底
	}
	for _, c := range cases {
		if got := shouldAudit(c.cat, c.sub); got != c.want {
			t.Errorf("shouldAudit(%q,%q) = %v, want %v", c.cat, c.sub, got, c.want)
		}
	}
}

func TestIOAuditWrapper_WritesFullContent(t *testing.T) {
	repo := setupAuditRepo(t)
	runAuditedChatSync(t, repo, Scenario{
		Category: CategoryPostChat, SubCategory: SubProfileExtract,
		UserID: "u1", ConvID: "c1",
	}, false)

	rows := waitForRows(t, repo)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Category != CategoryPostChat || r.SubCategory != SubProfileExtract {
		t.Errorf("category mismatch: %s/%s", r.Category, r.SubCategory)
	}
	if r.UserID != "u1" || r.ConvID != "c1" {
		t.Errorf("user/conv mismatch: %s/%s", r.UserID, r.ConvID)
	}
	// 大字段不在列表行，用 GetByID 取详情
	detail, err := repo.GetByID(context.Background(), r.ID)
	if err != nil || detail == nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.SystemPrompt != "sys" {
		t.Errorf("system prompt not captured: %q", detail.SystemPrompt)
	}
	if !json.Valid([]byte(detail.MessagesJSON)) {
		t.Errorf("messages json invalid: %s", detail.MessagesJSON)
	}
	if detail.Response != `{"职业领域":["5年Go后端"]}` {
		t.Errorf("response not captured: %q", detail.Response)
	}
	if detail.PromptTokens != 10 || detail.CompletionTokens != 5 {
		t.Errorf("tokens not captured: %d/%d", detail.PromptTokens, detail.CompletionTokens)
	}
	if detail.LatencyMs < 0 {
		t.Error("negative latency")
	}
	if detail.ErrorMsg != "" {
		t.Errorf("unexpected error msg: %q", detail.ErrorMsg)
	}
}

func TestIOAuditWrapper_SkipsWhitelistExcluded(t *testing.T) {
	repo := setupAuditRepo(t)
	runAuditedChatSync(t, repo, Scenario{Category: CategoryReflection, SubCategory: SubThemeAnalyze}, false)
	runAuditedChatSync(t, repo, Scenario{Category: CategoryMainChat, SubCategory: SubReActLoop}, false)
	time.Sleep(50 * time.Millisecond)
	rows, total, err := repo.List(context.Background(), service.LLMIOListParams{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("reflection/main_chat should not be audited, got %d rows", total)
	}
}

func TestIOAuditWrapper_RecordsError(t *testing.T) {
	repo := setupAuditRepo(t)
	runAuditedChatSync(t, repo, Scenario{Category: CategoryPostChat, SubCategory: SubSummary}, true)

	rows := waitForRows(t, repo)
	r := rows[0]
	if r.ErrorMsg != "boom" {
		t.Errorf("error msg not captured: %q", r.ErrorMsg)
	}
	if r.Response != "" {
		t.Errorf("failed call should have no response, got %q", r.Response)
	}
}

func TestIOAuditWrapper_UncategorizedFallback(t *testing.T) {
	repo := setupAuditRepo(t)
	runAuditedChatSync(t, repo, Scenario{}, false) // 无 scenario

	rows := waitForRows(t, repo)
	if rows[0].Category != "" || rows[0].SubCategory != "" {
		t.Errorf("uncategorized call should have empty category, got %s/%s", rows[0].Category, rows[0].SubCategory)
	}
}

func TestLLMCallIORepo_CleanupBefore(t *testing.T) {
	repo := setupAuditRepo(t)
	ctx := context.Background()

	old := &model.LLMCallIO{ID: "old1", Category: CategoryPostChat, SubCategory: SubSummary, CreatedAt: time.Now().Add(-8 * 24 * time.Hour)}
	newRow := &model.LLMCallIO{ID: "new1", Category: CategoryPostChat, SubCategory: SubSummary, CreatedAt: time.Now()}
	if err := repo.Create(ctx, old); err != nil {
		t.Fatalf("create old: %v", err)
	}
	if err := repo.Create(ctx, newRow); err != nil {
		t.Fatalf("create new: %v", err)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	n, err := repo.CleanupBefore(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted (old), got %d", n)
	}
	got, _ := repo.GetByID(ctx, "new1")
	if got == nil {
		t.Error("new row should survive cleanup")
	}
	oldGot, _ := repo.GetByID(ctx, "old1")
	if oldGot != nil {
		t.Error("old row should be deleted")
	}
}
