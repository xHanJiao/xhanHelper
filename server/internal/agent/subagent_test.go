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
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// blockingLLM — ChatSync 阻塞直到 ctx 取消（模拟超时/LLM 挂起）
type blockingLLM struct{}

func (m *blockingLLM) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	<-ctx.Done()
	return llm.Message{}, ctx.Err()
}
func (m *blockingLLM) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return nil, errors.New("not implemented")
}
func (m *blockingLLM) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, errors.New("not implemented")
}
func (m *blockingLLM) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string { return base }
func (m *blockingLLM) ModelName() string                                                { return "blocking" }

// recordingMsgSink — 记录 MsgSink 调用
type recordingMsgSink struct {
	users []string
	asst  []llm.Message
	tools []string
}

func (s *recordingMsgSink) SaveUserMsg(content string)       { s.users = append(s.users, content) }
func (s *recordingMsgSink) SaveAssistantMsg(msg llm.Message) { s.asst = append(s.asst, msg) }
func (s *recordingMsgSink) SaveToolMsg(toolCallID, content string) {
	s.tools = append(s.tools, toolCallID+"|"+content)
}

// setupSubAgentRepos 创建 sqlite 的 Conversation/Message repo
func setupSubAgentRepos(t *testing.T) (*service.ConversationRepo, *service.MessageRepo, *gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	convRepo := service.NewConversationRepo(db)
	msgRepo := service.NewMessageRepo(db)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return convRepo, msgRepo, db, cleanup
}

func newSubAgentOrchestrator(flash llm.LLMClient, convRepo *service.ConversationRepo, msgRepo *service.MessageRepo) *Orchestrator {
	return &Orchestrator{
		flashClient:           flash,
		registry:              noopRegistry(),
		subMsgRepo:            msgRepo,
		subConvRepo:           convRepo,
		subAgentMaxIterations: 6,
		subAgentTimeout:       5 * time.Second,
	}
}

func TestDepthCheck_SubAgentRejectsNested(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()

	mainID := "main-1"
	subID := "sub-1"
	if err := convRepo.Create(ctx, &model.Conversation{ID: mainID, UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := convRepo.Create(ctx, &model.Conversation{ID: subID, UserID: "u1", ParentID: &mainID}); err != nil {
		t.Fatal(err)
	}

	orch := newSubAgentOrchestrator(&syncMockLLM{}, convRepo, msgRepo)
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "explore", Description: "调研", Prompt: "p", ParentConvID: subID,
	})
	if res.Success {
		t.Fatal("子会话再派生子任务应被拒绝")
	}
	if !strings.Contains(res.Error, "深度限制") {
		t.Errorf("错误应说明深度限制: %s", res.Error)
	}
}

func TestRunSubAgent_Completes(t *testing.T) {
	convRepo, msgRepo, db, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()

	mainID := "main-1"
	if err := convRepo.Create(ctx, &model.Conversation{ID: mainID, UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `<research_result>结论</research_result>`},
	}}
	orch := newSubAgentOrchestrator(llmClient, convRepo, msgRepo)
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "explore", Description: "调研LLM", Prompt: "去调研", ParentConvID: mainID,
	})

	if !res.Success {
		t.Fatalf("应成功: %+v", res)
	}
	if res.State != "completed" || res.ConvID == "" {
		t.Errorf("state/convID 错误: %+v", res)
	}
	if !strings.Contains(res.Text, "research_result") {
		t.Errorf("Text 应为子 agent 最终回复: %s", res.Text)
	}

	// 子会话存在且带 SubAgentType，用户列表不可见
	subConv, err := convRepo.FindByID(ctx, res.ConvID)
	if err != nil || subConv == nil {
		t.Fatalf("子会话应存在: %v", err)
	}
	extra, err := convRepo.GetExtra(ctx, res.ConvID)
	if err != nil || extra == nil || extra.SubAgentType != "explore" {
		t.Errorf("子会话 Extra.SubAgentType 应为 explore: %+v", extra)
	}
	if subConv.ParentID == nil || *subConv.ParentID != mainID {
		t.Errorf("ParentID 应指向主会话: %v", subConv.ParentID)
	}

	// 消息落库（MsgSink）：user + assistant 各 1 条
	var count int64
	db.Model(&model.Message{}).Where("conversation_id = ?", res.ConvID).Count(&count)
	if count != 2 {
		t.Errorf("子会话应落库 2 条消息, got %d", count)
	}
}

func TestRunSubAgent_TimeoutSalvagesPartial(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()

	mainID := "main-1"
	if err := convRepo.Create(ctx, &model.Conversation{ID: mainID, UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	orch := newSubAgentOrchestrator(&blockingLLM{}, convRepo, msgRepo)
	orch.subAgentTimeout = 50 * time.Millisecond

	start := time.Now()
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "explore", Description: "调研", Prompt: "p", ParentConvID: mainID,
	})
	if time.Since(start) > 2*time.Second {
		t.Errorf("超时兜底过慢: %v", time.Since(start))
	}
	if res.Success {
		t.Fatal("超时应失败")
	}
	if res.State != "timeout" {
		t.Errorf("state 应为 timeout, got %s", res.State)
	}
	if res.Text == "" {
		t.Error("超时应返回部分成果文本（工具日志摘要）")
	}
}

func TestSubAgent_EventForwarding(t *testing.T) {
	orch := &Orchestrator{}
	mainEvents := make(chan *llm.StreamEvent, 16)
	fwd := orch.makeSubAgentForwarder(mainEvents, "sub-1", "调研LLM")

	// client_tool_call 必须送达
	fwd(&llm.StreamEvent{Type: "client_tool_call", Content: "payload"})
	// status 打包为 subagent 事件（含 conv_id）
	fwd(&llm.StreamEvent{Type: "mini_react:status", Content: "第 2/20 轮"})
	// tool_call 打包为 subagent 事件
	fwd(&llm.StreamEvent{Type: "mini_react:tool_call", Content: `{"tool":"webfetch"}`})
	// thought/done 丢弃
	fwd(&llm.StreamEvent{Type: "mini_react:thought", Content: "思考"})
	fwd(&llm.StreamEvent{Type: "mini_react:done", Content: "{}"})

	close(mainEvents)
	var got []*llm.StreamEvent
	for evt := range mainEvents {
		got = append(got, evt)
	}
	if len(got) != 3 {
		t.Fatalf("应转发 3 个事件, got %d: %+v", len(got), got)
	}
	if got[0].Type != "client_tool_call" || got[0].Content != "payload" {
		t.Errorf("client_tool_call 转发错误: %+v", got[0])
	}
	// subagent 事件：JSON 含 conv_id/description/type/content
	var e1, e2 subAgentEvent
	if err := json.Unmarshal([]byte(got[1].Content), &e1); err != nil {
		t.Fatalf("subagent 事件应为 JSON: %v", err)
	}
	if e1.ConvID != "sub-1" || e1.Description != "调研LLM" || e1.Type != "status" || e1.Content != "第 2/20 轮" {
		t.Errorf("status 打包错误: %+v", e1)
	}
	if err := json.Unmarshal([]byte(got[2].Content), &e2); err != nil {
		t.Fatalf("subagent 事件应为 JSON: %v", err)
	}
	if e2.Type != "tool_call" || !strings.Contains(e2.Content, "webfetch") {
		t.Errorf("tool_call 打包错误: %+v", e2)
	}

	// nil 通道 = 丢弃全部
	noop := orch.makeSubAgentForwarder(nil, "sub-1", "d")
	noop(&llm.StreamEvent{Type: "client_tool_call", Content: "x"})
}

func TestSubAgent_MsgSinkPersists(t *testing.T) {
	sink := &recordingMsgSink{}
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: "第一轮", ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{}`}}}},
		{Content: "最终结果"},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         registryWithUnifiedSearch().GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  registryWithUnifiedSearch(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		ConvID:        "sub-1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent:      SubAgentExt{MsgSink: sink},
	})
	result, err := m.Run(context.Background(), "任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalContent != "最终结果" {
		t.Errorf("unexpected final: %s", result.FinalContent)
	}
	if len(sink.users) != 1 || sink.users[0] != "任务" {
		t.Errorf("user 消息未落库: %+v", sink.users)
	}
	if len(sink.asst) != 2 {
		t.Errorf("assistant 消息应落库 2 条（含工具轮）, got %d", len(sink.asst))
	}
	if len(sink.tools) != 1 || !strings.Contains(sink.tools[0], "call-1") {
		t.Errorf("tool 消息未落库: %+v", sink.tools)
	}
}

func TestMiniReAct_WhitelistGuard(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: "", ToolCalls: []llm.ToolCall{{ID: "c1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{}`}}}},
		{Content: "done"},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         registryWithUnifiedSearch().GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  registryWithUnifiedSearch(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent:      SubAgentExt{AllowedTools: map[string]bool{"webfetch": true}}, // 不包含 unified_search
	})
	result, err := m.Run(context.Background(), "任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.ToolCallLog) != 1 {
		t.Fatalf("应记录 1 次工具调用, got %d", len(result.ToolCallLog))
	}
	if result.ToolCallLog[0].Result.Success {
		t.Error("白名单外工具应被拦截")
	}
	if !strings.Contains(result.ToolCallLog[0].Result.Error, "不在白名单中") {
		t.Errorf("拦截错误信息: %s", result.ToolCallLog[0].Result.Error)
	}
}

// 执行超时不置位 unavailable（客户端可能只是慢，可重试）
func TestMiniReAct_ClientToolTimeoutNoFastFail(t *testing.T) {
	bridge := NewClientToolBridge()
	m := NewMiniReAct(&MiniReActConfig{
		Client:        &blockingLLM{},
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent: SubAgentExt{
			ClientTools:       []ClientTool{{ServerName: "browser", Name: "navigate", IsReadOnly: true}},
			ClientBridge:      bridge,
			ClientToolTimeout: 30 * time.Millisecond,
		},
		OnEvent: func(*llm.StreamEvent) bool { return true },
	})
	// 首个超时：不置位，可重试
	_, unavailable := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "navigate"})
	if unavailable {
		t.Fatal("执行超时不应标记不可用")
	}
	if m.clientToolsUnavailable {
		t.Fatal("执行超时不应置位 clientToolsUnavailable")
	}
	// 后续调用真实执行（再超时），而非快速失败
	r, _ := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "navigate"})
	if r.Success || strings.Contains(r.Error, "已跳过") {
		t.Errorf("超时后应可重试（真实执行）: %+v", r)
	}
}

// 送达失败（事件未到客户端）= 真断连：置位 unavailable，后续快速失败
func TestMiniReAct_ClientToolSendFailFastFails(t *testing.T) {
	bridge := NewClientToolBridge()
	m := NewMiniReAct(&MiniReActConfig{
		Client:        &blockingLLM{},
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "u1",
		MaxIterations: 5,
		Caller:        "test",
		SubAgent: SubAgentExt{
			ClientTools:       []ClientTool{{ServerName: "browser", Name: "navigate", IsReadOnly: true}},
			ClientBridge:      bridge,
			ClientToolTimeout: 2 * time.Second,
		},
		// 模拟事件送达失败（forwarder 5s 超时丢弃）：OnEvent 返回 false
		OnEvent: func(*llm.StreamEvent) bool { return false },
	})
	_, unavailable := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "navigate"})
	if !unavailable {
		t.Fatal("送达失败应标记不可用（真断连）")
	}
	if !m.clientToolsUnavailable {
		t.Fatal("送达失败应置位 clientToolsUnavailable")
	}
	// 后续快速失败
	r, _ := m.execClientTool(context.Background(), tool.ToolCall{Tool: "browser", Function: "navigate"})
	if r.Success || !strings.Contains(r.Error, "已跳过") {
		t.Errorf("断连后后续客户端工具应快速失败: %+v", r)
	}
}

func TestSummarizeToolCallLog(t *testing.T) {
	logs := []ToolCallLog{
		{Tool: "webfetch", Function: "fetch_url", Result: &tool.Result{Success: true, Data: json.RawMessage(`{"text":"内容"}`)}},
		{Tool: "webfetch", Function: "fetch_url", Result: &tool.Result{Success: false, Error: "抓取失败"}},
	}
	s := summarizeToolCallLog(logs)
	if !strings.Contains(s, "[1] webfetch.fetch_url → 成功") || !strings.Contains(s, "内容") {
		t.Errorf("成功条目格式错误: %s", s)
	}
	if !strings.Contains(s, "[2] webfetch.fetch_url → 失败") || !strings.Contains(s, "抓取失败") {
		t.Errorf("失败条目格式错误: %s", s)
	}
	if s2 := summarizeToolCallLog(nil); !strings.Contains(s2, "尚未产生") {
		t.Errorf("空日志应说明: %s", s2)
	}
}

func TestBuildSubAgentSystemPrompt(t *testing.T) {
	s := buildSubAgentSystemPrompt()
	if !strings.Contains(s, "联网调研专家") || !strings.Contains(s, "当前时间") {
		t.Errorf("system prompt 应含角色与时间: %s", s)
	}
}

func TestFilterClientToolsByWhitelist(t *testing.T) {
	defs := []llm.ToolDefinition{
		{Name: "browser", Description: "b"},
		{Name: "chart", Description: "c"},
	}
	filtered := filterClientToolsByWhitelist(defs, []string{"browser"})
	if len(filtered) != 1 || filtered[0].Name != "browser" {
		t.Errorf("白名单外客户端工具应过滤: %+v", filtered)
	}
}

// ---- 成本中心接入测试 ----

type costRecorder struct {
	records []llmcost.UsageRecord
}

func (r *costRecorder) Record(rec llmcost.UsageRecord) { r.records = append(r.records, rec) }

// TestRunSubAgent_CostScenario — 子循环 LLM 调用记入成本中心（main_chat/sub_agent_run，ConvID=子会话）
func TestRunSubAgent_CostScenario(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()

	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	rec := &costRecorder{}
	llmClient := &syncMockLLM{responses: []llm.Message{{Content: "调研结论", Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 20}}}}
	wrapped := llmcost.WrapLLMClient(llmClient, rec)

	orch := newSubAgentOrchestrator(wrapped, convRepo, msgRepo)
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "explore", Description: "调研", Prompt: "p", ParentConvID: "main-1",
	})
	if !res.Success {
		t.Fatalf("RunSubAgent: %+v", res)
	}
	// CostTracker record 是异步的（go t.record），轮询等待
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.records) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(rec.records) != 1 {
		t.Fatalf("应记录 1 条成本, got %d", len(rec.records))
	}
	r := rec.records[0]
	if r.Category != llmcost.CategoryMainChat || r.SubCategory != llmcost.SubSubAgentRun {
		t.Errorf("场景应为 main_chat/sub_agent_run, got %s/%s", r.Category, r.SubCategory)
	}
	if r.ConvID != res.ConvID {
		t.Errorf("ConvID 应为子会话 %s, got %s", res.ConvID, r.ConvID)
	}
	if r.UserID != "u1" {
		t.Errorf("UserID 错误: %s", r.UserID)
	}
}

// ---- 标签清理 ----

func TestCollectTabIDsFromLog(t *testing.T) {
	logs := []ToolCallLog{
		{Tool: "browser", Function: "agent_browser_open", Result: &tool.Result{Success: true, Data: json.RawMessage(`{"tabId":"browser-1"}`)}},
		{Tool: "browser", Function: "agent_browser_open", Result: &tool.Result{Success: true, Data: json.RawMessage(`{"tabId":"browser-2"}`)}},
		{Tool: "browser", Function: "agent_browser_open", Result: &tool.Result{Success: true, Data: json.RawMessage(`{"tabId":"browser-1"}`)}},     // 重复去重
		{Tool: "browser", Function: "agent_browser_open", Result: &tool.Result{Success: false, Error: "打开失败"}},                                     // 失败跳过
		{Tool: "browser", Function: "agent_browser_snapshot", Result: &tool.Result{Success: true, Data: json.RawMessage(`{"tabId":"browser-9"}`)}}, // 非 open 跳过
		{Tool: "unified_search", Function: "semantic_search", Result: &tool.Result{Success: true}},                                                 // 非 browser 跳过
	}
	ids := collectTabIDsFromLog(logs)
	if len(ids) != 2 || ids[0] != "browser-1" || ids[1] != "browser-2" {
		t.Errorf("应收集 2 个去重 tabId: %v", ids)
	}
	if got := collectTabIDsFromLog(nil); len(got) != 0 {
		t.Errorf("空日志应返回空: %v", got)
	}
}

// TestRunSubAgent_ClosesTabsOnEnd — 子任务结束后对本次打开的全部标签发 close_tab 清理
func TestRunSubAgent_ClosesTabsOnEnd(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	bridge := NewClientToolBridge()
	mainEvents := make(chan *llm.StreamEvent, 64)
	orch := newSubAgentOrchestrator(nil, convRepo, msgRepo)
	orch.clientBridge = bridge
	orch.registry = noopRegistry()
	orch.clientToolTimeout = 2 * time.Second
	orch.subAgentCleanupTimeout = 2 * time.Second
	orch.flashClient = &syncMockLLM{responses: []llm.Message{
		{Content: "", ToolCalls: []llm.ToolCall{{ID: "c1", Function: llm.ToolCallFunction{Name: "browser__agent_browser_open", Arguments: `{"url":"https://example.com","background":true}`}}}},
		{Content: "调研完成"},
	}}

	// 模拟客户端：消费 client_tool_call，open 回传 tabId，close_tab 回传成功
	closed := make(chan string, 4)
	go func() {
		for evt := range mainEvents {
			if evt.Type != "client_tool_call" {
				continue
			}
			var payload struct {
				CallID   string                 `json:"call_id"`
				ToolName string                 `json:"tool_name"`
				Args     map[string]interface{} `json:"arguments"`
			}
			if json.Unmarshal([]byte(evt.Content), &payload) != nil {
				continue
			}
			if payload.ToolName == "browser__agent_browser_open" {
				res := ClientToolResult{Result: json.RawMessage(`{"tabId":"browser-1","title":"Example"}`)}
				_ = bridge.OnResult(payload.CallID, "u1", res)
			} else if payload.ToolName == "browser__agent_browser_close_tab" {
				closed <- (payload.Args["tabId"]).(string)
				_ = bridge.OnResult(payload.CallID, "u1", ClientToolResult{Result: json.RawMessage(`{"closed":true}`)})
			}
		}
	}()

	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", MainEvents: mainEvents,
		ClientTools: []tool.ClientToolInfo{{ServerName: "browser", Name: "agent_browser_open", IsReadOnly: true}},
	})
	if !res.Success {
		t.Fatalf("RunSubAgent: %+v", res)
	}
	select {
	case tabID := <-closed:
		if tabID != "browser-1" {
			t.Errorf("应关闭 browser-1, got %s", tabID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到 close_tab 清理事件")
	}
	select {
	case tabID := <-closed:
		t.Errorf("不应重复关闭 tab: %s", tabID)
	default:
	}
}

// TestRunSubAgent_EmitsStartEnd — 子任务活动面板 start/end 事件
func TestRunSubAgent_EmitsStartEnd(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	orch := newSubAgentOrchestrator(&syncMockLLM{responses: []llm.Message{{Content: "结论"}}}, convRepo, msgRepo)
	mainEvents := make(chan *llm.StreamEvent, 16)
	startCh := make(chan subAgentEvent, 2)
	go func() {
		for evt := range mainEvents {
			if evt.Type != "subagent" {
				continue
			}
			var e subAgentEvent
			if json.Unmarshal([]byte(evt.Content), &e) == nil {
				startCh <- e
			}
		}
	}()

	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", MainEvents: mainEvents,
	})
	if !res.Success {
		t.Fatalf("RunSubAgent: %+v", res)
	}
	// start 事件
	select {
	case e := <-startCh:
		if e.Type != "start" || e.State != "started" || e.ConvID != res.ConvID || e.Description != "调研" {
			t.Errorf("start 事件错误: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 start 事件")
	}
	// end 事件（跳过中间的 status/tool_call）
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-startCh:
			if e.Type == "end" {
				if e.State != "completed" || e.ConvID != res.ConvID {
					t.Errorf("end 事件错误: %+v", e)
				}
				return
			}
		case <-deadline:
			t.Fatal("未收到 end 事件")
		}
	}
}
