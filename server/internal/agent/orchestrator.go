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

				// 追加 tool result 到历史（豆包不支持 role=tool，用 user 代替）
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
				events <- &llm.StreamEvent{Type: "done"}
				return
			}

			// tool_calls 非空 → 不输出文本，等下一轮 LLM 拿到 tool results 后的最终回复
			// 继续下一轮迭代
		}

		// 超出最大迭代
		events <- &llm.StreamEvent{Type: "error", Error: "max iterations reached"}
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

