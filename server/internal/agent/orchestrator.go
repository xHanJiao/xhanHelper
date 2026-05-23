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

	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

const corePrinciples = `## 核心原则
1. 简洁回复：用最少的词把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 追问：如果用户意图不明确，追问关键信息
4. 记录：当用户分享反思、做出决定、总结经验教训、表达有深度的观点时，主动调用 note 工具记录下来，供未来检索
5. 诤友：不谄媚，敢直言。用户犯错时指出，用户逃避时提醒。好的关系不需要小心翼翼。`

const baseSystemPrompt = `你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

你也是用户的一面数字镜子——通过记忆和笔记了解用户的偏好、价值观、过往决定，当用户当下的选择与过去的自己背离时，温和地提醒。不是你在教育用户，而是过去的用户在提醒现在的用户，帮助做到知行合一。

`

const defaultMaxIterations = 8

// RunRequest — Orchestrator 输入
type RunRequest struct {
	UserID             string
	ConversationID     string
	Messages           []llm.Message
	DeepMode           bool
	UserMessageContent string
	AgentSystemPrompt  string   // 为空则用默认
	AgentID            string   // 关联的智能体 ID
	MemoryThreshold    float64  // 记忆检索相关度阈值，默认 0.5
	MemoryLimit        int      // 记忆检索最大条数，默认 8
	SkipMemory         bool     // 跳过记忆检索（短输入等场景）
	Temperature        *float64 // LLM 采样温度，nil 用配置默认
	MaxTokens          *int     // LLM 最大输出 token，nil 用配置默认
	Thinking           *bool    // 思考模式：nil=不传，true=开启，false=关闭
}

// LLMResponse — LLM 返回的结构化 JSON（Flash 文本解析用）
type LLMResponse struct {
	Reply     string          `json:"reply"`
	ToolCalls []tool.ToolCall `json:"tool_calls"`
}

// PromptResolver — 查找用户自定义 system prompt
type PromptResolver interface {
	ResolveActive(ctx context.Context, userID, promptType string) string
}

// Orchestrator — ReAct 循环编排器
type Orchestrator struct {
	flashClient        llm.LLMClient
	proClient          llm.LLMClient
	registry           *tool.Registry
	retriever          *Retriever
	promptBuilder      *SystemPromptBuilder
	msgRepo            *service.MessageRepo
	convRepo           *service.ConversationRepo
	embedder           *llm.Embedder
	vectorStore        *VectorStore
	embeddingRepo      *service.EmbeddingRepo
	themeStateRepo     *service.ThemeStateRepo
	promptResolver     PromptResolver
	maxIterations      int
	defaultTemperature *float64
	defaultMaxTokens   *int
	onChatEnd          func(userID, convID string)
}

// OrchestratorConfig — Orchestrator 依赖配置
type OrchestratorConfig struct {
	FlashClient    llm.LLMClient
	Registry       *tool.Registry
	ProClient      llm.LLMClient
	Retriever      *Retriever
	MsgRepo        *service.MessageRepo
	ConvRepo       *service.ConversationRepo
	Embedder       *llm.Embedder
	VectorStore    *VectorStore
	EmbeddingRepo  *service.EmbeddingRepo
	ThemeStateRepo *service.ThemeStateRepo
	MaxIterations  int
	Temperature    *float64
	MaxTokens      *int
	PromptResolver PromptResolver
	OnChatEnd      func(userID, convID string)
}

// New 创建 Orchestrator
func New(cfg *OrchestratorConfig) *Orchestrator {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = defaultMaxIterations
	}
	pb := NewSystemPromptBuilder(
		DefaultPromptConfig(),
		baseSystemPrompt,
		cfg.Registry,
		cfg.Retriever,
		cfg.ThemeStateRepo,
		cfg.ConvRepo,
		cfg.PromptResolver,
		cfg.VectorStore,
	)

	return &Orchestrator{
		flashClient:        cfg.FlashClient,
		proClient:          cfg.ProClient,
		registry:           cfg.Registry,
		retriever:          cfg.Retriever,
		promptBuilder:      pb,
		msgRepo:            cfg.MsgRepo,
		convRepo:           cfg.ConvRepo,
		embedder:           cfg.Embedder,
		vectorStore:        cfg.VectorStore,
		embeddingRepo:      cfg.EmbeddingRepo,
		themeStateRepo:     cfg.ThemeStateRepo,
		promptResolver:     cfg.PromptResolver,
		maxIterations:      cfg.MaxIterations,
		defaultTemperature: cfg.Temperature,
		defaultMaxTokens:   cfg.MaxTokens,
		onChatEnd:          cfg.OnChatEnd,
	}
}

// FlashClient 返回 Flash 模型客户端（供预处理管线使用）
func (o *Orchestrator) FlashClient() llm.LLMClient {
	return o.flashClient
}

// —— Run ——

func (o *Orchestrator) Run(ctx context.Context, req *RunRequest) (<-chan *llm.StreamEvent, error) {

	client := o.pickClient(req)

	log.Printf("[orch] run start: model=%s, msgs=%d", client.ModelName(), len(req.Messages))

	// 预计算 embedding（Builder 检索 + 存用户消息都需要）
	var queryVec []float64
	if o.embedder != nil && !req.SkipMemory {
		embStart := time.Now()
		vec, err := o.embedder.Embed(ctx, req.UserMessageContent)
		monitor.RecordEmbedding(time.Since(embStart).Milliseconds())
		if err != nil {
			log.Printf("[vector] embed user msg error: %v", err)
		} else {
			queryVec = vec
		}
	}

	result, err := o.promptBuilder.Build(ctx, BuildOptions{
		UserID:         req.UserID,
		ConversationID: req.ConversationID,
		UserMessage:    req.UserMessageContent,
		Embedding:      queryVec,
		AgentPrompt:    req.AgentSystemPrompt,
		DeepMode:       req.DeepMode,
	})
	if err != nil {
		log.Printf("[orch] prompt build error: %v", err)
	}

	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	events := make(chan *llm.StreamEvent, 512)
	go o.runReActLoop(ctx, events, client, result, queryVec, msgs, req)
	return events, nil
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
	client llm.LLMClient,
	result *BuildResult,
	queryVec []float64,
	msgs []llm.Message,
	req *RunRequest,
) {
	defer close(events)
	defer func() {
		if o.onChatEnd != nil {
			o.onChatEnd(req.UserID, req.ConversationID)
		}
	}()

	userMsgID := uuid.New().String()
	msgs = append(msgs, llm.Message{Role: "user", Content: req.UserMessageContent})
	if req.ConversationID == "" {
		log.Printf("[orch] ConversationID is empty")
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: "conversation_id is required"})
		return
	}
	extraJSON := (*string)(nil) // TODO: restore retrieval scores from builder
	if err := o.msgRepo.Create(ctx, &model.Message{
		ID:             userMsgID,
		ConversationID: req.ConversationID,
		UserID:         req.UserID,
		Role:           "user",
		Content:        req.UserMessageContent,
		Extra:          extraJSON,
	}); err != nil {
		log.Printf("[orch] save user msg error: %v", err)
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: "failed to save message"})
		return
	}
	if err := o.convRepo.Touch(ctx, req.ConversationID); err != nil {
		log.Printf("[orch] touch conv error: %v", err)
	}

	if o.vectorStore != nil && len(queryVec) > 0 && o.embeddingRepo != nil {
		go o.saveUserMsgEmbedding(userMsgID, queryVec, req.UserID, req.ConversationID)
	}

	var allToolCalls []tool.ToolCall
	var lastAssistantMsgID string
	isProMode := req.DeepMode && o.proClient != nil

	for iter := 0; iter < o.maxIterations; iter++ {
		if ctx.Err() != nil {
			log.Printf("[orch] context cancelled")
			o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: "request cancelled"})
			o.sendEvent(events, &llm.StreamEvent{Type: "done"})
			return
		}

		llmStart := time.Now()
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
		streamEvents, err := client.Chat(ctx, &llm.ChatRequest{
			SystemPrompt: result.Prompt,
			Messages:     msgs,
			Tools:        nil,
			Temperature:  temperature,
			MaxTokens:    maxTokens,
			UserID:       req.UserID,
			Thinking:     thinking,
		})
		if err != nil {
			log.Printf("[orch] round %d LLM error: %v", iter+1, err)
			o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: err.Error()})
			return
		}

		var streamContent, streamReasoning strings.Builder
		hasReasoning := false
		var lastVisibleLen int
		for se := range streamEvents {
			switch se.Type {
			case "text_delta":
				streamContent.WriteString(se.Content)
				visible := extractAllVisibleText(streamContent.String())
				if len(visible) > lastVisibleLen {
					o.sendEvent(events, &llm.StreamEvent{Type: "text_delta", Content: visible[lastVisibleLen:]})
					lastVisibleLen = len(visible)
				}
			case "thought_delta":
				streamReasoning.WriteString(se.Content)
				hasReasoning = true
				o.sendEvent(events, se)
			case "done":
				// 记录 LLM 耗时 + token 消耗
				caller := "orchestrator"
				if isProMode {
					caller = "orchestrator-pro"
				}
				elapsedMs := time.Since(llmStart).Milliseconds()
				pt, ct := 0, 0
				if se.Usage != nil {
					pt = se.Usage.PromptTokens
					ct = se.Usage.CompletionTokens
				}
				monitor.RecordLLMCall(caller, elapsedMs, pt, ct)

				if hasReasoning {
					o.sendEvent(events, &llm.StreamEvent{Type: "thought_done", Content: ""})
				}
				msg := llm.Message{
					Role:             "assistant",
					Content:          streamContent.String(),
					ReasoningContent: streamReasoning.String(),
					ToolCalls:        se.ToolCalls,
					RequestJSON:      se.RequestJSON,
					ResponseJSON:     se.ResponseJSON,
				}

				log.Printf("[orch] round %d response: content=%d, reasoning=%d, tool_calls=%d",
					iter+1, len(msg.Content), len(msg.ReasoningContent), len(msg.ToolCalls))

				var done bool
				done, lastAssistantMsgID = o.handleFlashResponse(ctx, events, &msgs, msg, req, &allToolCalls, &lastAssistantMsgID, result.MemoryIDs, result.Trace)
				if done {
					if len(allToolCalls) >= 2 {
						planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
						o.sendEvent(events, &llm.StreamEvent{Type: "plan", Content: string(planJSON)})
					}
					o.emitDone(events, req.ConversationID, lastAssistantMsgID, "", result.MemoryIDs, result.NoteIDs)
					return
				}
			case "error":
				o.sendEvent(events, se)
				return
			}
		}
	}

	o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: "max iterations reached"})
	if len(allToolCalls) >= 2 {
		planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
		o.sendEvent(events, &llm.StreamEvent{Type: "plan", Content: string(planJSON)})
	}
	o.emitDone(events, req.ConversationID, "", "max iterations reached", result.MemoryIDs, result.NoteIDs)
}

// sendEvent — 非阻塞发送事件，避免客户端断连后 goroutine 卡住
func (o *Orchestrator) sendEvent(events chan<- *llm.StreamEvent, evt *llm.StreamEvent) {
	select {
	case events <- evt:
	default:
	}
}

// —— Flash 文本响应处理 ——

// handleFlashResponse 解析 Flash 文本中的 JSON，返回 (done bool, msgID string)
func (o *Orchestrator) handleFlashResponse(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msgs *[]llm.Message,
	msg llm.Message,
	req *RunRequest,
	allToolCalls *[]tool.ToolCall,
	lastMsgID *string,
	memIDs []string,
	trace *PromptTrace,
) (bool, string) {
	parsed, parseErr := parseLLMResponse(msg.Content)

	if parseErr != nil {
		log.Printf("[orch] round parse error: %v, saving raw and retrying", parseErr)
		*lastMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, nil)
		o.sendEvent(events, &llm.StreamEvent{Type: "status", Content: "响应格式异常，正在重试…"})
		*msgs = append(*msgs, llm.Message{
			Role:    "user",
			Content: "[系统] 你上一轮的回复不是有效的 JSON 格式。请严格按照 {\"reply\":\"文字回复\",\"tool_calls\":[{\"tool\":\"工具名\",\"function\":\"函数名\",\"args\":{...}}]} 格式重新生成。",
		})
		return false, ""
	}

	if parsed == nil {
		log.Printf("[orch] round pure text reply, len=%d", len(msg.Content))
		*lastMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, trace)
		return true, *lastMsgID
	}

	log.Printf("[orch] round embedded tool_calls=%d: %s",
		len(parsed.ToolCalls), tool.ToolCallNames(parsed.ToolCalls))

	// 无工具调用 → 纯文本回复，正常结束
	if len(parsed.ToolCalls) == 0 {
		log.Printf("[orch] round text-only JSON reply, len=%d", len(msg.Content))
		*lastMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, trace)
		return true, *lastMsgID
	}

	// 统一走 prompt engineering，不填 ToolCallsJSON（由 parseLLMResponse 从文本解析）
	msg.ToolCalls = nil

	*lastMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg, trace)

	*msgs = append(*msgs, llm.Message{Role: "assistant", Content: msg.Content})
	*allToolCalls = append(*allToolCalls, parsed.ToolCalls...)

	// 为 memory_search 工具注入 exclude_ids（本轮已注入的记忆 ID）
	for i := range parsed.ToolCalls {
		if parsed.ToolCalls[i].Tool == "memory_search" && len(memIDs) > 0 {
			parsed.ToolCalls[i].Args = injectExcludeIDs(parsed.ToolCalls[i].Args, memIDs)
		}
	}

	results := executeConcurrently(ctx, events, req.UserID, o.registry, parsed.ToolCalls)

	for i := range parsed.ToolCalls {
		resJSON, _ := json.Marshal(results[i].result)
		o.sendEvent(events, &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)})

		toolCallID := idgen.Next()
		o.saveToolMsgWithID(ctx, req.ConversationID, req.UserID, toolCallID, string(resJSON))

		*msgs = append(*msgs, llm.Message{
			Role:    "user",
			Content: "[工具执行结果] " + string(resJSON),
		})
	}

	return false, *lastMsgID
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

func (o *Orchestrator) saveAssistantMsg(ctx context.Context, convID, userID string, msg llm.Message, trace *PromptTrace) string {
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
	if len(msg.ToolCalls) > 0 {
		b, _ := json.Marshal(msg.ToolCalls)
		s := string(b)
		modelMsg.ToolCallsJSON = &s
	}
	if msg.RequestJSON != nil {
		modelMsg.RequestJSON = msg.RequestJSON
	}
	if msg.ResponseJSON != nil {
		modelMsg.ResponseJSON = msg.ResponseJSON
	}
	if err := o.msgRepo.Create(ctx, modelMsg); err != nil {
		log.Printf("[orch] save assistant msg error: %v", err)
		return ""
	}
	if err := o.convRepo.Touch(ctx, convID); err != nil {
		log.Printf("[orch] touch conv error: %v", err)
	}
	return mid
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
	if err := o.convRepo.Touch(ctx, convID); err != nil {
		log.Printf("[orch] touch conv error: %v", err)
	}
	log.Printf("[orch] saved tool message: id=%s tool_call_id=%s", msgID, toolCallID)
}

// 保留 saveToolMsgWithID 供后续统一迁移用
func (o *Orchestrator) saveToolMsgWithID(ctx context.Context, convID, userID, toolCallID string, content string) {
	o.saveToolMsg(ctx, convID, userID, toolCallID, content)
}

// BuildMessagesForFlash 将数据库标准消息转换为 FlashClient 格式
func BuildMessagesForFlash(history []model.Message) []llm.Message {
	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		ts := "（发于" + m.CreatedAt.In(config.BJ).Format("01-02 15:04") + "）"
		if m.Role == "tool" {
			// FlashClient 不支持 role="tool"，包装成 user 消息
			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: "[工具执行结果] " + m.Content + ts,
			})
		} else {
			msg := llm.Message{Role: m.Role, Content: m.Content + ts}
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
func BuildMessagesForPro(history []model.Message) []llm.Message {
	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		ts := "（发于" + m.CreatedAt.In(config.BJ).Format("01-02 15:04") + "）"
		msg := llm.Message{Role: m.Role, Content: m.Content + ts}
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

func (o *Orchestrator) emitDone(events chan<- *llm.StreamEvent, convID, msgID, errStr string, memIDs, noteIDs []string) {
	parts := make([]string, 0)
	if convID != "" {
		parts = append(parts, fmt.Sprintf(`"conversation_id":"%s"`, convID))
	}
	if msgID != "" {
		parts = append(parts, fmt.Sprintf(`"message_id":"%s"`, msgID))
	}
	if len(memIDs) > 0 {
		b, _ := json.Marshal(memIDs)
		parts = append(parts, fmt.Sprintf(`"memory_ids":%s`, string(b)))
	}
	if len(noteIDs) > 0 {
		b, _ := json.Marshal(noteIDs)
		parts = append(parts, fmt.Sprintf(`"note_ids":%s`, string(b)))
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

func parseLLMResponse(text string) (*LLMResponse, error) {
	var resp LLMResponse
	_, err := ParseFlashJSON(text, &resp)
	if err != nil {
		return nil, err // 非法 JSON
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
