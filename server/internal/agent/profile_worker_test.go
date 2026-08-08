package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// ---- mock LLM client 用于 profile worker 测试 ----

// syncMockLLM 实现 llm.LLMClient，按调用顺序返回预设的 ChatSync 结果。
type syncMockLLM struct {
	responses []llm.Message
	callCount int
	reqs      []*llm.ChatRequest // 记录每次请求，便于断言
}

func (m *syncMockLLM) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	m.reqs = append(m.reqs, req)
	if m.callCount >= len(m.responses) {
		return llm.Message{}, errors.New("mock: no more responses")
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

func (m *syncMockLLM) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return nil, errors.New("mock: Chat not implemented")
}
func (m *syncMockLLM) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, errors.New("mock: ChatWithRawJSON not implemented")
}
func (m *syncMockLLM) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string { return base }
func (m *syncMockLLM) ModelName() string                                                { return "mock" }

// noopRegistry 空 registry：getLLMToolsFiltered 返回空，ExecuteByName 返回失败。
func noopRegistry() *tool.Registry {
	return tool.NewRegistry()
}

// fakeUnifiedSearchTool 最小化的 unified_search 桩，用于冷启动测试验证工具注入与执行。
type fakeUnifiedSearchTool struct{}

func (t *fakeUnifiedSearchTool) Name() string { return "unified_search" }
func (t *fakeUnifiedSearchTool) Description() string {
	return "统一搜索工具，覆盖记忆、笔记、摘要、主题、历史消息"
}
func (t *fakeUnifiedSearchTool) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{
		{Name: "semantic_search", Description: "语义搜索", Parameters: map[string]interface{}{}},
		{Name: "keyword_search", Description: "关键词搜索", Parameters: map[string]interface{}{}},
	}
}
func (t *fakeUnifiedSearchTool) ReadOnlyFuncs() map[string]bool { return nil }
func (t *fakeUnifiedSearchTool) Execute(_ *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	return &tool.Result{Success: true, Data: json.RawMessage(`{"result":"ok"}`)}
}

// registryWithUnifiedSearch 注册了 unified_search 桩工具的 registry。
func registryWithUnifiedSearch() *tool.Registry {
	r := tool.NewRegistry()
	r.Register(&fakeUnifiedSearchTool{})
	return r
}

// setupProfileRepo 创建内存 sqlite 的 UserProfileRepo。
func setupProfileRepo(t *testing.T) (*service.UserProfileRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfile{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewUserProfileRepo(db)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return repo, cleanup
}

func newTestProfileWorker(llmClient llm.LLMClient, repo *service.UserProfileRepo) *UserProfileWorker {
	return &UserProfileWorker{
		flashClient: llmClient,
		registry:    noopRegistry(),
		profileRepo: repo,
	}
}

// ---- profile_fields 测试 ----

func TestProfileFields_Whitelist(t *testing.T) {
	for _, f := range ProfileFields {
		if !InProfileWhitelist(f.Field) {
			t.Errorf("field %q should be in whitelist", f.Field)
		}
		if strings.TrimSpace(f.ExtractDesc) == "" {
			t.Errorf("field %q ExtractDesc is empty", f.Field)
		}
		if strings.TrimSpace(f.InjectDesc) == "" {
			t.Errorf("field %q InjectDesc is empty", f.Field)
		}
	}
	if InProfileWhitelist("不存在字段") {
		t.Error("non-whitelist field should be rejected")
	}
	if ProfileFieldDesc("不存在字段") != nil {
		t.Error("ProfileFieldDesc for non-whitelist field should be nil")
	}
}

func TestProfileFields_IncludesAllDocumentedFields(t *testing.T) {
	want := []string{"称呼", "出生年份", "籍贯", "性别", "职业领域", "重要关系", "饮食习惯", "财务概况", "长期目标", "补充说明"}
	for _, w := range want {
		if !InProfileWhitelist(w) {
			t.Errorf("documented field %q missing from whitelist", w)
		}
	}
}

func TestProfileExtractFieldText_ContainsAllFields(t *testing.T) {
	text := ProfileExtractFieldText()
	for _, f := range ProfileFields {
		if !strings.Contains(text, f.Field) {
			t.Errorf("field %q missing from extraction text", f.Field)
		}
	}
}

// ---- buildProfileExtractionXML 测试 ----

func TestBuildProfileExtractionXML_Basic(t *testing.T) {
	msgs := []model.Message{
		{ID: "msg-1", Content: "我老家是河南焦作的", CreatedAt: time.Date(2026, 8, 8, 7, 30, 0, 0, time.UTC)},
		{ID: "msg-2", Content: "我女朋友叫CJ", CreatedAt: time.Date(2026, 8, 8, 7, 32, 0, 0, time.UTC)},
	}
	existing := []model.UserProfile{
		{Field: "籍贯", FieldValue: "河南焦作", Source: "explicit", UpdatedAt: time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)},
	}

	xml := buildProfileExtractionXML(existing, msgs)

	// 结构断言
	if !strings.Contains(xml, "<profile_extraction>") {
		t.Error("missing <profile_extraction>")
	}
	if !strings.Contains(xml, "<existing_profile>") {
		t.Error("missing <existing_profile>")
	}
	if !strings.Contains(xml, `key="籍贯"`) {
		t.Error("missing existing field key")
	}
	if !strings.Contains(xml, `<msg time="08-08 15:30">我老家是河南焦作的</msg>`) {
		t.Error("missing msg element with time and content")
	}
	if !strings.Contains(xml, "<rules>") {
		t.Error("missing <rules>")
	}
	if !strings.Contains(xml, "<output_format>") {
		t.Error("missing <output_format>")
	}
	// fields 定义块
	if !strings.Contains(xml, `<field name="称呼">`) {
		t.Error("missing field definition block for 称呼")
	}
	if !strings.Contains(xml, "<description>") {
		t.Error("missing description in field defs")
	}
}

func TestBuildProfileExtractionXML_EmptyExisting(t *testing.T) {
	msgs := []model.Message{{ID: "msg-1", Content: "你好"}}
	xml := buildProfileExtractionXML(nil, msgs)
	if strings.Contains(xml, "<existing_profile>") {
		t.Error("existing_profile should be omitted when empty")
	}
}

// ---- 正常增量提取 ----

func TestProfileWorker_NormalExtraction_Upserts(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":"河南焦作","不存在字段":"x"}`},
	}}
	w := newTestProfileWorker(llmClient, repo)

	sess := &SessionSnapshot{
		ConvID: "conv-1",
		UserID: "user-1",
		Messages: []model.Message{
			{ID: "msg-1", Content: "我老家是河南焦作的"},
		},
	}

	if err := w.extractNormal(context.Background(), sess); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}

	// 已 upsert 籍贯（白名单内），丢弃不存在字段（白名单外）
	p, err := repo.GetByUserAndField(context.Background(), "user-1", "籍贯")
	if err != nil {
		t.Fatalf("GetByUserAndField: %v", err)
	}
	if p == nil || p.FieldValue != "河南焦作" {
		t.Fatalf("expected 籍贯=河南焦作, got %+v", p)
	}
	if p.Source != model.ProfileSourceExplicit {
		t.Errorf("expected source=explicit, got %s", p.Source)
	}
	non, _ := repo.GetByUserAndField(context.Background(), "user-1", "不存在字段")
	if non != nil {
		t.Error("non-whitelist field should not be written")
	}
}

func TestProfileWorker_NormalExtraction_AdditionalFieldSource(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"补充说明":"养了一只猫"}`},
	}}
	w := newTestProfileWorker(llmClient, repo)

	sess := &SessionSnapshot{
		ConvID: "conv-1",
		UserID: "user-1",
		Messages: []model.Message{
			{ID: "msg-1", Content: "我养了一只猫"},
		},
	}

	if err := w.extractNormal(context.Background(), sess); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	p, err := repo.GetByUserAndField(context.Background(), "user-1", "补充说明")
	if err != nil {
		t.Fatalf("GetByUserAndField: %v", err)
	}
	if p == nil {
		t.Fatal("expected 补充说明 to be written")
	}
	if p.Source != model.ProfileSourceLLMAdditional {
		t.Errorf("expected source=llm_additional, got %s", p.Source)
	}
}

func TestProfileWorker_NormalExtraction_EmptyResponse(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{}`},
	}}
	w := newTestProfileWorker(llmClient, repo)

	sess := &SessionSnapshot{ConvID: "conv-1", UserID: "user-1", Messages: []model.Message{{ID: "m1", Content: "你好"}}}
	if err := w.extractNormal(context.Background(), sess); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	hasAny, err := repo.HasAny(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("HasAny: %v", err)
	}
	if hasAny {
		t.Error("empty response should not write anything")
	}
}

func TestProfileWorker_NormalExtraction_UsesSharedSystemPrompt(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	llmClient := &syncMockLLM{responses: []llm.Message{{Content: `{}`}}}
	w := newTestProfileWorker(llmClient, repo)

	sess := &SessionSnapshot{ConvID: "conv-1", UserID: "user-1", Messages: []model.Message{{ID: "m1", Content: "你好"}}}
	if err := w.extractNormal(context.Background(), sess); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	if len(llmClient.reqs) == 0 {
		t.Fatal("no LLM call made")
	}
	if llmClient.reqs[0].SystemPrompt != postChatSharedSystemPrompt {
		t.Errorf("normal extraction should use shared system prompt, got: %q", llmClient.reqs[0].SystemPrompt)
	}
}

// ---- 冷启动 ----

func TestProfileWorker_ColdStart_ReActFlow(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	// 第一轮：返回 tool_call 请求（unified_search），第二轮：返回最终画像 JSON
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			Content: "",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"用户基本信息"}`}},
			},
		},
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	w := &UserProfileWorker{
		flashClient: llmClient,
		registry:    registryWithUnifiedSearch(),
		profileRepo: repo,
	}

	if err := w.runColdStartReAct(context.Background(), "user-1"); err != nil {
		t.Fatalf("runColdStartReAct: %v", err)
	}

	// 工具调用被追加为 role=tool 消息（第二次请求的 messages 应包含 assistant + tool）
	if len(llmClient.reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(llmClient.reqs))
	}
	if len(llmClient.reqs[0].Tools) == 0 {
		t.Error("cold start request should include tools")
	}

	// 画像写入
	p, err := repo.GetByUserAndField(context.Background(), "user-1", "籍贯")
	if err != nil {
		t.Fatalf("GetByUserAndField: %v", err)
	}
	if p == nil || p.FieldValue != "河南焦作" {
		t.Fatalf("expected 籍贯=河南焦作, got %+v", p)
	}
}

func TestProfileWorker_ColdStart_DirectJSON(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	// 第一轮直接返回 JSON，不调用工具
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":"河南焦作","称呼":"小韩"}`},
	}}
	w := newTestProfileWorker(llmClient, repo)

	if err := w.runColdStartReAct(context.Background(), "user-1"); err != nil {
		t.Fatalf("runColdStartReAct: %v", err)
	}
	p, _ := repo.GetByUserAndField(context.Background(), "user-1", "籍贯")
	if p == nil || p.FieldValue != "河南焦作" {
		t.Fatalf("expected 籍贯=河南焦作, got %+v", p)
	}
	p2, _ := repo.GetByUserAndField(context.Background(), "user-1", "称呼")
	if p2 == nil || p2.FieldValue != "小韩" {
		t.Fatalf("expected 称呼=小韩, got %+v", p2)
	}
}

func TestProfileWorker_ColdStart_MaxIterations_NoWrite(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	// 5 轮全部返回 tool_call（始终不产出最终 JSON）
	llmClient := &syncMockLLM{responses: make([]llm.Message, coldStartMaxIterations)}
	for i := range llmClient.responses {
		llmClient.responses[i] = llm.Message{ToolCalls: []llm.ToolCall{
			{ID: "call", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"x"}`}},
		}}
	}
	w := newTestProfileWorker(llmClient, repo)

	if err := w.runColdStartReAct(context.Background(), "user-1"); err != nil {
		t.Fatalf("runColdStartReAct: %v", err)
	}
	hasAny, _ := repo.HasAny(context.Background(), "user-1")
	if hasAny {
		t.Error("max iterations reached without final JSON should not write profile")
	}
}

// ---- Process 分流 ----

func TestProfileWorker_Process_ColdStartVsNormal(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()

	// 无画像 → 冷启动
	llmCold := &syncMockLLM{responses: []llm.Message{{Content: `{"籍贯":"河南焦作"}`}}}
	w := newTestProfileWorker(llmCold, repo)
	sess := &SessionSnapshot{ConvID: "conv-1", UserID: "user-1", Messages: []model.Message{{ID: "m1", Content: "你好"}}}
	if err := w.Process(context.Background(), sess); err != nil {
		t.Fatalf("Process (cold): %v", err)
	}
	if !strings.Contains(llmCold.reqs[0].SystemPrompt, "用户画像提取助手") {
		t.Error("cold start should use dedicated system prompt")
	}

	// 已有画像 → 正常增量
	llmNormal := &syncMockLLM{responses: []llm.Message{{Content: `{}`}}}
	w2 := newTestProfileWorker(llmNormal, repo)
	if err := w2.Process(context.Background(), sess); err != nil {
		t.Fatalf("Process (normal): %v", err)
	}
	if llmNormal.reqs[0].SystemPrompt != postChatSharedSystemPrompt {
		t.Error("normal extraction should use shared system prompt")
	}
}

// ---- repo 测试 ----

func TestUserProfileRepo_UpsertOverwrite(t *testing.T) {
	repo, cleanup := setupProfileRepo(t)
	defer cleanup()
	ctx := context.Background()

	if err := repo.Upsert(ctx, &model.UserProfile{UserID: "u1", Field: "籍贯", FieldValue: "河南焦作", Source: "explicit"}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// 冲突覆盖
	if err := repo.Upsert(ctx, &model.UserProfile{UserID: "u1", Field: "籍贯", FieldValue: "郑州", Source: "explicit"}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	p, err := repo.GetByUserAndField(ctx, "u1", "籍贯")
	if err != nil {
		t.Fatalf("GetByUserAndField: %v", err)
	}
	if p.FieldValue != "郑州" {
		t.Errorf("expected overwritten value 郑州, got %s", p.FieldValue)
	}
	list, err := repo.GetByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUser: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 row, got %d", len(list))
	}
	hasAny, err := repo.HasAny(ctx, "u1")
	if err != nil || !hasAny {
		t.Errorf("HasAny should be true, got %v err=%v", hasAny, err)
	}
	hasAny2, _ := repo.HasAny(ctx, "nobody")
	if hasAny2 {
		t.Error("HasAny for unknown user should be false")
	}
}
