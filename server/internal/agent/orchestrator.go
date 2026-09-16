package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
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

// finalRoundPrompt — 达到 max_iterations 上限后的收尾轮指令。
// 作为 assistant 消息追加到 state.msgs（仅内存，不落库），要求 LLM 停止工具调用、输出总结。
const finalRoundPrompt = `[系统] 本轮已达本任务的最大步数限制。请不要再调用任何工具，直接输出最终回复。
回复须诚实说明完成情况，必须包含：
1. 已完成：概括本轮完成的工作与关键结论
2. 未完成：说明因步骤限制未能覆盖的部分
3. 后续建议：若需要继续，下一步可以做什么
如信息已充分，正常给出最终回答即可，不必提及内部限制。`

// WindowResult 是扩张窗口计算的结果，描述本次请求的窗口大小和 session 状态。
type WindowResult struct {
	Size              int    // 本次窗口轮数
	IsNewSession      bool   // 本条消息开启了新 session
	SkipHistory       bool   // 新窗口首条不重放旧消息（Size 仍记录，供下一条窗口扩张）
	SessionStartMsgID string // 当前 session 的起始消息 ID（新 session 为空，orchestrator 用本条 msgID 填充）
	PrevUserXML       string // 上一条 user 消息的 XMLContext（资源区增量来源，空=无上一条）
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
	ClientTools        []ClientTool // 客户端（Mac App）上报的本地 MCP 工具，可为空
	Mode               string       // 会话模式：model.ConvModeWork（默认）| model.ConvModePlan（全部工具被禁用）
	ModeNotice         string       // 模式提示（注入当前 user 消息 XMLContext 的 notice 块，仅 LLM 可见，用户不可见）
	VerifyEnabled      *bool        // 响应检验开关覆盖：nil=跟随全局配置，true/false=强制覆盖

	// 资源区方案：上一条 user 消息的 XMLContext（增量资源区来源，零额外 DB 读）。
	// 历史消息不再重放完整 XML；注入资源收敛到当前消息的公共资源区。
	PreviousUserXMLContext string

	// RecentUserMessages 新窗口（SkipHistory）时由 ReduceRecentRounds 压缩的最近对话，
	// 时间正序的 user / assistant 交替序列（assistant 仅最终文本回复，不含 tool call）。
	// 渲染后注入资源区 recent 段，供 LLM 知晓上个窗口用户说了什么、自己回了什么。
	RecentUserMessages []model.Message

	// UserImages — 本轮用户消息携带的图片（已解析为 Files API file_id）
	UserImages []llm.ImageRef
	// UserImageIDs — 本轮图片的图床记录 id（用于消息落库 + 标记引用，顺带驱动清理保留期）
	UserImageIDs []string

	// 检索参数覆盖（0 值表示使用 PromptConfig 默认值）
	Memory  retrieval.SourceConfig
	Note    retrieval.SourceConfig
	History retrieval.SourceConfig
	Theme   retrieval.SourceConfig
	Summary retrieval.SourceConfig

	// HiddenRound 隐藏轮：本轮所有消息（user/assistant/tool）落库为隐藏状态，
	// 不进对话记录/LLM 上下文/检索/后处理。接力等控制轮用。
	HiddenRound bool
}

// reactState — runReActLoop 循环过程中的可变状态
type reactState struct {
	msgs                   []llm.Message
	allToolCalls           []tool.ToolCall
	lastAssistantMsgID     string
	userMsgID              string
	verifyRetries          int  // 本轮对话已重检验次数
	nativeFCTextDeferred   bool // 原生 FC 下文本因 verify 暂缓推送
	jsonBuffered           bool // 原生 FC 下文本首字符为 { 暂缓推送，防 JSON 泄露
	clientToolsUnavailable bool // 客户端断连/超时后置位，本次 Run 内后续客户端工具调用快速失败
	finalRound             bool // 达到 max_iterations 后的收尾轮：禁用工具，强制输出总结
	maxPromptTokens        int  // 本次 Run 内主循环各轮 prompt_tokens 最大值（即当前上下文大小）

	// 渐进式工具暴露：本会话窗口内已启用的函数（仅 native FC 主循环使用）。
	// key = "组名.函数名"（如 "note.search"）。
	exposedTools map[string]bool
	expMutex     sync.Mutex // 并发工具调用写 exposedTools 用
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

type profileFactGetter interface {
	GetActiveByUser(ctx context.Context, userID string) ([]model.UserProfileFact, error)
}

// convToucher — Orchestrator 需要的会话更新/读取能力（*service.ConversationRepo 自动满足）
type convToucher interface {
	Touch(ctx context.Context, id string) error
	GetExtra(ctx context.Context, convID string) (*model.ConversationExtra, error)
	UpdateExtraField(ctx context.Context, convID string, fn func(extra *model.ConversationExtra)) error
}

// promptBuilder — Orchestrator 需要的 prompt 构建能力（*promptbuilder.PromptBuilder 自动满足）
type promptBuilder interface {
	Build(ctx context.Context, opts promptbuilder.BuildOptions) (*promptbuilder.BuildResult, error)
}

// Orchestrator — ReAct 循环编排器
type Orchestrator struct {
	flashClient             llm.LLMClient
	proClient               llm.LLMClient
	registry                *tool.Registry
	systemPromptBuilder     promptBuilder
	msgRepo                 msgCreator
	convRepo                convToucher
	subMsgRepo              *service.MessageRepo      // 具体类型（subAgent 落库用），与 msgRepo 同源
	subConvRepo             *service.ConversationRepo // 具体类型（subAgent 建子会话用），与 convRepo 同源
	embedder                *llm.Embedder
	vectorStore             *VectorStore
	embeddingRepo           *service.EmbeddingRepo
	maxIterations           int
	defaultTemperature      *float64
	defaultMaxTokens        *int
	contextWindow           int // 弱模型上下文窗口 token 上限
	strongContextWindow     int // 强模型上下文窗口 token 上限
	maxToolResultChars      int
	maxConcurrency          int
	onChatEnd               func(userID, convID, userMsgID string)
	verifyEnabled           bool
	verifyMaxRetries        int
	verifyModel             string // "flash" | "pro"
	useNativeFC             bool
	toolExcludes            []string // 对 LLM 隐藏的工具名（控 token 成本）
	subAgentOnlyClientTools []string // 仅 subAgent 可用的客户端工具组（主循环对 LLM 隐藏）
	debugPersistJSON        bool
	debugJSONSink           DebugJSONSink // request_json 异步落库 sink（开关开启时必须注入）
	clientBridge            *ClientToolBridge
	clientToolTimeout       time.Duration
	subAgentMaxIterations   int
	subAgentTimeout         time.Duration
	subAgentCleanupTimeout  time.Duration // 子任务结束标签清理单 tab 超时，默认 10s
	subAgentLocks           sync.Map      // 子会话执行互斥（按 convID，续跑串行化防并发写乱序）
	summaryRepo             *service.ConversationSummaryRepo
	contextRetriever        *ctxflow.ContextRetriever // 上下文检索
	contextFormatter        ctxflow.ContextFormatter  // 上下文格式化（接口）
	reminderRepo            *service.ReminderRepo
	profileFactRepo         profileFactGetter
	imageStore              *service.ImageStore // 图床（懒补 file_id / 标记引用）

	// 摘要附着控制
	summaryAttachCount        int
	summaryAttachMaxDays      int
	summaryRelevanceThreshold float64
	summaryRelevanceHistoryN  int
	summaryDedupThreshold     float64

	// 公共资源区控制
	resourceZoneEnabled bool
	resourceZoneBudget  int                 // 总预算帽（chars）
	resourceZoneBudgets ctxflow.ZoneBudgets // 每类型预算（chars）
	resourceZoneQuotas  ctxflow.ZoneQuotas
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
	ContextWindow             int // 弱模型上下文窗口 token 上限（UI 展示用，0=未配置）
	StrongContextWindow       int // 强模型上下文窗口 token 上限
	MaxToolResultChars        int
	ToolMaxConcurrency        int
	OnChatEnd                 func(userID, convID, userMsgID string)
	VerifyEnabled             bool
	VerifyMaxRetries          int
	VerifyModel               string            // "flash" | "pro"，默认 flash
	UseNativeFC               bool              // 是否使用原生 function calling
	ToolExcludes              []string          // 对 LLM 隐藏的工具名列表（控 token 成本）
	SubAgentOnlyClientTools   []string          // 仅 subAgent 可用的客户端工具组（主循环对 LLM 隐藏），默认 ["browser","fs","shell"]
	DebugPersistJSON          bool              // 是否持久化完整 LLM 请求 JSON（异步限频写入，配 debug_json 控制频率与保留）
	DebugJSONSink             DebugJSONSink     // request_json 异步落库 sink，DebugPersistJSON 开启时必填
	ClientBridge              *ClientToolBridge // 客户端工具桥（客户端工具调用的 pending 路由）
	ClientToolTimeoutSecs     int               // 客户端工具执行超时秒数，默认 60
	SubAgentMaxIterations     int               // 子 agent 最大轮次（含强制收敛轮），默认 20（防死循环兜底）
	SubAgentTimeoutSecs       int               // 子 agent 执行超时秒数，默认 600（真实预算）
	SummaryRepo               *service.ConversationSummaryRepo
	ContextRetriever          *ctxflow.ContextRetriever // 上下文检索
	ContextFormatter          ctxflow.ContextFormatter  // 上下文格式化
	SummaryAttachCount        int                       // 摘要附着条数上限，默认 10
	SummaryAttachMaxDays      int                       // 摘要附着时间窗口（天），默认 3
	SummaryRelevanceThreshold float64                   // 摘要相关性阈值，默认 0.5
	SummaryRelevanceHistoryN  int                       // 相关性检查的历史消息窗口，默认 5
	SummaryDedupThreshold     float64                   // 摘要附着去重阈值，默认 0.85
	ResourceZoneEnabled       bool                      // 公共资源区开关
	ResourceZoneBudget        int                       // 资源区总预算帽（chars）
	ResourceZoneBudgets       ctxflow.ZoneBudgets       // 资源区每类型预算（chars）
	ResourceZoneQuotas        ctxflow.ZoneQuotas        // 资源区每类型配额
	ReminderRepo              *service.ReminderRepo
	ProfileFactRepo           *service.UserProfileFactRepo
	ImageStore                *service.ImageStore // 图床（图片输入：懒补 file_id、标记引用）
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
	if cfg.ClientToolTimeoutSecs <= 0 {
		// 默认 120s：覆盖客户端 L3 工具审批（60s 超时视为拒绝）+ 执行余量（0.9×下发值）
		cfg.ClientToolTimeoutSecs = 120
	}
	if cfg.SubAgentMaxIterations <= 0 {
		// 兜底防死循环（timeout 才是真实预算上限）；不设太大——轮次太多任务转不完
		cfg.SubAgentMaxIterations = 20
	}
	if cfg.SubAgentTimeoutSecs <= 0 {
		cfg.SubAgentTimeoutSecs = 600
	}
	if cfg.DebugPersistJSON && cfg.DebugJSONSink == nil {
		panic("[orch] debug_persist_json 开启但 DebugJSONSink 为 nil（接线 bug）")
	}

	return &Orchestrator{
		flashClient:               cfg.FlashClient,
		proClient:                 cfg.ProClient,
		registry:                  cfg.Registry,
		systemPromptBuilder:       cfg.PromptBuilder,
		msgRepo:                   cfg.MsgRepo,
		convRepo:                  cfg.ConvRepo,
		subMsgRepo:                cfg.MsgRepo,
		subConvRepo:               cfg.ConvRepo,
		embedder:                  cfg.Embedder,
		vectorStore:               cfg.VectorStore,
		embeddingRepo:             cfg.EmbeddingRepo,
		maxIterations:             cfg.MaxIterations,
		defaultTemperature:        cfg.Temperature,
		defaultMaxTokens:          cfg.MaxTokens,
		contextWindow:             cfg.ContextWindow,
		strongContextWindow:       cfg.StrongContextWindow,
		maxToolResultChars:        cfg.MaxToolResultChars,
		maxConcurrency:            cfg.ToolMaxConcurrency,
		onChatEnd:                 cfg.OnChatEnd,
		verifyEnabled:             cfg.VerifyEnabled,
		verifyMaxRetries:          cfg.VerifyMaxRetries,
		verifyModel:               cfg.VerifyModel,
		useNativeFC:               cfg.UseNativeFC,
		toolExcludes:              cfg.ToolExcludes,
		subAgentOnlyClientTools:   cfg.SubAgentOnlyClientTools,
		debugPersistJSON:          cfg.DebugPersistJSON,
		debugJSONSink:             cfg.DebugJSONSink,
		clientBridge:              cfg.ClientBridge,
		clientToolTimeout:         time.Duration(cfg.ClientToolTimeoutSecs) * time.Second,
		subAgentMaxIterations:     cfg.SubAgentMaxIterations,
		subAgentTimeout:           time.Duration(cfg.SubAgentTimeoutSecs) * time.Second,
		subAgentCleanupTimeout:    10 * time.Second,
		summaryRepo:               cfg.SummaryRepo,
		contextRetriever:          cfg.ContextRetriever,
		contextFormatter:          cfg.ContextFormatter,
		summaryAttachCount:        cfg.SummaryAttachCount,
		summaryAttachMaxDays:      cfg.SummaryAttachMaxDays,
		summaryRelevanceThreshold: cfg.SummaryRelevanceThreshold,
		summaryRelevanceHistoryN:  cfg.SummaryRelevanceHistoryN,
		summaryDedupThreshold:     cfg.SummaryDedupThreshold,
		resourceZoneEnabled:       cfg.ResourceZoneEnabled,
		resourceZoneBudget:        cfg.ResourceZoneBudget,
		resourceZoneBudgets:       cfg.ResourceZoneBudgets,
		resourceZoneQuotas:        cfg.ResourceZoneQuotas,
		reminderRepo:              cfg.ReminderRepo,
		profileFactRepo:           cfg.ProfileFactRepo,
		imageStore:                cfg.ImageStore,
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

	// 资源区增量来源：上一条 user 消息的 XMLContext（computeWindowSize 已查，零额外 DB 读）
	req.PreviousUserXMLContext = req.Window.PrevUserXML

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

	log.Printf("[orch] run start: model=%s, msgs=%d, client_tools=%d", o.pickClient(req).ModelName(), len(req.Messages), len(req.ClientTools))

	// 1. 静态 system prompt + 稳定上下文（画像+摘要，会话内字节稳定）
	// 客户端工具目录：native FC 且非 plan 模式时生成（客户端工具经 expose_tools 渐进暴露，
	// 不再无条件下发；目录并入「工具使用」段，是 LLM 发现客户端工具的唯一渠道）。
	// prompt-eng 模式本就不支持客户端工具，目录留空。
	clientCatalog := ""
	if o.useNativeFC && req.Mode != model.ConvModePlan {
		clientCatalog = clientToolCatalog(filterSubAgentOnlyClientTools(req.ClientTools, o.subAgentOnlyClientTools), o.registry)
	}
	stableXML := o.buildStableContextXML(ctx, req)
	result, err := o.systemPromptBuilder.Build(ctx, promptbuilder.BuildOptions{
		UserID:            req.UserID,
		AgentPrompt:       req.AgentSystemPrompt,
		Mode:              req.Mode,
		StableContextXML:  stableXML,
		ClientToolCatalog: clientCatalog,
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
	defer func() { monitor.RecordRequestLatency(time.Since(loopStart).Milliseconds()) }()

	userMsg := o.buildUserMessage(ctx, req, retrieveResult)

	if err := o.msgRepo.Create(ctx, userMsg); err != nil {
		log.Printf("[orch] save user msg error: %v", err)
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrInternal)})
		return
	}
	util.SafeGo(func() { o.convRepo.Touch(ctx, req.ConversationID) })
	// 隐藏轮不落用户消息向量：避免接力提示词污染历史检索/记忆
	if !req.HiddenRound {
		util.SafeGo(func() { o.saveUserMsgEmbedding(userMsg.ID, queryVec, req.UserID, req.ConversationID) })
	}

	// 图片已挂到消息上 → 标记为"被引用"，驱动清理保留期（未被引用的孤儿图短窗口就删）
	if o.imageStore != nil && len(req.UserImageIDs) > 0 {
		if err := o.imageStore.Repo().MarkReferenced(ctx, req.UserImageIDs, req.UserID); err != nil {
			log.Printf("[orch] mark images referenced error: %v", err)
		}
	}

	// 在整个 ReAct 循环结束后通知外部（用于入队延迟任务：post_chat + summary）。
	// 隐藏轮跳过：其消息不进对话记录，后处理无内容可做。
	if o.onChatEnd != nil && !req.HiddenRound {
		defer o.onChatEnd(req.UserID, req.ConversationID, userMsg.ID)
	}

	state := &reactState{
		msgs:      append(req.Messages, llm.Message{Role: "user", Content: *userMsg.XMLContext, Images: req.UserImages}),
		userMsgID: userMsg.ID,
	}
	o.loadExposure(ctx, req, userMsg, state)
	defer o.persistExposure(ctx, req, userMsg, state)

	maxIter := o.maxIterations
	if req.MaxIterations > 0 {
		maxIter = req.MaxIterations
	}
	for iter := 0; iter < maxIter; iter++ {
		if o.runOneRound(ctx, events, o.pickClient(req), req, systemPrompt, retrieveResult, state, iter) {
			return
		}
	}

	// 达到 max_iterations 仍未收敛：执行收尾轮（注入收尾指令 + 禁用工具，强制输出总结），
	// 不再直接报"任务未完成"硬错误。
	o.runFinalRound(ctx, events, req, systemPrompt, retrieveResult, state, maxIter)
}

// runFinalRound — 收尾轮：达到 max_iterations 后的最后一轮 LLM 调用。
// 在 state.msgs 追加收尾指令（仅内存不落库，避免污染历史前缀缓存），
// 并以空工具集调用 LLM，强制其输出最终总结。
func (o *Orchestrator) runFinalRound(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	req *RunRequest,
	systemPrompt string,
	retrieveResult *ctxflow.RetrieveResult,
	state *reactState,
	iter int,
) {
	state.finalRound = true
	state.msgs = append(state.msgs, llm.Message{Role: "assistant", Content: finalRoundPrompt})
	log.Printf("[orch] final round: max iterations reached, forcing summary (msgs=%d)", len(state.msgs))

	done := o.runOneRound(ctx, events, o.pickClient(req), req, systemPrompt, retrieveResult, state, iter)
	if !done {
		// 兜底：收尾轮仍未结束（如 prompt-eng 模式 LLM 仍返回工具调用），保留硬错误行为
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: UserErrorMessage(ErrMaxIterations)})
		o.emitDone(events, req.ConversationID, state.lastAssistantMsgID, state.userMsgID, "max iterations reached", idsByTypeFromResult(retrieveResult), state.maxPromptTokens, o.contextWindowFor(req.DeepMode && o.proClient != nil))
	}
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

// exposureWindowKey 返回当前会话窗口的暴露 key（与 prepareUserMsg 的 SessionStart 同口径）。
func exposureWindowKey(req *RunRequest, userMsg *model.Message) string {
	if req.Window.IsNewSession {
		return userMsg.ID
	}
	return req.Window.SessionStartMsgID
}

// loadExposure 从会话 Extra 加载本窗口的已暴露函数。
// key 不匹配（新窗口/无记录）→ 空集。仅 native FC 主循环使用。
// 兼容旧格式：裸组名条目（无 "."）展开为该组全部函数。
func (o *Orchestrator) loadExposure(ctx context.Context, req *RunRequest, userMsg *model.Message, state *reactState) {
	state.exposedTools = map[string]bool{}
	if !o.useNativeFC || req.Mode == model.ConvModePlan {
		return
	}
	extra, err := o.convRepo.GetExtra(ctx, req.ConversationID)
	if err != nil {
		log.Printf("[orch] loadExposure GetExtra error: %v", err)
		return
	}
	if extra == nil || extra.ToolExposure == nil {
		return
	}
	if extra.ToolExposure.SessionStartMsgID != exposureWindowKey(req, userMsg) {
		return // 旧窗口记录，自然失效
	}
	for _, entry := range extra.ToolExposure.Tools {
		if strings.Contains(entry, ".") {
			state.exposedTools[entry] = true
			continue
		}
		if t := o.registry.Get(entry); t != nil {
			for _, f := range t.Functions() {
				state.exposedTools[entry+"."+f.Name] = true
			}
		}
	}
}

// persistExposure 把本窗口的已暴露函数写回会话 Extra。
// 走事务内的 UpdateExtraField 读-改-写，只改 ToolExposure 字段，绝不覆盖并发写入的其它字段
// （used_tool_ids / subagent_type 等）。空集不写（避免无意义 DB 写）。
// 持久化失败降级为日志（下次 run 重新暴露，无副作用）。
func (o *Orchestrator) persistExposure(ctx context.Context, req *RunRequest, userMsg *model.Message, state *reactState) {
	if !o.useNativeFC || req.Mode == model.ConvModePlan || len(state.exposedTools) == 0 {
		return
	}
	state.expMutex.Lock()
	entries := make([]string, 0, len(state.exposedTools))
	for e := range state.exposedTools {
		entries = append(entries, e)
	}
	sort.Strings(entries)
	state.expMutex.Unlock()

	key := exposureWindowKey(req, userMsg)
	err := o.convRepo.UpdateExtraField(ctx, req.ConversationID, func(extra *model.ConversationExtra) {
		if extra.ToolExposure == nil {
			extra.ToolExposure = &model.ToolExposure{}
		}
		extra.ToolExposure.SessionStartMsgID = key
		extra.ToolExposure.Tools = entries
	})
	if err != nil {
		log.Printf("[orch] persistExposure UpdateExtraField error: %v", err)
	}
}

// buildSummaryPackets 拉取当前会话最近的对话摘要，构建摘要 ContextPacket 列表。
// 口径：最多 summaryAttachCount 条，且都在最近 summaryAttachMaxDays 天内；返回按时间旧→新排列。
func (o *Orchestrator) buildSummaryPackets(ctx context.Context, req *RunRequest) []retrieval.ContextPacket {
	if o.summaryRepo == nil {
		log.Printf("[orch] WARNING: summaryRepo is nil, skip summary attachment (check wiring in appinit)")
		return nil
	}
	count := o.summaryAttachCount
	if count <= 0 {
		count = 1
	}
	days := o.summaryAttachMaxDays
	if days <= 0 {
		days = 3
	}

	now := time.Now()
	summaries, err := o.summaryRepo.ListByTimeRange(ctx, req.UserID, req.ConversationID, now.AddDate(0, 0, -days), now, count)
	if err != nil {
		log.Printf("[orch] ListByTimeRange for summary attach error: %v", err)
		return nil
	}
	if len(summaries) == 0 {
		return nil
	}

	// 相邻摘要按 embedding 余弦相似度判重：更相似则丢弃较旧者（增量合并生成的相邻摘要内容高度重叠）。
	// embedding 走 getOrEmbedSummary 缓存（VectorStore/DB），无额外 embed 调用。
	summaries = o.dedupSummaries(ctx, summaries)

	// repo 返回时间倒序，翻转为旧→新，便于 LLM 按时间线阅读
	pkts := make([]retrieval.ContextPacket, 0, len(summaries))
	for i := len(summaries) - 1; i >= 0; i-- {
		s := summaries[i]
		parent := ""
		if s.ParentSummaryID != nil {
			parent = *s.ParentSummaryID
		}
		pkts = append(pkts, retrieval.ContextPacket{
			ID:    s.ID,
			Type:  ctxflow.TypeSummary,
			Text:  s.SummaryText,
			Score: 1.0,
			Meta: retrieval.SummaryMeta{
				SummaryID:       s.ID,
				SummaryText:     s.SummaryText,
				ConversationID:  s.ConversationID,
				CoverFromMsgID:  s.CoverFromMsgID,
				CoverToMsgID:    s.CoverToMsgID,
				ParentSummaryID: parent,
				Topics:          s.Topics,
				CreatedAt:       s.CreatedAt,
			},
		})
	}
	log.Printf("[orch] buildSummaryPackets: attached %d summaries (window=%dd, limit=%d)", len(pkts), days, count)
	return pkts
}

// buildStableContextXML 构建稳定上下文（画像 + 近期摘要）的 XML，注入 system prompt。
// 两类内容会话内基本不变（画像只在后台提取更新时变、摘要在会话边界变化），
// 放进 system prompt 使请求前缀字节稳定 → Prompt Cache 全程命中。
// 预算沿用资源区配置的 Profile/Summary 预算，总帽 = 两者之和；确定性排序（profile 按 key、summary 按时间）。
// 依赖仓库为 nil 时（replay/测试场景）跳过，返回空串。
func (o *Orchestrator) buildStableContextXML(ctx context.Context, req *RunRequest) string {
	var pkts []retrieval.ContextPacket
	if o.summaryRepo != nil {
		pkts = append(pkts, o.buildSummaryPackets(ctx, req)...)
	}
	if o.profileFactRepo != nil {
		pkts = append(pkts, o.buildProfilePackets(ctx, req.UserID)...)
	}
	if len(pkts) == 0 {
		return ""
	}
	budgets := o.resourceZoneBudgets
	total := budgets.Profile + budgets.Summary
	pkts = ctxflow.BudgetResourceZone(pkts, budgets, total)
	return ctxflow.FormatResourceZone(pkts)
}

// buildUserMessage 构建 user 消息的完整内容并写入 userMsg.XMLContext。
func (o *Orchestrator) buildUserMessage(
	ctx context.Context,
	req *RunRequest,
	retrieveResult *ctxflow.RetrieveResult,
) *model.Message {

	userMsg := o.prepareUserMsg(req)

	var extraPackets = []retrieval.ContextPacket{ctxflow.DateTimePacket(time.Now())}

	// 模式提示（计划模式约束 / 退出提示）：注入 notice 块，仅 LLM 可见
	if req.ModeNotice != "" {
		extraPackets = append(extraPackets, ctxflow.NoticePacket(req.ModeNotice))
	}

	reminderPkts := o.buildReminderPackets(ctx, req.UserID)
	extraPackets = append(extraPackets, reminderPkts...)

	// —— 公共资源区：资源类检索结果（memory/note/theme）收敛到资源区 ——
	// 增量式：只解析上一条消息的 XMLContext + 本轮检索 merge（去重 → quota → 预算帽），
	// 历史消息重放不再携带注入资源（BuildMessagesForFlash 已简化）。
	// profile/summary 已移入 system prompt 稳定上下文，不再进资源区。
	var zoneXML string
	if o.resourceZoneEnabled {
		var zoneFresh []retrieval.ContextPacket
		if retrieveResult != nil {
			for typ, pkts := range retrieveResult.Packets {
				switch typ {
				case ctxflow.TypeMemory, ctxflow.TypeNote, ctxflow.TypeTheme:
					zoneFresh = append(zoneFresh, pkts...)
				}
			}
		}
		// 新窗口首条注入最近对话（用户 + AI 最终回复）到资源区，随后随增量携带、新窗口重置
		if req.Window.IsNewSession {
			zoneFresh = append(zoneFresh, buildRecentUserPackets(req.RecentUserMessages)...)
		}
		zonePkts := ctxflow.MergeResourceZone(
			ctxflow.ParseResourceZone(req.PreviousUserXMLContext),
			zoneFresh,
			o.resourceZoneQuotas,
		)
		zonePkts = ctxflow.BudgetResourceZone(zonePkts, o.resourceZoneBudgets, o.resourceZoneBudget)
		if len(zonePkts) > 0 {
			zoneXML = ctxflow.FormatResourceZone(zonePkts)
		}
	}

	// 非资源类 packets（history/reminders/datetime/notice）+ user_query 走常规格式化。
	// 资源类 packets 已进资源区，从常规注入中剔除（避免重复）。
	var packets map[string][]retrieval.ContextPacket
	if zoneXML != "" {
		packets = make(map[string][]retrieval.ContextPacket)
		for _, pkt := range extraPackets {
			switch pkt.Type {
			case ctxflow.TypeMemory, ctxflow.TypeNote, ctxflow.TypeTheme, ctxflow.TypeSummary, ctxflow.TypeProfile:
				continue
			}
			packets[pkt.Type] = append(packets[pkt.Type], pkt)
		}
		if retrieveResult != nil {
			for typ, pkts := range retrieveResult.Packets {
				switch typ {
				case ctxflow.TypeMemory, ctxflow.TypeNote, ctxflow.TypeTheme, ctxflow.TypeSummary:
					continue
				}
				packets[typ] = append(packets[typ], pkts...)
			}
		}
	} else {
		packets = ctxflow.MergePackets(retrieveResult, extraPackets...)
	}

	userContent := o.contextFormatter.FormatUserMessage(packets, req.UserMessageContent)
	if zoneXML != "" {
		// 资源区放最前（显式工作记忆），与常规块 + user_query 拼接
		userContent = zoneXML + "\n" + userContent
	}
	userMsg.XMLContext = &userContent
	return userMsg
}

// buildReminderPackets 查询用户 pending 提醒，生成 ContextPacket 列表注入 user message。
// 长提醒（>120 字）中间截断，保留 deadline（尾部），LLM 可调 search_reminders 查看全文。
func (o *Orchestrator) buildReminderPackets(ctx context.Context, userID string) []retrieval.ContextPacket {
	if o.reminderRepo == nil {
		return nil
	}
	now := time.Now()
	upcoming, _ := o.reminderRepo.ListUpcoming(ctx, userID, now, reminderUpcomingWindow, 5)
	expired, _ := o.reminderRepo.ListExpiredUnmentioned(ctx, userID, now, 3)

	var packets []retrieval.ContextPacket
	for _, r := range upcoming {
		packets = append(packets, retrieval.ContextPacket{
			ID:   r.ID,
			Type: ctxflow.TypeReminder,
			Text: truncateReminderInjection(fmt.Sprintf("%s（%s）", r.Content, r.Deadline.In(config.BJ).Format("01-02 15:04"))),
		})
	}
	for _, r := range expired {
		ago := formatDuration(now.Sub(r.Deadline))
		packets = append(packets, retrieval.ContextPacket{
			ID:   r.ID,
			Type: ctxflow.TypeReminder,
			Text: truncateReminderInjection(fmt.Sprintf("%s（过期 %s）", r.Content, ago)),
		})
	}
	return packets
}

// truncateReminderInjection 长提醒注入中间截断（保留 deadline 等尾部信息）。
func truncateReminderInjection(text string) string {
	truncated := util.TruncateMiddle(text, reminderInjectedHead, reminderInjectedTail)
	if truncated != text {
		return truncated + "（已截断，可调 search_reminders 查看全文）"
	}
	return truncated
}

// 长提醒注入截断参数：单条超过 120 字时中间截断，保留头部 100 字与尾部 20 字。
const (
	reminderInjectedHead = 100
	reminderInjectedTail = 20
)

// reminderUpcomingWindow 只注入未来 2 天内的 pending 提醒，更远的留给 search_reminders。
const reminderUpcomingWindow = 48 * time.Hour

// buildProfilePackets 加载用户画像稳定层（facts 表 active 事实），按字段聚合后生成 ContextPacket。
// 画像为空时不注入任何块。字段按名字母序排序，保证块内顺序确定，缓存友好。
func (o *Orchestrator) buildProfilePackets(ctx context.Context, userID string) []retrieval.ContextPacket {
	facts, err := o.profileFactRepo.GetActiveByUser(ctx, userID)
	if err != nil {
		log.Printf("[orch] profile GetActiveByUser error: %v", err)
		return nil
	}
	byField := make(map[string]struct {
		values    []string
		updatedAt time.Time
	}, 8)
	for _, f := range facts {
		agg := byField[f.Field]
		agg.values = append(agg.values, f.Value)
		if f.CreatedAt.After(agg.updatedAt) {
			agg.updatedAt = f.CreatedAt
		}
		byField[f.Field] = agg
	}
	packets := make([]retrieval.ContextPacket, 0, len(byField))
	for field, agg := range byField {
		// 聚合用换行分隔每条 fact（行 = fact 边界，避免与 fact 内部的全角分号混淆）
		packets = append(packets, ctxflow.ProfilePacket(field, strings.Join(agg.values, "\n"), agg.updatedAt))
	}
	sort.SliceStable(packets, func(i, j int) bool {
		return packets[i].ID < packets[j].ID
	})
	return packets
}

// formatDuration 将时间间隔格式化为可读中文。
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
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

// dedupSummaries 相邻摘要按 embedding 余弦相似度判重：相似度 >= 阈值时丢弃较旧者，保留最新。
// summaries 需按时间旧→新排列（与 buildSummaryPackets 的阅读顺序一致）。
// embedding 缺失时乐观保留（不判重），避免误丢。
func (o *Orchestrator) dedupSummaries(ctx context.Context, summaries []model.ConversationSummary) []model.ConversationSummary {
	threshold := o.summaryDedupThreshold
	if threshold <= 0 {
		threshold = 0.85
	}
	result := make([]model.ConversationSummary, 0, len(summaries))
	for _, s := range summaries {
		if len(result) > 0 {
			last := &result[len(result)-1]
			cur := s
			lastVec := o.getOrEmbedSummary(ctx, last)
			vec := o.getOrEmbedSummary(ctx, &cur)
			if len(lastVec) > 0 && len(vec) > 0 && cosineSimilarity(lastVec, vec) >= threshold {
				// 相邻摘要内容重叠：丢弃较旧者（result 尾部即较旧者）
				result[len(result)-1] = s
				continue
			}
		}
		result = append(result, s)
	}
	return result
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
	if _, ok := llmcost.ScenarioFromContext(ctx); !ok {
		ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
			Category:    llmcost.CategoryMainChat,
			SubCategory: llmcost.SubReActLoop,
			UserID:      req.UserID,
			ConvID:      req.ConversationID,
		})
	}
	streamEvents, err := client.Chat(ctx, o.mkLlmChatReq(req, systemPrompt, state))
	if err != nil {
		log.Printf("[orch] round %d LLM error: %v", round+1, err)
		// 主动取消/停止（runCtx 被取消）：静默退出，不向用户报"服务出错"
		if ctx.Err() != nil {
			o.sendEvent(events, &llm.StreamEvent{Type: "done"})
			return true
		}
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
			if se.Usage != nil && se.Usage.PromptTokens > state.maxPromptTokens {
				state.maxPromptTokens = se.Usage.PromptTokens
			}
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

func (o *Orchestrator) mkLlmChatReq(req *RunRequest, systemPrompt string, state *reactState) *llm.ChatRequest {
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
	// plan 模式：仅注入只读工具定义（写操作执行级仍被拦截）；work 模式：渐进式暴露。
	if !state.finalRound && o.useNativeFC && req.Mode == model.ConvModePlan {
		tools = filterToolDefs(o.registry.ReadOnlyToolDefinitions(), o.toolExcludes)
	} else if !state.finalRound && o.useNativeFC {
		// 渐进式暴露：常驻 expose_tools + 已暴露函数（函数粒度）的完整定义 + 客户端工具。
		state.expMutex.Lock()
		exposed := make([]string, 0, len(state.exposedTools))
		for e := range state.exposedTools {
			exposed = append(exposed, e)
		}
		sort.Strings(exposed)
		state.expMutex.Unlock()
		tools = append(tools, o.exposeToolsDefinition())
		exposedDefs := o.registry.GetLLMToolsFiltered(exposedFuncTools(exposed))
		tools = append(tools, filterExposedFuncDefs(exposedDefs, exposed)...)
		tools = append(tools, filterExposedFuncDefs(buildClientToolDefs(filterSubAgentOnlyClientTools(req.ClientTools, o.subAgentOnlyClientTools), o.registry), exposed)...)
		tools = filterToolDefs(tools, o.toolExcludes)
	} else if len(req.ClientTools) > 0 && !state.finalRound {
		log.Printf("[orch] client_tools ignored: use_native_fc=false")
	}
	return &llm.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     state.msgs,
		Tools:        tools,
		Temperature:  temperature,
		MaxTokens:    maxTokens,
		UserID:       req.UserID,
		Thinking:     thinking,
	}
}

// exposeToolsDefinition 返回唯一常驻工具 expose_tools 的完整定义（供请求体 tools 字段）。
// 优先取注册表中的函数定义；未注册（测试/降级场景）回退内置定义，避免 schema 文案重复维护。
func (o *Orchestrator) exposeToolsDefinition() llm.ToolDefinition {
	t := o.registry.Get("expose_tools")
	if t == nil {
		fallback := tool.NewExposeToolsTool()
		fns := fallback.Functions()
		funcs := make([]llm.FuncDef, len(fns))
		for i, f := range fns {
			funcs[i] = llm.FuncDef{Name: f.Name, Description: f.Description, Parameters: f.Parameters}
		}
		return llm.ToolDefinition{Name: fallback.Name(), Description: fallback.Description(), Functions: funcs}
	}
	fns := t.Functions()
	funcs := make([]llm.FuncDef, len(fns))
	for i, f := range fns {
		funcs[i] = llm.FuncDef{Name: f.Name, Description: f.Description, Parameters: f.Parameters}
	}
	return llm.ToolDefinition{Name: t.Name(), Description: t.Description(), Functions: funcs}
}

// exposedFuncTools 从扁平暴露集（"组名.函数名"）推导去重后的组名集合（供 GetLLMToolsFiltered 使用）。
func exposedFuncTools(exposed []string) []string {
	seen := make(map[string]bool, len(exposed))
	names := make([]string, 0, len(exposed))
	for _, e := range exposed {
		name := e
		if i := strings.IndexByte(e, '.'); i >= 0 {
			name = e[:i]
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// filterExposedFuncDefs 按函数级暴露集过滤工具定义：每个 def 只保留 "组名.函数名" 在 exposed 中的
// FuncDef；过滤后无函数的 def 丢弃。exposed 为空时返回 nil。
func filterExposedFuncDefs(defs []llm.ToolDefinition, exposed []string) []llm.ToolDefinition {
	if len(exposed) == 0 {
		return nil
	}
	keep := make(map[string]bool, len(exposed))
	for _, e := range exposed {
		keep[e] = true
	}
	out := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		kept := make([]llm.FuncDef, 0, len(d.Functions))
		for _, fn := range d.Functions {
			if keep[d.Name+"."+fn.Name] {
				kept = append(kept, fn)
			}
		}
		if len(kept) > 0 {
			d.Functions = kept
			out = append(out, d)
		}
	}
	return out
}

// filterToolDefs 从工具定义中剔除配置排除的工具（按工具名精确匹配，控 token 成本）。
func filterToolDefs(defs []llm.ToolDefinition, excludes []string) []llm.ToolDefinition {
	if len(excludes) == 0 {
		return defs
	}
	excluded := make(map[string]bool, len(excludes))
	for _, n := range excludes {
		excluded[n] = true
	}
	kept := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if !excluded[d.Name] {
			kept = append(kept, d)
		}
	}
	return kept
}

func mkUserMsg(req *RunRequest) *model.Message {
	m := &model.Message{
		ID:             uuid.New().String(),
		ConversationID: req.ConversationID,
		UserID:         req.UserID,
		Role:           "user",
		Content:        req.UserMessageContent,
		Status:         msgStatusFor(req.HiddenRound),
	}
	if req.RawUserMessage != "" && req.RawUserMessage != req.UserMessageContent {
		raw := req.RawUserMessage
		m.OriginalContent = &raw
	}
	SetImageIDs(m, req.UserImageIDs)
	return m
}

// msgStatusFor 正常落库用 MsgStatusNormal，隐藏轮用 MsgStatusHidden。
func msgStatusFor(hidden bool) int {
	if hidden {
		return model.MsgStatusHidden
	}
	return model.MsgStatusNormal
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
		o.emitDone(events, req.ConversationID, state.lastAssistantMsgID, state.userMsgID, "", idsByTypeFromResult(retrieveResult), state.maxPromptTokens, o.contextWindowFor(req.DeepMode && o.proClient != nil))
	}
	return done
}

func newAssistantMessage(streamContent string, streamReasoning string, se *llm.StreamEvent) llm.Message {
	return llm.Message{
		Role:             "assistant",
		Content:          streamContent,
		ReasoningContent: streamReasoning,
		EncryptedContent: se.EncryptedContent,
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
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
		state.msgs = append(state.msgs, msg)
		state.allToolCalls = append(state.allToolCalls, internalTCs...)
		toolCallResults := o.executeConcurrently(ctx, events, req.UserID, req.ConversationID, o.registry, internalTCs, clientToolIndex(filterSubAgentOnlyClientTools(req.ClientTools, o.subAgentOnlyClientTools), o.registry), state, req.Mode, clientToolReadOnlyIndex(req.ClientTools), toToolClientInfos(req.ClientTools))
		state.msgs = o.collectToolResultsNative(ctx, internalTCs, msg.ToolCalls, toolCallResults, events, req.ConversationID, req.UserID, state.msgs, req.HiddenRound)
		return false
	}

	// 原生 FC 路径：无工具调用，纯文本回复（文本已在 runOneRound 中实时推送）
	if o.useNativeFC && len(msg.ToolCalls) == 0 {
		// 兜底：DeepSeek 偶发不走原生 FC，仍在 text 中输出 JSON
		if parsed, parseErr := ParseLLMResponse(msg.Content); parseErr == nil && parsed != nil {
			if len(parsed.ToolCalls) > 0 {
				return o.executePromptEngTools(ctx, events, msg, req, retrieveResult, state, parsed)
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
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
		return true
	}

	parsed, parseErr := ParseLLMResponse(msg.Content)

	if parseErr != nil {
		log.Printf("[orch] round parse error: %v, saving raw and retrying", parseErr)
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, nil, req.HiddenRound)
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
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
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
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
		return true
	}

	return o.executePromptEngTools(ctx, events, msg, req, retrieveResult, state, parsed)
}

// verifyResult — Flash 检验返回的结构
type verifyResult struct {
	Complete       bool     `json:"complete"`
	Hallucinations []string `json:"hallucinations"`
	MissingAspects []string `json:"missing_aspects"`
	Suggestion     string   `json:"suggestion"`
}

func (o *Orchestrator) shouldVerify(req *RunRequest, state *reactState) bool {
	enabled := o.verifyEnabled
	if req.VerifyEnabled != nil {
		enabled = *req.VerifyEnabled
	}
	return enabled && len(state.allToolCalls) > 0 && !req.SkipRetrieval && !state.finalRound
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
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
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

	// 构建检验请求——复用对话的 system prompt（与上一次主对话调用完全一致 → 前缀缓存命中），
	// 检验指令 + 被检验的对话上下文编码为追加的一条用户消息（不修改 system prompt 与历史）。
	verifyPrompt := o.buildVerifyPrompt(msg)
	verifyInstructions := `你是一个内容检验助手。请判断模型的回复是否完整、准确。

严格按以下 JSON 格式回复，不要加任何额外文字：
{"complete":true或false,"hallucinations":["模型捏造的事实，必须能在工具返回数据中找到依据，没有则为空数组"],"missing_aspects":["还缺少的信息维度，如未查笔记/未查记忆/未查账单等，没有则为空数组"],"suggestion":"针对缺失或幻觉的具体建议，complete为true且无幻觉时留空"}

检验标准：
- complete: 用户问题是否已被充分回答？信息源是否需要多角度交叉验证？
- hallucinations: 回复中的数据必须在【工具返回数据】或【system prompt 检索上下文】中找到依据。检索上下文中的信息是合法的已知事实，模型引用它们不算幻觉。
- 不要因为措辞风格标记为幻觉，只标记实质性的数据错误`

	// 完整历史 + 检验任务 = 最终消息列表；history 前缀与上次主对话调用一致 → 缓存命中，仅增量 token 付费
	verifyMsgs := make([]llm.Message, 0, len(state.msgs)+1)
	verifyMsgs = append(verifyMsgs, state.msgs...)
	verifyMsgs = append(verifyMsgs, llm.Message{Role: "user", Content: verifyInstructions + "\n\n" + verifyPrompt})

	// 保存检验请求到 DB（调试页可见）
	if _, ok := llmcost.ScenarioFromContext(ctx); !ok {
		ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
			Category:    llmcost.CategoryMainChat,
			SubCategory: llmcost.SubVerify,
			UserID:      req.UserID,
			ConvID:      req.ConversationID,
		})
	}
	verifyMsg, err := client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     verifyMsgs,
	})
	if err != nil {
		log.Printf("[verify] LLM call error: %v", err)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
		return true
	}

	var vr verifyResult
	if err := json.Unmarshal([]byte(verifyMsg.Content), &vr); err != nil {
		log.Printf("[verify] parse result error: %v, raw=%s", err, verifyMsg.Content)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
		return true
	}

	log.Printf("[verify] complete=%v hallucinations=%d missing=%d", vr.Complete, len(vr.Hallucinations), len(vr.MissingAspects))

	// 通过：无幻觉且完成
	if vr.Complete && len(vr.Hallucinations) == 0 {
		o.saveVerifyFeedback(ctx, req.ConversationID, req.UserID, "检验通过：无幻觉，回复完整", systemPrompt, verifyInstructions+"\n\n"+verifyPrompt, req.HiddenRound)
		log.Printf("[verify] round %d passed", state.verifyRetries)
		o.sendVerifyEvent(events, "done", state.verifyRetries, "")
		state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
		return true
	}

	// 不通过：存档草稿（IsInternal），注入反馈，继续
	log.Printf("[verify] round %d failed: complete=%v hallucinations=%d missing=%d", state.verifyRetries, vr.Complete, len(vr.Hallucinations), len(vr.MissingAspects))
	o.sendVerifyEvent(events, "retry", state.verifyRetries, vr.Suggestion)
	o.saveAssistantMsgInternal(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)

	feedback := o.buildVerifyFeedback(vr)
	o.saveVerifyFeedback(ctx, req.ConversationID, req.UserID, feedback, systemPrompt, verifyInstructions+"\n\n"+verifyPrompt, req.HiddenRound)
	state.msgs = append(state.msgs, llm.Message{Role: "assistant", Content: msg.Content})
	state.msgs = append(state.msgs, llm.Message{
		Role:    "user",
		Content: feedback,
	})
	return false
}

// buildVerifyPrompt 输出待检验的模型回复草稿。
// 对话历史（用户问题、工具调用、工具返回）经 verifyMsgs 以原始消息格式传给检验 LLM，
// 草稿回复是消息列表中尚不存在的内容，这里单独补充。
func (o *Orchestrator) buildVerifyPrompt(msg llm.Message) string {
	return "【模型回复草稿】\n" + msg.Content
}

// executePromptEngTools 统一处理 prompt engineering 工具调用路径：
// 保存 assistant 消息 → 注入排除 ID → 并发执行 → 收集结果。
// 返回 false（继续 ReAct 循环）。
func (o *Orchestrator) executePromptEngTools(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msg llm.Message,
	req *RunRequest,
	retrieveResult *ctxflow.RetrieveResult,
	state *reactState,
	parsed *LLMResponse,
) bool {
	msg.ToolCalls = nil
	state.lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, traceFromResult(retrieveResult), req.HiddenRound)
	state.msgs = append(state.msgs, llm.Message{Role: "assistant", Content: msg.Content})
	state.allToolCalls = append(state.allToolCalls, parsed.ToolCalls...)
	// 客户端工具只在原生 FC 模式下发给 LLM（mkLlmChatReq），
	// use_native_fc=false 时不参与分发，避免 LLM 幻觉出未告知的工具名被路由到客户端
	var clientIdx map[string]bool
	if o.useNativeFC {
		clientIdx = clientToolIndex(filterSubAgentOnlyClientTools(req.ClientTools, o.subAgentOnlyClientTools), o.registry)
	}
	toolCallResults := o.executeConcurrently(ctx, events, req.UserID, req.ConversationID, o.registry, parsed.ToolCalls, clientIdx, state, req.Mode, clientToolReadOnlyIndex(req.ClientTools), toToolClientInfos(req.ClientTools))
	state.msgs = o.collectToolResults(ctx, parsed, toolCallResults, events, req.ConversationID, req.UserID, state.msgs, req.HiddenRound)
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
func (o *Orchestrator) saveAssistantMsgInternal(ctx context.Context, convID, userID string, msg llm.Message, trace *ctxflow.RetrieveTrace, hidden bool) string {
	return o.saveAssistantMsgWithFlags(ctx, convID, userID, msg, trace, true, hidden)
}

// DebugJSONSink — 调试 JSON 异步持久化接口：消息落库后把 request_json 交给后台限频写入。
type DebugJSONSink interface {
	Submit(msgID string, reqJSON *string) bool
}

// NoopDebugJSONSink — debug_persist_json 关闭时的空实现。
type NoopDebugJSONSink struct{}

func (NoopDebugJSONSink) Submit(string, *string) bool { return false }

// saveVerifyFeedback 将检验反馈存为内部系统消息（含检验请求 request_json），调试页可见
func (o *Orchestrator) saveVerifyFeedback(ctx context.Context, convID, userID, feedback, systemPrompt, userPrompt string, hidden bool) {
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
	msg := &model.Message{
		ID:             uuid.New().String(),
		UserID:         userID,
		ConversationID: convID,
		Role:           "system",
		Content:        feedback,
		IsInternal:     true,
		Status:         msgStatusFor(hidden),
	}
	if err := o.msgRepo.Create(ctx, msg); err != nil {
		log.Printf("[verify] save feedback error: %v", err)
		return
	}
	// 消息落库成功后才异步刷 request_json（避免对不存在的行做无效 UPDATE）
	if o.debugPersistJSON {
		o.debugJSONSink.Submit(msg.ID, &reqStr)
	}
}

func (o *Orchestrator) saveAssistantMsgWithFlags(ctx context.Context, convID, userID string, msg llm.Message, trace *ctxflow.RetrieveTrace, isInternal bool, hidden bool) string {
	if o.msgRepo == nil || convID == "" {
		return ""
	}
	mid := uuid.New().String()
	modelMsg := &model.Message{
		ID:             mid,
		UserID:         userID,
		ConversationID: convID,
		Role:           "assistant",
		Content:        msg.Content,
		IsInternal:     isInternal,
		Status:         msgStatusFor(hidden),
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
	if msg.EncryptedContent != "" {
		ec := msg.EncryptedContent
		modelMsg.EncryptedContent = &ec
	}
	if tcJSON := extractToolCallsJSON(msg.Content); tcJSON != nil {
		modelMsg.ToolCallsJSON = tcJSON
	} else if len(msg.ToolCalls) > 0 {
		b, _ := json.Marshal(msg.ToolCalls)
		s := string(b)
		modelMsg.ToolCallsJSON = &s
	}
	if err := o.msgRepo.Create(ctx, modelMsg); err != nil {
		log.Printf("[orch] save assistant msg error: %v", err)
		return ""
	}
	// 消息落库成功后才异步刷 request_json（避免对不存在的行做无效 UPDATE）
	if o.debugPersistJSON && msg.RequestJSON != nil {
		o.debugJSONSink.Submit(mid, msg.RequestJSON)
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

func (o *Orchestrator) collectToolResults(ctx context.Context, parsed *LLMResponse, toolCallResults []toolExecResult, events chan<- *llm.StreamEvent, convID string, userID string, msgs []llm.Message, hidden bool) []llm.Message {
	for i := range parsed.ToolCalls {
		res := toolCallResults[i].result
		resJSON, err := json.Marshal(res)
		if err != nil {
			log.Printf("[orch] tool result marshal error: %v", err)
			resJSON = []byte(fmt.Sprintf(`{"success":false,"error":"marshal failed: %s"}`, res.Error))
		}
		o.sendEvent(events, &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)})
		o.saveToolMsg(ctx, convID, userID, idgen.Next(), string(resJSON), hidden)
		msgs = append(msgs, llm.Message{
			Role:    "user",
			Content: string(resJSON),
		})
	}
	return msgs
}

// collectToolResultsNative 原生 FC 路径：工具结果以 role=tool 保存，关联真实 tool_call_id。
func (o *Orchestrator) collectToolResultsNative(ctx context.Context, internalTCs []tool.ToolCall, nativeTCs []llm.ToolCall, toolCallResults []toolExecResult, events chan<- *llm.StreamEvent, convID string, userID string, msgs []llm.Message, hidden bool) []llm.Message {
	for i := range internalTCs {
		res := toolCallResults[i].result
		resJSON, err := json.Marshal(res)
		if err != nil {
			log.Printf("[orch] tool result marshal error: %v", err)
			resJSON = []byte(fmt.Sprintf(`{"success":false,"error":"marshal failed: %s"}`, res.Error))
		}
		o.sendEvent(events, &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)})
		o.saveToolMsg(ctx, convID, userID, nativeTCs[i].ID, string(resJSON), hidden)
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

// makeExposeFn 构造 expose_tools 的回调：组级校验（存在/未隐藏/未排除/非 expose_tools 自身，
// 客户端工具组按上报的 server_name 识别）+ 函数级校验（函数名存在于该组 Functions()），
// 合法函数写入 state.exposedTools（并发安全）。
// 组级失败以组名入 Rejected，函数级失败以 "组名.函数名" 入 Rejected。返回结果含可用组全集，供 LLM 纠正。
func (o *Orchestrator) makeExposeFn(state *reactState, clientGroups map[string]map[string]bool) func(specs []tool.ExposeSpec) *tool.ExposeResult {
	excluded := make(map[string]bool, len(o.toolExcludes))
	for _, n := range o.toolExcludes {
		excluded[n] = true
	}
	available := append([]string{}, o.registry.VisibleToolNames()...)
	for g := range clientGroups {
		available = append(available, g)
	}
	sort.Strings(available)
	return func(specs []tool.ExposeSpec) *tool.ExposeResult {
		res := &tool.ExposeResult{Enabled: []string{}, Rejected: []string{}, Available: available}
		state.expMutex.Lock()
		defer state.expMutex.Unlock()
		for _, spec := range specs {
			g := spec.Tool
			switch {
			case g == "expose_tools":
				res.Rejected = append(res.Rejected, g)
			case excluded[g]:
				res.Rejected = append(res.Rejected, g)
			case o.registry.Has(g) && !o.registry.IsHidden(g):
				o.exposeServerGroup(res, state, spec, g)
			case clientGroups[g] != nil:
				for _, fn := range spec.Functions {
					key := g + "." + fn
					if !clientGroups[g][fn] {
						res.Rejected = append(res.Rejected, key)
						continue
					}
					state.exposedTools[key] = true
					res.Enabled = append(res.Enabled, key)
				}
			default:
				res.Rejected = append(res.Rejected, g)
			}
		}
		return res
	}
}

// exposeServerGroup 校验并暴露一个服务端注册表工具组的函数。
func (o *Orchestrator) exposeServerGroup(res *tool.ExposeResult, state *reactState, spec tool.ExposeSpec, g string) {
	defined := make(map[string]bool)
	for _, f := range o.registry.Get(g).Functions() {
		defined[f.Name] = true
	}
	for _, fn := range spec.Functions {
		key := g + "." + fn
		if !defined[fn] {
			res.Rejected = append(res.Rejected, key)
			continue
		}
		state.exposedTools[key] = true
		res.Enabled = append(res.Enabled, key)
	}
}

// injectExcludeIDs 将 exclude_ids（已注入的记忆 ID）注入到 memory_search 工具的 args 中
func (o *Orchestrator) executeConcurrently(ctx context.Context, events chan<- *llm.StreamEvent, userID, convID string,
	registry *tool.Registry, toolCalls []tool.ToolCall, clientIdx map[string]bool, state *reactState,
	mode string, clientReadOnly map[string]bool, clientTools []tool.ClientToolInfo) []toolExecResult {
	// clientReadOnly 已不再参与 plan 拦截（2026-08-14 起 plan 模式全量禁用客户端工具），
	// 保留形参仅为最小改动与未来只读模式复用，勿再依赖。

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

	var clientCallIdxs []int // 客户端工具下标，串行执行（见设计文档第五节）

	clientGroups := clientToolGroupMap(filterSubAgentOnlyClientToolInfos(clientTools, o.subAgentOnlyClientTools))

	for i, tc := range toolCalls {
		if clientIdx[tc.Tool] {
			clientCallIdxs = append(clientCallIdxs, i)
			continue
		}
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
				ec := &tool.ExecuteContext{Ctx: ctx, UserID: userID, ConvID: convID, Mode: mode, Events: events, ClientTools: clientTools, Expose: o.makeExposeFn(state, clientGroups)}
				r = registry.ExecuteByName(ec, call.Tool, call.Function, call.Args)
			}()

			log.Printf("[orch] tool exec: %s.%s success=%v", call.Tool, call.Function, r.Success)
			results[idx] = toolExecResult{tc: call, result: r}
		}(i, tc)
	}

	for _, idx := range clientCallIdxs {
		tc := toolCalls[idx]
		if state.clientToolsUnavailable {
			// 本次 Run 内客户端已断连/超时，快速失败，避免每次调用都白等一个超时周期
			results[idx] = toolExecResult{tc: tc, result: &tool.Result{Success: false,
				Error: "客户端本地工具不可用（本轮对话中已发生断连或超时），已跳过 " + tc.Tool + "." + tc.Function + "。请不要再调用客户端本地工具，直接告知用户即可。"}}
			continue
		}
		// —— Plan 模式拦截：plan 模式下客户端工具全部禁用（2026-08-14 激进收紧，
		// 不再依据 IsReadOnly 放行——只读机制保留仅为未来"只读浏览模式"备用）。
		// 执行在客户端，服务端在此拦断——
		if mode == model.ConvModePlan {
			results[idx] = toolExecResult{tc: tc, result: &tool.Result{Success: false,
				Error: fmt.Sprintf("当前处于计划模式（只读讨论状态），本模式下已禁用全部客户端工具，禁止执行 %s.%s。请仅进行方案讨论；如需执行操作，请让用户切换到工作模式。", tc.Tool, tc.Function)}}
			continue
		}
		r, unavailable := o.executeClientTool(ctx, events, userID, tc, mode, "main")
		if unavailable {
			state.clientToolsUnavailable = true
		}
		results[idx] = toolExecResult{tc: tc, result: r}
	}

	wg.Wait()
	return results
}

// executeClientTool 将工具调用经桥中转到客户端执行，阻塞等待 HTTP 回传。
// 第二个返回值表示客户端不可用（断连/超时），调用方据此对本次 Run 内后续客户端工具快速失败。
// 实现委托共享的 clientToolCaller（orchestrator / MiniReAct 共用，见 client_tools.go）。
func (o *Orchestrator) executeClientTool(ctx context.Context, events chan<- *llm.StreamEvent, userID string, call tool.ToolCall, mode string, owner string) (*tool.Result, bool) {
	if o.clientBridge == nil {
		// 接线 bug：请求携带了 client_tools 却没有注入桥，炸在明处
		panic("[orch] clientBridge is nil but client tool call dispatched")
	}
	cc := &clientToolCaller{bridge: o.clientBridge, timeout: o.clientToolTimeout}
	return cc.Call(ctx,
		func(evt *llm.StreamEvent) bool {
			// 指令事件必须送达：阻塞发送 + 超时兜底。
			// chat 侧 orch.Run 用 context.Background()，客户端断连不会取消 ctx，
			// 断连后 events 无人消费会填满，靠 time.After 防止 goroutine 永久阻塞。
			select {
			case events <- evt:
				return true
			case <-time.After(5 * time.Second):
				return false
			case <-ctx.Done():
				return false
			}
		},
		func(evt *llm.StreamEvent) { o.sendEvent(events, evt) },
		userID, call, mode, owner) // 标签归属：主循环 "main"，子任务 "subagent:<convID>"
}

// —— DB 保存 ——

func (o *Orchestrator) saveAssistantMsg(ctx context.Context, convID, userID string, msg llm.Message, trace *ctxflow.RetrieveTrace, hidden bool) string {
	return o.saveAssistantMsgWithFlags(ctx, convID, userID, msg, trace, false, hidden)
}

func (o *Orchestrator) saveToolMsg(ctx context.Context, convID, userID, toolCallID string, content string, hidden bool) {
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
		Status:         msgStatusFor(hidden),
	}); err != nil {
		log.Printf("[orch] save tool msg error: %v", err)
		return
	}
	log.Printf("[orch] saved tool message: id=%s tool_call_id=%s", msgID, toolCallID)
}

// truncateToolResult 截断过长的工具结果，保留头尾，中间标注截断量。
// 按 rune 截断（复用 truncateStringKeepHeadTail）：早期按字节切片会在中文等多字节字符
// 中间劈开，产出非法 UTF-8，经 JSON 序列化后变成 �（子任务摘要、注入 LLM 的工具结果都中招）。
func truncateToolResult(content string, maxLen int) string {
	return truncateStringKeepHeadTail(content, maxLen)
}

// SimpleUserContent 生成历史 user 消息的简化内容：原文 + 时间戳（资源区方案）。
// 优先 OriginalContent（管线改动前的原始输入），否则用 Content（处理后的内容）。
// 时间戳按北京时间，格式 `[01-02 15:04] `。
func SimpleUserContent(m *model.Message) string {
	text := m.Content
	if m.OriginalContent != nil && *m.OriginalContent != "" {
		text = *m.OriginalContent
	}
	return fmt.Sprintf("[%s] %s", m.CreatedAt.In(config.BJ).Format("01-02 15:04"), text)
}

// ReduceRecentRounds 将新窗口加载的最近几轮压缩为「user + 非 tool call 的 assistant 最终回复」
// 的时间正序扁平序列（供 recent 资源区注入）。跳过 system、verify 内部草稿、工具调用中间
// assistant 及 tool 结果；某轮没有最终文本回复时只保留该轮 user。
func ReduceRecentRounds(rounds [][]model.Message) []model.Message {
	var out []model.Message
	for _, round := range rounds {
		flat := FlattenRound(round)
		var user *model.Message
		for i := range flat {
			if flat[i].Role == "user" {
				user = &flat[i]
				break
			}
		}
		if user == nil {
			continue
		}
		out = append(out, *user)
		// 逆序取该轮最后一条「非 tool call」assistant 作为最终回复
		for i := len(flat) - 1; i >= 0; i-- {
			m := flat[i]
			tc := m.ToolCallsJSON != nil && *m.ToolCallsJSON != ""
			content := strings.TrimSpace(m.Content)
			if m.Role != "assistant" || tc || content == "" || strings.HasPrefix(content, "{") {
				continue
			}
			out = append(out, m)
			break
		}
	}
	return out
}

// buildRecentUserPackets 构建新窗口注入的 recent 资源区段 packets。
// 输入为 ReduceRecentRounds 产出的时间正序 user/assistant 序列，每条消息一个 packet，
// 按 role 分块渲染（不含工具调用过程）。单条截断 500 字防超长粘贴。
// ponytail: 单条 500 字硬编码，需要更细粒度时提配置。
func buildRecentUserPackets(msgs []model.Message) []retrieval.ContextPacket {
	if len(msgs) == 0 {
		return nil
	}
	pkts := make([]retrieval.ContextPacket, 0, len(msgs))
	for _, m := range msgs {
		role := m.Role
		if role == "" {
			role = "user"
		}
		pkts = append(pkts, ctxflow.RecentPacket(m.ID, m.CreatedAt, role, recentText(m)))
	}
	return pkts
}

// recentText 取消息原文（优先 OriginalContent），单条截断 500 字。
func recentText(m model.Message) string {
	text := m.Content
	if m.OriginalContent != nil && *m.OriginalContent != "" {
		text = *m.OriginalContent
	}
	if r := []rune(text); len(r) > 500 {
		text = string(r[:500]) + "…"
	}
	return text
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
			content := m.Content
			if m.Role == "user" && m.XMLContext != nil && *m.XMLContext != "" {
				// 资源区方案：历史 user 消息不再重放完整 XMLContext（含当时注入的检索资源），
				// 只发原文 + 时间戳。注入资源收敛到最新消息的公共资源区，消除历史重复放大。
				content = SimpleUserContent(&m)
			}
			msg := llm.Message{ID: m.ID, Role: m.Role, Content: content}
			if m.Role == "user" {
				// 只带引用 id，具体 file_id 由 AttachReplayImages 按重放窗口解析（需 I/O + 懒上传）
				msg.ImageIDs = GetImageIDs(&m)
			}
			if m.ToolCallID != nil {
				msg.ToolCallID = *m.ToolCallID
			}
			// 必须回传 reasoning_content：ReAct 循环内发送的 assistant 消息带思维链，
			// 若重放时省略，请求字节与上一轮不一致，破坏 DeepSeek 上下文前缀缓存。
			// （DeepSeek 文档：工具调用轮的 reasoning_content 必须回传；纯文本轮回传会被忽略，无害）
			if m.ReasoningContent != nil {
				msg.ReasoningContent = *m.ReasoningContent
			}
			// 方舟思考模型要求回传 encrypted_content（加密思考原文），同理随消息恢复
			if m.EncryptedContent != nil {
				msg.EncryptedContent = *m.EncryptedContent
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
		msgs = sanitizeOrphanToolMsgs(msgs)
		sanitizeOrphanToolCalls(msgs)
	}
	return msgs
}

// sanitizeOrphanToolMsgs 清理孤儿 tool 消息（与 sanitizeOrphanToolCalls 反向）：
// role=tool 消息的 tool_call_id 必须能对应到其前侧最近一个携带 tool_calls 的
// assistant 消息，且两者之间不能被 user 等其他消息隔断。
//
// 产生背景：同一会话并发执行两个 ReAct 循环（旧循环客户端工具超时等待期间，
// 用户重发消息启动新循环）且写入交叉时，DB 会留下 assistant(tool_calls) → user →
// tool 的错位结构。DeepSeek 对 role=tool 校验"必须是前置 assistant tool_calls 的
// 响应"，此时返回 400 拒绝整个请求，会话后续所有消息报废。
//
// 孤儿 tool 消息降级为 role=user（保留工具结果内容，LLM 仍可引用），随后
// sanitizeOrphanToolCalls 会把对应 assistant 上已无结果的 tool_calls 剔除，
// 两者组合保证发给 LLM 的消息结构合法。
func sanitizeOrphanToolMsgs(msgs []llm.Message) []llm.Message {
	for i := range msgs {
		if msgs[i].Role != "tool" {
			continue
		}
		matched := false
		for j := i - 1; j >= 0; j-- {
			if msgs[j].Role == "tool" {
				continue
			}
			// 遇到非 tool 消息即停止：最近的相关消息不是 assistant(tool_calls)
			// （user 隔断 / assistant 无 tool_calls / 序列开头）则视为孤儿
			if msgs[j].Role == "assistant" {
				for _, tc := range msgs[j].ToolCalls {
					if tc.ID == msgs[i].ToolCallID {
						matched = true
						break
					}
				}
			}
			break
		}
		if !matched {
			log.Printf("[orch] sanitize: degrading orphan tool result (no matching preceding tool_calls), tool_call_id=%s → role=tool becomes user", msgs[i].ToolCallID)
			msgs[i].Role = "user"
			// tool_call_id 仅对 role=tool 合法，降级后必须清掉
			msgs[i].ToolCallID = ""
		}
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

func (o *Orchestrator) emitDone(events chan<- *llm.StreamEvent, convID, assistantMsgID, userMsgID, errStr string, resourceIDs map[string][]string, contextTokens, contextWindow int) {
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
	if contextTokens > 0 {
		parts = append(parts, fmt.Sprintf(`"context_tokens":%d`, contextTokens))
	}
	if contextWindow > 0 {
		parts = append(parts, fmt.Sprintf(`"context_window":%d`, contextWindow))
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

// contextWindowFor 返回当前档位的上下文窗口上限。
// isStrong 与 pickClient 判定一致（req.DeepMode && proClient != nil）。
func (o *Orchestrator) contextWindowFor(isStrong bool) int {
	if isStrong && o.proClient != nil && o.strongContextWindow > 0 {
		return o.strongContextWindow
	}
	return o.contextWindow
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

// extractCleanText 提取 JSON 回复中的可见文本（供流式展示）
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
