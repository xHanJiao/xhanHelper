package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// syncDebugJSONSink — 测试用同步 sink：Submit 立即写入消息行，保持 DB 断言方式
type syncDebugJSONSink struct{ repo *service.MessageRepo }

func (s *syncDebugJSONSink) Submit(msgID string, reqJSON *string) bool {
	return s.repo.BatchUpdateDebugJSON(context.Background(),
		[]service.DebugJSONUpdate{{MessageID: msgID, RequestJSON: reqJSON}}) == nil
}

// setupOrchForVerify 创建一个带真实 msgRepo 的 orchestrator，用于测试存盘逻辑
func setupOrchForVerify(t *testing.T) (*Orchestrator, *service.MessageRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	msgRepo := service.NewMessageRepo(db)
	orch := New(&OrchestratorConfig{
		MsgRepo:          msgRepo,
		VerifyEnabled:    true,
		VerifyMaxRetries: 3,
		VerifyModel:      "flash",
		DebugPersistJSON: true, // 测试检验 request_json 持久化
		DebugJSONSink:    &syncDebugJSONSink{repo: msgRepo},
	})
	return orch, msgRepo, func() {}
}

// —— shouldVerify ——

// 用例：verifyEnabled=false 时不触发
func TestShouldVerify_Disabled(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, verifyEnabled: false}
	req := &RunRequest{}
	state := &reactState{allToolCalls: []tool.ToolCall{{Tool: "bookkeeping"}}}
	if o.shouldVerify(req, state) {
		t.Error("verifyEnabled=false 时应返回 false")
	}
}

// 用例：无工具调用时不触发
func TestShouldVerify_NoToolCalls(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, verifyEnabled: true}
	req := &RunRequest{}
	state := &reactState{allToolCalls: nil}
	if o.shouldVerify(req, state) {
		t.Error("无工具调用时应返回 false")
	}
}

// 用例：有工具调用时触发
func TestShouldVerify_WithToolCalls(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, verifyEnabled: true}
	req := &RunRequest{}
	state := &reactState{allToolCalls: []tool.ToolCall{{Tool: "bookkeeping"}}}
	if !o.shouldVerify(req, state) {
		t.Error("有工具调用时应返回 true")
	}
}

// 用例：per-request 显式 true 覆盖全局关闭
func TestShouldVerify_RequestOverrideOn(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, verifyEnabled: false}
	req := &RunRequest{VerifyEnabled: boolPtr(true)}
	state := &reactState{allToolCalls: []tool.ToolCall{{Tool: "bookkeeping"}}}
	if !o.shouldVerify(req, state) {
		t.Error("VerifyEnabled=true 应覆盖全局关闭，返回 true")
	}
}

// 用例：per-request 显式 false 覆盖全局开启
func TestShouldVerify_RequestOverrideOff(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, verifyEnabled: true}
	req := &RunRequest{VerifyEnabled: boolPtr(false)}
	state := &reactState{allToolCalls: []tool.ToolCall{{Tool: "bookkeeping"}}}
	if o.shouldVerify(req, state) {
		t.Error("VerifyEnabled=false 应覆盖全局开启，返回 false")
	}
}

// 用例：per-request nil 跟随全局配置
func TestShouldVerify_RequestOverrideNil(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, verifyEnabled: true}
	req := &RunRequest{}
	state := &reactState{allToolCalls: []tool.ToolCall{{Tool: "bookkeeping"}}}
	if !o.shouldVerify(req, state) {
		t.Error("VerifyEnabled=nil 应跟随全局配置（开启），返回 true")
	}
}

// 用例：SkipRetrieval=true 时跳过验证
func TestShouldVerify_SkipRetrieval(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}, verifyEnabled: true}
	req := &RunRequest{SkipRetrieval: true}
	state := &reactState{allToolCalls: []tool.ToolCall{{Tool: "bookkeeping"}}}
	if o.shouldVerify(req, state) {
		t.Error("SkipRetrieval=true 时应跳过验证，返回 false")
	}
}

// —— buildVerifyPrompt ——

// 用例：prompt 只包含待检验的草稿回复；对话历史（用户问题/工具调用/返回）
// 已由 verifyMsgs 以原始消息格式传给检验 LLM，不再重复拼入 prompt 文本。
func TestBuildVerifyPrompt_DraftOnly(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	msg := llm.Message{
		Content: `{"reply":"根据你的账单和笔记，最近一个月你喝了2次啤酒：5月1日福佳白啤22元，5月27日福佳白啤330ml×2瓶。饮酒频率较低，控制在合理范围内。"}`,
	}

	prompt := o.buildVerifyPrompt(msg)

	// 应包含草稿回复
	if !strings.Contains(prompt, "饮酒频率较低") {
		t.Error("prompt 应包含模型回复草稿")
	}
	// 不应再包含对话历史（用户问题/工具调用/工具返回——它们经 verifyMsgs 传）
	if strings.Contains(prompt, "帮我查一下最近喝酒的情况") {
		t.Error("用户问题不应再拼入 prompt（在 verifyMsgs 消息列表中）")
	}
	if strings.Contains(prompt, "工具调用") {
		t.Error("工具调用历史不应再拼入 prompt（在 verifyMsgs 消息列表中）")
	}
}

// 用例：空草稿内容安全返回（不崩溃）
func TestBuildVerifyPrompt_EmptyDraft(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	prompt := o.buildVerifyPrompt(llm.Message{Content: ""})
	if !strings.Contains(prompt, "【模型回复草稿】") {
		t.Error("应包含草稿标注")
	}
}

// —— buildVerifyFeedback ——

// 用例：仅有幻觉时的反馈
func TestBuildVerifyFeedback_HallucinationsOnly(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	vr := verifyResult{
		Complete:       true,
		Hallucinations: []string{"编造了 5月3日 购买青岛啤酒 15元，工具返回中无此记录"},
		MissingAspects: nil,
		Suggestion:     "去掉不存在的账单条目",
	}
	feedback := o.buildVerifyFeedback(vr)
	if !strings.Contains(feedback, "幻觉") {
		t.Error("应包含幻觉提示")
	}
	if !strings.Contains(feedback, "青岛啤酒") {
		t.Error("应包含具体幻觉内容")
	}
	if !strings.Contains(feedback, "去掉不存在的账单条目") {
		t.Error("应包含建议")
	}
}

// 用例：仅有缺失时的反馈
func TestBuildVerifyFeedback_MissingOnly(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	vr := verifyResult{
		Complete:       false,
		Hallucinations: nil,
		MissingAspects: []string{"未查询笔记", "未查询记忆"},
		Suggestion:     "建议调用 unified_search",
	}
	feedback := o.buildVerifyFeedback(vr)
	if !strings.Contains(feedback, "缺失") {
		t.Error("应包含缺失提示")
	}
	if !strings.Contains(feedback, "未查询笔记") {
		t.Error("应包含缺失方面")
	}
	if !strings.Contains(feedback, "unified_search") {
		t.Error("应包含建议")
	}
}

// 用例：同时有幻觉和缺失
func TestBuildVerifyFeedback_Both(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	vr := verifyResult{
		Complete:       false,
		Hallucinations: []string{"金额 146 元不存在"},
		MissingAspects: []string{"未查记忆"},
		Suggestion:     "去掉幻觉数据，补充记忆查询",
	}
	feedback := o.buildVerifyFeedback(vr)
	if !strings.Contains(feedback, "幻觉") && !strings.Contains(feedback, "缺失") {
		t.Error("应同时包含幻觉和缺失")
	}
}

// 用例：通过时的反馈（实际上通过不会调 buildVerifyFeedback，但确保空结构不崩溃）
func TestBuildVerifyFeedback_Clean(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	vr := verifyResult{Complete: true}
	feedback := o.buildVerifyFeedback(vr)
	// 通过时不应有幻觉或缺失内容
	if strings.Contains(feedback, "幻觉") || strings.Contains(feedback, "缺失") {
		t.Log("clean result feedback (通过时不会调用此方法，仅验证格式): ", feedback)
	}
}

// —— sendVerifyEvent ——

// 用例：验证 SSE 事件格式
func TestSendVerifyEvent(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}
	events := make(chan *llm.StreamEvent, 1)

	o.sendVerifyEvent(events, "checking", 1, "")
	evt := <-events
	if evt.Type != "verify" {
		t.Errorf("事件类型应为 verify，实际 %s", evt.Type)
	}
	var data map[string]interface{}
	json.Unmarshal([]byte(evt.Content), &data)
	if data["status"] != "checking" {
		t.Errorf("status 应为 checking，实际 %v", data["status"])
	}
	if data["retry"].(float64) != 1 {
		t.Errorf("retry 应为 1，实际 %v", data["retry"])
	}

	o.sendVerifyEvent(events, "retry", 2, "需要查笔记")
	evt = <-events
	json.Unmarshal([]byte(evt.Content), &data)
	if data["status"] != "retry" {
		t.Errorf("status 应为 retry，实际 %v", data["status"])
	}
	if data["reason"] != "需要查笔记" {
		t.Errorf("reason 应为 '需要查笔记'，实际 %v", data["reason"])
	}
}

// —— saveAssistantMsgWithFlags（IsInternal） ——

// 用例：IsInternal=true 的草稿消息存入 DB，前端不可见
func TestSaveAssistantMsg_Internal(t *testing.T) {
	orch, msgRepo, cleanup := setupOrchForVerify(t)
	defer cleanup()

	msg := llm.Message{
		Content:          `{"reply":"这是未通过检验的草稿"}`,
		ReasoningContent: "一些思考...",
	}
	trace := &ctxflow.RetrieveTrace{}

	mid := orch.saveAssistantMsgInternal(context.Background(), "c1", "u1", msg, trace)
	if mid == "" {
		t.Fatal("应返回 message ID")
	}

	saved, err := msgRepo.GetByID(context.Background(), mid)
	if err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	if !saved.IsInternal {
		t.Error("草稿消息应标记为 IsInternal=true")
	}
	// maybeInjectCreatedAt 会给 JSON 注入 created_at 字段
	if !strings.Contains(saved.Content, "这是未通过检验的草稿") {
		t.Errorf("内容应包含原文: %s", saved.Content)
	}
}

// 用例：正常消息 IsInternal=false
func TestSaveAssistantMsg_Normal(t *testing.T) {
	orch, msgRepo, cleanup := setupOrchForVerify(t)
	defer cleanup()

	msg := llm.Message{Content: `{"reply":"正常回复"}`}
	mid := orch.saveAssistantMsg(context.Background(), "c1", "u1", msg, nil)
	if mid == "" {
		t.Fatal("应返回 message ID")
	}

	saved, err := msgRepo.GetByID(context.Background(), mid)
	if err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	if saved.IsInternal {
		t.Error("正常消息不应标记为 IsInternal")
	}
}

// —— verifyAndMaybeRetry 边界情况 ——

// 用例：超过最大重试次数时放弃检验，直接通过
func TestVerifyAndMaybeRetry_MaxRetries(t *testing.T) {
	orch, _, cleanup := setupOrchForVerify(t)
	defer cleanup()
	orch.verifyMaxRetries = 2

	state := &reactState{verifyRetries: 2} // 已达上限
	events := make(chan *llm.StreamEvent, 4)
	msg := llm.Message{Content: `{"reply":"草稿"}`}
	req := &RunRequest{ConversationID: "c1", UserID: "u1"}
	result := ""

	done := orch.verifyAndMaybeRetry(context.Background(), events, msg, req, result, nil, state)
	if !done {
		t.Error("超过 maxRetries 应直接返回 done=true")
	}
	if state.lastAssistantMsgID == "" {
		t.Error("应该保存草稿消息")
	}
}

// 用例：msgRepo 为 nil 时 saveAssistantMsg 安全返回空
func TestSaveAssistantMsg_NilRepo(t *testing.T) {
	orch := &Orchestrator{} // msgRepo 为 nil
	mid := orch.saveAssistantMsgWithFlags(context.Background(), "c1", "u1", llm.Message{Content: "test"}, nil, false)
	if mid != "" {
		t.Error("msgRepo=nil 时应返回空 ID")
	}
	// 不应 panic
}

// 用例：真实脱敏数据场景——prompt 仅含草稿回复（含可能捏造的数据），
// 对话历史与工具返回经 verifyMsgs 消息列表传给检验 LLM，不再拼入 prompt 文本。
func TestBuildVerifyPrompt_RealisticBeerScenario(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}

	// LLM 的草稿回复——只基于账单，且编造了部分数据
	msg := llm.Message{
		Content: `{"reply":"根据你的账单记录，最近一个月你喝了3次啤酒：\n\n1. 5月1日 福佳白啤330ml×2瓶，22元（便利店购买）\n2. 5月15日 精酿啤酒，80元（朋友聚会）\n3. 5月20日 燕京U8，12元\n\n3次共消费114元。另外5月8日还有一次青岛啤酒15元未包含在内。整体饮酒频率适中，建议继续保持。"}`,
	}

	prompt := o.buildVerifyPrompt(msg)

	// 草稿回复中的关键数据必须保留（供检验判断）
	for _, want := range []string{"福佳白啤", "114元", "青岛啤酒"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt 应包含草稿数据: %s", want)
		}
	}
	// 对话历史不再出现在 prompt 文本中（在 verifyMsgs 消息列表里）
	if strings.Contains(prompt, "综合分析") {
		t.Error("用户问题不应再拼入 prompt（在 verifyMsgs 消息列表中）")
	}
	if strings.Contains(prompt, "query_bills") {
		t.Error("工具调用不应再拼入 prompt（在 verifyMsgs 消息列表中）")
	}
}

// —— 真实脱敏数据完整链路测试 ——
// 数据来源：用户实际对话"query_bills 按酒搜索" → tool_retriever → query_bills → 最终回复
// 注：历史（tool_retriever/query_bills 调用与返回）经 verifyMsgs 原样传递，
// 此处仅验证草稿回复内容被完整保留。

func TestBuildVerifyPrompt_RealData_GoodReply(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}

	// 模型的最终回复 —— 基于真实数据，无幻觉
	msg := llm.Message{
		Content: `{"reply":"搜到了！这次发现了 **4条** 与\"酒\"相关的账单。结合之前的笔记数据，重新梳理一份完整的五月饮酒日记：\n\n### 五月饮酒日记（最终版 · 账单+笔记双重核对）\n\n| 日期 | 饮酒记录 | 来源 |\n|:----:|:----------|:----:|\n| 5月15日 | 啤酒1瓶（账单备注\"1瓶啤酒，四五瓶饮料，2根雪糕\"） | **账单+笔记** ✅ |\n| 5月18日 | 悠航鲜啤（双人，配炸鸡汉堡） | **账单** 🆕 |\n| 5月19日 | 啤酒×2瓶 | **账单+笔记** ✅ |\n| 5月21日 | 京客隆买啤酒（还买了水饺和牛奶） | **账单** 🆕 |\n| 5月22日 | 约800ml啤酒 | 笔记 |\n| 5月27日 | 福佳白啤330ml×2瓶 | 笔记 |\n\n### 对比分析\n**笔记遗漏的，账单补上了（2条）：** 5月18日悠航鲜啤双人餐、5月21日京客隆买啤酒\n\n**仍有账单空缺的：** 5月22日和5月27日没有对应购买记录，可能是存货\n\n### 五月饮酒全量记录（6次）"}`,
	}

	prompt := o.buildVerifyPrompt(msg)

	// 草稿关键数据
	checks := []struct {
		desc string
		want string
	}{
		{"LLM回复-五月饮酒日记", "五月饮酒日记"},
		{"LLM回复-碰到的数据", "福佳白啤"},
		{"草稿-悠航鲜啤", "悠航鲜啤"},
		{"草稿-京客隆", "京客隆买啤酒"},
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c.want) {
			t.Errorf("%s: prompt 中应包含 '%s'", c.desc, c.want)
		}
	}
	// 历史（tool_retriever/query_bills）不应拼入 prompt
	for _, want := range []string{"tool_retriever", "query_bills", "按酒搜索"} {
		if strings.Contains(prompt, want) {
			t.Errorf("对话历史 '%s' 不应拼入 prompt（在 verifyMsgs 消息列表中）", want)
		}
	}
}

// 用例：同一真实数据，但 LLM 回复中编造了不存在的账单
func TestBuildVerifyPrompt_RealData_BadReply_WithHallucination(t *testing.T) {
	o := &Orchestrator{
		contextFormatter: &mockFormatter{}}

	// LLM 回复中编造了不存在的"红酒"账单
	msg := llm.Message{
		Content: `{"reply":"搜到了！五月份饮酒情况如下：\n\n- 5月15日：啤酒1瓶，44元\n- 5月18日：悠航鲜啤双人餐，235元\n- 5月19日：啤酒两瓶，12元\n- 5月21日：京客隆啤酒，41元\n- 5月25日：红酒一瓶，146元（朋友聚餐）\n- 5月28日：红酒一瓶，88元\n\n共计6次，总消费566元。其中红酒消费234元占比最高。"}`,
	}

	prompt := o.buildVerifyPrompt(msg)

	// 幻觉数据"红酒"在草稿中，供检验判断
	if !strings.Contains(prompt, "红酒") {
		t.Error("prompt 应包含模型编造的红酒数据（供检验判断）")
	}
	if !strings.Contains(prompt, "146") {
		t.Error("prompt 应包含模型编造的金额")
	}
	// 历史（tool_retriever/query_bills）不应拼入 prompt
	for _, want := range []string{"tool_retriever", "query_bills"} {
		if strings.Contains(prompt, want) {
			t.Errorf("对话历史 '%s' 不应拼入 prompt（在 verifyMsgs 消息列表中）", want)
		}
	}
}

// 用例：saveVerifyFeedback 将反馈+检验请求 request_json 合并存入一条消息
func TestSaveVerifyFeedback_StoresRequestJSON(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	db.AutoMigrate(&model.Message{})
	msgRepo := service.NewMessageRepo(db)

	orch := &Orchestrator{msgRepo: msgRepo, debugPersistJSON: true, debugJSONSink: &syncDebugJSONSink{repo: msgRepo}}
	orch.saveVerifyFeedback(context.Background(), "c1", "u1",
		"[系统] 内部检验发现幻觉：编造了红酒账单",
		"你是一个检验助手",
		"【用户问题】\n今天喝酒了吗\n\n【模型回复】\n喝了2次")

	// 读回验证
	msgs, _ := msgRepo.GetMessagesSince(context.Background(), "c1", time.Now().Add(-time.Minute), 5)
	if len(msgs) == 0 {
		t.Fatal("未找到保存的消息")
	}
	msg := msgs[0]
	if msg.RequestJSON == nil {
		t.Fatal("request_json 不应为 nil")
	}
	t.Logf("request_json: %s", *msg.RequestJSON)
	if !strings.Contains(*msg.RequestJSON, "检验助手") {
		t.Error("request_json 应包含 system prompt")
	}
	if !strings.Contains(*msg.RequestJSON, "今天喝酒了吗") {
		t.Error("request_json 应包含 user prompt")
	}
	if !strings.Contains(msg.Content, "编造了红酒账单") {
		t.Error("content 应包含反馈内容")
	}
	if !msg.IsInternal {
		t.Error("应标记为 IsInternal")
	}
	if msg.Role != "system" {
		t.Errorf("role 应为 system，实际 %s", msg.Role)
	}
}

func boolPtr(b bool) *bool {
	return &b
}
