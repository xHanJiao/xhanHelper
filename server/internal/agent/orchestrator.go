package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
	"xhan-helper/internal/util"
)

const BaseSystemPrompt = `你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

你也是用户的一面数字镜子——通过记忆和笔记了解用户的偏好、价值观、过往决定，当用户当下的选择与过去的自己背离时，温和地提醒。不是你在教育用户，而是过去的用户在提醒现在的用户，帮助做到知行合一。

`

const defaultMaxIterations = 8

// RunRequest — Orchestrator 输入
type RunRequest struct {
	UserID             string
	ConversationID     string
	Messages           []llm.Message
	DeepMode           bool
	UserMessageContent string   // 管线处理后的内容，供内部使用（嵌入、检索等）
	RawUserMessage     string   // 用户原始输入，展示用
	AgentSystemPrompt  string   // 为空则用默认
	AgentID            string   // 关联的智能体 ID
	SkipRetrieval      bool     // 跳过记忆检索（短输入等场景）
	Temperature        *float64 // LLM 采样温度，nil 用配置默认
	MaxTokens          *int     // LLM 最大输出 token，nil 用配置默认
	MaxIterations      int      // ReAct 最大轮次，0 用配置默认
	Thinking           *bool    // 思考模式：nil=不传，true=开启，false=关闭

	OverrideSystemPrompt *string

	// 检索参数覆盖（0 值表示使用 PromptConfig 默认值）
	MemoryThreshold  float64
	MemoryLimit      int
	NoteThreshold    float64
	NoteLimit        int
	HistoryThreshold float64
	HistoryLimit     int
}

// reactState — runReActLoop 循环过程中的可变状态
type reactState struct {
	msgs               []llm.Message
	allToolCalls       []tool.ToolCall
	lastAssistantMsgID string
	userMsgID          string
	verifyRetries      int // 本轮对话已重检验次数
}

// LLMResponse — LLM 返回的结构化 JSON（文本解析用）
type LLMResponse struct {
	Reply     string          `json:"reply"`
	ToolCalls []tool.ToolCall `json:"tool_calls"`
}

// msgCreator — Orchestrator 需要的消息写入能力（*service.MessageRepo 自动满足）
type msgCreator interface {
	Create(ctx context.Context, m *model.Message) error
}

// convToucher — Orchestrator 需要的会话更新时间能力（*service.ConversationRepo 自动满足）
type convToucher interface {
	Touch(ctx context.Context, id string) error
}

// promptBuilder — Orchestrator 需要的 prompt 构建能力（*promptbuilder.SystemPromptBuilder 自动满足）
type promptBuilder interface {
	Build(ctx context.Context, opts promptbuilder.BuildOptions) (*promptbuilder.BuildResult, error)
}

// Orchestrator — ReAct 循环编排器
type Orchestrator struct {
	flashClient         llm.LLMClient
	proClient           llm.LLMClient
	registry            *tool.Registry
	systemPromptBuilder promptBuilder
	msgRepo             msgCreator
	convRepo            convToucher
	embedder            *llm.Embedder
	vectorStore         *VectorStore
	embeddingRepo       *service.EmbeddingRepo
	maxIterations       int
	defaultTemperature  *float64
	defaultMaxTokens    *int
	maxToolResultChars  int
	onChatEnd           func(userID, convID string)
	verifyEnabled       bool
	verifyMaxRetries    int
	verifyModel         string // "flash" | "pro"
}

// OrchestratorConfig — Orchestrator 依赖配置
type OrchestratorConfig struct {
	FlashClient        llm.LLMClient
	Registry           *tool.Registry
	ProClient          llm.LLMClient
	PromptBuilder      promptBuilder
	MsgRepo            *service.MessageRepo
	ConvRepo           *service.ConversationRepo
	Embedder           *llm.Embedder
	VectorStore        *VectorStore
	EmbeddingRepo      *service.EmbeddingRepo
	MaxIterations      int
	Temperature        *float64
	MaxTokens          *int
	MaxToolResultChars int
	OnChatEnd          func(userID, convID string)
	VerifyEnabled      bool
	VerifyMaxRetries   int
	VerifyModel        string // "flash" | "pro"，默认 flash
}

// New 创建 Orchestrator
func New(cfg *OrchestratorConfig) *Orchestrator {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = defaultMaxIterations
	}
	if cfg.VerifyMaxRetries <= 0 {
		cfg.VerifyMaxRetries = 1
	}
	if cfg.VerifyModel == "" {
		cfg.VerifyModel = "flash"
	}

	return &Orchestrator{
		flashClient:         cfg.FlashClient,
		proClient:           cfg.ProClient,
		registry:            cfg.Registry,
		systemPromptBuilder: cfg.PromptBuilder,
		msgRepo:             cfg.MsgRepo,
		convRepo:            cfg.ConvRepo,
		embedder:            cfg.Embedder,
		vectorStore:         cfg.VectorStore,
		embeddingRepo:       cfg.EmbeddingRepo,
		maxIterations:       cfg.MaxIterations,
		defaultTemperature:  cfg.Temperature,
		defaultMaxTokens:    cfg.MaxTokens,
		maxToolResultChars:  cfg.MaxToolResultChars,
		onChatEnd:           cfg.OnChatEnd,
		verifyEnabled:       cfg.VerifyEnabled,
		verifyMaxRetries:    cfg.VerifyMaxRetries,
		verifyModel:         cfg.VerifyModel,
	}
}

// FlashClient 返回 Flash 模型客户端（供预处理管线使用）
func (o *Orchestrator) FlashClient() llm.LLMClient {
	return o.flashClient
}

// —— Run ——

func (o *Orchestrator) Run(ctx context.Context, req *RunRequest) (<-chan *llm.StreamEvent, error) {
	if req.ConversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}

	var queryVec []float64
	var systemPromptResult *promptbuilder.BuildResult
	if o.embedder != nil && !req.SkipRetrieval {
		queryVec = o.tryEmbedContent(ctx, req.UserMessageContent)
	}

	log.Printf("[orch] run start: model=%s, msgs=%d", o.pickClient(req).ModelName(), len(req.Messages))

	if req.OverrideSystemPrompt != nil {
		systemPromptResult = &promptbuilder.BuildResult{Prompt: *req.OverrideSystemPrompt}
	} else {
		var err error
		systemPromptResult, err = o.systemPromptBuilder.Build(ctx, mkPromptBuildOpt(req, queryVec))
		if err != nil {
			log.Printf("[orch] prompt build error: %v", err)
			return nil, err
		}
	}

	events := make(chan *llm.StreamEvent, 512)
	go o.runReActLoop(ctx, events, systemPromptResult, queryVec, req)
	return events, nil
}

func mkPromptBuildOpt(req *RunRequest, queryVec []float64) promptbuilder.BuildOptions {
	// 从已有消息中采集 DB ID，供检索时去重
	excludeMsgIDs := make(map[string]bool)
	for _, m := range req.Messages {
		if m.ID != "" {
			excludeMsgIDs[m.ID] = true
		}
	}

	return promptbuilder.BuildOptions{
		UserID:           req.UserID,
		ConversationID:   req.ConversationID,
		UserMessage:      req.UserMessageContent,
		Embedding:        queryVec,
		AgentPrompt:      req.AgentSystemPrompt,
		MemoryThreshold:  req.MemoryThreshold,
		MemoryLimit:      req.MemoryLimit,
		NoteThreshold:    req.NoteThreshold,
		NoteLimit:        req.NoteLimit,
		HistoryThreshold: req.HistoryThreshold,
		HistoryLimit:     req.HistoryLimit,
		ExcludeMsgIDs:    excludeMsgIDs,
	}
}

func (o *Orchestrator) tryEmbedContent(ctx context.Context, userMsgContent string) []float64 {
	embStart := time.Now()
	vec, err := o.embedder.Embed(ctx, userMsgContent)
	monitor.RecordEmbedding(time.Since(embStart).Milliseconds())
	if err != nil {
		log.Printf("[vector] embed user msg error: %v", err)
	} else {
		return vec
	}
	return nil
}

func (o *Orchestrator) pickClient(req *RunRequest) llm.LLMClient {
	if req.DeepMode && o.proClient != nil {
		return o.proClient
	}
	return o.flashClient
}

// —— ReAct 主循环 ——

func (o *Orchestrator) runReActLoop(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	systemPromptResult *promptbuilder.BuildResult,
	queryVec []float64,
	req *RunRequest,
) {
	defer close(events)
	defer util.SafeGo(func() {
		if o.onChatEnd != nil {
			o.onChatEnd(req.UserID, req.ConversationID)
		}
	})

	userMsg := mkUserMsg(req)
	if err := o.msgRepo.Create(ctx, userMsg); err != nil {
		log.Printf("[orch] save user msg error: %v", err)
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrInternal)})
		return
	}
	util.SafeGo(func() { o.convRepo.Touch(ctx, req.ConversationID) })
	util.SafeGo(func() { o.saveUserMsgEmbedding(userMsg.ID, queryVec, req.UserID, req.ConversationID) })

	state := &reactState{msgs: append(req.Messages, llm.Message{Role: "user", Content: req.UserMessageContent}), userMsgID: userMsg.ID}

	maxIter := o.maxIterations
	if req.MaxIterations > 0 {
		maxIter = req.MaxIterations
	}
	for iter := 0; iter < maxIter; iter++ {
		if o.runOneRound(ctx, events, o.pickClient(req), req, systemPromptResult, state, iter) {
			return
		}
	}

	o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrMaxIterations)})
	o.emitDone(events, req.ConversationID, "", userMsg.ID, "max iterations reached", systemPromptResult.IDsByType)
}

// runOneRound 执行一轮 ReAct：LLM 调用 → 流式收集 → done/error 处理，返回 true 表示对话结束。
func (o *Orchestrator) runOneRound(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	client llm.LLMClient,
	req *RunRequest,
	result *promptbuilder.BuildResult,
	state *reactState,
	round int,
) bool {
	if ctx.Err() != nil {
		log.Printf("[orch] context cancelled: %v", ctx.Err())
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrContextCancelled)})
		o.sendEvent(events, &llm.StreamEvent{Type: "done"})
		return true
	}

	llmStart := time.Now()
	streamEvents, err := client.Chat(ctx, o.mkLlmChatReq(req, result, state.msgs))
	if err != nil {
		log.Printf("[orch] round %d LLM error: %v", round+1, err)
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrLLMApi)})
		return true
	}

	var streamContent, streamReasoning strings.Builder
	for se := range streamEvents {
		switch se.Type {
		case "text_delta":
			streamContent.WriteString(se.Content)
		case "thought_delta":
			streamReasoning.WriteString(se.Content)
			o.sendEvent(events, se)
		case "done":
			elapsedMs := time.Since(llmStart).Milliseconds()
			recordLLMCall(req.DeepMode && o.proClient != nil, elapsedMs, se.Usage)
			log.Printf("[orch] round %d response: content=%d, reasoning=%d, tool_calls=%d",
				round+1, streamContent.Len(), streamReasoning.Len(), len(se.ToolCalls))
			return o.finalizeRound(ctx, events, se, streamContent.String(), streamReasoning.String(), req, result, state)
		case "error":
			o.sendEvent(events, se)
			return true
		}
	}
	return false
}

func (o *Orchestrator) mkLlmChatReq(req *RunRequest, systemPromptResult *promptbuilder.BuildResult, msgs []llm.Message) *llm.ChatRequest {
	isProMode := req.DeepMode && o.proClient != nil
	temperature := req.Temperature
	if temperature == nil {
		temperature = o.defaultTemperature
	}
	maxTokens := req.MaxTokens
	if maxTokens == nil {
		maxTokens = o.defaultMaxTokens
	}
	thinking := req.Thinking
	if thinking == nil && isProMode {
		t := true
		thinking = &t
	}
	return &llm.ChatRequest{
		SystemPrompt: systemPromptResult.Prompt,
		Messages:     msgs,
		Tools:        nil,
		Temperature:  temperature,
		MaxTokens:    maxTokens,
		UserID:       req.UserID,
		Thinking:     thinking,
	}
}

func mkUserMsg(req *RunRequest) *model.Message {
	m := &model.Message{
		ID:             uuid.New().String(),
		ConversationID: req.ConversationID,
		UserID:         req.UserID,
		Role:           "user",
		Content:        req.UserMessageContent,
	}
	if req.RawUserMessage != "" && req.RawUserMessage != req.UserMessageContent {
		raw := req.RawUserMessage
		m.OriginalContent = &raw
	}
	return m
}

// sendEvent — 非阻塞发送事件，避免客户端断连后 goroutine 卡住
func (o *Orchestrator) sendEvent(events chan<- *llm.StreamEvent, evt *llm.StreamEvent) {
	select {
	case events <- evt:
	default:
	}
}

// recordLLMCall 记录 LLM 调用指标。
func recordLLMCall(isProMode bool, elapsedMs int64, usage *llm.Usage) {
	caller := "orchestrator"
	if isProMode {
		caller = "orchestrator-pro"
	}
	pt, ct := 0, 0
	if usage != nil {
		pt = usage.PromptTokens
		ct = usage.CompletionTokens
	}
	monitor.RecordLLMCall(caller, elapsedMs, pt, ct)
}

// finalizeRound 处理一轮 ReAct 的 done 事件，返回 true 表示本轮对话结束。
func (o *Orchestrator) finalizeRound(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	se *llm.StreamEvent,
	streamContent, streamReasoning string,
	req *RunRequest,
	result *promptbuilder.BuildResult,
	state *reactState,
) bool {
	if streamReasoning != "" {
		o.sendEvent(events, &llm.StreamEvent{Type: "thought_done", Content: ""})
	}

	msg := newAssistantMessage(streamContent, streamReasoning, se)

	done := o.handleOneRoundResponse(ctx, events, msg, req, result, state)
	if done {
		cleanText := extractCleanText(streamContent)
		if cleanText != "" {
			o.streamTextChunks(ctx, events, cleanText)
		}
		o.emitDone(events, req.ConversationID, state.lastAssistantMsgID, state.userMsgID, "", result.IDsByType)
	}
	return done
}

func newAssistantMessage(streamContent string, streamReasoning string, se *llm.StreamEvent) llm.Message {
	return llm.Message{
		Role:             "assistant",
		Content:          streamContent,
		ReasoningContent: streamReasoning,
		ToolCalls:        se.ToolCalls,
		RequestJSON:      se.RequestJSON,
		ResponseJSON:     se.ResponseJSON,
	}
}

// —— 单轮响应处理 ——

// handleOneRoundResponse 解析 LLM 响应文本中的 JSON，更新 state 中的消息和工具调用状态。
// 返回 true 表示本轮对话结束。
func (o *Orchestrator) handleOneRoundResponse(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msg llm.Message,
	req *RunRequest,
	result *promptbuilder.BuildResult,
	state *reactState,
) bool {
	parsed, parseErr := ParseLLMResponse(msg.Content)

	if parseErr != nil {
		log.Printf("[orch] round parse error: %v, saving raw and retrying", parseErr)
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, nil)
		o.sendEvent(events, &llm.StreamEvent{Type: "status", Content: "响应格式异常，正在重试…"})
		state.msgs = append(state.msgs, llm.Message{
			Role:    "user",
			Content: "[系统] 你上一轮的回复不是有效的 JSON 格式。请严格按照 {\"reply\":\"文字回复\",\"tool_calls\":[{\"tool\":\"工具名\",\"function\":\"函数名\",\"args\":{...}}]} 格式重新生成。",
		})
		return false
	}

	if parsed == nil {
		// 纯文本（无 JSON），system prompt 已要求 JSON 但容错接受
		log.Printf("[orch] round pure text (no JSON), accepting gracefully")
		if o.shouldVerify(req, state) {
			return o.verifyAndMaybeRetry(ctx, events, msg, req, result, state)
		}
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, result.Trace)
		return true
	}

	log.Printf("[orch] round embedded tool_calls=%d: %s",
		len(parsed.ToolCalls), tool.ToolCallNames(parsed.ToolCalls))

	// 无工具调用 → 纯文本回复
	if len(parsed.ToolCalls) == 0 {
		log.Printf("[orch] round text-only JSON reply, len=%d", len(msg.Content))
		if o.shouldVerify(req, state) {
			return o.verifyAndMaybeRetry(ctx, events, msg, req, result, state)
		}
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, result.Trace)
		return true
	}

	// 统一走 prompt engineering，不填 ToolCallsJSON（由 ParseLLMResponse 从文本解析）
	msg.ToolCalls = nil

	state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, result.Trace)

	state.msgs = append(state.msgs, llm.Message{Role: "assistant", Content: msg.Content})
	state.allToolCalls = append(state.allToolCalls, parsed.ToolCalls...)

	// 为 memory_search 工具注入 exclude_ids（本轮已注入的记忆 ID）
	o.injectExcludeMemIDsForMemSearch(parsed, result.IDsByType["memory"])

	toolCallResults := executeConcurrently(ctx, events, req.UserID, o.registry, parsed.ToolCalls)

	state.msgs = o.collectToolResults(ctx, parsed, toolCallResults, events, req.ConversationID, req.UserID, state.msgs)

	return false
}

// verifyResult — Flash 检验返回的结构
type verifyResult struct {
	Complete       bool     `json:"complete"`
	Hallucinations []string `json:"hallucinations"`
	MissingAspects []string `json:"missing_aspects"`
	Suggestion     string   `json:"suggestion"`
}

func (o *Orchestrator) shouldVerify(req *RunRequest, state *reactState) bool {
	return o.verifyEnabled && len(state.allToolCalls) > 0 && !req.SkipRetrieval
}

func (o *Orchestrator) verifyAndMaybeRetry(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msg llm.Message,
	req *RunRequest,
	result *promptbuilder.BuildResult,
	state *reactState,
) bool {
	if state.verifyRetries >= o.verifyMaxRetries {
		log.Printf("[verify] max retries reached (%d/%d), accepting draft", state.verifyRetries, o.verifyMaxRetries)
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, result.Trace)
		return true
	}

	state.verifyRetries++
	log.Printf("[verify] round %d/%d", state.verifyRetries, o.verifyMaxRetries)

	// 发 verify:checking 事件
	o.sendVerifyEvent(events, "checking", state.verifyRetries, "")

	// 选模型
	client := o.flashClient
	if o.verifyModel == "pro" && o.proClient != nil {
		client = o.proClient
	}

	// 构建检验 prompt（含检索命中内容，避免误判检索来源数据为幻觉）
	verifyPrompt := o.buildVerifyPrompt(msg, req, state, result.RetrievedItems)
	verifySystemPrompt := `你是一个内容检验助手。请判断模型的回复是否完整、准确。

严格按以下 JSON 格式回复，不要加任何额外文字：
{"complete":true或false,"hallucinations":["模型捏造的事实，必须能在工具返回数据中找到依据，没有则为空数组"],"missing_aspects":["还缺少的信息维度，如未查笔记/未查记忆/未查账单等，没有则为空数组"],"suggestion":"针对缺失或幻觉的具体建议，complete为true且无幻觉时留空"}

检验标准：
- complete: 用户问题是否已被充分回答？信息源是否需要多角度交叉验证？
- hallucinations: 回复中是否有工具返回数据中找不到的具体数据/事实？
- 不要因为措辞风格标记为幻觉，只标记实质性的数据错误`

	// 保存检验请求到 DB（调试页可见）
	verifyMsg, err := client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: verifySystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: verifyPrompt}},
	})
	if err != nil {
		log.Printf("[verify] LLM call error: %v", err)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, result.Trace)
		return true
	}

	var vr verifyResult
	if err := json.Unmarshal([]byte(verifyMsg.Content), &vr); err != nil {
		log.Printf("[verify] parse result error: %v, raw=%s", err, verifyMsg.Content)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, result.Trace)
		return true
	}

	log.Printf("[verify] complete=%v hallucinations=%d missing=%d", vr.Complete, len(vr.Hallucinations), len(vr.MissingAspects))

	// 通过：无幻觉且完成
	if vr.Complete && len(vr.Hallucinations) == 0 {
		o.saveVerifyFeedback(ctx, req.ConversationID, req.UserID, "检验通过：无幻觉，回复完整", verifySystemPrompt, verifyPrompt)
		log.Printf("[verify] round %d passed", state.verifyRetries)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, result.Trace)
		return true
	}

	// 不通过：存档草稿（IsInternal），注入反馈，继续
	log.Printf("[verify] round %d failed: complete=%v hallucinations=%d missing=%d", state.verifyRetries, vr.Complete, len(vr.Hallucinations), len(vr.MissingAspects))
	o.sendVerifyEvent(events, "retry", state.verifyRetries, vr.Suggestion)
	o.saveAssistantMsgInternal(ctx, req.ConversationID, req.UserID, msg, result.Trace)

	feedback := o.buildVerifyFeedback(vr)
	o.saveVerifyFeedback(ctx, req.ConversationID, req.UserID, feedback, verifySystemPrompt, verifyPrompt)
	state.msgs = append(state.msgs, llm.Message{Role: "assistant", Content: msg.Content})
	state.msgs = append(state.msgs, llm.Message{
		Role:    "user",
		Content: feedback,
	})
	return false
}

func (o *Orchestrator) buildVerifyPrompt(msg llm.Message, req *RunRequest, state *reactState, retrieved []promptbuilder.RetrievedItem) string {
	var sb strings.Builder
	sb.WriteString("【用户问题】\n")
	sb.WriteString(req.RawUserMessage)

	sb.WriteString("\n\n【模型准备回复的内容】\n")
	sb.WriteString(msg.Content)

	sb.WriteString("\n\n【工具调用历史】\n")
	hasTools := false
	for _, m := range state.msgs {
		switch m.Role {
		case "assistant":
			if strings.Contains(m.Content, "tool_calls") {
				hasTools = true
				sb.WriteString("[工具调用] ")
				sb.WriteString(m.Content)
				sb.WriteString("\n")
			}
		case "user":
			if strings.Contains(m.Content, `"success"`) {
				sb.WriteString("[工具返回] ")
				sb.WriteString(m.Content)
				sb.WriteString("\n")
			}
		}
	}
	if !hasTools {
		sb.WriteString("（本轮未调用工具）\n")
	}

	if len(retrieved) > 0 {
		sb.WriteString("\n【检索命中内容（模型可引用，不算幻觉）】\n")
		for _, it := range retrieved {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", it.Type, it.Text))
		}
	}

	return sb.String()
}

func (o *Orchestrator) buildVerifyFeedback(vr verifyResult) string {
	var parts []string
	parts = append(parts, "[系统] 内部检验发现以下问题：")

	for i, h := range vr.Hallucinations {
		parts = append(parts, fmt.Sprintf("(%d) 幻觉：%s", i+1, h))
	}
	for i, m := range vr.MissingAspects {
		parts = append(parts, fmt.Sprintf("(%d) 缺失：%s", i+1, m))
	}
	if vr.Suggestion != "" {
		parts = append(parts, "建议："+vr.Suggestion)
	}
	parts = append(parts, "请修正你的回复并重新输出，确保所有事实都有依据，信息维度完整。注意：这是内部质量检查，不要对这段话道歉或解释（如'你说得对，我确实算错了'），直接生成修正后的最终回复，就像它是你对用户问题的第一次回答。")

	return strings.Join(parts, "\n")
}

func (o *Orchestrator) sendVerifyEvent(events chan<- *llm.StreamEvent, status string, retry int, reason string) {
	content, _ := json.Marshal(map[string]interface{}{
		"status": status,
		"retry":  retry,
		"reason": reason,
	})
	o.sendEvent(events, &llm.StreamEvent{Type: "verify", Content: string(content)})
}

// saveAssistantMsgInternal 以 IsInternal=true 保存草稿，仅存档不展示
func (o *Orchestrator) saveAssistantMsgInternal(ctx context.Context, convID, userID string, msg llm.Message, trace *promptbuilder.PromptTrace) string {
	return o.saveAssistantMsgWithFlags(ctx, convID, userID, msg, trace, true)
}

// saveVerifyFeedback 将检验反馈存为内部系统消息（含检验请求 request_json），调试页可见
func (o *Orchestrator) saveVerifyFeedback(ctx context.Context, convID, userID, feedback, systemPrompt, userPrompt string) {
	if o.msgRepo == nil || convID == "" {
		return
	}
	reqJSON, err := json.Marshal(map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	})
	if err != nil {
		log.Printf("[verify] marshal request error: %v", err)
		return
	}
	reqStr := string(reqJSON)
	if err := o.msgRepo.Create(ctx, &model.Message{
		ID:             uuid.New().String(),
		UserID:         userID,
		ConversationID: convID,
		Role:           "system",
		Content:        feedback,
		RequestJSON:    &reqStr,
		IsInternal:     true,
	}); err != nil {
		log.Printf("[verify] save feedback error: %v", err)
	}
}

func (o *Orchestrator) saveAssistantMsgWithFlags(ctx context.Context, convID, userID string, msg llm.Message, trace *promptbuilder.PromptTrace, isInternal bool) string {
	if o.msgRepo == nil || convID == "" {
		return ""
	}
	mid := uuid.New().String()
	modelMsg := &model.Message{
		ID:             mid,
		UserID:         userID,
		ConversationID: convID,
		Role:           "assistant",
		Content:        maybeInjectCreatedAt(msg.Content),
		IsInternal:     isInternal,
	}
	if trace != nil {
		b, _ := json.Marshal(trace)
		s := string(b)
		modelMsg.Extra = &s
	}
	if msg.ReasoningContent != "" {
		rc := msg.ReasoningContent
		modelMsg.ReasoningContent = &rc
	}
	if tcJSON := extractToolCallsJSON(msg.Content); tcJSON != nil {
		modelMsg.ToolCallsJSON = tcJSON
	} else if len(msg.ToolCalls) > 0 {
		b, _ := json.Marshal(msg.ToolCalls)
		s := string(b)
		modelMsg.ToolCallsJSON = &s
	}
	if msg.RequestJSON != nil {
		modelMsg.RequestJSON = msg.RequestJSON
	}
	if err := o.msgRepo.Create(ctx, modelMsg); err != nil {
		log.Printf("[orch] save assistant msg error: %v", err)
		return ""
	}
	return mid
}

func (*Orchestrator) injectExcludeMemIDsForMemSearch(parsed *LLMResponse, memIDs []string) {
	for i := range parsed.ToolCalls {
		if (parsed.ToolCalls[i].Tool == "memory_search" || parsed.ToolCalls[i].Tool == "search_memories_v2") && len(memIDs) > 0 {
			parsed.ToolCalls[i].Args = injectExcludeIDs(parsed.ToolCalls[i].Args, memIDs)
		}
	}
}

func (o *Orchestrator) collectToolResults(ctx context.Context, parsed *LLMResponse, toolCallResults []toolExecResult, events chan<- *llm.StreamEvent, convID string, userID string, msgs []llm.Message) []llm.Message {
	for i := range parsed.ToolCalls {
		res := toolCallResults[i].result
		resJSON, err := json.Marshal(res)
		if err != nil {
			log.Printf("[orch] tool result marshal error: %v", err)
			resJSON = []byte(fmt.Sprintf(`{"success":false,"error":"marshal failed: %s"}`, res.Error))
		}
		o.sendEvent(events, &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)})
		o.saveToolMsg(ctx, convID, userID, idgen.Next(), string(resJSON))
		msgs = append(msgs, llm.Message{
			Role:    "user",
			Content: string(resJSON),
		})
	}
	return msgs
}

// —— 并发执行 ——

type toolExecResult struct {
	tc     tool.ToolCall
	result *tool.Result
}

// injectExcludeIDs 将 exclude_ids（已注入的记忆 ID）注入到 memory_search 工具的 args 中
func injectExcludeIDs(args json.RawMessage, memIDs []string) json.RawMessage {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return args
	}
	ids := make([]interface{}, len(memIDs))
	for i, id := range memIDs {
		ids[i] = id
	}
	m["exclude_ids"] = ids
	b, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return b
}

func executeConcurrently(ctx context.Context, events chan<- *llm.StreamEvent, userID string,
	registry *tool.Registry, toolCalls []tool.ToolCall) []toolExecResult {

	if len(toolCalls) >= 2 {
		log.Printf("[orch] concurrent exec %d tools", len(toolCalls))
	}

	var wg sync.WaitGroup
	results := make([]toolExecResult, len(toolCalls))

	for i, tc := range toolCalls {
		tcJSON, _ := json.Marshal(tc)
		select {
		case events <- &llm.StreamEvent{Type: "tool_call", Content: string(tcJSON)}:
		default:
		}

		wg.Add(1)
		go func(idx int, call tool.ToolCall) {
			defer wg.Done()
			ec := &tool.ExecuteContext{Ctx: ctx, UserID: userID}
			r := registry.ExecuteByName(ec, call.Tool, call.Function, call.Args)
			log.Printf("[orch] tool exec: %s.%s success=%v", call.Tool, call.Function, r.Success)
			results[idx] = toolExecResult{tc: call, result: r}
		}(i, tc)
	}
	wg.Wait()
	return results
}

// —— DB 保存 ——

func (o *Orchestrator) saveAssistantMsg(ctx context.Context, convID, userID string, msg llm.Message, trace *promptbuilder.PromptTrace) string {
	return o.saveAssistantMsgWithFlags(ctx, convID, userID, msg, trace, false)
}

func (o *Orchestrator) saveToolMsg(ctx context.Context, convID, userID, toolCallID string, content string) {
	if o.msgRepo == nil || convID == "" {
		return
	}
	tcid := toolCallID
	msgID := uuid.New().String()
	if err := o.msgRepo.Create(ctx, &model.Message{
		UserID:         userID,
		ID:             msgID,
		ConversationID: convID,
		Role:           "tool",
		Content:        content,
		ToolCallID:     &tcid,
		IsInternal:     true,
	}); err != nil {
		log.Printf("[orch] save tool msg error: %v", err)
		return
	}
	log.Printf("[orch] saved tool message: id=%s tool_call_id=%s", msgID, toolCallID)
}

// truncateToolResult 截断过长的工具结果，保留头尾，中间标注截断量
func truncateToolResult(content string, maxLen int) string {
	if maxLen <= 0 || len(content) <= maxLen {
		return content
	}
	head := maxLen * 7 / 10
	tail := maxLen * 2 / 10
	if head+tail > maxLen {
		head = maxLen * 7 / 10
		tail = maxLen - head
	}
	truncated := len(content) - head - tail
	return content[:head] + fmt.Sprintf("\n…[已截断 %d 字符]…\n", truncated) + content[len(content)-tail:]
}

// FlattenRound 裁剪一轮消息为 LLM 上下文格式：只保留 user + 最终 assistant 回复。
// 用于默认会话加载最近 N 轮时裁剪 tool/internal 消息。
func FlattenRound(round []model.Message) []model.Message {
	var out []model.Message
	var lastAssistant *model.Message
	for i := len(round) - 1; i >= 0; i-- {
		if round[i].Role == "assistant" && !round[i].IsInternal {
			lastAssistant = &round[i]
			break
		}
	}
	if len(round) > 0 && round[0].Role == "user" {
		out = append(out, round[0])
	}
	if lastAssistant != nil {
		out = append(out, *lastAssistant)
	}
	return out
}

// BuildMessagesForFlash 将数据库标准消息转换为 FlashClient 格式
func BuildMessagesForFlash(history []model.Message, maxToolResultChars int) []llm.Message {
	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		// 过滤内部系统消息（检验请求/反馈），避免泄露到 LLM 对话
		if m.Role == "system" {
			continue
		}
		// ts := "（发于" + m.CreatedAt.In(config.BJ).Format("01-02 15:04") + "）"
		if m.Role == "tool" {
			// FlashClient 不支持 role="tool"，包装成 user 消息

			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: truncateToolResult(m.Content, maxToolResultChars),
			})
		} else {
			msg := llm.Message{ID: m.ID, Role: m.Role, Content: m.Content}
			if m.ToolCallID != nil {
				msg.ToolCallID = *m.ToolCallID
			}
			if m.ToolCallsJSON != nil {
				var tcs []llm.ToolCall
				if err := json.Unmarshal([]byte(*m.ToolCallsJSON), &tcs); err == nil {
					msg.ToolCalls = tcs
				}
			}

			msgs = append(msgs, msg)
		}
	}
	return msgs
}

// BuildMessagesForPro 将数据库标准消息转换为 ProClient 格式（原样）
func BuildMessagesForPro(history []model.Message, maxToolResultChars int) []llm.Message {
	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		// 过滤内部系统消息（检验请求/反馈），避免泄露到 LLM 对话
		if m.Role == "system" {
			continue
		}
		// ts := "（发于" + m.CreatedAt.In(config.BJ).Format("01-02 15:04") + "）"
		ts := ""
		content := m.Content
		if m.Role == "tool" {
			content = truncateToolResult(m.Content, maxToolResultChars)
		}
		msg := llm.Message{ID: m.ID, Role: m.Role, Content: content + ts}
		if m.ToolCallID != nil {
			msg.ToolCallID = *m.ToolCallID
		}
		if m.ToolCallsJSON != nil {
			var tcs []llm.ToolCall
			if err := json.Unmarshal([]byte(*m.ToolCallsJSON), &tcs); err == nil {
				msg.ToolCalls = tcs
			}
		}

		msgs = append(msgs, msg)
	}
	return msgs
}

func (o *Orchestrator) saveUserMsgEmbedding(userMsgID string, vec []float64, userID, convID string) {
	if len(vec) == 0 || o.vectorStore == nil || o.embeddingRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o.vectorStore.Upsert("msg:"+userMsgID, vec, map[string]string{
		"type":    "msg",
		"conv_id": convID,
		"user_id": userID,
	})
	embStr := llm.EmbeddingToString(vec)
	err := o.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         userMsgID,
		EntityType: model.EntityTypeMessage,
		EntityID:   userMsgID,
		UserID:     userID,
		Model:      o.embedder.ModelName(),
		Embedding:  &embStr,
	})
	if err != nil {
		log.Printf("[orch] save message embedding error: %v", err)
	}
}

// streamTextChunks 将文本按 rune 分块、逐块延时发送，模拟流式打字效果。
// ctx 取消时提前返回。短文本（<= chunkSize）一次性发送。
func (o *Orchestrator) streamTextChunks(ctx context.Context, events chan<- *llm.StreamEvent, text string) {
	if text == "" {
		return
	}
	runes := []rune(text)
	chunkSize := 3
	delay := 30 * time.Millisecond

	if len(runes) <= chunkSize {
		o.sendEvent(events, &llm.StreamEvent{Type: "text_delta", Content: text})
		return
	}

	for i := 0; i < len(runes); i += chunkSize {
		select {
		case <-ctx.Done():
			return
		default:
		}

		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		o.sendEvent(events, &llm.StreamEvent{Type: "text_delta", Content: chunk})

		if i+chunkSize < len(runes) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}
}

func (o *Orchestrator) emitDone(events chan<- *llm.StreamEvent, convID, assistantMsgID, userMsgID, errStr string, resourceIDs map[string][]string) {
	parts := make([]string, 0)
	if convID != "" {
		parts = append(parts, fmt.Sprintf(`"conversation_id":"%s"`, convID))
	}
	if assistantMsgID != "" {
		parts = append(parts, fmt.Sprintf(`"message_id":"%s"`, assistantMsgID))
	}
	if userMsgID != "" {
		parts = append(parts, fmt.Sprintf(`"user_message_id":"%s"`, userMsgID))
	}
	for _, typ := range []string{"memory", "note"} {
		if ids := resourceIDs[typ]; len(ids) > 0 {
			b, _ := json.Marshal(ids)
			parts = append(parts, fmt.Sprintf(`"%s_ids":%s`, typ, string(b)))
		}
	}
	if errStr != "" {
		parts = append(parts, fmt.Sprintf(`"error":"%s"`, errStr))
	}
	o.sendEvent(events, &llm.StreamEvent{Type: "done", Content: "{" + strings.Join(parts, ",") + "}"})
}

// —— 工具函数 ——

func (o *Orchestrator) GenerateTitle(ctx context.Context, userMessage string) string {
	msg, err := o.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是一个标题生成器，根据用户的消息内容生成一个 10 字以内的简短标题，只输出标题本身，不要加任何标点或引号。",
		Messages:     []llm.Message{{Role: "user", Content: userMessage}},
	})
	if err != nil {
		return ""
	}
	title := strings.TrimSpace(msg.Content)
	if len([]rune(title)) > 20 {
		title = string([]rune(title)[:20])
	}
	return title
}

func ParseLLMResponse(text string) (*LLMResponse, error) {
	var resp LLMResponse
	prefix, err := ParseFlashJSON(text, &resp)
	if err != nil {
		return nil, err // 非法 JSON
	}
	if prefix != "" {
		if resp.Reply != "" {
			resp.Reply = prefix + "\n\n" + resp.Reply
		} else {
			resp.Reply = prefix
		}
	}
	if resp.ToolCalls == nil && resp.Reply == "" {
		return nil, nil // 没找到 JSON，纯文本消息
	}
	if resp.ToolCalls == nil {
		resp.ToolCalls = []tool.ToolCall{}
	}
	for i, tc := range resp.ToolCalls {
		resp.ToolCalls[i].Function = strings.TrimPrefix(tc.Function, "_")
	}
	return &resp, nil
}

// extractCleanText 从 LLM 原始输出中提取干净的用户可见文本。
// extractToolCallsJSON 从 Flash JSON Content 中提取 tool_calls 并序列化为 JSON。
// 没有 tool_calls 或解析失败返回 nil。
func extractToolCallsJSON(raw string) *string {
	var parsed LLMResponse
	if _, err := ParseFlashJSON(raw, &parsed); err != nil {
		return nil
	}
	if len(parsed.ToolCalls) == 0 {
		return nil
	}
	b, err := json.Marshal(parsed.ToolCalls)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// maybeInjectCreatedAt 如果 content 是合法 JSON（含 reply 字段），注入 created_at
func maybeInjectCreatedAt(raw string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	if _, ok := m["reply"]; !ok {
		return raw
	}
	m["created_at"] = time.Now().Format(time.RFC3339)
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

func extractCleanText(raw string) string {
	parsed, err := ParseLLMResponse(raw)
	if err != nil {
		// parse 失败（非法 JSON / 前缀违规）→ 用 extractFlashReply 兜底，
		// 保证流式时有内容展示（handleOneRoundResponse 仍会触发重试）
		return extractFlashReply(raw)
	}
	if parsed == nil {
		return raw // 纯文本，容错接受
	}
	return parsed.Reply
}

// extractFlashReply 从 Flash LLM 输出中提取可见文本：prefix + reply。
// 与 controller/extractFlashReply 逻辑相同，避免循环依赖。
func extractFlashReply(raw string) string {
	var parsed struct {
		Reply string `json:"reply"`
	}
	prefix, err := ParseFlashJSON(raw, &parsed)
	if err != nil {
		return raw
	}
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && parsed.Reply != "" {
		return prefix + "\n\n" + parsed.Reply
	}
	if parsed.Reply != "" {
		return parsed.Reply
	}
	return raw
}

func buildPlanEvent(toolCalls []tool.ToolCall) map[string]interface{} {
	nodes := make([]map[string]interface{}, 0)
	edges := make([]map[string]interface{}, 0)
	for _, tc := range toolCalls {
		nid := fmt.Sprintf("n%d", len(nodes))
		nodes = append(nodes, map[string]interface{}{
			"id":     nid,
			"label":  tc.Function,
			"status": "done",
		})
		if len(nodes) > 1 {
			edges = append(edges, map[string]interface{}{
				"from": fmt.Sprintf("n%d", len(nodes)-2),
				"to":   nid,
			})
		}
	}
	return map[string]interface{}{"nodes": nodes, "edges": edges}
}
