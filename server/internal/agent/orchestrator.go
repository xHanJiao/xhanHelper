package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"strings"

	"xhan-helper/internal/llm"
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
	DeviceID string
	Messages []llm.Message
}

// LLMResponse — LLM 返回的结构化 JSON
type LLMResponse struct {
	Reply     string        `json:"reply"`
	ToolCalls []tool.ToolCall `json:"tool_calls"`
}

// Orchestrator — ReAct 循环编排器
type Orchestrator struct {
	llmClient   llm.LLMClient
	registry    *tool.Registry
	memoryStore MemoryStore
}

// New 创建 Orchestrator
func New(llmClient llm.LLMClient, registry *tool.Registry, memoryStore MemoryStore) *Orchestrator {
	return &Orchestrator{llmClient: llmClient, registry: registry, memoryStore: memoryStore}
}

// Run 启动 ReAct 循环，返回事件 channel
func (o *Orchestrator) Run(ctx context.Context, req *RunRequest) (<-chan *llm.StreamEvent, error) {
	sysPrompt := o.llmClient.BuildSystemPrompt(baseSystemPrompt, o.registry.GetLLMTools())

	// 检索相关记忆（在构建 system prompt 时同步完成）
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

		// 先发记忆状态
		if memoryNote != "" {
			events <- &llm.StreamEvent{Type: "status", Content: memoryNote}
		}

		var allToolCalls []tool.ToolCall

		for iter := 0; iter < maxIterations; iter++ {
			select {
			case <-ctx.Done():
			log.Printf("[orch] context cancelled")
			events <- &llm.StreamEvent{Type: "error", Error: "request cancelled"}
			events <- &llm.StreamEvent{Type: "done"}
			return
			default:
			}

			// 调用 LLM
			resp, err := o.llmClient.ChatSync(ctx, &llm.ChatRequest{
				SystemPrompt: sysPrompt,
				Messages:     msgs,
			Tools:        o.registry.GetLLMTools(),
			})
			if err != nil {
				events <- &llm.StreamEvent{Type: "error", Error: err.Error()}
				return
			}

			// 提取 JSON 前的思考文本作为 thought 事件
			cleaned := strings.TrimSpace(resp)
			if idx := strings.Index(cleaned, "{"); idx > 0 {
				thoughtText := strings.TrimSpace(cleaned[:idx])
				if thoughtText != "" {
					events <- &llm.StreamEvent{Type: "thought", Content: thoughtText}
				}
			}

			// 尝试解析 JSON
			parsed := parseLLMResponse(resp)

			if parsed == nil {
				// 纯文本回复 → 流式输出 → done
				for _, r := range resp {
					events <- &llm.StreamEvent{Type: "text_delta", Content: string(r)}
				}
			if len(allToolCalls) >= 2 {
				planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
				events <- &llm.StreamEvent{Type: "plan", Content: string(planJSON)}
			}
				events <- &llm.StreamEvent{Type: "done"}
				return
			}

			// 有 tool_calls → 执行（2+ 时自动并发）
			msgs = append(msgs, llm.Message{Role: "assistant", Content: resp})
		allToolCalls = append(allToolCalls, parsed.ToolCalls...)

			if len(parsed.ToolCalls) >= 2 {
				log.Printf("[orch] concurrent exec %d tools", len(parsed.ToolCalls))
			}

			type toolResult struct {
				tc     tool.ToolCall
				result *tool.Result
			}

			var wg sync.WaitGroup
			results := make([]toolResult, len(parsed.ToolCalls))

			for i, tc := range parsed.ToolCalls {
				tcJSON, _ := json.Marshal(tc)
				events <- &llm.StreamEvent{Type: "tool_call", Content: string(tcJSON)}

				wg.Add(1)
				go func(idx int, call tool.ToolCall) {
					defer wg.Done()
					ec := &tool.ExecuteContext{Ctx: ctx, DeviceID: req.DeviceID}
					results[idx] = toolResult{
						tc:     call,
						result: o.registry.ExecuteByName(ec, call.Tool, call.Function, call.Args),
					}
				}(i, tc)
			}
			wg.Wait()

			for _, r := range results {
				resJSON, _ := json.Marshal(r.result)
				events <- &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)}
				msgs = append(msgs, llm.Message{
					Role:    "user",
					Content: "[工具执行结果] " + string(resJSON),
				})
			}
			// 如果 tool_calls 为空，说明是最终回复 → 输出 reply → done
			if len(parsed.ToolCalls) == 0 {
				for _, r := range parsed.Reply {
					events <- &llm.StreamEvent{Type: "text_delta", Content: string(r)}
				}
			if len(allToolCalls) >= 2 {
				planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
				events <- &llm.StreamEvent{Type: "plan", Content: string(planJSON)}
			}
				events <- &llm.StreamEvent{Type: "done"}
				return
			}

			// tool_calls 非空 → 不输出文本，等下一轮 LLM 拿到 tool results 后的最终回复
			// 继续下一轮迭代
		}

		// 超出最大迭代
		events <- &llm.StreamEvent{Type: "error", Error: "max iterations reached"}
			if len(allToolCalls) >= 2 {
				planJSON, _ := json.Marshal(buildPlanEvent(allToolCalls))
				events <- &llm.StreamEvent{Type: "plan", Content: string(planJSON)}
			}
		events <- &llm.StreamEvent{Type: "done"}
	}()

	return events, nil
}

// GenerateTitle 根据用户消息生成会话标题（异步调用，失败返回空字符串）
func (o *Orchestrator) GenerateTitle(ctx context.Context, userMessage string) string {
	resp, err := o.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是一个标题生成器，根据用户的消息内容生成一个10字以内的简短标题，只输出标题本身，不要加任何标点或引号。",
		Messages:     []llm.Message{{Role: "user", Content: userMessage}},
	})
	if err != nil {
		return ""
	}
	title := strings.TrimSpace(resp)
	if len([]rune(title)) > 20 {
		title = string([]rune(title)[:20])
	}
	return title
}

// parseLLMResponse 尝试从 LLM 回复中解析 JSON
// 返回 nil 表示纯文本回复（不含 JSON）
func parseLLMResponse(text string) *LLMResponse {
	cleaned := strings.TrimSpace(text)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	// 尝试找到 JSON 对象
	start := strings.Index(cleaned, "{")
	if start < 0 {
		return nil // 纯文本
	}

	// 从第一个 { 开始解析
	jsonStr := cleaned[start:]

	var resp LLMResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		// JSON 格式错误 → 降级为纯文本
		return nil
	}

	// 确保 tool_calls 不为 nil
	if resp.ToolCalls == nil {
		resp.ToolCalls = []tool.ToolCall{}
	}

	return &resp
}


// buildPlanEvent 构建计划事件（客户端图可视化用）
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
