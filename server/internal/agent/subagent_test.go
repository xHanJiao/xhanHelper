package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// recordingDebugSink — 记录 DebugJSONSink.Submit 调用
type recordingDebugSink struct {
	submits []struct {
		id  string
		req *string
	}
}

func (s *recordingDebugSink) Submit(id string, req *string) bool {
	s.submits = append(s.submits, struct {
		id  string
		req *string
	}{id, req})
	return true
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
		clientBridge:          NewClientToolBridge(), // 子 agent 硬要求客户端在线（prod 由 appinit 全局注入）
	}
}

// testClientToolOpts 模拟客户端在线：请求携带浏览器工具（web_explore 依赖）。
func testClientToolOpts() []tool.ClientToolInfo {
	return []tool.ClientToolInfo{{ServerName: "browser", Name: "navigate", IsReadOnly: true}}
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
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: subID, ClientTools: testClientToolOpts(),
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
		SubAgentType: "web_explore", Description: "调研LLM", Prompt: "去调研", ParentConvID: mainID, ClientTools: testClientToolOpts(),
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
	if err != nil || extra == nil || extra.SubAgentType != "web_explore" {
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
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: mainID, ClientTools: testClientToolOpts(),
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

// TestSubAgent_MsgSinkPersists — 子循环消息完整落库（可观测/可调试）
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
		SubAgent:      SubAgentExt{AllowedTools: map[string][]string{}}, // 空 map = 全部拒绝，不含 unified_search
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
	web := buildSubAgentSystemPrompt(subagentSpecs["web_explore"], 20, 600)
	if !strings.Contains(web, "联网调研专家") || !strings.Contains(web, "当前时间") {
		t.Errorf("web_explore system prompt 应含角色与时间: %s", web)
	}
	if !strings.Contains(web, "最多执行 19 轮工具调用") || !strings.Contains(web, "第 20 轮为强制总结轮") || !strings.Contains(web, "总时长约 600 秒") {
		t.Errorf("web_explore system prompt 应含固定预算: %s", web)
	}
	file := buildSubAgentSystemPrompt(subagentSpecs["file_explore"], 20, 600)
	if !strings.Contains(file, "本地文件与只读命令探索专家") {
		t.Errorf("file_explore system prompt 应含本地探索角色: %s", file)
	}
	if !strings.Contains(file, "shell__run") {
		t.Errorf("file_explore system prompt 应含 shell__run 能力: %s", file)
	}
	if !strings.Contains(file, "最多执行 19 轮工具调用") {
		t.Errorf("file_explore system prompt 应含预算: %s", file)
	}
	edit := buildSubAgentSystemPrompt(subagentSpecs["file_edit"], 20, 600)
	if !strings.Contains(edit, "本地文件编辑专家") {
		t.Errorf("file_edit system prompt 应含编辑角色: %s", edit)
	}
	if !strings.Contains(edit, "file_edit_result") {
		t.Errorf("file_edit system prompt 应含输出结构: %s", edit)
	}
	uc := buildSubAgentSystemPrompt(subagentSpecs["user_context"], 20, 600)
	if !strings.Contains(uc, "用户信息检索专家") {
		t.Errorf("user_context system prompt 应含检索角色: %s", uc)
	}
	if !strings.Contains(uc, "user_context_result") {
		t.Errorf("user_context system prompt 应含输出结构: %s", uc)
	}
	if !strings.Contains(uc, "最多执行 19 轮工具调用") {
		t.Errorf("user_context system prompt 应含预算: %s", uc)
	}
}

func TestFilterClientToolsByFuncWhitelist(t *testing.T) {
	defs := []llm.ToolDefinition{
		{Name: "browser", Description: "b", Functions: []llm.FuncDef{{Name: "navigate"}, {Name: "close_tab"}}},
		{Name: "fs", Description: "fs", Functions: []llm.FuncDef{{Name: "glob"}, {Name: "grep"}, {Name: "read"}, {Name: "write"}, {Name: "edit"}}},
		{Name: "chart", Description: "c"},
	}

	// fs 函数级过滤：只留 glob/grep/read，剥 write/edit
	filtered := filterClientToolsByFuncWhitelist(defs, map[string][]string{"fs": {"glob", "grep", "read"}})
	if len(filtered) != 1 || filtered[0].Name != "fs" {
		t.Fatalf("应只保留 fs 组: %+v", filtered)
	}
	names := make([]string, 0, len(filtered[0].Functions))
	for _, f := range filtered[0].Functions {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "glob,grep,read" {
		t.Errorf("fs 应只留 glob/grep/read: %v", names)
	}

	// nil funcs = 整组放行
	filtered2 := filterClientToolsByFuncWhitelist(defs, map[string][]string{"browser": nil})
	if len(filtered2) != 1 || len(filtered2[0].Functions) != 2 {
		t.Errorf("browser 整组应放行: %+v", filtered2)
	}

	// 白名单外 server 过滤
	filtered3 := filterClientToolsByFuncWhitelist(defs, map[string][]string{"chart": nil})
	if len(filtered3) != 1 || filtered3[0].Name != "chart" {
		t.Errorf("chart 应保留: %+v", filtered3)
	}

	// 空白名单 / 空 defs
	if got := filterClientToolsByFuncWhitelist(defs, nil); got != nil {
		t.Errorf("nil 白名单应返回 nil: %+v", got)
	}
	if got := filterClientToolsByFuncWhitelist(nil, map[string][]string{"fs": nil}); got != nil {
		t.Errorf("nil defs 应返回 nil: %+v", got)
	}
}

func TestSubAgentSpecs_DispatchTable(t *testing.T) {
	if len(subagentSpecs) != 4 {
		t.Fatalf("应有 4 种子 agent 类型: %+v", subagentSpecs)
	}
	web := subagentSpecs["web_explore"]
	if !web.requiresClient || web.maxToolResult <= 0 {
		t.Errorf("web_explore 应 requiresClient + 正数预算: %+v", web)
	}
	// webfetch 已从 web_explore 移除（用户决策：无用且费 token）
	for _, n := range web.serverWhitelist {
		if n == "webfetch" {
			t.Error("web_explore 不应含 webfetch")
		}
	}
	file := subagentSpecs["file_explore"]
	if !file.requiresClient || file.maxToolResult <= web.maxToolResult {
		t.Errorf("file_explore 应 requiresClient + 预算大于 web_explore: %+v", file)
	}
	// file_explore 客户端白名单：fs 只读三函数 + shell 只读命令 + ui
	fsFuncs := file.clientWhitelist["fs"]
	if fsFuncs == nil || strings.Join(fsFuncs, ",") != "glob,grep,read" {
		t.Errorf("file_explore fs 白名单应只含 glob/grep/read: %v", fsFuncs)
	}
	shellFuncs := file.clientWhitelist["shell"]
	if shellFuncs == nil || strings.Join(shellFuncs, ",") != "run" {
		t.Errorf("file_explore shell 白名单应只含 run: %v", shellFuncs)
	}
	if _, ok := file.clientWhitelist["ui"]; !ok {
		t.Error("file_explore 应含 ui（ask_user）")
	}
	// 服务端白名单不含 webfetch/检索类
	for _, n := range file.serverWhitelist {
		if n == "webfetch" || n == "search_notes_v2" || n == "unified_search" {
			t.Errorf("file_explore 服务端白名单不应含 %s", n)
		}
	}
	// file_edit：唯一开放 fs 写函数（write/edit）的子任务，需客户端
	edit := subagentSpecs["file_edit"]
	if !edit.requiresClient || edit.maxToolResult <= 0 {
		t.Errorf("file_edit 应 requiresClient + 正数预算: %+v", edit)
	}
	editFsFuncs := edit.clientWhitelist["fs"]
	if editFsFuncs == nil || strings.Join(editFsFuncs, ",") != "glob,grep,read,write,edit" {
		t.Errorf("file_edit fs 白名单应含 glob/grep/read/write/edit: %v", editFsFuncs)
	}
	if _, ok := edit.clientWhitelist["ui"]; !ok {
		t.Error("file_edit 应含 ui（ask_user）")
	}
	if !strings.Contains(edit.prompt, "file_edit_result") {
		t.Error("file_edit prompt 应含输出结构 file_edit_result")
	}
	// 写能力只应出现在 file_edit：其它子任务不得放开 write/edit
	for name, spec := range subagentSpecs {
		if name == "file_edit" {
			continue
		}
		for _, fn := range spec.clientWhitelist["fs"] {
			if fn == "write" || fn == "edit" {
				t.Errorf("%s 不应含写函数 %s", name, fn)
			}
		}
	}
	// user_context：纯服务端，不依赖客户端，检索是主力
	uc := subagentSpecs["user_context"]
	if uc.requiresClient {
		t.Error("user_context 不应 requiresClient（纯服务端）")
	}
	if uc.clientWhitelist != nil {
		t.Errorf("user_context 不应有客户端白名单: %+v", uc.clientWhitelist)
	}
	if uc.maxToolResult <= 2000 {
		t.Errorf("user_context 预算应大于 2000（检索是主力，2000 会截断）: %d", uc.maxToolResult)
	}
	hasProfile := false
	hasUnified := false
	for _, n := range uc.serverWhitelist {
		if n == "profile" {
			hasProfile = true
		}
		if n == "unified_search" {
			hasUnified = true
		}
	}
	if !hasProfile || !hasUnified {
		t.Errorf("user_context 服务端白名单应含 profile + unified_search: %v", uc.serverWhitelist)
	}
	if strings.Contains(uc.prompt, "task") {
		t.Error("user_context prompt 不应提及 task（禁止套娃）")
	}
}

func TestRunSubAgent_UnknownType(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	orch := newSubAgentOrchestrator(&syncMockLLM{}, convRepo, msgRepo)
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", ClientTools: testClientToolOpts(),
	})
	if res.Success {
		t.Fatal("未知类型应失败")
	}
	if !strings.Contains(res.Error, "未知子 agent 类型") {
		t.Errorf("应提示未知类型: %s", res.Error)
	}
	// 不创建子会话
	var count int64
	convRepo.DB().Model(&model.Conversation{}).Where("parent_id = ?", "main-1").Count(&count)
	if count != 0 {
		t.Errorf("未知类型不应创建子会话, got %d", count)
	}
}

func TestRunSubAgent_RequiresClient(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	orch := newSubAgentOrchestrator(&syncMockLLM{}, convRepo, msgRepo)
	// 请求未携带客户端工具（客户端离线）
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: "main-1",
	})
	if res.Success {
		t.Fatal("客户端离线应失败")
	}
	if !strings.Contains(res.Error, "需要 macOS 客户端在线") {
		t.Errorf("应提示客户端在线要求: %s", res.Error)
	}
	// file_explore 同理
	res2 := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "file_explore", Description: "检索", Prompt: "p", ParentConvID: "main-1",
	})
	if res2.Success || !strings.Contains(res2.Error, "需要 macOS 客户端在线") {
		t.Errorf("file_explore 离线应失败: %+v", res2)
	}
	// 不创建子会话
	var count int64
	convRepo.DB().Model(&model.Conversation{}).Where("parent_id = ?", "main-1").Count(&count)
	if count != 0 {
		t.Errorf("离线失败不应创建子会话, got %d", count)
	}
}

// TestRunSubAgent_UserContext_NoClientRequired — user_context 纯服务端，无客户端工具也能跑
// （对比 web/file 的离线失败用例）。无 ClientTools、bridge 为 nil 均不阻塞。
func TestRunSubAgent_UserContext_NoClientRequired(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	orch := newSubAgentOrchestrator(&syncMockLLM{responses: []llm.Message{{Content: `<user_context_result>结论</user_context_result>`}}}, convRepo, msgRepo)
	orch.clientBridge = nil // user_context 不依赖客户端桥
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "user_context", Description: "查我上次说的计划", Prompt: "p", ParentConvID: "main-1",
	})
	if !res.Success {
		t.Fatalf("无客户端工具也应成功: %+v", res)
	}
	if res.State != "completed" || res.ConvID == "" {
		t.Errorf("state/convID 错误: %+v", res)
	}
	if !strings.Contains(res.Text, "user_context_result") {
		t.Errorf("Text 应为子 agent 最终回复: %s", res.Text)
	}
}

func TestRunSubAgent_FileExplore_ClientWithoutFs(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	orch := newSubAgentOrchestrator(&syncMockLLM{}, convRepo, msgRepo)
	// 客户端在线但只带浏览器工具（无 fs）
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "file_explore", Description: "检索", Prompt: "p", ParentConvID: "main-1", ClientTools: testClientToolOpts(),
	})
	if res.Success {
		t.Fatal("file_explore 无 fs 工具应失败")
	}
	if !strings.Contains(res.Error, "客户端未提供") {
		t.Errorf("应提示缺少本地工具: %s", res.Error)
	}
	// 不留下孤儿子会话（错误路径在创建子会话之前）
	var count int64
	convRepo.DB().Model(&model.Conversation{}).Where("parent_id = ?", "main-1").Count(&count)
	if count != 0 {
		t.Errorf("缺少工具失败不应创建子会话, got %d", count)
	}
}

// TestRunSubAgent_Resume — 续跑复用同一子会话：不新建、历史装载、预算重置
func TestRunSubAgent_Resume(t *testing.T) {
	convRepo, msgRepo, db, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	// 首跑：完成
	orch := newSubAgentOrchestrator(&syncMockLLM{responses: []llm.Message{{Content: "结果A"}}}, convRepo, msgRepo)
	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "web_explore", Description: "调研", Prompt: "首跑任务", ParentConvID: "main-1", ClientTools: testClientToolOpts(),
	})
	if !res.Success {
		t.Fatalf("首跑应成功: %+v", res)
	}

	// 续跑：带 task_id 复用同一子会话，历史装载进 LLM 上下文
	llm2 := &syncMockLLM{responses: []llm.Message{{Content: "结果B"}}}
	orch2 := newSubAgentOrchestrator(llm2, convRepo, msgRepo)
	res2 := orch2.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "web_explore", Description: "调研", Prompt: "继续调研", ParentConvID: "main-1",
		ClientTools: testClientToolOpts(), TaskID: res.ConvID,
	})
	if !res2.Success {
		t.Fatalf("续跑应成功: %+v", res2)
	}
	if res2.ConvID != res.ConvID {
		t.Errorf("续跑应复用同一子会话: %s != %s", res2.ConvID, res.ConvID)
	}

	// 只创建 1 个子会话
	var convCount int64
	db.Model(&model.Conversation{}).Where("parent_id = ?", "main-1").Count(&convCount)
	if convCount != 1 {
		t.Errorf("应只有 1 个子会话, got %d", convCount)
	}

	// 续跑请求 messages = 历史（首跑 user 消息）+ 新 user 消息
	if len(llm2.reqs) == 0 || len(llm2.reqs[0].Messages) < 3 {
		t.Fatalf("续跑请求应含历史+新消息: %+v", llm2.reqs)
	}
	req := llm2.reqs[0].Messages
	if req[0].Role != "user" || !strings.Contains(req[0].Content, "首跑任务") {
		t.Errorf("历史首条应为首跑 user 消息: %+v", req[0])
	}
	if req[len(req)-1].Role != "user" || !strings.Contains(req[len(req)-1].Content, "继续调研") {
		t.Errorf("末条应为新 user 消息: %+v", req[len(req)-1])
	}

	// 落库：2 轮 × (user+assistant) = 4 条
	var msgCount int64
	db.Model(&model.Message{}).Where("conversation_id = ?", res.ConvID).Count(&msgCount)
	if msgCount != 4 {
		t.Errorf("子会话应落库 4 条消息, got %d", msgCount)
	}
}

// TestRunSubAgent_ResumeInvalidTaskID_Errors — task_id 不可复用 → 报错，不静默新建
func TestRunSubAgent_ResumeInvalidTaskID_Errors(t *testing.T) {
	convRepo, msgRepo, db, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	if err := convRepo.Create(ctx, &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	extraJSON, _ := json.Marshal(&model.ConversationExtra{SubAgentType: "web_explore"})
	mainID := "main-1"
	// 跨用户
	if err := convRepo.Create(ctx, &model.Conversation{ID: "other-sub", UserID: "u2", ParentID: &mainID, Extra: string(extraJSON)}); err != nil {
		t.Fatal(err)
	}
	// 跨主会话（同用户挂别的父会话）
	otherMain := "other-main"
	if err := convRepo.Create(ctx, &model.Conversation{ID: "other-parent-sub", UserID: "u1", ParentID: &otherMain, Extra: string(extraJSON)}); err != nil {
		t.Fatal(err)
	}
	// 类型不符（user_context 会话按 web_explore 续跑）
	ucExtra, _ := json.Marshal(&model.ConversationExtra{SubAgentType: "user_context"})
	if err := convRepo.Create(ctx, &model.Conversation{ID: "uc-sub", UserID: "u1", ParentID: &mainID, Extra: string(ucExtra)}); err != nil {
		t.Fatal(err)
	}

	orch := newSubAgentOrchestrator(&syncMockLLM{responses: []llm.Message{{Content: "结果"}}}, convRepo, msgRepo)
	for _, tid := range []string{"nonexistent", "other-sub", "other-parent-sub", "uc-sub"} {
		res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
			SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", ClientTools: testClientToolOpts(), TaskID: tid,
		})
		if res.Success {
			t.Fatalf("task_id=%s 应报错而非静默新建: %+v", tid, res)
		}
		if !strings.Contains(res.Error, "不可复用") {
			t.Errorf("task_id=%s 错误应提示不可复用: %s", tid, res.Error)
		}
	}
	// main-1 下子会话 = other-sub + uc-sub = 2（无效 task_id 不新建）
	var count int64
	db.Model(&model.Conversation{}).Where("parent_id = ?", "main-1").Count(&count)
	if count != 2 {
		t.Errorf("无效 task_id 不应新建子会话, 应 2 个, got %d", count)
	}
}

// TestLoadSubAgentHistory_BudgetAndUserPrefix — 超预算丢最旧 + 保留下界为 user 消息
func TestLoadSubAgentHistory_BudgetAndUserPrefix(t *testing.T) {
	_, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	ctx := context.Background()
	convID := "sub-budget"
	// 60 条 × ~1k = 60k > 40k，强制触发截断
	for i := 0; i < 60; i++ {
		role, content := "assistant", strings.Repeat("x", 1000)
		if i%2 == 0 {
			role, content = "user", "任务"+strings.Repeat("y", 900)
		}
		if err := msgRepo.Create(ctx, &model.Message{ID: fmt.Sprintf("m%d", i), ConversationID: convID, Role: role, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	hist := loadSubAgentHistory(ctx, msgRepo, convID)
	if len(hist) == 0 {
		t.Fatal("应加载到历史")
	}
	if hist[0].Role != "user" {
		t.Errorf("历史首条应为 user（保留下界）: %s", hist[0].Role)
	}
	total := 0
	for _, m := range hist {
		total += utf8.RuneCountInString(m.Content)
	}
	if total > subagentHistoryBudgetChars {
		t.Errorf("历史总长应≤预算 %d, got %d", subagentHistoryBudgetChars, total)
	}
}

// ---- 成本中心接入测试 ----

type costRecorder struct {
	mu      sync.Mutex
	records []llmcost.UsageRecord
}

func (r *costRecorder) Record(rec llmcost.UsageRecord) {
	r.mu.Lock()
	r.records = append(r.records, rec)
	r.mu.Unlock()
}

func (r *costRecorder) snapshot() []llmcost.UsageRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]llmcost.UsageRecord(nil), r.records...)
}

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
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", ClientTools: testClientToolOpts(),
	})
	if !res.Success {
		t.Fatalf("RunSubAgent: %+v", res)
	}
	// CostTracker record 是异步的（go t.record），轮询等待
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	records := rec.snapshot()
	if len(records) != 1 {
		t.Fatalf("应记录 1 条成本, got %d", len(records))
	}
	r := records[0]
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

func TestSubAgentOpenedBrowserTabs(t *testing.T) {
	logs := []ToolCallLog{
		{Tool: "browser", Function: "agent_browser_open", Result: &tool.Result{Success: true, Data: json.RawMessage(`{"tabId":"browser-1"}`)}},
		{Tool: "browser", Function: "agent_browser_snapshot", Result: &tool.Result{Success: true}},
		{Tool: "unified_search", Function: "semantic_search", Result: &tool.Result{Success: true}},
	}
	if !subAgentOpenedBrowserTabs(logs) {
		t.Error("含 agent_browser_open 应返回 true")
	}
	// 失败/被拦截的 open 也应算（标签已入池，必须清理）
	failed := []ToolCallLog{{Tool: "browser", Function: "agent_browser_open", Result: &tool.Result{Success: false, Error: "打开失败"}}}
	if !subAgentOpenedBrowserTabs(failed) {
		t.Error("失败的 open 也应返回 true")
	}
	if subAgentOpenedBrowserTabs(nil) {
		t.Error("空日志应返回 false")
	}
	if subAgentOpenedBrowserTabs([]ToolCallLog{{Tool: "browser", Function: "agent_browser_snapshot"}}) {
		t.Error("未 open 应返回 false")
	}
}

// TestRunSubAgent_ClosesTabsOnEnd — 子任务结束后按 owner 批量发 agent_browser_close 清理
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

	// 模拟客户端：消费 client_tool_call，open 回传 tabId，agent_browser_close 回传成功
	type closeEvt struct{ owner string }
	closed := make(chan closeEvt, 4)
	go func() {
		for evt := range mainEvents {
			if evt.Type != "client_tool_call" {
				continue
			}
			var payload struct {
				CallID   string                 `json:"call_id"`
				ToolName string                 `json:"tool_name"`
				Args     map[string]interface{} `json:"arguments"`
				Owner    string                 `json:"owner"`
			}
			if json.Unmarshal([]byte(evt.Content), &payload) != nil {
				continue
			}
			if payload.ToolName == "browser__agent_browser_open" {
				res := ClientToolResult{Result: json.RawMessage(`{"tabId":"browser-1","title":"Example"}`)}
				_ = bridge.OnResult(payload.CallID, "u1", res)
			} else if payload.ToolName == "browser__agent_browser_close" {
				closed <- closeEvt{owner: payload.Owner}
				_ = bridge.OnResult(payload.CallID, "u1", ClientToolResult{Result: json.RawMessage(`{"closed":true,"count":1}`)})
			}
		}
	}()

	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", MainEvents: mainEvents,
		ClientTools: []tool.ClientToolInfo{{ServerName: "browser", Name: "agent_browser_open", IsReadOnly: true}},
	})
	if !res.Success {
		t.Fatalf("RunSubAgent: %+v", res)
	}
	select {
	case evt := <-closed:
		if !strings.HasPrefix(evt.owner, "subagent:") {
			t.Errorf("清理必须带子任务 owner（否则客户端归属保护会拒绝），got %q", evt.owner)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到 agent_browser_close 清理事件")
	}
	select {
	case evt := <-closed:
		t.Errorf("不应重复关闭: owner=%s", evt.owner)
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
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", ClientTools: testClientToolOpts(), MainEvents: mainEvents,
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

// ---- truncateToolResultJSON 保结构截断 ----

func TestTruncateToolResultJSON_KeepsValidJSON(t *testing.T) {
	content := strings.Repeat("文件内容", 5000) // 15000 字符
	data, _ := json.Marshal(&tool.Result{Success: true, Data: json.RawMessage(`{"content":"` + content + `"}`)})

	out := truncateToolResultJSON(data, 2000)
	if len(out) > 2200 {
		t.Fatalf("截断后仍过大: %d", len(out))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("截断后必须是合法 JSON: %v\n%s", err, out)
	}
	if m["success"] != true {
		t.Errorf("success 字段应保留: %+v", m)
	}
	if !strings.Contains(string(out), "已截断") {
		t.Errorf("应有截断标记: %s", out)
	}
	// 尾部保留：原文尾缀在输出中（截断的是 data 内部字符串）
	if !strings.Contains(string(out), "文件内容") {
		t.Errorf("应保留尾部内容")
	}
}

// 大事件（event.get 返回几十条记忆）光条目骨架就超过 2000，字符串截断救不回来。
// 必须丢数组尾部条目，而不是回退字节切片吐断裂 JSON——拼装循环的模型要靠这份 JSON 判断归属。
func TestTruncateToolResultJSON_DropsArrayItemsWhenSkeletonTooBig(t *testing.T) {
	memories := make([]map[string]interface{}, 0, 40)
	for i := 0; i < 40; i++ {
		memories = append(memories, map[string]interface{}{
			"id":         fmt.Sprintf("mem-%036d", i),
			"content":    strings.Repeat("科三考试细节，", 8),
			"created_at": "2026-11-03",
			"updated_at": "2026-11-03",
			"facts":      []string{"用户原话一", "用户原话二"},
		})
	}
	detail, _ := json.Marshal(map[string]interface{}{
		"id": "evt-1", "name": "驾照考取", "summary": "科三待补考",
		"memory_count": 40, "memories": memories, "is_deleted": false,
	})
	data, _ := json.Marshal(&tool.Result{Success: true, Data: detail})

	out := truncateToolResultJSON(data, 2000)
	if len(out) > 2000 {
		t.Fatalf("截断后仍超预算: %d", len(out))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("截断后必须是合法 JSON: %v\n%s", err, out)
	}
	if m["success"] != true {
		t.Errorf("success 字段应保留: %+v", m)
	}
	if _, ok := m[truncatedItemsKey]; !ok {
		t.Errorf("丢条目后应在根对象标注省略数: %v", m)
	}
	d, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data 结构应保留: %v", m)
	}
	if d["id"] != "evt-1" || d["name"] != "驾照考取" || d["summary"] != "科三待补考" {
		t.Errorf("事件元信息应保留: %v", d)
	}
	mems, ok := d["memories"].([]interface{})
	if !ok || len(mems) == 0 || len(mems) >= 40 {
		t.Fatalf("应保留部分记忆条目: %T len=%d", d["memories"], len(mems))
	}
	// 保留下来的每个条目结构完整（时间越新越靠前，丢的是尾部）
	for _, item := range mems {
		mem, ok := item.(map[string]interface{})
		if !ok || mem["id"] == "" || mem["content"] == "" {
			t.Fatalf("保留条目结构破损: %+v", item)
		}
	}
}

// 没有数组可丢、骨架本身超预算 → 最小标记 JSON，但仍保留原 success/error（不误读成失败）。
func TestTruncateToolResultJSON_MarkerKeepsSuccess(t *testing.T) {
	data := []byte(`{"success":true,"error":"boom","data":{"a":"1","b":"2","c":"3"}}`)
	out := truncateToolResultJSON(data, 20)
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("兜底必须是合法 JSON: %v\n%s", err, out)
	}
	if m["success"] != true || m["error"] != "boom" || m["truncated"] == nil {
		t.Fatalf("兜底标记应保留 success/error: %v", m)
	}
}

func TestTruncateToolResultJSON_NoTruncationBelowLimit(t *testing.T) {
	data, _ := json.Marshal(&tool.Result{Success: true, Data: json.RawMessage(`{"content":"短内容"}`)})
	out := truncateToolResultJSON(data, 2000)
	if string(out) != string(data) {
		t.Errorf("未超限不应截断: %s", out)
	}
}

func TestTruncateToolResultJSON_FallbackOnInvalidJSON(t *testing.T) {
	// 反序列化失败 → 回退字节切片（不 panic）
	invalid := append([]byte(`{"success":true,"data":"`), bytes.Repeat([]byte("a"), 5000)...)
	invalid = append(invalid, ')')
	out := truncateToolResultJSON(invalid, 100)
	if len(out) != 100 {
		t.Errorf("回退应字节切片: %d", len(out))
	}
}

func TestTruncateStringKeepHeadTail_NoUTF8Split(t *testing.T) {
	s := strings.Repeat("中文内容", 3000) // 12000 字符
	out := truncateStringKeepHeadTail(s, 2000)
	if !utf8.ValidString(out) {
		t.Fatal("截断后必须是合法 UTF-8")
	}
	if !strings.Contains(out, "已截断") || !strings.HasPrefix(out, "中文内容") || !strings.HasSuffix(out, "内容") {
		t.Errorf("头尾保留错误: %s...", out[:30])
	}
	// 短字符串不动
	if got := truncateStringKeepHeadTail("short", 100); got != "short" {
		t.Errorf("短字符串不应截断: %s", got)
	}
}

// TestSubAgentMsgSink_PersistsRequestJSONAndUsage — 子消息落库后 request_json 入异步 sink、
// token 用量进 Extra（msgDebug 各 tab 对齐主聊天）。
func TestSubAgentMsgSink_PersistsRequestJSONAndUsage(t *testing.T) {
	_, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	req := `{"model":"deepseek-v4-flash","messages":[{"role":"system","content":"sys"}]}`
	sink := &recordingDebugSink{}
	s := &subAgentMsgSink{msgRepo: msgRepo, convID: "sub-1", userID: "u1", debugPersistJSON: true, debugSink: sink}

	s.SaveAssistantMsg(llm.Message{
		Content:     "回复",
		Usage:       &llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		RequestJSON: &req,
	})

	if len(sink.submits) != 1 {
		t.Fatalf("request_json 应提交 1 次, got %d", len(sink.submits))
	}
	if *sink.submits[0].req != req {
		t.Errorf("提交的 request_json 不符: %s", *sink.submits[0].req)
	}

	m, err := msgRepo.GetByID(context.Background(), sink.submits[0].id)
	if err != nil || m == nil {
		t.Fatalf("消息应落库: %v", err)
	}
	if m.RequestJSON == nil || *m.RequestJSON != req {
		t.Errorf("消息 request_json 未落库: %v", m.RequestJSON)
	}
	if m.Extra == nil {
		t.Fatal("Extra 应写入 usage")
	}
	var extra MsgExtra
	if err := json.Unmarshal([]byte(*m.Extra), &extra); err != nil {
		t.Fatalf("Extra 解析失败: %v", err)
	}
	if extra.Usage == nil || extra.Usage.TotalTokens != 15 {
		t.Errorf("Extra.Usage 错误: %+v", extra.Usage)
	}
}

// TestSubAgentMsgSink_DebugPersistOff_NoSubmit — debug_persist_json 关闭时不提交 request_json
func TestSubAgentMsgSink_DebugPersistOff_NoSubmit(t *testing.T) {
	_, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	req := `{"messages":[]}`
	sink := &recordingDebugSink{}
	s := &subAgentMsgSink{msgRepo: msgRepo, convID: "sub-1", userID: "u1", debugPersistJSON: false, debugSink: sink}

	s.SaveAssistantMsg(llm.Message{Content: "回复", RequestJSON: &req})
	if len(sink.submits) != 0 {
		t.Fatalf("开关关闭不应提交, got %d", len(sink.submits))
	}
}
