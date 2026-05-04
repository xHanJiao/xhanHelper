package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

const baseSystemPrompt = `你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

核心原则：
1. 简洁回复：用最少的字把事说清楚
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
}

// LLMResponse — LLM 返回的结构化 JSON（Flash 文本解析用）
type LLMResponse struct {
	Reply     string          `json:"reply"`
	ToolCalls []tool.ToolCall `json:"tool_calls"`
}

// Orchestrator — ReAct 循环编排器
type Orchestrator struct {
	flashClient   llm.LLMClient
	proClient     llm.LLMClient
	registry      *tool.Registry
	msgRepo       *service.MessageRepo
	convRepo      *service.ConversationRepo
	vectorStore   *VectorStore
	embedder      *llm.Embedder
	memRepo       *service.UserMemoryRepo
	noteRepo      *service.NoteRepo
	maxIterations int
}

// New 创建 Orchestrator
func New(flashClient llm.LLMClient, registry *tool.Registry,
	proClient llm.LLMClient, msgRepo *service.MessageRepo, convRepo *service.ConversationRepo,
	vectorStore *VectorStore, embedder *llm.Embedder, memRepo *service.UserMemoryRepo,
	noteRepo *service.NoteRepo, maxIterations int) *Orchestrator {
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}
	return &Orchestrator{
		flashClient:   flashClient,
		proClient:     proClient,
		registry:      registry,
		msgRepo:       msgRepo,
		convRepo:      convRepo,
		vectorStore:   vectorStore,
		embedder:      embedder,
		memRepo:       memRepo,
		noteRepo:      noteRepo,
		maxIterations: maxIterations,
	}
}

// —— Run ——

func (o *Orchestrator) Run(ctx context.Context, req *RunRequest) (<-chan *llm.StreamEvent, error) {
	client := o.pickClient(req)
	log.Printf("[orch] run start: model=%s, msgs=%d, tools=%d",
		client.ModelName(), len(req.Messages), len(o.registry.GetLLMTools()))

	sysPrompt, memoryNote := o.buildSysPrompt(ctx, client, req)

	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	events := make(chan *llm.StreamEvent, 64)
	go o.runReActLoop(ctx, events, client, sysPrompt, memoryNote, msgs, req)
	return events, nil
}

func (o *Orchestrator) pickClient(req *RunRequest) llm.LLMClient {
	if req.DeepMode && o.proClient != nil {
		return o.proClient
	}
	return o.flashClient
}

func (o *Orchestrator) buildSysPrompt(ctx context.Context, client llm.LLMClient, req *RunRequest) (string, string) {
	prompt := client.BuildSystemPrompt(baseSystemPrompt, o.registry.GetLLMTools())

	if o.vectorStore == nil || o.embedder == nil {
		return prompt, ""
	}
	memText := o.retrieveMemories(ctx, req)
	if memText == "" {
		return prompt, ""
	}
	prompt += "\n\n===== 关于用户的信息 =====\n" + memText
	return prompt, fmt.Sprintf(`{"message":"已关联向量记忆","count":%d}`, strings.Count(memText, "\n"))
}

// —— ReAct 主循环 ——

func (o *Orchestrator) runReActLoop(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	client llm.LLMClient,
	sysPrompt, memoryNote string,
	msgs []llm.Message,
	req *RunRequest,
) {
	defer close(events)

	if memoryNote != "" {
		events <- &llm.StreamEvent{Type: "status", Content: memoryNote}
	}

	userMsgID := uuid.New().String()
	msgs = append(msgs, llm.Message{Role: "user", Content: req.UserMessageContent})
	if o.msgRepo != nil && req.ConversationID != "" {
		o.msgRepo.Create(ctx, &model.Message{
			ID:             userMsgID,
			ConversationID: req.ConversationID,
			Role:           "user",
			Content:        req.UserMessageContent,
		})
	}

	var allToolCalls []tool.ToolCall
	var lastAssistantMsgID string

	for iter := 0; iter < o.maxIterations; iter++ {
		if ctx.Err() != nil {
			log.Printf("[orch] context cancelled")
			events <- &llm.StreamEvent{Type: "error", Error: "request cancelled"}
			events <- &llm.StreamEvent{Type: "done"}
			return
		}

		msg, err := client.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: sysPrompt,
			Messages:     msgs,
			Tools:        o.registry.GetLLMTools(),
		})
		if err != nil {
			log.Printf("[orch] round %d LLM error: %v", iter+1, err)
			events <- &llm.StreamEvent{Type: "error", Error: err.Error()}
			return
		}

		log.Printf("[orch] round %d response: content=%d, reasoning=%d, tool_calls=%d",
			iter+1, len(msg.Content), len(msg.ReasoningContent), len(msg.ToolCalls))

		if msg.ReasoningContent != "" {
			events <- &llm.StreamEvent{Type: "thought_delta", Content: msg.ReasoningContent}
			events <- &llm.StreamEvent{Type: "thought_done", Content: ""}
		}

		// 原生 tool_calls（Pro 或 Flash 原生函数调用）
		if len(msg.ToolCalls) > 0 {
			o.handleNativeToolCalls(ctx, events, &msgs, msg, req, &allToolCalls)
			continue
		}

		// Flash 文本 JSON 解析
		done, msgID := o.handleFlashResponse(ctx, events, &msgs, msg, req, &allToolCalls, &lastAssistantMsgID)
		if done {
			if len(allToolCalls) >= 2 {
				planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
				events <- &llm.StreamEvent{Type: "plan", Content: string(planJSON)}
			}
			o.emitDone(events, req.ConversationID, msgID, "")
			return
		}
	}

	events <- &llm.StreamEvent{Type: "error", Error: "max iterations reached"}
	if len(allToolCalls) >= 2 {
		planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
		events <- &llm.StreamEvent{Type: "plan", Content: string(planJSON)}
	}
	o.emitDone(events, req.ConversationID, "", "max iterations reached")
}

// —— 工具调用处理 ——

func (o *Orchestrator) handleNativeToolCalls(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msgs *[]llm.Message,
	msg llm.Message,
	req *RunRequest,
	allToolCalls *[]tool.ToolCall,
) {
	log.Printf("[orch] round native tool_calls=%d", len(msg.ToolCalls))
	*msgs = append(*msgs, msg)
	if req.DeepMode {
		o.saveAssistantMsg(ctx, req.ConversationID, msg)
	}

	toolCalls := convertToolCalls(msg.ToolCalls)
	*allToolCalls = append(*allToolCalls, toolCalls...)

	// plan 事件：节点开始
	for _, tc := range toolCalls {
		n := planNode{ID: toolCallNodeID(tc), Label: tc.Function, Status: "running"}
		b, _ := json.Marshal(n)
		events <- &llm.StreamEvent{Type: "plan_node_start", Content: string(b)}
	}

	results := executeConcurrently(ctx, events, req.UserID, o.registry, toolCalls)

	for i := range toolCalls {
		resJSON, _ := json.Marshal(results[i].result)
		events <- &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)}
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
		events <- &llm.StreamEvent{Type: "plan_node_done", Content: string(b)}

		if req.DeepMode {
			o.saveToolMsg(ctx, req.ConversationID, msg.ToolCalls[i].ID, string(resJSON))
		}
	}
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

// handleFlashResponse 解析 Flash 文本中的 JSON，返回 (done bool, msgID string)
func (o *Orchestrator) handleFlashResponse(
	ctx context.Context,
	events chan<- *llm.StreamEvent,
	msgs *[]llm.Message,
	msg llm.Message,
	req *RunRequest,
	allToolCalls *[]tool.ToolCall,
	lastMsgID *string,
) (bool, string) {
	parsed := parseLLMResponse(msg.Content)

	if parsed == nil {
		// 纯文本回复 → 流式输出 → done
		log.Printf("[orch] round pure text reply, len=%d", len(msg.Content))
		for _, r := range msg.Content {
			events <- &llm.StreamEvent{Type: "text_delta", Content: string(r)}
		}
		*lastMsgID = o.saveAssistantMsg(ctx, req.ConversationID, msg)
		return true, *lastMsgID
	}

	// 嵌入 tool_calls → 执行 → 继续下一轮
	log.Printf("[orch] round embedded tool_calls=%d: %s",
		len(parsed.ToolCalls), tool.ToolCallNames(parsed.ToolCalls))
	*msgs = append(*msgs, llm.Message{Role: "assistant", Content: msg.Content})
	*allToolCalls = append(*allToolCalls, parsed.ToolCalls...)

	results := executeConcurrently(ctx, events, req.UserID, o.registry, parsed.ToolCalls)

	for i := range parsed.ToolCalls {
		resJSON, _ := json.Marshal(results[i].result)
		events <- &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)}
		*msgs = append(*msgs, llm.Message{
			Role:    "user",
			Content: "[工具执行结果] " + string(resJSON),
		})
	}
	return false, ""
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
		events <- &llm.StreamEvent{Type: "tool_call", Content: string(tcJSON)}

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

// —— 向量检索 ——

func (o *Orchestrator) retrieveMemories(ctx context.Context, req *RunRequest) string {
	vec, err := o.embedder.Embed(ctx, req.UserMessageContent)
	if err != nil {
		log.Printf("[vector] embed user msg error: %v", err)
		return ""
	}
	results := o.vectorStore.Search(vec, 8, func(e vectorEntry) bool {
		if e.Metadata["user_id"] != "" && e.Metadata["user_id"] != req.UserID {
			return false
		}
		if e.Metadata["type"] != "msg" {
			return true
		}
		return e.Metadata["conv_id"] != req.ConversationID
	})
	// 搜索完成后再写入当前消息 embedding，避免被自己检索到
	go o.saveMsgEmbedding(ctx, req, vec)

	if len(results) == 0 {
		return ""
	}

	var parts []string

	// Route 2: 记忆摘要
	if memText := o.searchMemories(ctx, results); memText != "" {
		parts = append(parts, memText)
	}

	// Route 1: 历史消息 + 上下文
	if msgText := o.searchHistoryMessages(ctx, req.ConversationID, results); msgText != "" {
		parts = append(parts, msgText)
	}

	// Route 3: note 原文（仅 deep_mode）
	if req.DeepMode {
		if noteText := o.searchNotes(ctx, results); noteText != "" {
			parts = append(parts, noteText)
		}
	}

	return strings.Join(parts, "\n")
}

func (o *Orchestrator) searchMemories(ctx context.Context, results []struct {
	ID    string
	Score float64
}) string {
	var memIDs []string
	for _, r := range results {
		if r.Score < 0.5 || !strings.HasPrefix(r.ID, "mem:") {
			continue
		}
		memIDs = append(memIDs, strings.TrimPrefix(r.ID, "mem:"))
		log.Printf("[vector] memory hit: id=%s, score=%.4f", r.ID, r.Score)
	}
	if len(memIDs) == 0 {
		return ""
	}
	memories, err := o.memRepo.GetByIDs(ctx, memIDs)
	if err != nil || len(memories) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", m.MemoryKey, m.MemoryValue))
	}
	return "===== 相关记忆 =====\n" + sb.String()
}

func (o *Orchestrator) searchHistoryMessages(ctx context.Context, currentConvID string, results []struct {
	ID    string
	Score float64
}) string {
	for _, r := range results {
		if r.Score < 0.5 || !strings.HasPrefix(r.ID, "msg:") {
			continue
		}
		msgID := strings.TrimPrefix(r.ID, "msg:")
		msg, err := o.msgRepo.GetByID(ctx, msgID)
		if err != nil || msg == nil {
			continue
		}
		if msg.ConversationID == currentConvID {
			continue
		}
		log.Printf("[vector] msg hit: conv=%s, score=%.4f, text=%s",
			msg.ConversationID[:min(8, len(msg.ConversationID))], r.Score,
			msg.Content[:min(80, len(msg.Content))])

		// 取同一会话的上下文
		history, _ := o.msgRepo.ListByConversation(ctx, msg.ConversationID, 20)
		return "===== 历史相关对话 =====\n" + formatHistoryContext(history, msgID)
	}
	return ""
}

func (o *Orchestrator) searchNotes(ctx context.Context, results []struct {
	ID    string
	Score float64
}) string {
	var noteIDs []string
	for _, r := range results {
		if r.Score < 0.5 || !strings.HasPrefix(r.ID, "note:") {
			continue
		}
		noteIDs = append(noteIDs, strings.TrimPrefix(r.ID, "note:"))
		log.Printf("[vector] note hit: id=%s, score=%.4f", r.ID, r.Score)
	}
	if len(noteIDs) == 0 || o.noteRepo == nil {
		return ""
	}
	notes, err := o.noteRepo.GetByIDs(ctx, noteIDs)
	if err != nil || len(notes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("===== 相关笔记 =====\n")
	for _, n := range notes {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", n.Topic, n.Content[:min(200, len(n.Content))]))
	}
	return sb.String()
}

func formatHistoryContext(history []model.Message, focusID string) string {
	var sb strings.Builder
	found := -1
	for i, m := range history {
		if m.ID == focusID {
			found = i
			break
		}
	}
	if found < 0 {
		return ""
	}
	start := found - 3
	if start < 0 {
		start = 0
	}
	end := found + 4
	if end > len(history) {
		end = len(history)
	}
	for i := start; i < end; i++ {
		m := history[i]
		marker := " "
		if m.ID == focusID {
			marker = "→"
		}
		sb.WriteString(fmt.Sprintf("%s [%s]: %s\n", marker, m.Role, m.Content[:min(200, len(m.Content))]))
	}
	return sb.String()
}

func (o *Orchestrator) saveMsgEmbedding(ctx context.Context, req *RunRequest, vec []float64) {
	if o.msgRepo == nil || req.ConversationID == "" {
		return
	}
	// 取刚保存的用户消息 ID
	history, err := o.msgRepo.ListByConversation(ctx, req.ConversationID, 1)
	if err != nil || len(history) == 0 {
		return
	}
	lastMsg := history[len(history)-1]
	if lastMsg.Role != "user" {
		return
	}
	o.vectorStore.Upsert("msg:"+lastMsg.ID, vec, map[string]string{
		"type":    "msg",
		"conv_id": req.ConversationID,
		"user_id": req.UserID,
	})
	s := llm.EmbeddingToString(vec)
	o.msgRepo.UpdateLastUserEmbedding(ctx, req.ConversationID, &s)
}

// Warmup 启动时加载已有记忆到向量库
func (o *Orchestrator) Warmup(ctx context.Context) {
	if o.vectorStore == nil || o.embedder == nil || o.memRepo == nil {
		log.Printf("[vector] warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] warmup: loading existing memories...")

	memories, err := o.memRepo.ListAll(ctx)
	if err != nil {
		log.Printf("[vector] warmup: list memories error: %v", err)
		return
	}

	loaded, embedded := 0, 0
	for _, m := range memories {
		if m.Embedding != nil {
			if vec, err := llm.StringToEmbedding(*m.Embedding); err == nil && len(vec) > 0 {
				o.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
				loaded++
				continue
			}
		}
		// 没有持久化向量的，重新嵌入
		text := m.MemoryKey + ": " + m.MemoryValue
		vec, err := o.embedder.Embed(ctx, text)
		if err != nil {
			log.Printf("[vector] warmup: embed memory %s error: %v", m.ID[:8], err)
			continue
		}
		o.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
		embStr := llm.EmbeddingToString(vec)
		o.memRepo.UpdateEmbedding(ctx, m.ID, &embStr)
		embedded++
	}

	log.Printf("[vector] warmup: %d from persisted embeddings, %d new, total=%d in store",
		loaded, embedded, o.vectorStore.Size())
}

// —— DB 保存 ——

func (o *Orchestrator) saveAssistantMsg(ctx context.Context, convID string, msg llm.Message) string {
	if o.msgRepo == nil || convID == "" {
		return ""
	}
	mid := uuid.New().String()
	modelMsg := &model.Message{
		ID:             mid,
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
	o.msgRepo.Create(ctx, modelMsg)
	return mid
}

func (o *Orchestrator) saveToolMsg(ctx context.Context, convID string, toolCallID string, content string) {
	if o.msgRepo == nil || convID == "" {
		return
	}
	tcid := toolCallID
	o.msgRepo.Create(ctx, &model.Message{
		ID:             uuid.New().String(),
		ConversationID: convID,
		Role:           "tool",
		Content:        content,
		ToolCallID:     &tcid,
	})
}

func (o *Orchestrator) emitDone(events chan<- *llm.StreamEvent, convID, msgID, errStr string) {
	parts := make([]string, 0)
	if convID != "" {
		parts = append(parts, fmt.Sprintf(`"conversation_id":"%s"`, convID))
	}
	if msgID != "" {
		parts = append(parts, fmt.Sprintf(`"message_id":"%s"`, msgID))
	}
	if errStr != "" {
		parts = append(parts, fmt.Sprintf(`"error":"%s"`, errStr))
	}
	events <- &llm.StreamEvent{Type: "done", Content: "{" + strings.Join(parts, ",") + "}"}
}

// —— 工具函数 ——

func (o *Orchestrator) GenerateTitle(ctx context.Context, userMessage string) string {
	msg, err := o.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是一个标题生成器，根据用户的消息内容生成一个10字以内的简短标题，只输出标题本身，不要加任何标点或引号。",
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
	cleaned := strings.TrimSpace(text)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	start := strings.Index(cleaned, "{")
	if start < 0 {
		return nil
	}

	jsonStr := cleaned[start:]

	var resp LLMResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil
	}

	if resp.ToolCalls == nil {
		resp.ToolCalls = []tool.ToolCall{}
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
