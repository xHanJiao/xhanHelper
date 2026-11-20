package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
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
	scenarios []llmcost.Scenario // 记录每次调用的场景标注，便于断言
}

func (m *syncMockLLM) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	m.reqs = append(m.reqs, req)
	if s, ok := llmcost.ScenarioFromContext(ctx); ok {
		m.scenarios = append(m.scenarios, s)
	} else {
		m.scenarios = append(m.scenarios, llmcost.Scenario{})
	}
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

// setupProfileRepos 创建内存 sqlite 的旧表 + facts 表 repo。
func setupProfileRepos(t *testing.T) (*service.UserProfileRepo, *service.UserProfileFactRepo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfile{}, &model.UserProfileFact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return service.NewUserProfileRepo(db), service.NewUserProfileFactRepo(db)
}

func newTestProfileWorker(llmClient llm.LLMClient, legacyRepo *service.UserProfileRepo, factRepo *service.UserProfileFactRepo) *UserProfileWorker {
	return &UserProfileWorker{
		flashClient: llmClient,
		registry:    noopRegistry(),
		profileRepo: legacyRepo,
		factRepo:    factRepo,
	}
}

func testSess(msgs ...string) *SessionSnapshot {
	out := make([]model.Message, 0, len(msgs))
	for i, c := range msgs {
		out = append(out, model.Message{ID: string(rune('a' + i)), Role: "user", Content: c})
	}
	return &SessionSnapshot{ConvID: "conv-1", UserID: "user-1", msgs: out}
}

// ---- buildExtractionXML 测试 ----

func TestBuildExtractionXML_FactMode(t *testing.T) {
	_, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	if err := factRepo.ReplaceFieldFacts(ctx, "user-1", "重要关系", []string{"CJ: 伴侣"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := newTestProfileWorker(nil, nil, factRepo)

	xmlStr, err := w.buildExtractionXML(ctx, testSess("我老家是河南焦作的"), false)
	if err != nil {
		t.Fatalf("buildExtractionXML: %v", err)
	}
	for _, want := range []string{
		"<profile_extraction>",
		`<field key="重要关系">`,
		`<fact created_at=`,
		"CJ: 伴侣",
		"<output_format>",
		"事实短句",          // caveman 规则
		"已有事实未过时必须原样保留", // caveman 规则
		"(≤", // 极简字段清单带配额
	} {
		if !strings.Contains(xmlStr, want) {
			t.Errorf("fact mode XML missing %q\n---\n%s", want, xmlStr)
		}
	}
	if strings.Contains(xmlStr, "用户希望你如何称呼") {
		t.Error("fact mode should use brief field list, not full ExtractDesc")
	}
}

func TestBuildExtractionXML_LegacyMode(t *testing.T) {
	legacyRepo, _ := setupProfileRepos(t)
	ctx := context.Background()
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "user-1", Field: "籍贯", FieldValue: "河南焦作"}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	w := newTestProfileWorker(nil, legacyRepo, nil)

	xmlStr, err := w.buildExtractionXML(ctx, testSess("你好"), true)
	if err != nil {
		t.Fatalf("buildExtractionXML: %v", err)
	}
	for _, want := range []string{
		"<existing_profile>",
		`key="籍贯"`,
		"河南焦作",
		"历史画像文本", // legacy task 提示
	} {
		if !strings.Contains(xmlStr, want) {
			t.Errorf("legacy mode XML missing %q\n---\n%s", want, xmlStr)
		}
	}
}

func TestBuildExtractionXML_NoExisting(t *testing.T) {
	_, factRepo := setupProfileRepos(t)
	w := newTestProfileWorker(nil, nil, factRepo)
	xmlStr, err := w.buildExtractionXML(context.Background(), testSess("你好"), false)
	if err != nil {
		t.Fatalf("buildExtractionXML: %v", err)
	}
	if strings.Contains(xmlStr, "<existing_profile>") {
		t.Error("existing_profile should be omitted when empty")
	}
}

// ---- 正常增量提取 ----

func TestProfileWorker_NormalExtraction_WritesFacts(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"职业阶段":["5年Go后端","gap期求职中"],"不存在字段":["x"]}`},
	}}
	w := newTestProfileWorker(llmClient, legacyRepo, factRepo)

	if err := w.extractNormal(context.Background(), testSess("我5年Go后端，现在gap期求职中，我在面试"), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	facts, err := factRepo.GetActiveByField(context.Background(), "user-1", "职业阶段")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts, got %+v", facts)
	}
	if facts[0].Value != "5年Go后端" || facts[1].Value != "gap期求职中" {
		t.Errorf("unexpected facts order: %+v", facts)
	}
	for _, f := range facts {
		if f.Status != model.FactStatusActive {
			t.Errorf("fact should be active, got %s", f.Status)
		}
	}
}

func TestProfileWorker_NormalExtraction_SingleStringValue(t *testing.T) {
	_, factRepo := setupProfileRepos(t)

	// 兼容字符串单值输出（旧格式）
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	w := newTestProfileWorker(llmClient, nil, factRepo)

	if err := w.extractNormal(context.Background(), testSess("我老家河南焦作的"), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	facts, err := factRepo.GetActiveByField(context.Background(), "user-1", "籍贯")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "河南焦作" {
		t.Fatalf("expected 1 fact 河南焦作, got %+v", facts)
	}
}

func TestProfileWorker_NormalExtraction_NoChangeSkip(t *testing.T) {
	_, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	if err := factRepo.ReplaceFieldFacts(ctx, "user-1", "籍贯", []string{"河南焦作"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":["河南焦作"]}`}, // 与现有相同（顺序无关）
	}}
	w := newTestProfileWorker(llmClient, nil, factRepo)

	if err := w.extractNormal(ctx, testSess("我老家河南焦作的"), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	facts, err := factRepo.GetActiveByField(ctx, "user-1", "籍贯")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %+v", facts)
	}
	if facts[0].BatchID != "b0" {
		t.Errorf("no-change extract should not rewrite facts (batch should stay b0), got %s", facts[0].BatchID)
	}
}

func TestProfileWorker_NormalExtraction_QuotaRetry(t *testing.T) {
	_, factRepo := setupProfileRepos(t)

	longFact := strings.Repeat("长", 100) // 职业阶段 MaxLen=50
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"职业阶段":["` + longFact + `"]}`},
		{Content: `{"职业阶段":["5年Go后端"]}`}, // 重试后合规
	}}
	w := newTestProfileWorker(llmClient, nil, factRepo)

	if err := w.extractNormal(context.Background(), testSess("我是后端，5年Go后端"), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	if llmClient.callCount != 2 {
		t.Fatalf("expected 2 LLM calls (1 retry), got %d", llmClient.callCount)
	}
	// 第二次请求的 messages 应含反馈
	msgs := llmClient.reqs[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "不满足约束") {
		t.Errorf("retry should include quota feedback, got role=%s content=%s", last.Role, last.Content)
	}
	if !strings.Contains(last.Content, "职业阶段") || !strings.Contains(last.Content, "上限 50 字") {
		t.Errorf("feedback should mention field and limit, got %s", last.Content)
	}
	facts, err := factRepo.GetActiveByField(context.Background(), "user-1", "职业阶段")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "5年Go后端" {
		t.Fatalf("expected retried fact written, got %+v", facts)
	}
}

func TestProfileWorker_NormalExtraction_QuotaRetryExhausted_TruncateFallback(t *testing.T) {
	_, factRepo := setupProfileRepos(t)

	tooLong := strings.Repeat("超", 200) // 职业阶段 MaxLen=50
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"职业阶段":["` + tooLong + `"]}`},
		{Content: `{"职业阶段":["` + tooLong + `"]}`},
		{Content: `{"职业阶段":["` + tooLong + `"]}`}, // 重试耗尽（初始+2次）
	}}
	w := newTestProfileWorker(llmClient, nil, factRepo)

	// 用户原话里确实说过这段（截断后仍是子串）→ 判为用户自陈，落 active
	if err := w.extractNormal(context.Background(), testSess("我是后端，"+strings.Repeat("超", 50)), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	facts, err := factRepo.GetActiveByField(context.Background(), "user-1", "职业阶段")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 truncated fact, got %+v", facts)
	}
	if utf8.RuneCountInString(facts[0].Value) != 50 {
		t.Errorf("fallback fact should be truncated to MaxLen=50, got %d chars", utf8.RuneCountInString(facts[0].Value))
	}
}

func TestProfileWorker_NormalExtraction_MaxFactsTruncate(t *testing.T) {
	_, factRepo := setupProfileRepos(t)

	// 协作偏好 MaxFacts=5，输出 7 条 → 重试耗尽后截取前 5
	facts := make([]string, 7)
	for i := range facts {
		facts[i] = "偏好" + string(rune('a'+i))
	}
	payload, _ := json.Marshal(map[string][]string{"协作偏好": facts})
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: string(payload)},
		{Content: string(payload)},
		{Content: string(payload)}, // 初始 + 2 次重试，仍超限 → 截断兜底
	}}
	w := newTestProfileWorker(llmClient, nil, factRepo)

	if err := w.extractNormal(context.Background(), testSess("我的偏好：偏好a偏好b偏好c偏好d偏好e偏好f偏好g"), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	got, err := factRepo.GetActiveByField(context.Background(), "user-1", "协作偏好")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("expected 5 facts after MaxFacts truncate, got %d", len(got))
	}
}

func TestProfileWorker_NormalExtraction_EmptyArrayClearsField(t *testing.T) {
	_, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	if err := factRepo.ReplaceFieldFacts(ctx, "user-1", "长期目标", []string{"转型全栈", "定居买房"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// LLM 判定该字段所有事实均已过时 → 输出空数组 → 字段清空
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"长期目标":[]}`},
	}}
	w := newTestProfileWorker(llmClient, nil, factRepo)

	if err := w.extractNormal(ctx, testSess("目标都完成了"), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	facts, err := factRepo.GetActiveByField(ctx, "user-1", "长期目标")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("empty array should clear field, got %d active facts: %+v", len(facts), facts)
	}
	hasAny, err := factRepo.HasActive(ctx, "user-1")
	if err != nil {
		t.Fatalf("HasActive: %v", err)
	}
	if hasAny {
		t.Error("no active facts should remain after clear")
	}
}

func TestProfileWorker_NormalExtraction_PlaceholderOnly_NotClearing(t *testing.T) {
	_, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	if err := factRepo.ReplaceFieldFacts(ctx, "user-1", "称呼", []string{"哥"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 全是占位符 → 过滤后变空 → 忽略，不清空（防 LLM 输出"未知"误清字段）
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"称呼":["未知"]}`},
	}}
	w := newTestProfileWorker(llmClient, nil, factRepo)

	if err := w.extractNormal(ctx, testSess("你好"), false); err != nil {
		t.Fatalf("extractNormal: %v", err)
	}
	facts, err := factRepo.GetActiveByField(ctx, "user-1", "称呼")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "哥" || facts[0].BatchID != "b0" {
		t.Errorf("placeholder-only output should be ignored, got %+v", facts)
	}
}

// ---- 延迟迁移 ----

func TestProfileWorker_LegacyMigration_SplitsToFacts(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	// 旧表混装值
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "user-1", Field: "职业阶段", FieldValue: "5年Go后端；gap期求职中"}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"职业阶段":["5年Go后端","gap期求职中"],"称呼":["哥"]}`},
	}}
	w := newTestProfileWorker(llmClient, legacyRepo, factRepo)

	if err := w.extractNormal(ctx, testSess("我5年Go后端，现在gap期求职中，我在面试"), true); err != nil {
		t.Fatalf("extractNormal legacy: %v", err)
	}
	// LLM 看到的 existing 应含旧表整段值
	if !strings.Contains(llmClient.reqs[0].Messages[0].Content, "5年Go后端；gap期求职中") {
		t.Error("legacy extract should show old table value in existing_profile")
	}
	facts, err := factRepo.GetActiveByField(ctx, "user-1", "职业阶段")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 split facts, got %+v", facts)
	}
	// 旧表不动（保留审计）
	legacyRows, err := legacyRepo.GetByUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("legacy rows: %v", err)
	}
	legacyKept := false
	for _, r := range legacyRows {
		if r.Field == "职业阶段" {
			legacyKept = true
		}
	}
	if !legacyKept {
		t.Fatal("legacy row should remain")
	}
}

// ---- 冷启动 ----

func TestProfileWorker_ColdStart_WritesFacts(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)

	// 第一轮：返回 tool_call 请求（unified_search），第二轮：返回最终画像 JSON
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			Content: "",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"用户基本信息"}`}},
			},
		},
		{Content: `{"籍贯":["河南焦作"],"称呼":["小韩"]}`},
	}}
	w := &UserProfileWorker{
		flashClient: llmClient,
		registry:    registryWithUnifiedSearch(),
		profileRepo: legacyRepo,
		factRepo:    factRepo,
	}

	if err := w.runColdStartReAct(context.Background(), "user-1"); err != nil {
		t.Fatalf("runColdStartReAct: %v", err)
	}
	if len(llmClient.reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(llmClient.reqs))
	}
	if len(llmClient.reqs[0].Tools) == 0 {
		t.Error("cold start request should include tools")
	}
	facts, err := factRepo.GetActiveByField(context.Background(), "user-1", "籍贯")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "河南焦作" {
		t.Fatalf("expected 籍贯 fact, got %+v", facts)
	}
}

func TestProfileWorker_ColdStart_DirectJSON(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":["河南焦作"]}`},
	}}
	w := newTestProfileWorker(llmClient, legacyRepo, factRepo)

	if err := w.runColdStartReAct(context.Background(), "user-1"); err != nil {
		t.Fatalf("runColdStartReAct: %v", err)
	}
	facts, _ := factRepo.GetActiveByField(context.Background(), "user-1", "籍贯")
	if len(facts) != 1 || facts[0].Value != "河南焦作" {
		t.Fatalf("expected 籍贯=河南焦作, got %+v", facts)
	}
}

func TestProfileWorker_ColdStart_DeepModeUsesStrongClient(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)

	strongClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":["河南焦作"]}`},
	}}
	flashClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":["错的值"]}`},
	}}
	w := &UserProfileWorker{
		flashClient:  flashClient,
		strongClient: strongClient,
		registry:     noopRegistry(),
		profileRepo:  legacyRepo,
		factRepo:     factRepo,
	}

	if err := w.runColdStartReAct(context.Background(), "user-1"); err != nil {
		t.Fatalf("runColdStartReAct: %v", err)
	}
	if strongClient.callCount != 1 {
		t.Errorf("expected strong client called, calls=%d", strongClient.callCount)
	}
	if flashClient.callCount != 0 {
		t.Errorf("expected flash NOT called, calls=%d", flashClient.callCount)
	}
	if len(strongClient.reqs) == 0 || strongClient.reqs[0].Thinking == nil || !*strongClient.reqs[0].Thinking {
		t.Errorf("expected thinking=true, got %+v", strongClient.reqs)
	}
}

func TestProfileWorker_ColdStart_MaxIterations_NoWrite(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)

	llmClient := &syncMockLLM{responses: make([]llm.Message, coldStartMaxIterations)}
	for i := range llmClient.responses {
		llmClient.responses[i] = llm.Message{ToolCalls: []llm.ToolCall{
			{ID: "call", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"x"}`}},
		}}
	}
	w := newTestProfileWorker(llmClient, legacyRepo, factRepo)

	if err := w.runColdStartReAct(context.Background(), "user-1"); err != nil {
		t.Fatalf("runColdStartReAct: %v", err)
	}
	hasAny, _ := factRepo.HasActive(context.Background(), "user-1")
	if hasAny {
		t.Error("max iterations without final JSON should not write")
	}
}

// ---- Process 分流 ----

func TestProfileWorker_Process_Routing(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)
	ctx := context.Background()

	// 1. 无 facts 无 legacy → 冷启动
	llmCold := &syncMockLLM{responses: []llm.Message{{Content: `{"籍贯":["河南焦作"]}`}}}
	w := newTestProfileWorker(llmCold, legacyRepo, factRepo)
	if err := w.Process(ctx, testSess("你好")); err != nil {
		t.Fatalf("Process (cold): %v", err)
	}
	if !strings.Contains(llmCold.reqs[0].SystemPrompt, "用户画像提取助手") {
		t.Error("cold start should use dedicated system prompt")
	}

	// 2. facts 空 + legacy 有值 → 延迟迁移（共享 prompt + legacy task）——用 user-2（user-1 已被冷启动写入 facts）
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "user-2", Field: "籍贯", FieldValue: "河南焦作"}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	llmLagacy := &syncMockLLM{responses: []llm.Message{{Content: `{}`}}}
	w2 := newTestProfileWorker(llmLagacy, legacyRepo, factRepo)
	if err := w2.Process(ctx, &SessionSnapshot{ConvID: "conv-1", UserID: "user-2", msgs: []model.Message{{ID: "m1", Role: "user", Content: "你好"}}}); err != nil {
		t.Fatalf("Process (legacy): %v", err)
	}
	if llmLagacy.reqs[0].SystemPrompt != postChatSharedSystemPrompt {
		t.Error("legacy mode should use shared system prompt")
	}
	if !strings.Contains(llmLagacy.reqs[0].Messages[0].Content, "历史画像文本") {
		t.Error("legacy mode task should mention splitting")
	}

	// 3. facts 有值 → 正常增量
	llmNormal := &syncMockLLM{responses: []llm.Message{{Content: `{}`}}}
	w3 := newTestProfileWorker(llmNormal, legacyRepo, factRepo)
	if err := w3.Process(ctx, testSess("你好")); err != nil {
		t.Fatalf("Process (normal): %v", err)
	}
	if llmNormal.reqs[0].SystemPrompt != postChatSharedSystemPrompt {
		t.Error("normal extraction should use shared system prompt")
	}
}

// ---- 输出格式文本断言 ----

func TestProfileOutputFormat_ArrayFormat(t *testing.T) {
	if !strings.Contains(profileOutputFormat, "[\"事实1\",\"事实2\"]") {
		t.Error("output format should instruct array format")
	}
	if !strings.Contains(profileOutputFormat, "无变化输出 {}") {
		t.Error("output format should require only-changed-fields")
	}
}

func TestProfileRules_Caveman(t *testing.T) {
	for _, want := range []string{"无引导词", "分号分隔", "原样保留", "只输出有变化"} {
		found := false
		for _, r := range profileRules {
			if strings.Contains(r, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("rule mentioning %q not found", want)
		}
	}
}

// ---- 启动迁移 ----

func TestProfileWorker_RunStartupMigration_MigratesLegacy(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	// 造一个用户 + 旧表混装值
	db := newTestUserRepoDB(t)
	userRepo := service.NewUserRepo(db, 60, 4)
	if err := db.Create(&model.User{ID: "user-1", Username: "u1"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "user-1", Field: "职业阶段", FieldValue: "5年Go后端；gap期求职中"}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "user-1", Field: "重要关系", FieldValue: "CJ: 伴侣"}); err != nil {
		t.Fatalf("seed legacy2: %v", err)
	}

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"职业阶段":["5年Go后端","gap期求职中"],"重要关系":["CJ: 伴侣"]}`},
	}}
	w := &UserProfileWorker{flashClient: llmClient, profileRepo: legacyRepo, factRepo: factRepo, userRepo: userRepo}

	w.RunStartupMigration(ctx)

	// 请求是迁移专用 prompt（全量拆分，非"仅变化字段"）
	req := llmClient.reqs[0]
	reqMsg := req.Messages[0].Content
	if !strings.Contains(reqMsg, "拆分为独立事实列表") {
		t.Errorf("migration should use dedicated task, got: %s", reqMsg)
	}
	if strings.Contains(reqMsg, "只输出有变化或需修订的字段") {
		t.Error("migration rules must NOT contain incremental only-changed rule")
	}
	if !strings.Contains(reqMsg, "所有已有画像字段") {
		t.Error("migration rules should require full output of all fields")
	}

	// facts 写入两个字段
	facts, err := factRepo.GetActiveByUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("GetActiveByUser: %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("expected 3 facts, got %+v", facts)
	}
}

func TestProfileWorker_RunStartupMigration_SkipsMigrated(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	db := newTestUserRepoDB(t)
	userRepo := service.NewUserRepo(db, 60, 4)
	if err := db.Create(&model.User{ID: "user-1", Username: "u1"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "user-1", Field: "籍贯", FieldValue: "河南焦作"}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	// facts 已有数据 → 应跳过
	if err := factRepo.ReplaceFieldFacts(ctx, "user-1", "籍贯", []string{"河南焦作"}, "b0"); err != nil {
		t.Fatalf("seed facts: %v", err)
	}

	llmClient := &syncMockLLM{responses: []llm.Message{{Content: `{"籍贯":["别处"]}`}}}
	w := &UserProfileWorker{flashClient: llmClient, profileRepo: legacyRepo, factRepo: factRepo, userRepo: userRepo}

	w.RunStartupMigration(ctx)

	if llmClient.callCount != 0 {
		t.Errorf("migrated user should skip LLM call, calls=%d", llmClient.callCount)
	}
}

func TestProfileWorker_RunStartupMigration_SkipsNoLegacy(t *testing.T) {
	_, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	db := newTestUserRepoDB(t)
	userRepo := service.NewUserRepo(db, 60, 4)
	if err := db.Create(&model.User{ID: "user-1", Username: "u1"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	llmClient := &syncMockLLM{responses: []llm.Message{{Content: `{}`}}}
	w := &UserProfileWorker{flashClient: llmClient, profileRepo: service.NewUserProfileRepo(db), factRepo: factRepo, userRepo: userRepo}

	w.RunStartupMigration(ctx)

	if llmClient.callCount != 0 {
		t.Errorf("no legacy data should skip LLM call, calls=%d", llmClient.callCount)
	}
}

func TestProfileWorker_RunStartupMigration_QuotaRetry(t *testing.T) {
	legacyRepo, factRepo := setupProfileRepos(t)
	ctx := context.Background()
	db := newTestUserRepoDB(t)
	userRepo := service.NewUserRepo(db, 60, 4)
	if err := db.Create(&model.User{ID: "user-1", Username: "u1"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// 饮食偏好 MaxLen=40，旧值是一整段长文本
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "user-1", Field: "饮食偏好", FieldValue: "这是一段非常非常非常非常非常非常非常非常非常非常非常非常长的旧值"}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	longFact := strings.Repeat("超", 100)
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"饮食偏好":["` + longFact + `"]}`},
		{Content: `{"饮食偏好":["睡眠差"]}`}, // 重生成后合规
	}}
	w := &UserProfileWorker{flashClient: llmClient, profileRepo: legacyRepo, factRepo: factRepo, userRepo: userRepo}

	w.RunStartupMigration(ctx)

	if llmClient.callCount != 2 {
		t.Fatalf("expected 2 LLM calls (1 retry), got %d", llmClient.callCount)
	}
	facts, err := factRepo.GetActiveByField(ctx, "user-1", "饮食偏好")
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	if len(facts) != 1 || facts[0].Value != "睡眠差" {
		t.Fatalf("expected retried fact written, got %+v", facts)
	}
}

func newTestUserRepoDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// ---- 老 repo 测试保留 ----

func TestUserProfileRepo_UpsertOverwrite(t *testing.T) {
	legacyRepo, _ := setupProfileRepos(t)
	ctx := context.Background()

	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "u1", Field: "籍贯", FieldValue: "河南焦作", Source: "explicit"}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := legacyRepo.Upsert(ctx, &model.UserProfile{UserID: "u1", Field: "籍贯", FieldValue: "郑州", Source: "explicit"}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	rows, err := legacyRepo.GetByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUser: %v", err)
	}
	var got string
	for _, r := range rows {
		if r.Field == "籍贯" {
			got = r.FieldValue
		}
	}
	if got != "郑州" {
		t.Errorf("expected overwritten value 郑州, got %s", got)
	}
}

func TestProfileWorker_TimeInMsg(t *testing.T) {
	_, factRepo := setupProfileRepos(t)
	msgs := []model.Message{
		{ID: "m1", Role: "user", Content: "我老家是河南焦作的", CreatedAt: time.Date(2026, 8, 8, 7, 30, 0, 0, time.UTC)},
	}
	w := newTestProfileWorker(nil, nil, factRepo)
	xmlStr, err := w.buildExtractionXML(context.Background(), &SessionSnapshot{ConvID: "c", UserID: "u", msgs: msgs}, false)
	if err != nil {
		t.Fatalf("buildExtractionXML: %v", err)
	}
	if !strings.Contains(xmlStr, `<msg time="08-08 15:30">我老家是河南焦作的</msg>`) {
		t.Errorf("msg element should carry BJ time and content\n%s", xmlStr)
	}
}
