package agent

import (
	"context"
	"encoding/json"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

const baseSystemPrompt = `你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

核心原则：
1. 简洁回复：用最少的字把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 主动：如果用户意图不明确，追问关键信息`

const maxIterations = 5

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
	llmClient llm.LLMClient
	registry  *tool.Registry
}

// New 创建 Orchestrator
func New(llmClient llm.LLMClient, registry *tool.Registry) *Orchestrator {
	return &Orchestrator{llmClient: llmClient, registry: registry}
}

// Run 启动 ReAct 循环，返回事件 channel
func (o *Orchestrator) Run(ctx context.Context, req *RunRequest) (<-chan *llm.StreamEvent, error) {
	sysPrompt := baseSystemPrompt + o.registry.GeneratePrompt()

	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	events := make(chan *llm.StreamEvent, 64)

	go func() {
		defer close(events)

		for iter := 0; iter < maxIterations; iter++ {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// 调用 LLM
			resp, err := o.llmClient.ChatSync(ctx, &llm.ChatRequest{
				SystemPrompt: sysPrompt,
				Messages:     msgs,
			})
			if err != nil {
				events <- &llm.StreamEvent{Type: "error", Error: err.Error()}
				return
			}

			// 尝试解析 JSON
			parsed := parseLLMResponse(resp)

			if parsed == nil {
				// 纯文本回复 → 流式输出 → done
				for _, r := range resp {
					events <- &llm.StreamEvent{Type: "text_delta", Content: string(r)}
				}
				events <- &llm.StreamEvent{Type: "done"}
				return
			}

			// 有 tool_calls → 执行
			// 将 LLM 的原始 JSON 追加到历史
			msgs = append(msgs, llm.Message{Role: "assistant", Content: resp})

			for _, tc := range parsed.ToolCalls {
				// 发送 tool_call 事件
				tcJSON, _ := json.Marshal(tc)
				events <- &llm.StreamEvent{Type: "tool_call", Content: string(tcJSON)}

				// 执行工具
				ec := &tool.ExecuteContext{
					Ctx:      ctx,
					DeviceID: req.DeviceID,
				}
				result := o.registry.ExecuteByName(ec, tc.Tool, tc.Function, tc.Args)

				// 发送 tool_result 事件
				resJSON, _ := json.Marshal(result)
				events <- &llm.StreamEvent{Type: "tool_result", Content: string(resJSON)}

				// 追加 tool result 到历史
				msgs = append(msgs, llm.Message{
					Role:    "tool",
					Content: string(resJSON),
				})
			}

			// 如果 tool_calls 为空，说明是纯聊天 JSON → 输出 reply → done
			if len(parsed.ToolCalls) == 0 {
				for _, r := range parsed.Reply {
					events <- &llm.StreamEvent{Type: "text_delta", Content: string(r)}
				}
				events <- &llm.StreamEvent{Type: "done"}
				return
			}

			// tool_calls 非空，且 reply 中有文本 → 先输出 reply 再继续循环
			if parsed.Reply != "" {
				for _, r := range parsed.Reply {
					events <- &llm.StreamEvent{Type: "text_delta", Content: string(r)}
				}
			}
			// 继续下一轮迭代（LLM 会看到 tool results 后给出最终回复）
		}

		// 超出最大迭代
		events <- &llm.StreamEvent{Type: "error", Error: "max iterations reached"}
		events <- &llm.StreamEvent{Type: "done"}
	}()

	return events, nil
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

