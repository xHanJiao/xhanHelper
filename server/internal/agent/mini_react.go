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
//
// 未来扩展方向：
//   - prompt-engineering 模式（Tools 为空时，工具定义拼入 system prompt，结果 role=user）
//   - OnProgress 回调（子代理向主 Orchestrator 报告中间进度）
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
}

// MiniReActConfig — MiniReAct 依赖配置。
type MiniReActConfig struct {
	Client        llm.LLMClient
	Tools         []llm.ToolDefinition // 原生 FC 工具定义（nil 表示无工具，纯文本对话）
	ToolRegistry  *tool.Registry
	SystemPrompt  string
	UserID        string
	MaxIterations int    // 默认 5
	MaxToolResult int    // 单条工具结果注入上限字符数，默认 2000
	Caller        string // 日志前缀，便于区分调用方
}

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
	}
}

// MiniReActResult — MiniReAct 运行结果。
type MiniReActResult struct {
	FinalContent    string // LLM 最终响应文本（无工具调用时返回）
	Iterations      int
	ToolCalls       int  // 本轮执行的工具调用总数
	TerminatedEarly bool // 达到 maxIterations 提前结束（未产出最终内容）
	Err             error
}

// Run 执行 mini ReAct 循环。
// 每轮：ChatSync（带原生 FC 工具）→ 有 ToolCalls 则执行工具并回传 role=tool → 继续；
// 无 ToolCalls 视为完成，返回 FinalContent。
func (m *MiniReAct) Run(ctx context.Context, userMessage string) (*MiniReActResult, error) {
	msgs := []llm.Message{{Role: "user", Content: userMessage}}
	result := &MiniReActResult{}

	for iter := 0; iter < m.maxIterations; iter++ {
		if ctx.Err() != nil {
			result.Err = ctx.Err()
			result.TerminatedEarly = true
			return result, ctx.Err()
		}

		resp, err := m.client.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: m.systemPrompt,
			Messages:     msgs,
			Tools:        m.tools,
			UserID:       m.userID,
		})
		if err != nil {
			result.Err = err
			result.TerminatedEarly = true
			return result, fmt.Errorf("mini_react[%s] chat iter=%d: %w", m.caller, iter, err)
		}

		result.Iterations = iter + 1

		if len(resp.ToolCalls) > 0 {
			result.ToolCalls += len(resp.ToolCalls)
			msgs = append(msgs, llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
			msgs = m.executeToolCalls(ctx, msgs, resp.ToolCalls)
			continue
		}

		// 无工具调用 → 视为完成
		result.FinalContent = resp.Content
		return result, nil
	}

	result.TerminatedEarly = true
	log.Printf("mini_react[%s] max iterations (%d) reached without final content", m.caller, m.maxIterations)
	return result, nil
}

// executeToolCalls 执行原生 FC 返回的工具调用，将结果以 role=tool 追加到 msgs。
func (m *MiniReAct) executeToolCalls(ctx context.Context, msgs []llm.Message, nativeTCs []llm.ToolCall) []llm.Message {
	internal := nativeToolCallsToInternal(nativeTCs)
	for i, tc := range internal {
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
		resJSON, err := json.Marshal(result)
		if err != nil {
			resJSON = []byte(fmt.Sprintf(`{"success":false,"error":"marshal failed: %s"}`, result.Error))
		}
		if m.maxToolResult > 0 && len(resJSON) > m.maxToolResult {
			resJSON = resJSON[:m.maxToolResult]
		}
		msgs = append(msgs, llm.Message{Role: "tool", Content: string(resJSON), ToolCallID: nativeTCs[i].ID})
	}
	return msgs
}
