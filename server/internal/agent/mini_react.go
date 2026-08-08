package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// MiniReAct — 通用 mini ReAct 循环组件。
//
// 与主 Orchestrator 的流式 ReAct 不同，MiniReAct 面向后台任务/子代理（subAgent）场景：
//   - 同步调用（ChatSync），不流式
//   - 原生 function calling（Tools 非空时），工具结果以 role=tool 回传
//   - 自包含：一个实例 = 独立 system prompt + 消息历史 + 工具沙箱，天然适合子代理隔离
//   - 事件回调（可选）：通过 OnEvent 上报执行过程，事件名带 mini_react: 前缀，避免与主对话 SSE 事件串台
//
// 未来扩展方向：
//   - prompt-engineering 模式（Tools 为空时，工具定义拼入 system prompt，结果 role=user）
//   - 工具并发执行
type MiniReAct struct {
	client        llm.LLMClient
	tools         []llm.ToolDefinition // 原生 FC 工具定义；为空则无工具
	toolRegistry  *tool.Registry
	systemPrompt  string
	userID        string
	maxIterations int
	maxToolResult int
	caller        string
	onEvent       func(evt *llm.StreamEvent) // nil = 静默执行（后台 worker 场景）
	toolCallLog   []ToolCallLog              // 本轮执行的完整工具调用历史
}

// MiniReActConfig — MiniReAct 依赖配置。
type MiniReActConfig struct {
	Client        llm.LLMClient
	Tools         []llm.ToolDefinition // 原生 FC 工具定义（nil 表示无工具，纯文本对话）
	ToolRegistry  *tool.Registry
	SystemPrompt  string
	UserID        string
	MaxIterations int                    // 默认 5
	MaxToolResult int                    // 单条工具结果注入上限字符数，默认 2000
	Caller        string                 // 日志前缀，便于区分调用方
	OnEvent       func(*llm.StreamEvent) // 事件回调，nil = 静默执行（后台 worker 场景）
}

// MiniReAct 事件类型（带 mini_react: 前缀，独立命名空间，避免与主对话 SSE 事件串台）。
const (
	MiniReactEventStatus     = "mini_react:status"      // 阶段状态
	MiniReactEventToolCall   = "mini_react:tool_call"   // 工具调用
	MiniReactEventToolResult = "mini_react:tool_result" // 工具结果
	MiniReactEventThought    = "mini_react:thought"     // 思考内容
	MiniReactEventDone       = "mini_react:done"        // 结束（Content 含 iterations/tool_calls/tool_call_logs）
	MiniReactEventError      = "mini_react:error"       // 错误
)

// NewMiniReAct 创建 MiniReAct。必需依赖为 nil 时 panic（接线 bug 暴露在明处）。
func NewMiniReAct(cfg *MiniReActConfig) *MiniReAct {
	if cfg.Client == nil || cfg.ToolRegistry == nil {
		panic("mini_react: required dependency nil (wiring bug)")
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 5
	}
	maxResult := cfg.MaxToolResult
	if maxResult <= 0 {
		maxResult = 2000
	}
	caller := cfg.Caller
	if caller == "" {
		caller = "mini_react"
	}
	return &MiniReAct{
		client:        cfg.Client,
		tools:         cfg.Tools,
		toolRegistry:  cfg.ToolRegistry,
		systemPrompt:  cfg.SystemPrompt,
		userID:        cfg.UserID,
		maxIterations: maxIter,
		maxToolResult: maxResult,
		caller:        caller,
		onEvent:       cfg.OnEvent,
	}
}

// emit 发送事件。onEvent 为 nil 时静默（后台 worker 场景零开销）。
// 回调内 panic 视为组件 bug，让上层 recover 处理（不吞）。
func (m *MiniReAct) emit(evt *llm.StreamEvent) {
	if m.onEvent == nil {
		return
	}
	m.onEvent(evt)
}

// MiniReActResult — MiniReAct 运行结果。
type MiniReActResult struct {
	FinalContent    string // LLM 最终响应文本（无工具调用时返回）
	Iterations      int
	ToolCalls       int           // 本轮执行的工具调用总数
	TerminatedEarly bool          // 达到 maxIterations 提前结束（未产出最终内容）
	ToolCallLog     []ToolCallLog // 完整工具调用历史（done 事件同款）
	Err             error
}

// Run 执行 mini ReAct 循环。
// 每轮：ChatSync（带原生 FC 工具）→ 有 ToolCalls 则执行工具并回传 role=tool → 继续；
// 无 ToolCalls 视为完成，返回 FinalContent。
// 事件序列：status（每轮） → [thought] → [tool_call + tool_result]×N → done | error。
func (m *MiniReAct) Run(ctx context.Context, userMessage string) (*MiniReActResult, error) {
	msgs := []llm.Message{{Role: "user", Content: userMessage}}
	result := &MiniReActResult{}
	m.toolCallLog = nil

	m.emit(&llm.StreamEvent{
		Type:    MiniReactEventStatus,
		Content: fmt.Sprintf("开始执行（最多 %d 轮）", m.maxIterations),
	})

	for iter := 0; iter < m.maxIterations; iter++ {
		if ctx.Err() != nil {
			result.Err = ctx.Err()
			result.TerminatedEarly = true
			result.ToolCallLog = m.toolCallLog
			m.emit(&llm.StreamEvent{Type: MiniReactEventError, Error: "上下文已取消"})
			return result, ctx.Err()
		}

		m.emit(&llm.StreamEvent{
			Type:    MiniReactEventStatus,
			Content: fmt.Sprintf("第 %d/%d 轮", iter+1, m.maxIterations),
		})

		resp, err := m.client.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: m.systemPrompt,
			Messages:     msgs,
			Tools:        m.tools,
			UserID:       m.userID,
		})
		if err != nil {
			result.Err = err
			result.TerminatedEarly = true
			result.ToolCallLog = m.toolCallLog
			m.emit(&llm.StreamEvent{Type: MiniReactEventError, Error: err.Error()})
			return result, fmt.Errorf("mini_react[%s] chat iter=%d: %w", m.caller, iter, err)
		}

		result.Iterations = iter + 1

		if resp.ReasoningContent != "" {
			m.emit(&llm.StreamEvent{Type: MiniReactEventThought, Content: resp.ReasoningContent})
		}

		if len(resp.ToolCalls) > 0 {
			result.ToolCalls += len(resp.ToolCalls)
			msgs = append(msgs, llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
			msgs = m.executeToolCalls(ctx, msgs, resp.ToolCalls)
			continue
		}

		// 无工具调用 → 视为完成
		result.FinalContent = resp.Content
		result.ToolCallLog = m.toolCallLog
		m.emitDone(result)
		return result, nil
	}

	result.TerminatedEarly = true
	result.ToolCallLog = m.toolCallLog
	log.Printf("mini_react[%s] max iterations (%d) reached without final content", m.caller, m.maxIterations)
	m.emitDone(result)
	return result, nil
}

// emitDone 发送 done 事件，Content 为 JSON（含 iterations/tool_calls/tool_call_logs）。
func (m *MiniReAct) emitDone(result *MiniReActResult) {
	b, err := json.Marshal(struct {
		Iterations  int           `json:"iterations"`
		ToolCalls   int           `json:"tool_calls"`
		ToolCallLog []ToolCallLog `json:"tool_call_logs"`
	}{
		Iterations:  result.Iterations,
		ToolCalls:   result.ToolCalls,
		ToolCallLog: result.ToolCallLog,
	})
	if err != nil {
		log.Printf("mini_react[%s] marshal done event: %v", m.caller, err)
		return
	}
	m.emit(&llm.StreamEvent{Type: MiniReactEventDone, Content: string(b)})
}

// executeToolCalls 执行原生 FC 返回的工具调用，将结果以 role=tool 追加到 msgs。
// 每次调用发送 tool_call + tool_result 事件，并累积到 m.toolCallLog。
func (m *MiniReAct) executeToolCalls(ctx context.Context, msgs []llm.Message, nativeTCs []llm.ToolCall) []llm.Message {
	internal := nativeToolCallsToInternal(nativeTCs)
	for i, tc := range internal {
		m.toolRegistry.FillDisplay(&tc)
		if tcJSON, err := json.Marshal(tc); err == nil {
			m.emit(&llm.StreamEvent{Type: MiniReactEventToolCall, Content: string(tcJSON)})
		}

		var result *tool.Result
		func() {
			defer func() {
				if p := recover(); p != nil {
					log.Printf("mini_react[%s] tool exec panic: %s.%s panic=%v", m.caller, tc.Tool, tc.Function, p)
					result = &tool.Result{Success: false, Error: fmt.Sprintf("工具执行异常: %v", p)}
				}
			}()
			ec := &tool.ExecuteContext{Ctx: ctx, UserID: m.userID, Mode: model.ConvModeWork}
			result = m.toolRegistry.ExecuteByName(ec, tc.Tool, tc.Function, tc.Args)
		}()

		m.toolCallLog = append(m.toolCallLog, ToolCallLog{
			Tool:     tc.Tool,
			Function: tc.Function,
			Args:     tc.Args,
			Result:   result,
		})

		resJSON, err := json.Marshal(result)
		if err != nil {
			resJSON = []byte(fmt.Sprintf(`{"success":false,"error":"marshal failed: %s"}`, result.Error))
		}
		if m.maxToolResult > 0 && len(resJSON) > m.maxToolResult {
			resJSON = resJSON[:m.maxToolResult]
		}
		m.emit(&llm.StreamEvent{Type: MiniReactEventToolResult, Content: string(resJSON)})
		msgs = append(msgs, llm.Message{Role: "tool", Content: string(resJSON), ToolCallID: nativeTCs[i].ID})
	}
	return msgs
}
