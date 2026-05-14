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

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

const baseSystemPrompt = `你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

核心原则：
1. 简洁回复：用最少的词把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 主动：如果用户意图不明确，追问关键信息`

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
	msgRepo            *service.MessageRepo
	convRepo           *service.ConversationRepo
	embedder           *llm.Embedder
	vectorStore        *VectorStore
	embeddingRepo      *service.EmbeddingRepo
	promptResolver     PromptResolver
	maxIterations      int
	defaultTemperature *float64
	defaultMaxTokens   *int
}

// New 创建 Orchestrator
func New(flashClient llm.LLMClient, registry *tool.Registry,
	proClient llm.LLMClient, retriever *Retriever,
	msgRepo *service.MessageRepo, convRepo *service.ConversationRepo,
	embedder *llm.Embedder, vectorStore *VectorStore,
	embeddingRepo *service.EmbeddingRepo,
	maxIterations int,
	defaultTemperature *float64, defaultMaxTokens *int) *Orchestrator {
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}
	return &Orchestrator{
		flashClient:        flashClient,
		proClient:          proClient,
		registry:           registry,
		retriever:          retriever,
		msgRepo:            msgRepo,
		convRepo:           convRepo,
		embedder:           embedder,
		vectorStore:        vectorStore,
		embeddingRepo:      embeddingRepo,
		maxIterations:      maxIterations,
		defaultTemperature: defaultTemperature,
		defaultMaxTokens:   defaultMaxTokens,
	}
}

// SetPromptResolver 注入用户自定义 prompt 查找器
func (o *Orchestrator) SetPromptResolver(r PromptResolver) {
	o.promptResolver = r
}

// FlashClient 返回 Flash 模型客户端（供预处理管线使用）
func (o *Orchestrator) FlashClient() llm.LLMClient {
	return o.flashClient
}

// —— Run ——

func (o *Orchestrator) Run(ctx context.Context, req *RunRequest) (<-chan *llm.StreamEvent, error) {

	client := o.pickClient(req)
	sysPrompt, memoryNote, memIDs, noteIDs, queryVec := o.buildSysPrompt(ctx, req)

	log.Printf("[orch] run start: model=%s, msgs=%d", client.ModelName(), len(req.Messages))

	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	// 加载历史用过的工具名
	usedNames := loadUsedToolNames(ctx, o.convRepo, req.ConversationID)
	activeNames := ensureToolRetriever(usedNames)

	events := make(chan *llm.StreamEvent, 512)
	go o.runReActLoop(ctx, events, client, sysPrompt, memoryNote, memIDs, noteIDs, queryVec, msgs, req, activeNames)
	return events, nil
}

func (o *Orchestrator) pickClient(req *RunRequest) llm.LLMClient {
	if req.DeepMode && o.proClient != nil {
		return o.proClient
	}
	return o.flashClient
}

func (o *Orchestrator) buildSysPrompt(ctx context.Context, req *RunRequest) (string, string, []string, []string, []float64) {
	sysPrompt := req.AgentSystemPrompt
	if sysPrompt == "" {
		sysPrompt = baseSystemPrompt
		if o.promptResolver != nil {
			clientType := "flash"
			if o.proClient != nil && req.DeepMode {
				clientType = "pro"
			}
			if custom := o.promptResolver.ResolveActive(ctx, req.UserID, clientType); custom != "" {
				sysPrompt = custom
			}
		}
	}
	sysPrompt += "\n当前时间: " + time.Now().Format("2006-01-02 15:04:05 Monday")
	sysPrompt += o.registry.ToolOverview()

	if o.retriever == nil || o.embedder == nil {
		return sysPrompt, "", nil, nil, nil
	}
	if req.SkipMemory {
		log.Printf("[orch] skip memory retrieval (SkipMemory=true)")
		return sysPrompt, "", nil, nil, nil
	}
	embStart := time.Now()
	vec, err := o.embedder.Embed(ctx, req.UserMessageContent)
	monitor.RecordEmbedding(time.Since(embStart).Milliseconds())
	if err != nil {
		log.Printf("[vector] embed user msg error: %v", err)
		return sysPrompt, "", nil, nil, nil
	}
	packets, memIDs, noteIDs := o.retriever.Retrieve(ctx, vec, req.UserID, req.ConversationID, req.DeepMode, req.MemoryThreshold, req.MemoryLimit)
	memText := o.retriever.FormatContext(packets)
	if memText == "" {
		return sysPrompt, "", nil, noteIDs, vec
	}
	sysPrompt += "\n\n===== 关于用户的信息 =====\n" + memText
	return sysPrompt, fmt.Sprintf(`{"message":"已关联向量记忆","count":%d}`, len(packets)), memIDs, noteIDs, vec
}

// —— ReAct 主循环 ——

func (o *Orchestrator) runReActLoop(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	client llm.LLMClient,
	sysPrompt, memoryNote string,
	memIDs, noteIDs []string,
	queryVec []float64,
	msgs []llm.Message,
	req *RunRequest,
	activeNames []string,
) {
	defer close(events)

	if memoryNote != "" {
		o.sendEvent(events, &llm.StreamEvent{Type: "status", Content: memoryNote})
	}

	userMsgID := uuid.New().String()
	msgs = append(msgs, llm.Message{Role: "user", Content: req.UserMessageContent})
	if req.ConversationID == "" {
		log.Printf("[orch] ConversationID is empty")
		o.sendEvent(events, &llm.StreamEvent{Type: "error", Error: "conversation_id is required"})
		return
	}
	var mIDs *string
	if len(memIDs) > 0 {
		b, _ := json.Marshal(memIDs)
		s := string(b)
		mIDs = &s
	}
	if err := o.msgRepo.Create(ctx, &model.Message{
		ID:             userMsgID,
		ConversationID: req.ConversationID,
		UserID:         req.UserID,
		Role:           "user",
		Content:        req.UserMessageContent,
		MemoryIDs:      mIDs,
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

		// — 根据当前 activeNames 构建本轮的工具列表和 system prompt —
		iterSysPrompt := sysPrompt + o.registry.GeneratePromptFor(activeNames)

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
			SystemPrompt: iterSysPrompt,
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

				log.Printf("[orch] round %d response: content=%d, reasoning=%d, tool_calls=%d, active_tools=%d",
					iter+1, len(msg.Content), len(msg.ReasoningContent), len(msg.ToolCalls), len(activeNames))

				var done bool
				var newNames []string
				done, lastAssistantMsgID, newNames = o.handleFlashResponse(ctx, events, &msgs, msg, req, &allToolCalls, &lastAssistantMsgID)
				if done {
					if len(allToolCalls) >= 2 {
						planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
						o.sendEvent(events, &llm.StreamEvent{Type: "plan", Content: string(planJSON)})
					}
					o.emitDone(events, req.ConversationID, lastAssistantMsgID, "", memIDs, noteIDs)
					return
				}

				// Phase 5: 把 tool_retriever 发现的新工具加入 activeNames，下次迭代生效
				if len(newNames) > 0 {
					before := len(activeNames)
					activeNames = mergeNewNames(activeNames, newNames)
					if len(activeNames) > before {
						log.Printf("[orch] new tools loaded: %v (total=%d)", newNames, len(activeNames))
						saveUsedToolNames(ctx, o.convRepo, req.ConversationID, activeNames)
					}
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
	o.emitDone(events, req.ConversationID, "", "max iterations reached", memIDs, noteIDs)
}

// sendEvent — 非阻塞发送事件，避免客户端断连后 goroutine 卡住
func (o *Orchestrator) sendEvent(events chan<- *llm.StreamEvent, evt *llm.StreamEvent) {
	select {
	case events <- evt:
	default:
	}
}

// —— 工具调用处理 ——

func (o *Orchestrator) handleNativeToolCalls(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msgs *[]llm.Message,
	msg llm.Message,
	req *RunRequest,
	allToolCalls *[]tool.ToolCall,
) []string {
	log.Printf("[orch] round native tool_calls=%d", len(msg.ToolCalls))
	*msgs = append(*msgs, msg)
	if req.DeepMode {
		o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg)
	}

	toolCalls := convertToolCalls(msg.ToolCalls)
	*allToolCalls = append(*allToolCalls, toolCalls...)

	// plan 事件：节点开始
	for _, tc := range toolCalls {
		n := planNode{ID: toolCallNodeID(tc), Label: tc.Function, Status: "running"}
		b, _ := json.Marshal(n)
		o.sendEvent(events, &llm.StreamEvent{Type: "plan_node_start", Content: string(b)})
	}

	results := executeConcurrently(ctx, events, req.UserID, o.registry, toolCalls)

	// 收集本轮的 tool names + tool_retriever 返回的新工具
	newNames := make([]string, 0)
	for i := range toolCalls {
		resJSON, _ := json.Marshal(results[i].result)
		o.sendEvent(events, &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)})
		*msgs = append(*msgs, llm.Message{
			Role:       "tool",
			ToolCallID: msg.ToolCalls[i].ID,
			Content:    string(resJSON),
		})

		// plan 事件：节点完成
		status := "done"
		if !results[i].result.Success {
			status = "failed"
		}
		n := planNode{ID: toolCallNodeID(toolCalls[i]), Label: toolCalls[i].Function, Status: status}
		b, _ := json.Marshal(n)
		o.sendEvent(events, &llm.StreamEvent{Type: "plan_node_done", Content: string(b)})

		if req.DeepMode {
			o.saveToolMsg(ctx, req.ConversationID, req.UserID, msg.ToolCalls[i].ID, string(resJSON))
		}

		// 记录使用的工具名
		if results[i].result.Success {
			newNames = append(newNames, toolCalls[i].Tool)
		}
		// 如果结果中包含工具列表，提取发现的工具名
		if results[i].result.Data != nil {
			names := extractToolNamesFromResult(results[i].result.Data)
			newNames = append(newNames, names...)
		}
	}
	return newNames
}

type planNode struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // running | done | failed
}

func toolCallNodeID(tc tool.ToolCall) string {
	return tc.Tool + "." + tc.Function
}

// —— Flash 文本响应处理 ——

// handleFlashResponse 解析 Flash 文本中的 JSON，返回 (done bool, msgID string, newNames []string)
func (o *Orchestrator) handleFlashResponse(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msgs *[]llm.Message,
	msg llm.Message,
	req *RunRequest,
	allToolCalls *[]tool.ToolCall,
	lastMsgID *string,
) (bool, string, []string) {
	parsed := parseLLMResponse(msg.Content)

	if parsed == nil {
		log.Printf("[orch] round pure text reply, len=%d", len(msg.Content))
		*lastMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg)
		return true, *lastMsgID, nil
	}

	log.Printf("[orch] round embedded tool_calls=%d: %s",
		len(parsed.ToolCalls), tool.ToolCallNames(parsed.ToolCalls))

	// 统一走 prompt engineering，不填 ToolCallsJSON（由 parseLLMResponse 从文本解析）
	msg.ToolCalls = nil

	*lastMsgID = o.saveAssistantMsg(ctx, req.ConversationID, req.UserID, msg)

	*msgs = append(*msgs, llm.Message{Role: "assistant", Content: msg.Content})
	*allToolCalls = append(*allToolCalls, parsed.ToolCalls...)

	results := executeConcurrently(ctx, events, req.UserID, o.registry, parsed.ToolCalls)

	newNames := make([]string, 0)
	for i := range parsed.ToolCalls {
		resJSON, _ := json.Marshal(results[i].result)
		o.sendEvent(events, &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)})

		toolCallID := idgen.Next()
		o.saveToolMsgWithID(ctx, req.ConversationID, req.UserID, toolCallID, string(resJSON))

		*msgs = append(*msgs, llm.Message{
			Role:    "user",
			Content: "[工具执行结果] " + string(resJSON),
		})

		// 记录使用的工具名 + tool_retriever 返回的新工具
		if results[i].result.Success {
			newNames = append(newNames, parsed.ToolCalls[i].Tool)
		}
		if results[i].result.Data != nil {
			names := extractToolNamesFromResult(results[i].result.Data)
			newNames = append(newNames, names...)
		}
	}

	return false, *lastMsgID, newNames
}

// —— 并发执行 ——

type toolExecResult struct {
	tc     tool.ToolCall
	result *tool.Result
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

func convertToolCalls(tcs []llm.ToolCall) []tool.ToolCall {
	result := make([]tool.ToolCall, len(tcs))
	for i, tc := range tcs {
		toolName := tc.Function.Name
		funcName := tc.Function.Name
		if idx := strings.LastIndex(tc.Function.Name, "__"); idx > 0 {
			toolName = tc.Function.Name[:idx]
			funcName = tc.Function.Name[idx+2:]
		}
		var args json.RawMessage
		if tc.Function.Arguments != "" {
			args = json.RawMessage(tc.Function.Arguments)
		}
		result[i] = tool.ToolCall{
			Tool:     toolName,
			Function: funcName,
			Args:     args,
		}
	}
	return result
}

// —— DB 保存 ——

func (o *Orchestrator) saveAssistantMsg(ctx context.Context, convID, userID string, msg llm.Message) string {
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
		if m.Role == "tool" {
			// FlashClient 不支持 role="tool"，包装成 user 消息
			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: "[工具执行结果] " + m.Content,
			})
		} else {
			msg := llm.Message{Role: m.Role, Content: m.Content}
			if m.ReasoningContent != nil {
				msg.ReasoningContent = *m.ReasoningContent
			}
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
		msg := llm.Message{Role: m.Role, Content: m.Content}
		if m.ReasoningContent != nil {
			msg.ReasoningContent = *m.ReasoningContent
		}
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

func parseLLMResponse(text string) *LLMResponse {
	var resp LLMResponse
	_, err := ParseFlashJSON(text, &resp)
	if err != nil || resp.ToolCalls == nil && resp.Reply == "" {
		return nil
	}
	if resp.ToolCalls == nil {
		resp.ToolCalls = []tool.ToolCall{}
	}
	for i, tc := range resp.ToolCalls {
		resp.ToolCalls[i].Function = strings.TrimPrefix(tc.Function, "_")
	}
	return &resp
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

// —— Phase 5/6 工具名管理辅助函数 ——

func loadUsedToolNames(ctx context.Context, convRepo *service.ConversationRepo, convID string) []string {
	if convRepo == nil || convID == "" {
		return nil
	}
	ids, err := convRepo.GetUsedToolIDs(ctx, convID)
	if err != nil {
		log.Printf("[orch] load used tool names error: %v", err)
		return nil
	}
	return ids
}

func ensureToolRetriever(names []string) []string {
	for _, n := range names {
		if n == "tool_retriever" {
			return names
		}
	}
	return append([]string{"tool_retriever"}, names...)
}

func mergeNewNames(existing, new []string) []string {
	set := make(map[string]bool, len(existing))
	for _, n := range existing {
		set[n] = true
	}
	for _, n := range new {
		if !set[n] && n != "" {
			existing = append(existing, n)
			set[n] = true
		}
	}
	return existing
}

func extractToolNamesFromResult(data json.RawMessage) []string {
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	names := make([]string, 0, len(result.Tools))
	for _, t := range result.Tools {
		if t.Name != "" {
			names = append(names, t.Name)
		}
	}
	return names
}

func saveUsedToolNames(ctx context.Context, convRepo *service.ConversationRepo, convID string, names []string) {
	if convRepo == nil || convID == "" {
		return
	}
	extra := &model.ConversationExtra{UsedToolIDs: names}
	if err := convRepo.SaveExtra(ctx, convID, extra); err != nil {
		log.Printf("[orch] save used tool names error: %v", err)
	}
}
