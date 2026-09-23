package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"xhan-helper/internal/util"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ---- 决策段测试脚手架 ----

// factMockLLM 按调用序号返回预设响应（0 = 抽取，之后逐候选一次决策），支持按序号注入错误。
type factMockLLM struct {
	responses []llm.Message
	errs      map[int]error
	calls     int
	reqs      []*llm.ChatRequest
	scenarios []llmcost.Scenario
}

func (m *factMockLLM) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	i := m.calls
	m.calls++
	m.reqs = append(m.reqs, req)
	if s, ok := llmcost.ScenarioFromContext(ctx); ok {
		m.scenarios = append(m.scenarios, s)
	} else {
		m.scenarios = append(m.scenarios, llmcost.Scenario{})
	}
	if err, ok := m.errs[i]; ok {
		return llm.Message{}, err
	}
	if i >= len(m.responses) {
		return llm.Message{}, errors.New("factMockLLM: no more responses")
	}
	return m.responses[i], nil
}

func (m *factMockLLM) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return nil, errors.New("mock: Chat not implemented")
}
func (m *factMockLLM) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, errors.New("mock: ChatWithRawJSON not implemented")
}
func (m *factMockLLM) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string { return base }
func (m *factMockLLM) ModelName() string                                                { return "mock" }

// factTestDeps — 决策段测试的真实依赖（sqlite + 内存向量库）。
type factTestDeps struct {
	factRepo *service.FactRepo
	logRepo  *service.FactChangelogRepo
	embRepo  *service.EmbeddingRepo
	store    *VectorStore
}

func setupFactDeps(t *testing.T) *factTestDeps {
	t.Helper()
	if err := idgen.Init(1); err != nil {
		t.Fatalf("init idgen: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Fact{}, &model.FactChangelog{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &factTestDeps{
		factRepo: service.NewFactRepo(db),
		logRepo:  service.NewFactChangelogRepo(db),
		embRepo:  service.NewEmbeddingRepo(db),
		store:    NewVectorStore(),
	}
}

// newFactWorkerForTest 装配完整 worker。dupeThreshold 传 1.1 可关掉「相似度静默跳过」，
// 让候选一定进决策段（mock embedder 对所有文本返回同一向量，cos 恒为 1.0）。
func newFactWorkerForTest(mock llm.LLMClient, deps *factTestDeps, dupeThreshold float64) *FactWorker {
	return NewFactWorker(&FactWorkerConfig{
		Extractor:     NewFactExtractor(&FactExtractorConfig{LLMClient: mock}),
		Decider:       NewFactDecider(&FactDeciderConfig{LLMClient: mock}),
		Enabled:       true,
		FactRepo:      deps.factRepo,
		ChangelogRepo: deps.logRepo,
		EmbeddingRepo: deps.embRepo,
		Embedder:      &mockEmbedder{},
		VectorStore:   NewVectorSearchAdapter(deps.store),
		TopK:          5,
		DupeThreshold: dupeThreshold,
	})
}

// extractJSON 构造抽取段响应。
func extractJSON(facts string) llm.Message {
	return llm.Message{Role: "assistant", Content: `{"facts":[` + facts + `]}`}
}

// toolCallMsg 构造原生 FC 响应（provider 侧的名字形态：facts__fn）。
func toolCallMsg(fn, args string) llm.Message {
	return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: "c1", Function: llm.ToolCallFunction{Name: "facts__" + fn, Arguments: args}},
	}}
}

// testSessWith 构造带 id 的快照（末条 user 消息的 id 会进 changelog）。
func testSessWith(id, content string) *SessionSnapshot {
	return &SessionSnapshot{
		ConvID: "conv-1", UserID: "u1",
		msgs: []model.Message{{ID: id, Role: "user", Content: content}},
	}
}

// seedFact 直接落一条事实并同步向量（模拟库里已有的旧事实）。
func seedFact(t *testing.T, deps *factTestDeps, subject, kind, content string) *model.Fact {
	t.Helper()
	f := &model.Fact{
		ID:          "11111111-1111-1111-1111-111111111111",
		UserID:      "u1",
		Subject:     subject,
		Kind:        kind,
		Content:     content,
		ContentHash: util.MD5Hex(normalizeFactContent(content)),
		Status:      model.FactStatusActive,
	}
	if err := deps.factRepo.Create(context.Background(), f); err != nil {
		t.Fatalf("seed fact: %v", err)
	}
	deps.store.Upsert(model.VectorPrefixFact+f.ID, []float64{0.1, 0.2, 0.3}, map[string]string{
		"type": model.VectorTypeFact, "user_id": "u1",
		"subject": subject, "kind": kind, "content": content,
	})
	return f
}

func listFacts(t *testing.T, deps *factTestDeps) []model.Fact {
	t.Helper()
	list, _, err := deps.factRepo.ListByUser(context.Background(), "u1", 50, 0)
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	return list
}

func listLogs(t *testing.T, deps *factTestDeps, factID string) []model.FactChangelog {
	t.Helper()
	list, _, err := deps.logRepo.ListByFact(context.Background(), "u1", factID, 20, 0)
	if err != nil {
		t.Fatalf("list changelogs: %v", err)
	}
	return list
}

// ---- 用例 ----

func TestFactWorker_Add(t *testing.T) {
	deps := setupFactDeps(t)
	mock := &factMockLLM{responses: []llm.Message{
		extractJSON(`{"subject":"老K小程序","kind":"state","content":"本人这次在老K的小程序上兼产品与开发"}`),
		toolCallMsg("add_fact", `{"subject":"老K小程序","kind":"state","content":"本人这次在老K的小程序上兼产品与开发"}`),
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m9", "老K那个小程序我这次兼产品开发")); err != nil {
		t.Fatalf("process: %v", err)
	}

	facts := listFacts(t, deps)
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Kind != model.FactKindState || f.Status != model.FactStatusActive {
		t.Errorf("unexpected fact: %+v", f)
	}
	if f.ContentHash != util.MD5Hex(normalizeFactContent(f.Content)) {
		t.Errorf("content hash not normalized: %q", f.ContentHash)
	}
	if deps.store.Size() != 1 {
		t.Errorf("expected 1 vector, got %d", deps.store.Size())
	}
	emb, err := deps.embRepo.GetByEntity(context.Background(), model.EntityTypeFact, f.ID)
	if err != nil {
		t.Fatalf("get embedding: %v", err)
	}
	if emb == nil || emb.Embedding == nil || emb.Model != "mock" {
		t.Errorf("embedding row missing/incomplete: %+v", emb)
	}
	if logs := listLogs(t, deps, f.ID); len(logs) != 0 {
		t.Errorf("ADD should not write changelog, got %d", len(logs))
	}
	// 决策段场景标注
	if len(mock.scenarios) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(mock.scenarios))
	}
	if sc := mock.scenarios[1]; sc.SubCategory != llmcost.SubFactDecide || sc.RefType != "fact" || sc.RefID != "0" {
		t.Errorf("unexpected decide scenario: %+v", sc)
	}
	if mock.reqs[1].SystemPrompt != factDecideSystemPrompt {
		t.Errorf("decide should use its own system prompt")
	}
	if len(mock.reqs[1].Tools) != 1 || mock.reqs[1].Tools[0].Name != "facts" {
		t.Errorf("decide should pass the facts tool definition")
	}
}

func TestFactWorker_HashDupeSkipsWithoutLLM(t *testing.T) {
	deps := setupFactDeps(t)
	seedFact(t, deps, "老K小程序", model.FactKindState, "本人只做技术")
	mock := &factMockLLM{responses: []llm.Message{
		// 写法不同（标点/全角/大小写），归一化后与库里那条同 hash
		extractJSON(`{"subject":"老K小程序","kind":"state","content":"本人只做技术。"}`),
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m1", "x")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(mock.reqs) != 1 {
		t.Errorf("hash dupe should skip decide, got %d LLM call(s)", len(mock.reqs))
	}
	if facts := listFacts(t, deps); len(facts) != 1 {
		t.Errorf("expected no new fact, got %d", len(facts))
	}
}

func TestFactWorker_SimilarTop1SkipsWithoutLLM(t *testing.T) {
	deps := setupFactDeps(t)
	seedFact(t, deps, "老K小程序", model.FactKindState, "本人只做技术")
	mock := &factMockLLM{responses: []llm.Message{
		// 内容不同（hash 不撞），但 mock embedder 让 cos = 1.0 ≥ 0.95
		extractJSON(`{"subject":"老K小程序","kind":"state","content":"本人这次兼产品与开发"}`),
	}}
	w := newFactWorkerForTest(mock, deps, 0.95)

	if err := w.Process(context.Background(), testSessWith("m1", "x")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(mock.reqs) != 1 {
		t.Errorf("similar top1 should skip decide, got %d LLM call(s)", len(mock.reqs))
	}
	if facts := listFacts(t, deps); len(facts) != 1 {
		t.Errorf("expected no new fact, got %d", len(facts))
	}
}

func TestFactWorker_Update(t *testing.T) {
	deps := setupFactDeps(t)
	old := seedFact(t, deps, "老K小程序", model.FactKindState, "本人只做技术")
	mock := &factMockLLM{responses: []llm.Message{
		extractJSON(`{"subject":"老K小程序","kind":"state","fields":{"due_at":"2026-09-25"},"content":"本人这次在老K的小程序上兼产品与开发"}`),
		toolCallMsg("update_fact", `{"target_id":"`+old.ID+`","content":"本人这次在老K的小程序上兼产品与开发","fields":{"due_at":"2026-09-25"}}`),
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m7", "改口了")); err != nil {
		t.Fatalf("process: %v", err)
	}

	facts := listFacts(t, deps)
	if len(facts) != 1 {
		t.Fatalf("update should not add a row, got %d", len(facts))
	}
	f := facts[0]
	if f.Content != "本人这次在老K的小程序上兼产品与开发" {
		t.Errorf("content not updated: %q", f.Content)
	}
	if f.ContentHash != util.MD5Hex(normalizeFactContent(f.Content)) {
		t.Errorf("hash not recomputed")
	}
	if f.Fields == nil || *f.Fields != `{"due_at":"2026-09-25"}` {
		t.Errorf("fields not updated: %v", f.Fields)
	}

	logs := listLogs(t, deps, f.ID)
	if len(logs) != 1 {
		t.Fatalf("expected 1 changelog, got %d", len(logs))
	}
	if logs[0].Op != model.FactOpUpdate || logs[0].OldContent != old.Content || logs[0].NewContent != f.Content {
		t.Errorf("unexpected changelog: %+v", logs[0])
	}
	if logs[0].SourceMsgID != "m7" {
		t.Errorf("source_msg_id should be the last user message id, got %q", logs[0].SourceMsgID)
	}
	// 向量 metadata 必须是新正文
	hits := deps.store.Search(nil, 5, nil)
	if len(hits) != 1 || hits[0].Metadata["content"] != f.Content {
		t.Errorf("vector metadata not refreshed: %+v", hits)
	}
}

func TestFactWorker_Delete(t *testing.T) {
	deps := setupFactDeps(t)
	old := seedFact(t, deps, "本人", model.FactKindState, "本人计划为家里还债")
	mock := &factMockLLM{responses: []llm.Message{
		extractJSON(`{"subject":"本人","kind":"state","content":"本人不想再管那笔债了"}`),
		toolCallMsg("delete_fact", `{"target_id":"`+old.ID+`"}`),
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m3", "不想管了")); err != nil {
		t.Fatalf("process: %v", err)
	}

	if facts := listFacts(t, deps); len(facts) != 0 {
		t.Errorf("deleted fact should not be listed, got %d", len(facts))
	}
	raw, err := deps.factRepo.FindByID(context.Background(), "u1", old.ID)
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if raw == nil || raw.Status != model.FactStatusDeleted {
		t.Errorf("expected soft-deleted row, got %+v", raw)
	}
	if deps.store.Size() != 0 {
		t.Errorf("vector should be removed, size=%d", deps.store.Size())
	}
	emb, err := deps.embRepo.GetByEntity(context.Background(), model.EntityTypeFact, old.ID)
	if err != nil {
		t.Fatalf("get embedding: %v", err)
	}
	if emb != nil {
		t.Errorf("embedding row should be deleted, got %+v", emb)
	}
	logs := listLogs(t, deps, old.ID)
	if len(logs) != 1 || logs[0].Op != model.FactOpDelete || logs[0].OldContent != old.Content {
		t.Errorf("unexpected changelog: %+v", logs)
	}
}

func TestFactWorker_FabricatedTargetIDDropped(t *testing.T) {
	deps := setupFactDeps(t)
	mock := &factMockLLM{responses: []llm.Message{
		extractJSON(`{"subject":"X","kind":"state","content":"一件新事"}`),
		toolCallMsg("update_fact", `{"target_id":"deadbeef-dead-dead-dead-deaddeadbeef","content":"改掉"}`),
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m1", "x")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if facts := listFacts(t, deps); len(facts) != 0 {
		t.Errorf("fabricated target_id must not write, got %d fact(s)", len(facts))
	}
}

func TestFactWorker_BadArgsDropped(t *testing.T) {
	deps := setupFactDeps(t)
	mock := &factMockLLM{responses: []llm.Message{
		extractJSON(`{"subject":"X","kind":"state","content":"一件新事"}`),
		toolCallMsg("add_fact", `{"subject": bad json`),
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m1", "x")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if facts := listFacts(t, deps); len(facts) != 0 {
		t.Errorf("bad args must not write, got %d fact(s)", len(facts))
	}
}

func TestFactWorker_NoToolCallIsNone(t *testing.T) {
	deps := setupFactDeps(t)
	mock := &factMockLLM{responses: []llm.Message{
		extractJSON(`{"subject":"X","kind":"state","content":"一件新事"}`),
		{Role: "assistant", Content: "与近邻同义，跳过"},
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m1", "x")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if facts := listFacts(t, deps); len(facts) != 0 {
		t.Errorf("NONE must not write, got %d fact(s)", len(facts))
	}
}

func TestFactWorker_DecideFailureDoesNotAbortOthers(t *testing.T) {
	deps := setupFactDeps(t)
	// responses 的下标 == 调用序号（0 = 抽取，之后逐候选一次决策）
	mock := &factMockLLM{
		responses: []llm.Message{
			extractJSON(`{"subject":"A","kind":"state","content":"第一条"},{"subject":"B","kind":"state","content":"第二条"}`),
			{}, // 候选 1 的决策调用被 errs 置为错误，这个响应不会被用到
			toolCallMsg("add_fact", `{"subject":"B","kind":"state","content":"第二条"}`),
		},
		errs: map[int]error{1: errors.New("boom")},
	}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m1", "x")); err != nil {
		t.Fatalf("single candidate failure must not abort: %v", err)
	}
	facts := listFacts(t, deps)
	if len(facts) != 1 || facts[0].Content != "第二条" {
		t.Fatalf("expected the second candidate to be written, got %+v", facts)
	}
}

// TestFactWorker_SerialVisibility 是串行设计的存在理由：
// 候选 2 的决策请求里必须能看到候选 1 刚落库的那条事实（否则批内互不可见，两条相似候选都会判 ADD）。
func TestFactWorker_SerialVisibility(t *testing.T) {
	deps := setupFactDeps(t)
	mock := &factMockLLM{responses: []llm.Message{
		extractJSON(`{"subject":"A","kind":"state","content":"第一条"},{"subject":"A","kind":"state","content":"第二条补充"}`),
		toolCallMsg("add_fact", `{"subject":"A","kind":"state","content":"第一条"}`),
		toolCallMsg("add_fact", `{"subject":"A","kind":"state","content":"第二条补充"}`),
	}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	if err := w.Process(context.Background(), testSessWith("m1", "x")); err != nil {
		t.Fatalf("process: %v", err)
	}
	facts := listFacts(t, deps)
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(facts))
	}

	var firstID string
	for _, f := range facts {
		if f.Content == "第一条" {
			firstID = f.ID
		}
	}
	if firstID == "" {
		t.Fatal("first fact not found")
	}
	if len(mock.reqs) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(mock.reqs))
	}
	secondPrompt := mock.reqs[2].Messages[0].Content
	if !containsStr(secondPrompt, firstID) {
		t.Errorf("second candidate must see the first one as a neighbor:\n%s", secondPrompt)
	}
}

func TestFactWorker_DisabledSkipsEverything(t *testing.T) {
	mock := &factMockLLM{}
	w := NewFactWorker(&FactWorkerConfig{
		Extractor: NewFactExtractor(&FactExtractorConfig{LLMClient: mock}),
		Enabled:   false,
	})
	if w.Name() != "fact" {
		t.Errorf("expected name 'fact', got %q", w.Name())
	}
	if err := w.Process(context.Background(), testSessWith("m1", "hi")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.reqs) != 0 {
		t.Errorf("disabled worker should not consume LLM, got %d call(s)", len(mock.reqs))
	}
}

func TestFactWorker_ExtractAnnotatesScenarioAndSendsAssistant(t *testing.T) {
	deps := setupFactDeps(t)
	mock := &factMockLLM{responses: []llm.Message{extractJSON("")}}
	w := newFactWorkerForTest(mock, deps, 1.1)

	sess := &SessionSnapshot{
		ConvID: "conv-1", UserID: "user-1",
		msgs: []model.Message{
			{ID: "m1", Role: "user", Content: "hi"},
			{ID: "m2", Role: "assistant", Content: "hello"},
		},
	}
	if err := w.Process(context.Background(), sess); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.reqs) != 1 {
		t.Fatalf("expected 1 LLM call (0 candidates), got %d", len(mock.reqs))
	}
	sc := mock.scenarios[0]
	if sc.Category != llmcost.CategoryPostChat || sc.SubCategory != llmcost.SubFactExtract {
		t.Errorf("unexpected extract scenario: %+v", sc)
	}
	if sc.UserID != "user-1" || sc.ConvID != "conv-1" {
		t.Errorf("scenario should carry user/conv, got %+v", sc)
	}
	if !containsStr(mock.reqs[0].Messages[0].Content, `>hello</turn>`) {
		t.Errorf("assistant message should be included: %.200s", mock.reqs[0].Messages[0].Content)
	}
}
