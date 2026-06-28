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

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/agent/retrieval"
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

// WindowResult 是扩张窗口计算的结果，描述本次请求的窗口大小和 session 状态。
type WindowResult struct {
	Size              int    // 本次窗口轮数
	IsNewSession      bool   // 本条消息开启了新 session
	SessionStartMsgID string // 当前 session 的起始消息 ID（新 session 为空，orchestrator 用本条 msgID 填充）
}

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
	Window             WindowResult

	// 检索参数覆盖（0 值表示使用 PromptConfig 默认值）
	Memory  retrieval.SourceConfig
	Note    retrieval.SourceConfig
	History retrieval.SourceConfig
	Theme   retrieval.SourceConfig
	Summary retrieval.SourceConfig
}

// reactState — runReActLoop 循环过程中的可变状态
type reactState struct {
	msgs                 []llm.Message
	allToolCalls         []tool.ToolCall
	lastAssistantMsgID   string
	userMsgID            string
	verifyRetries        int  // 本轮对话已重检验次数
	nativeFCTextDeferred bool // 原生 FC 下文本因 verify 暂缓推送
	jsonBuffered         bool // 原生 FC 下文本首字符为 { 暂缓推送，防 JSON 泄露
}

// LLMResponse — LLM 返回的结构化 JSON（文本解析用）
type LLMResponse struct {
	Reply     string          `json:"reply"`
	ToolCalls []tool.ToolCall `json:"tool_calls"`
}

// msgCreator — Orchestrator 需要的消息写入能力（*service.MessageRepo 自动满足）
type msgCreator interface {
	Create(ctx context.Context, m *model.Message) error
	ListRecentUserMessages(ctx context.Context, convID string, limit int) ([]model.Message, error)
}

// convToucher — Orchestrator 需要的会话更新时间能力（*service.ConversationRepo 自动满足）
type convToucher interface {
	Touch(ctx context.Context, id string) error
}

// promptBuilder — Orchestrator 需要的 prompt 构建能力（*promptbuilder.PromptBuilder 自动满足）
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
	maxConcurrency      int
	onChatEnd           func(userID, convID, userMsgID string)
	verifyEnabled       bool
	verifyMaxRetries    int
	verifyModel         string // "flash" | "pro"
	useNativeFC         bool
	summaryRepo         *service.ConversationSummaryRepo
	contextRetriever    *ctxflow.ContextRetriever // 上下文检索
	contextFormatter    ctxflow.ContextFormatter  // 上下文格式化（接口）

	// 摘要附着控制
	maxSummaryAttachRounds    int
	summaryRelevanceThreshold float64
	summaryRelevanceHistoryN  int
}

// OrchestratorConfig — Orchestrator 依赖配置
type OrchestratorConfig struct {
	FlashClient               llm.LLMClient
	Registry                  *tool.Registry
	ProClient                 llm.LLMClient
	PromptBuilder             promptBuilder
	MsgRepo                   *service.MessageRepo
	ConvRepo                  *service.ConversationRepo
	Embedder                  *llm.Embedder
	VectorStore               *VectorStore
	EmbeddingRepo             *service.EmbeddingRepo
	MaxIterations             int
	Temperature               *float64
	MaxTokens                 *int
	MaxToolResultChars        int
	ToolMaxConcurrency        int
	OnChatEnd                 func(userID, convID, userMsgID string)
	VerifyEnabled             bool
	VerifyMaxRetries          int
	VerifyModel               string // "flash" | "pro"，默认 flash
	UseNativeFC               bool   // 是否使用原生 function calling
	SummaryRepo               *service.ConversationSummaryRepo
	ContextRetriever          *ctxflow.ContextRetriever // 上下文检索
	ContextFormatter          ctxflow.ContextFormatter  // 上下文格式化
	MaxSummaryAttachRounds    int                       // 摘要附着轮次上限，默认 5
	SummaryRelevanceThreshold float64                   // 摘要相关性阈值，默认 0.5
	SummaryRelevanceHistoryN  int                       // 相关性检查的历史消息窗口，默认 5
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
		flashClient:               cfg.FlashClient,
		proClient:                 cfg.ProClient,
		registry:                  cfg.Registry,
		systemPromptBuilder:       cfg.PromptBuilder,
		msgRepo:                   cfg.MsgRepo,
		convRepo:                  cfg.ConvRepo,
		embedder:                  cfg.Embedder,
		vectorStore:               cfg.VectorStore,
		embeddingRepo:             cfg.EmbeddingRepo,
		maxIterations:             cfg.MaxIterations,
		defaultTemperature:        cfg.Temperature,
		defaultMaxTokens:          cfg.MaxTokens,
		maxToolResultChars:        cfg.MaxToolResultChars,
		maxConcurrency:            cfg.ToolMaxConcurrency,
		onChatEnd:                 cfg.OnChatEnd,
		verifyEnabled:             cfg.VerifyEnabled,
		verifyMaxRetries:          cfg.VerifyMaxRetries,
		verifyModel:               cfg.VerifyModel,
		useNativeFC:               cfg.UseNativeFC,
		summaryRepo:               cfg.SummaryRepo,
		contextRetriever:          cfg.ContextRetriever,
		contextFormatter:          cfg.ContextFormatter,
		maxSummaryAttachRounds:    cfg.MaxSummaryAttachRounds,
		summaryRelevanceThreshold: cfg.SummaryRelevanceThreshold,
		summaryRelevanceHistoryN:  cfg.SummaryRelevanceHistoryN,
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
	var historyEmbeddings [][]float64
	if o.embedder != nil && !req.SkipRetrieval {
		// 使用原始消息（RawUserMessage）做 embedding 检索，而非归一化后的消息
		// 原因：日期归一化会把 "今天" → "2026-06-11"，绝对日期在 embedding 空间里
		// 会因年份/格式相似产生虚假匹配（如 "2026-06-11 买菜" 误匹配 "2026-05-29 睡觉"）
		rawMsg := req.RawUserMessage
		if rawMsg == "" {
			rawMsg = req.UserMessageContent
		}
		queryVec = o.tryEmbedContent(ctx, rawMsg)
		// 从 VectorStore 按 ID 查最近 N 条历史用户消息 embedding
		historyEmbeddings = o.embedHistoryUserMessages(req.Messages, 10)
	}

	log.Printf("[orch] run start: model=%s, msgs=%d", o.pickClient(req).ModelName(), len(req.Messages))

	// 1. 静态 system prompt
	result, err := o.systemPromptBuilder.Build(ctx, promptbuilder.BuildOptions{
		UserID:      req.UserID,
		AgentPrompt: req.AgentSystemPrompt,
	})
	if err != nil {
		log.Printf("[orch] prompt build error: %v", err)
		return nil, err
	}

	// 2. 上下文检索
	var retrieveResult *ctxflow.RetrieveResult
	if !req.SkipRetrieval && len(queryVec) > 0 {
		retrieveResult, err = o.contextRetriever.Retrieve(ctx, mkSearchOptions(req, queryVec, historyEmbeddings))
		if err != nil {
			log.Printf("[orch] retrieve error: %v", err)
		}
	}

	events := make(chan *llm.StreamEvent, 512)
	go o.runReActLoop(ctx, events, result.Prompt, retrieveResult, queryVec, req)
	return events, nil
}

func mkSearchOptions(req *RunRequest, queryVec []float64, historyEmbeddings [][]float64) retrieval.SearchOptions {
	// 从已有消息中采集 DB ID，供检索时去重
	excludeMsgIDs := make(map[string]bool)
	for _, m := range req.Messages {
		if m.ID != "" {
			excludeMsgIDs[m.ID] = true
		}
	}
	return retrieval.SearchOptions{
		Query:             req.UserMessageContent,
		UserID:            req.UserID,
		ConversationID:    req.ConversationID,
		Embedding:         queryVec,
		HistoryEmbeddings: historyEmbeddings,
		Memory:            req.Memory,
		Note:              req.Note,
		History:           req.History,
		Theme:             req.Theme,
		Summary:           req.Summary,
		ExcludeMsgIDs:     excludeMsgIDs,
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

// embedHistoryUserMessages 从 VectorStore 中按 ID 查最近 N 条用户消息的 embedding
func (o *Orchestrator) embedHistoryUserMessages(messages []llm.Message, n int) [][]float64 {
	if o.vectorStore == nil {
		return nil
	}
	var result [][]float64
	for i := len(messages) - 1; i >= 0 && len(result) < n; i-- {
		if messages[i].Role == "user" && messages[i].ID != "" {
			vec, ok := o.vectorStore.GetVector("msg:" + messages[i].ID)
			if ok && len(vec) > 0 {
				result = append(result, vec)
			}
		}
	}
	return result
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
	systemPrompt string,
	retrieveResult *ctxflow.RetrieveResult,
	queryVec []float64,
	req *RunRequest,
) {
	defer close(events)

	loopStart := time.Now()
	defer func() {
		monitor.RecordRequestLatency(time.Since(loopStart).Milliseconds())
	}()

	userMsg := o.prepareUserMsg(req)
	summaryBlock := o.resolveSummaryAttachment(ctx, req, userMsg, queryVec)

	if err := o.msgRepo.Create(ctx, userMsg); err != nil {
		log.Printf("[orch] save user msg error: %v", err)
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrInternal)})
		return
	}
	util.SafeGo(func() { o.convRepo.Touch(ctx, req.ConversationID) })
	util.SafeGo(func() { o.saveUserMsgEmbedding(userMsg.ID, queryVec, req.UserID, req.ConversationID) })

	// 在整个 ReAct 循环结束后通知外部（用于入队延迟任务：post_chat + summary）
	if o.onChatEnd != nil {
		defer o.onChatEnd(req.UserID, req.ConversationID, userMsg.ID)
	}

	// 格式化 user 消息
	var extraBlocks []ctxflow.ExtraBlock
	extraBlocks = append(extraBlocks, ctxflow.DateTimeBlock(time.Now()))
	if summaryBlock != "" {
		extraBlocks = append(extraBlocks, ctxflow.ExtraBlock{Type: "summary", Content: summaryBlock})
	}
	userContent := o.contextFormatter.FormatUserMessage(retrieveResult, req.UserMessageContent, extraBlocks...)

	state := &reactState{
		msgs:      append(req.Messages, llm.Message{Role: "user", Content: userContent}),
		userMsgID: userMsg.ID,
	}

	maxIter := o.maxIterations
	if req.MaxIterations > 0 {
		maxIter = req.MaxIterations
	}
	for iter := 0; iter < maxIter; iter++ {
		if o.runOneRound(ctx, events, o.pickClient(req), req, systemPrompt, retrieveResult, state, iter) {
			return
		}
	}

	o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrMaxIterations)})
	o.emitDone(events, req.ConversationID, "", userMsg.ID, "max iterations reached", idsByTypeFromResult(retrieveResult))
}

// prepareUserMsg 创建用户消息并设置 WindowSize/SessionStart Extra 字段。
func (o *Orchestrator) prepareUserMsg(req *RunRequest) *model.Message {
	userMsg := mkUserMsg(req)
	if req.Window.Size > 0 {
		SetWindowSize(userMsg, req.Window.Size)
	}
	if req.Window.IsNewSession {
		SetSessionStart(userMsg, userMsg.ID)
	} else if req.Window.SessionStartMsgID != "" {
		SetSessionStart(userMsg, req.Window.SessionStartMsgID)
	}
	return userMsg
}

// resolveSummaryAttachment 决定本轮是否附着摘要，返回摘要 XML block（空串表示不附着）。
// 副作用：设置 userMsg 的 SummaryAttachRound（必须在 Create 之前调用）。
func (o *Orchestrator) resolveSummaryAttachment(
	ctx context.Context,
	req *RunRequest,
	userMsg *model.Message,
	queryVec []float64,
) string {
	// 查上一条用户消息的附着轮数
	var prevUserMsg *model.Message
	if recent, err := o.msgRepo.ListRecentUserMessages(ctx, req.ConversationID, 1); err == nil && len(recent) > 0 {
		prevUserMsg = &recent[0]
	} else if err != nil {
		log.Printf("[orch] ListRecentUserMessages for summary attach error: %v", err)
	}

	// 确定本轮轮数
	round := 1
	if req.Window.IsNewSession {
		round = 1
	} else if prevUserMsg != nil {
		prev := GetSummaryAttachRound(prevUserMsg)
		log.Printf("[orch] previous user msg summary attach round: %d", prev)
		if prev < 0 || prev >= o.maxSummaryAttachRounds {
			round = -1
		} else {
			round = prev + 1
		}
	}
	log.Printf("[orch] resolveSummaryAttachment: round=%d", round)

	var block string
	if o.summaryRepo != nil && round > 0 {
		summary, err := o.summaryRepo.GetLatest(ctx, req.ConversationID)
		if err != nil {
			log.Printf("[orch] GetLatest summary error: %v", err)
		} else if summary != nil {
			parent := ""
			if summary.ParentSummaryID != nil {
				parent = *summary.ParentSummaryID
			}
			block = fmt.Sprintf("<conversation_summary id=\"%s\" cover_from=\"%s\" cover_to=\"%s\" parent=\"%s\">\n%s\n</conversation_summary>\n",
				summary.ID, summary.CoverFromMsgID, summary.CoverToMsgID, parent, summary.SummaryText)
		}
	}

	SetSummaryAttachRound(userMsg, round)
	return block
}

// checkSummaryRelevance 用 embedding 窗口检查摘要与当前对话的相关性。
// 参照 Theme 系统的相似度检查模式：取当前消息 + 最近 N 条历史用户消息的 embedding，
// 分别与摘要 embedding 做余弦相似度，取最大值判断是否 >= 阈值。
func (o *Orchestrator) checkSummaryRelevance(
	ctx context.Context,
	summary *model.ConversationSummary,
	req *RunRequest,
	queryVec []float64,
) bool {
	summaryVec := o.getOrEmbedSummary(ctx, summary)
	if len(summaryVec) == 0 {
		// embed 失败，乐观附着
		return true
	}

	// 收集查询向量窗口：上游已嵌入的 queryVec + 最近 N 条历史
	var queryVecs [][]float64
	if len(queryVec) > 0 {
		queryVecs = append(queryVecs, queryVec)
	}
	if o.summaryRelevanceHistoryN > 0 {
		queryVecs = append(queryVecs, o.embedHistoryUserMessages(req.Messages, o.summaryRelevanceHistoryN)...)
	}

	if len(queryVecs) == 0 {
		// 无查询向量，乐观附着
		return true
	}

	// 计算 max 相似度
	maxSim := 0.0
	for _, v := range queryVecs {
		if sim := cosineSimilarity(v, summaryVec); sim > maxSim {
			maxSim = sim
		}
	}

	return maxSim >= o.summaryRelevanceThreshold
}

// getOrEmbedSummary 获取摘要的 embedding（惰性 embed → VectorStore + DB 双写）。
func (o *Orchestrator) getOrEmbedSummary(ctx context.Context, summary *model.ConversationSummary) []float64 {
	vsKey := "summary:" + summary.ID
	if vec, ok := o.vectorStore.GetVector(vsKey); ok {
		return vec
	}

	vec, err := o.embedder.Embed(ctx, summary.SummaryText)
	if err != nil {
		log.Printf("[summary] embed summary %s error: %v", summary.ID[:8], err)
		return nil
	}

	// 写入内存 VectorStore
	o.vectorStore.Upsert(vsKey, vec, map[string]string{
		"type":       "summary",
		"summary_id": summary.ID,
		"user_id":    summary.UserID,
	})

	// 写入 DB embeddings 表，供 warmup 恢复
	embStr := llm.EmbeddingToString(vec)
	if err := o.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         summary.ID,
		EntityType: model.EntityTypeSummary,
		EntityID:   summary.ID,
		UserID:     summary.UserID,
		Model:      o.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[summary] save embedding to DB error: %v", err)
	}

	return vec
}

// runOneRound 执行一轮 ReAct：LLM 调用 → 流式收集 → done/error 处理，返回 true 表示对话结束。
func (o *Orchestrator) runOneRound(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	client llm.LLMClient,
	req *RunRequest,
	systemPrompt string,
	retrieveResult *ctxflow.RetrieveResult,
	state *reactState,
	round int,
) bool {
	if ctx.Err() != nil {
		log.Printf("[orch] context cancelled: %v", ctx.Err())
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrContextCancelled)})
		o.sendEvent(events, &llm.StreamEvent{Type: "done"})
		return true
	}

	state.nativeFCTextDeferred = o.shouldVerify(req, state)
	llmStart := time.Now()
	streamEvents, err := client.Chat(ctx, o.mkLlmChatReq(req, systemPrompt, state.msgs))
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
			// 首字符为 { 可能是 JSON，暂缓推送防止裸 JSON 泄露到前端
			if o.useNativeFC && !state.jsonBuffered && len(streamContent.String()) <= 10 && strings.HasPrefix(strings.TrimSpace(streamContent.String()), "{") {
				state.jsonBuffered = true
			}
			if o.useNativeFC && !state.nativeFCTextDeferred && !state.jsonBuffered {
				o.sendEvent(events, se)
			}
		case "thought_delta":
			streamReasoning.WriteString(se.Content)
			o.sendEvent(events, se)
		case "done":
			elapsedMs := time.Since(llmStart).Milliseconds()
			recordLLMCall(req.DeepMode && o.proClient != nil, elapsedMs, se.Usage)
			log.Printf("[orch] round %d response: content=%d, reasoning=%d, tool_calls=%d",
				round+1, streamContent.Len(), streamReasoning.Len(), len(se.ToolCalls))
			return o.finalizeRound(ctx, events, se, streamContent.String(), streamReasoning.String(), req, systemPrompt, retrieveResult, state)
		case "error":
			o.sendEvent(events, se)
			return true
		}
	}
	return false
}

func (o *Orchestrator) mkLlmChatReq(req *RunRequest, systemPrompt string, msgs []llm.Message) *llm.ChatRequest {
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
	var tools []llm.ToolDefinition
	if o.useNativeFC {
		tools = o.registry.GetNativeFCTools()
	}
	return &llm.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     msgs,
		Tools:        tools,
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
	systemPrompt string,
	retrieveResult *ctxflow.RetrieveResult,
	state *reactState,
) bool {
	if streamReasoning != "" {
		o.sendEvent(events, &llm.StreamEvent{Type: "thought_done", Content: ""})
	}

	msg := newAssistantMessage(streamContent, streamReasoning, se)

	done := o.handleOneRoundResponse(ctx, events, msg, req, systemPrompt, retrieveResult, state)
	if done {
		if !o.useNativeFC {
			cleanText := extractCleanText(streamContent)
			if cleanText != "" {
				o.streamTextChunks(ctx, events, cleanText)
			}
		} else if (state.nativeFCTextDeferred || state.jsonBuffered) && streamContent != "" {
			o.streamTextChunks(ctx, events, streamContent)
		}
		o.emitDone(events, req.ConversationID, state.lastAssistantMsgID, state.userMsgID, "", idsByTypeFromResult(retrieveResult))
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
		Usage:            se.Usage,
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
	systemPrompt string,
	retrieveResult *ctxflow.RetrieveResult,
	state *reactState,
) bool {
	// 原生 FC 路径：tool_calls 已由 API 在 done 事件中解析好
	if o.useNativeFC && len(msg.ToolCalls) > 0 {
		internalTCs := nativeToolCallsToInternal(msg.ToolCalls)
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
		state.msgs = append(state.msgs, msg)
		state.allToolCalls = append(state.allToolCalls, internalTCs...)
		toolCallResults := o.executeConcurrently(ctx, events, req.UserID, o.registry, internalTCs)
		state.msgs = o.collectToolResultsNative(ctx, internalTCs, msg.ToolCalls, toolCallResults, events, req.ConversationID, req.UserID, state.msgs)
		return false
	}

	// 原生 FC 路径：无工具调用，纯文本回复（文本已在 runOneRound 中实时推送）
	if o.useNativeFC && len(msg.ToolCalls) == 0 {
		// 兜底：DeepSeek 偶发不走原生 FC，仍在 text 中输出 JSON
		if parsed, parseErr := ParseLLMResponse(msg.Content); parseErr == nil && parsed != nil {
			if len(parsed.ToolCalls) > 0 {
				return o.executePromptEngTools(ctx, events, msg, req, systemPrompt, retrieveResult, state, parsed)
			}
			// 纯 JSON（有 reply 无 tool_calls）：提取纯文本
			msg.Content = parsed.Reply
			if state.jsonBuffered {
				o.sendEvent(events, &llm.StreamEvent{Type: "text_delta", Content: parsed.Reply})
				state.jsonBuffered = false
			}
		}
		if o.shouldVerify(req, state) {
			return o.verifyAndMaybeRetry(ctx, events, msg, req, systemPrompt, retrieveResult, state)
		}
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
		return true
	}

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
			return o.verifyAndMaybeRetry(ctx, events, msg, req, systemPrompt, retrieveResult, state)
		}
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
		return true
	}

	log.Printf("[orch] round embedded tool_calls=%d: %s",
		len(parsed.ToolCalls), tool.ToolCallNames(parsed.ToolCalls))

	// 无工具调用 → 纯文本回复
	if len(parsed.ToolCalls) == 0 {
		log.Printf("[orch] round text-only JSON reply, len=%d", len(msg.Content))
		if o.shouldVerify(req, state) {
			return o.verifyAndMaybeRetry(ctx, events, msg, req, systemPrompt, retrieveResult, state)
		}
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
		return true
	}

	return o.executePromptEngTools(ctx, events, msg, req, systemPrompt, retrieveResult, state, parsed)
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
	systemPrompt string,
	retrieveResult *ctxflow.RetrieveResult,
	state *reactState,
) bool {
	if state.verifyRetries >= o.verifyMaxRetries {
		log.Printf("[verify] max retries reached (%d/%d), accepting draft", state.verifyRetries, o.verifyMaxRetries)
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
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

	// 构建检验 prompt——检索上下文注入 system prompt，避免误判为幻觉
	retrievalCtx := buildRetrievalContext(retrievedItemsFromPackets(retrieveResult))
	verifyPrompt := o.buildVerifyPrompt(msg, req, state)
	verifySystemPrompt := `你是一个内容检验助手。请判断模型的回复是否完整、准确。

严格按以下 JSON 格式回复，不要加任何额外文字：
{"complete":true或false,"hallucinations":["模型捏造的事实，必须能在工具返回数据中找到依据，没有则为空数组"],"missing_aspects":["还缺少的信息维度，如未查笔记/未查记忆/未查账单等，没有则为空数组"],"suggestion":"针对缺失或幻觉的具体建议，complete为true且无幻觉时留空"}

检验标准：
- complete: 用户问题是否已被充分回答？信息源是否需要多角度交叉验证？
- hallucinations: 回复中的数据必须在【工具返回数据】或【检索上下文】中找到依据。检索上下文中的信息是合法的已知事实，模型引用它们不算幻觉。
- 不要因为措辞风格标记为幻觉，只标记实质性的数据错误`
	if retrievalCtx != "" {
		verifySystemPrompt += "\n\n" + retrievalCtx
	}

	// 保存检验请求到 DB（调试页可见）
	verifyMsg, err := client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: verifySystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: verifyPrompt}},
	})
	if err != nil {
		log.Printf("[verify] LLM call error: %v", err)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
		return true
	}

	var vr verifyResult
	if err := json.Unmarshal([]byte(verifyMsg.Content), &vr); err != nil {
		log.Printf("[verify] parse result error: %v, raw=%s", err, verifyMsg.Content)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
		return true
	}

	log.Printf("[verify] complete=%v hallucinations=%d missing=%d", vr.Complete, len(vr.Hallucinations), len(vr.MissingAspects))

	// 通过：无幻觉且完成
	if vr.Complete && len(vr.Hallucinations) == 0 {
		o.saveVerifyFeedback(ctx, req.ConversationID, req.UserID, "检验通过：无幻觉，回复完整", verifySystemPrompt, verifyPrompt)
		log.Printf("[verify] round %d passed", state.verifyRetries)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
		return true
	}

	// 不通过：存档草稿（IsInternal），注入反馈，继续
	log.Printf("[verify] round %d failed: complete=%v hallucinations=%d missing=%d", state.verifyRetries, vr.Complete, len(vr.Hallucinations), len(vr.MissingAspects))
	o.sendVerifyEvent(events, "retry", state.verifyRetries, vr.Suggestion)
	o.saveAssistantMsgInternal(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))

	feedback := o.buildVerifyFeedback(vr)
	o.saveVerifyFeedback(ctx, req.ConversationID, req.UserID, feedback, verifySystemPrompt, verifyPrompt)
	state.msgs = append(state.msgs, llm.Message{Role: "assistant", Content: msg.Content})
	state.msgs = append(state.msgs, llm.Message{
		Role:    "user",
		Content: feedback,
	})
	return false
}

func (o *Orchestrator) buildVerifyPrompt(msg llm.Message, req *RunRequest, state *reactState) string {
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
			if len(m.ToolCalls) > 0 {
				// 原生 FC：输出 tool_calls 的函数名 + 参数
				hasTools = true
				for _, tc := range m.ToolCalls {
					sb.WriteString(fmt.Sprintf("[工具调用] %s(%s)\n", tc.Function.Name, tc.Function.Arguments))
				}
			} else if strings.Contains(m.Content, "tool_calls") {
				// 旧格式：content 包含 JSON 工具调用
				hasTools = true
				sb.WriteString("[工具调用] ")
				sb.WriteString(m.Content)
				sb.WriteString("\n")
			}
		case "tool":
			// 原生 FC：工具结果
			hasTools = true
			sb.WriteString("[工具返回] ")
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		case "user":
			if strings.Contains(m.Content, `"success"`) {
				// 旧格式：工具结果伪装成 user 消息
				hasTools = true
				sb.WriteString("[工具返回] ")
				sb.WriteString(m.Content)
				sb.WriteString("\n")
			}
		}
	}
	if !hasTools {
		sb.WriteString("（本轮未调用工具）\n")
	}

	return sb.String()
}

// buildRetrievalContext 将检索命中内容格式化为 verify system prompt 的已知事实段。
// 检索上下文注入 system prompt（而非 user prompt），确保检验 LLM 将其视为合法信息来源。
func buildRetrievalContext(retrieved []retrievedItemView) string {
	if len(retrieved) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("【检索上下文——以下信息是已知事实，模型回复引用它们不算幻觉】\n")
	for _, it := range retrieved {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", it.Type, it.Text))
	}
	return sb.String()
}

// executePromptEngTools 统一处理 prompt engineering 工具调用路径：
// 保存 assistant 消息 → 注入排除 ID → 并发执行 → 收集结果。
// 返回 false（继续 ReAct 循环）。
func (o *Orchestrator) executePromptEngTools(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msg llm.Message,
	req *RunRequest,
	systemPrompt string,
	retrieveResult *ctxflow.RetrieveResult,
	state *reactState,
	parsed *LLMResponse,
) bool {
	msg.ToolCalls = nil
	state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult))
	state.msgs = append(state.msgs, llm.Message{Role: "assistant", Content: msg.Content})
	state.allToolCalls = append(state.allToolCalls, parsed.ToolCalls...)
	o.injectExcludeMemIDsForMemSearch(parsed, idsByTypeFromResult(retrieveResult)["memory"])
	toolCallResults := o.executeConcurrently(ctx, events, req.UserID, o.registry, parsed.ToolCalls)
	state.msgs = o.collectToolResults(ctx, parsed, toolCallResults, events, req.ConversationID, req.UserID, state.msgs)
	return false
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
func (o *Orchestrator) saveAssistantMsgInternal(ctx context.Context, convID, userID string, msg llm.Message, trace *ctxflow.RetrieveTrace) string {
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

func (o *Orchestrator) saveAssistantMsgWithFlags(ctx context.Context, convID, userID string, msg llm.Message, trace *ctxflow.RetrieveTrace, isInternal bool) string {
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
	mergeMsgExtra(modelMsg, func(extra *MsgExtra) {
		if msg.Usage != nil {
			extra.Usage = msg.Usage
		}
		if trace != nil {
			extra.Providers = trace.Providers
		}
	})
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

// --- helpers for working with ctxflow types ---

func traceFromResult(r *ctxflow.RetrieveResult) *ctxflow.RetrieveTrace {
	if r == nil {
		return nil
	}
	return r.Trace
}

func idsByTypeFromResult(r *ctxflow.RetrieveResult) map[string][]string {
	if r == nil {
		return nil
	}
	ids := make(map[string][]string)
	for typ, packets := range r.Packets {
		for _, pkt := range packets {
			ids[typ] = append(ids[typ], pkt.ID)
		}
	}
	return ids
}

type retrievedItemView struct {
	Type string
	Text string
}

func retrievedItemsFromPackets(r *ctxflow.RetrieveResult) []retrievedItemView {
	if r == nil {
		return nil
	}
	var items []retrievedItemView
	for typ, packets := range r.Packets {
		for _, pkt := range packets {
			items = append(items, retrievedItemView{Type: typ, Text: pkt.Text})
		}
	}
	return items
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

// collectToolResultsNative 原生 FC 路径：工具结果以 role=tool 保存，关联真实 tool_call_id。
func (o *Orchestrator) collectToolResultsNative(ctx context.Context, internalTCs []tool.ToolCall, nativeTCs []llm.ToolCall, toolCallResults []toolExecResult, events chan<- *llm.StreamEvent, convID string, userID string, msgs []llm.Message) []llm.Message {
	for i := range internalTCs {
		res := toolCallResults[i].result
		resJSON, err := json.Marshal(res)
		if err != nil {
			log.Printf("[orch] tool result marshal error: %v", err)
			resJSON = []byte(fmt.Sprintf(`{"success":false,"error":"marshal failed: %s"}`, res.Error))
		}
		o.sendEvent(events, &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)})
		o.saveToolMsg(ctx, convID, userID, nativeTCs[i].ID, string(resJSON))
		msgs = append(msgs, llm.Message{
			Role:       "tool",
			Content:    string(resJSON),
			ToolCallID: nativeTCs[i].ID,
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

func (o *Orchestrator) executeConcurrently(ctx context.Context, events chan<- *llm.StreamEvent, userID string,
	registry *tool.Registry, toolCalls []tool.ToolCall) []toolExecResult {

	limit := o.maxConcurrency
	if limit <= 0 {
		limit = len(toolCalls) // 不限制
	}
	if len(toolCalls) >= 2 {
		log.Printf("[orch] concurrent exec %d tools (limit=%d)", len(toolCalls), limit)
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	results := make([]toolExecResult, len(toolCalls))

	for i, tc := range toolCalls {
		registry.FillDisplay(&tc)
		tcJSON, _ := json.Marshal(tc)
		select {
		case events <- &llm.StreamEvent{Type: "tool_call", Content: string(tcJSON)}:
		default:
		}

		wg.Add(1)
		go func(idx int, call tool.ToolCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var r *tool.Result
			func() {
				defer func() {
					if p := recover(); p != nil {
						log.Printf("[orch] tool exec panic: %s.%s panic=%v", call.Tool, call.Function, p)
						r = &tool.Result{Success: false, Error: fmt.Sprintf("工具执行异常: %v", p)}
					}
				}()
				ec := &tool.ExecuteContext{Ctx: ctx, UserID: userID}
				r = registry.ExecuteByName(ec, call.Tool, call.Function, call.Args)
			}()

			log.Printf("[orch] tool exec: %s.%s success=%v", call.Tool, call.Function, r.Success)
			results[idx] = toolExecResult{tc: call, result: r}
		}(i, tc)
	}
	wg.Wait()
	return results
}

// —— DB 保存 ——

func (o *Orchestrator) saveAssistantMsg(ctx context.Context, convID, userID string, msg llm.Message, trace *ctxflow.RetrieveTrace) string {
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

// FlattenRound 裁剪一轮消息为 LLM 上下文格式。
// 保留完整的 user → assistant(tool_calls) → tool(result) → assistant(final) 链路，
// 过滤 system 消息和内部 assistant（verify 草稿），但保留 tool 消息（含 IsInternal 的）。
func FlattenRound(round []model.Message) []model.Message {
	var out []model.Message
	for _, m := range round {
		if m.Role == "system" {
			continue
		}
		// 跳过 verify 的内部 assistant 草稿，但保留 tool 结果
		if m.IsInternal && m.Role == "assistant" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// isNativeToolCallID 判断 tool_call_id 是否为 LLM 原生 FC 返回的真实 ID。
// idgen 使用 Snowflake，ID 为纯数字字符串；LLM 返回的 tool_call ID 含字母。
func isNativeToolCallID(id string) bool {
	for _, c := range id {
		if c < '0' || c > '9' {
			return true
		}
	}
	return false
}

// BuildMessagesForFlash 将数据库标准消息转换为 LLM 消息格式。
// useNativeFC=true 时保留 role=tool 和 tool_call_id（供原生 function calling）。
func BuildMessagesForFlash(history []model.Message, maxToolResultChars int, useNativeFC bool) []llm.Message {
	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		// 过滤内部系统消息（检验请求/反馈），避免泄露到 LLM 对话
		if m.Role == "system" {
			continue
		}
		if m.Role == "tool" {
			if useNativeFC && m.ToolCallID != nil && isNativeToolCallID(*m.ToolCallID) {
				// 原生 FC：保留 role=tool，关联 tool_call_id
				msgs = append(msgs, llm.Message{
					Role:       "tool",
					Content:    truncateToolResult(m.Content, maxToolResultChars),
					ToolCallID: *m.ToolCallID,
				})
			} else {
				// prompt engineering：包装成 user 消息
				msgs = append(msgs, llm.Message{
					Role:    "user",
					Content: truncateToolResult(m.Content, maxToolResultChars),
				})
			}
		} else {
			msg := llm.Message{ID: m.ID, Role: m.Role, Content: m.Content}
			if m.ToolCallID != nil {
				msg.ToolCallID = *m.ToolCallID
			}
			if useNativeFC && m.ToolCallsJSON != nil {
				var tcs []llm.ToolCall
				if err := json.Unmarshal([]byte(*m.ToolCallsJSON), &tcs); err == nil {
					msg.ToolCalls = tcs
				}
			}

			msgs = append(msgs, msg)
		}
	}
	if useNativeFC {
		sanitizeOrphanToolCalls(msgs)
	}
	return msgs
}

// sanitizeOrphanToolCalls 清理孤儿 tool_calls：
// DB 中若某条 assistant 消息声明了 tool_calls，但对应的 tool 结果消息被手动删除，
// 则 DeepSeek API 会返回 400 "insufficient tool messages following tool_calls message"。
// 此函数扫描消息列表，将没有匹配 tool 结果的 tool_call 剔除，并记录日志。
func sanitizeOrphanToolCalls(msgs []llm.Message) {
	for i := 0; i < len(msgs); i++ {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		// 收集该 assistant 后面连续 role=tool 消息的 tool_call_id
		seen := make(map[string]bool)
		for j := i + 1; j < len(msgs); j++ {
			if msgs[j].Role == "tool" {
				seen[msgs[j].ToolCallID] = true
			} else {
				break
			}
		}
		// 剔除孤儿 tool_call
		var kept []llm.ToolCall
		for _, tc := range msgs[i].ToolCalls {
			if seen[tc.ID] {
				kept = append(kept, tc)
			} else {
				log.Printf("[orch] sanitize: dropping orphan tool_call id=%s func=%s — no matching tool result in history", tc.ID, tc.Function.Name)
			}
		}
		if len(kept) != len(msgs[i].ToolCalls) {
			msgs[i].ToolCalls = kept
		}
	}
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

// nativeToolCallsToInternal 将原生 function calling 的 ToolCall 转为内部 tool.ToolCall。
// 按 "__" 拆分函数名，得到 tool 名和 func 名。
func nativeToolCallsToInternal(tcs []llm.ToolCall) []tool.ToolCall {
	out := make([]tool.ToolCall, len(tcs))
	for i, tc := range tcs {
		toolName, funcName := splitToolFuncName(tc.Function.Name)
		out[i] = tool.ToolCall{
			Tool:     toolName,
			Function: funcName,
			Args:     json.RawMessage(tc.Function.Arguments),
		}
	}
	return out
}

// splitToolFuncName 按 "__" 拆分名称，返回 (toolName, funcName)。
func splitToolFuncName(name string) (string, string) {
	idx := strings.LastIndex(name, "__")
	if idx < 0 {
		return name, name
	}
	return name[:idx], name[idx+2:]
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
