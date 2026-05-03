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

const maxIterations = 8

// RunRequest — Orchestrator 输入
type RunRequest struct {
	DeviceID           string
	ConversationID     string
	Messages           []llm.Message
	DeepMode           bool   // 客户端 toggle 是否开启深度思考模式
	UserMessageContent string // 用户原始消息
}

// LLMResponse — LLM 返回的结构化 JSON（Flash 文本解析用）
type LLMResponse struct {
	Reply     string          `json:"reply"`
	ToolCalls []tool.ToolCall `json:"tool_calls"`
}

// Orchestrator — ReAct 循环编排器
type Orchestrator struct {
	flashClient llm.LLMClient
	proClient   llm.LLMClient
	registry    *tool.Registry
	memoryStore MemoryStore
	msgRepo     *service.MessageRepo
	convRepo    *service.ConversationRepo
}

// New 创建 Orchestrator
func New(flashClient llm.LLMClient, registry *tool.Registry, memoryStore MemoryStore, proClient llm.LLMClient, msgRepo *service.MessageRepo, convRepo *service.ConversationRepo) *Orchestrator {
	return &Orchestrator{
		flashClient: flashClient,
		proClient:   proClient,
		registry:    registry,
		memoryStore: memoryStore,
		msgRepo:     msgRepo,
		convRepo:    convRepo,
	}
}

// Run 启动 ReAct 循环，返回事件 channel
func (o *Orchestrator) Run(ctx context.Context, req *RunRequest) (<-chan *llm.StreamEvent, error) {
	client := o.flashClient
	if req.DeepMode && o.proClient != nil {
		client = o.proClient
	}
	log.Printf("[orch] run start: model=%s, msgs=%d, tools=%d", client.ModelName(), len(req.Messages), len(o.registry.GetLLMTools()))

	sysPrompt := client.BuildSystemPrompt(baseSystemPrompt, o.registry.GetLLMTools())

	var memoryNote string
	if o.memoryStore != nil && len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1].Content
		memories, err := o.memoryStore.GetRelevant(ctx, req.DeviceID, lastMsg)
		if err == nil && len(memories) > 0 {
			var sb strings.Builder
			sb.WriteString("\n\n===== 关于用户的信息 =====\n")
			for _, m := range memories {
				sb.WriteString(fmt.Sprintf("- %s\n", m.MemoryValue))
			}
			sysPrompt += sb.String()
			memoryNote = fmt.Sprintf(`{"message":"已关联%d条记忆","count":%d}`, len(memories), len(memories))
		}
	}

	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	events := make(chan *llm.StreamEvent, 64)

	go func() {
		defer close(events)

		if memoryNote != "" {
			events <- &llm.StreamEvent{Type: "status", Content: memoryNote}
		}

		// 用户消息加入上下文并保存
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

		for iter := 0; iter < maxIterations; iter++ {
			select {
			case <-ctx.Done():
				log.Printf("[orch] context cancelled")
				events <- &llm.StreamEvent{Type: "error", Error: "request cancelled"}
				events <- &llm.StreamEvent{Type: "done"}
				return
			default:
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

			// Pro 的思维链作为 thought_delta 推送
			if msg.ReasoningContent != "" {
				events <- &llm.StreamEvent{Type: "thought_delta", Content: msg.ReasoningContent}
				events <- &llm.StreamEvent{Type: "thought_done", Content: ""}
			}

			// 原生 tool_calls
			if len(msg.ToolCalls) > 0 {
				log.Printf("[orch] round %d native tool_calls=%d", iter+1, len(msg.ToolCalls))
				msgs = append(msgs, msg)
				if req.DeepMode {
					o.saveAssistantMsg(ctx, req.ConversationID, msg)
				}

				toolCalls := convertToolCalls(msg.ToolCalls)
				allToolCalls = append(allToolCalls, toolCalls...)

				results := executeConcurrently(ctx, events, req.DeviceID, o.registry, toolCalls)

				for i := range toolCalls {
					resJSON, _ := json.Marshal(results[i].result)
					events <- &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)}
					msgs = append(msgs, llm.Message{
						Role:       "tool",
						ToolCallID: msg.ToolCalls[i].ID,
						Content:    string(resJSON),
					})
					if req.DeepMode {
						o.saveToolMsg(ctx, req.ConversationID, msg.ToolCalls[i].ID, string(resJSON))
					}
				}
				continue
			}

			// Flash 文本中的 JSON 解析（非思考模式，tool_calls 嵌入在回复文本中）
			parsed := parseLLMResponse(msg.Content)

			if parsed == nil {
				log.Printf("[orch] round %d pure text reply, len=%d", iter+1, len(msg.Content))
				for _, r := range msg.Content {
					events <- &llm.StreamEvent{Type: "text_delta", Content: string(r)}
				}
				lastAssistantMsgID = o.saveAssistantMsg(ctx, req.ConversationID, msg)
				if len(allToolCalls) >= 2 {
					planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
					events <- &llm.StreamEvent{Type: "plan", Content: string(planJSON)}
				}
				o.emitDone(events, req.ConversationID, lastAssistantMsgID, "")
				return
			}

			// 有嵌入 tool_calls
			log.Printf("[orch] round %d embedded tool_calls=%d: %s", iter+1, len(parsed.ToolCalls), tool.ToolCallNames(parsed.ToolCalls))
			msgs = append(msgs, llm.Message{Role: "assistant", Content: msg.Content})
			allToolCalls = append(allToolCalls, parsed.ToolCalls...)

			results := executeConcurrently(ctx, events, req.DeviceID, o.registry, parsed.ToolCalls)

			for i := range parsed.ToolCalls {
				resJSON, _ := json.Marshal(results[i].result)
				events <- &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)}
				msgs = append(msgs, llm.Message{
					Role:    "user",
					Content: "[工具执行结果] " + string(resJSON),
				})
			}
		}

		events <- &llm.StreamEvent{Type: "error", Error: "max iterations reached"}
		if len(allToolCalls) >= 2 {
			planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
			events <- &llm.StreamEvent{Type: "plan", Content: string(planJSON)}
		}
		o.emitDone(events, req.ConversationID, "", "max iterations reached")
	}()

	return events, nil
}

type toolExecResult struct {
	tc     tool.ToolCall
	result *tool.Result
}

func executeConcurrently(ctx context.Context, events chan<- *llm.StreamEvent, deviceID string, registry *tool.Registry, toolCalls []tool.ToolCall) []toolExecResult {

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
			ec := &tool.ExecuteContext{Ctx: ctx, DeviceID: deviceID}
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

// ——— DB 保存 ———

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
	log.Printf("[orch] save tool msg: conv=%s, tool_call_id=%s, content=%s", convID[:min(8, len(convID))], toolCallID, content[:min(100, len(content))])
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
	doneJSON := "{" + strings.Join(parts, ",") + "}"
	events <- &llm.StreamEvent{Type: "done", Content: doneJSON}
}

// ——— 工具函数 ———

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
