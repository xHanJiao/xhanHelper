package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

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
	client           llm.LLMClient
	tools            []llm.ToolDefinition // 原生 FC 工具定义；为空则无工具
	toolRegistry     *tool.Registry
	systemPrompt     string
	finalRoundPrompt string // 强制收敛轮指令（主循环耗尽后追加，禁止再调工具）
	userID           string
	convID           string // 注入 ExecuteContext（工具按会话上下文工作），空=无
	maxIterations    int
	maxToolResult    int
	caller           string
	onEvent          func(evt *llm.StreamEvent) // nil = 静默执行（后台 worker 场景）
	toolCallLog      []ToolCallLog              // 本轮执行的完整工具调用历史
	thinking         *bool                      // 思考模式（deep mode 用 Pro 时开启），nil 用 LLM 默认

	// subAgent 场景扩展（零值 = 不启用，profile_worker 等既有调用方零变化）
	subAgent               SubAgentExt
	clientIdx              map[string]bool // 运行时构建：server_name → 客户端工具分发索引（Run 内初始化）
	clientToolsUnavailable bool            // 运行时状态：客户端断连/超时后置位，后续客户端工具快速失败
}

// SubAgentExt — MiniReAct 的 subAgent 场景扩展配置（含客户端工具、白名单、持久化回调）。
// 零值 = 不启用，profile_worker 等既有调用方零变化。
type SubAgentExt struct {
	ClientTools       []ClientTool      // 客户端工具定义（browser 等）
	ClientBridge      *ClientToolBridge // 客户端工具结果回传桥
	ClientToolTimeout time.Duration     // 客户端工具执行超时，0 = 60s
	AllowedTools      map[string]bool   // 运行时白名单 guard，nil=不限
	MsgSink           MsgSink           // 可选持久化回调，nil=纯内存
}

// MsgSink — MiniReAct 的可选持久化回调。nil = 纯内存运行（profile_worker 现状）。
// RunSubAgent 注入落库实现，子会话消息完整落库（可观测/可调试）。
type MsgSink interface {
	SaveUserMsg(content string)
	SaveAssistantMsg(msg llm.Message)
	SaveToolMsg(toolCallID, content string)
}

// MiniReActConfig — MiniReAct 依赖配置。
type MiniReActConfig struct {
	Client           llm.LLMClient
	Tools            []llm.ToolDefinition // 原生 FC 工具定义（nil 表示无工具，纯文本对话）
	ToolRegistry     *tool.Registry
	SystemPrompt     string
	FinalRoundPrompt string // 强制收敛轮指令（主循环耗尽后追加，禁止再调工具），默认 genericFinalRoundPrompt
	UserID           string
	ConvID           string                 // 注入 ExecuteContext，空=无
	MaxIterations    int                    // 总轮数上限（含强制收敛轮），默认 10
	MaxToolResult    int                    // 单条工具结果注入上限字符数，默认 2000
	Caller           string                 // 日志前缀，便于区分调用方
	Thinking         *bool                  // 思考模式（deep mode 用 Pro 时开启），nil 用 LLM 默认
	OnEvent          func(*llm.StreamEvent) // 事件回调，nil = 静默执行（后台 worker 场景）

	// subAgent 场景扩展（零值 = 不启用，既有调用方零变化）：
	SubAgent SubAgentExt
}

// genericFinalRoundPrompt — 默认强制收敛轮指令。
const genericFinalRoundPrompt = `[系统] 本轮已达任务的最大步数限制。请不要再调用任何工具，直接输出最终结果。
如信息已充分，正常给出最终回答即可，不必提及内部限制。`

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
		maxIter = 10
	}
	maxResult := cfg.MaxToolResult
	if maxResult <= 0 {
		maxResult = 2000
	}
	caller := cfg.Caller
	if caller == "" {
		caller = "mini_react"
	}
	finalPrompt := cfg.FinalRoundPrompt
	if finalPrompt == "" {
		finalPrompt = genericFinalRoundPrompt
	}
	clientToolTimeout := cfg.SubAgent.ClientToolTimeout
	if clientToolTimeout <= 0 {
		clientToolTimeout = 60 * time.Second
	}
	subAgent := cfg.SubAgent
	subAgent.ClientToolTimeout = clientToolTimeout
	return &MiniReAct{
		client:           cfg.Client,
		tools:            cfg.Tools,
		toolRegistry:     cfg.ToolRegistry,
		systemPrompt:     cfg.SystemPrompt,
		finalRoundPrompt: finalPrompt,
		userID:           cfg.UserID,
		convID:           cfg.ConvID,
		maxIterations:    maxIter,
		maxToolResult:    maxResult,
		caller:           caller,
		onEvent:          cfg.OnEvent,
		thinking:         cfg.Thinking,
		subAgent:         subAgent,
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
// 主循环耗尽（达到 maxIterations-1 轮）后追加一轮强制收敛轮（计入 maxIterations），
// 追加 FinalRoundPrompt 并以空工具集调用，强制 LLM 产出最终内容，避免空手结束。
// 事件序列：status（每轮） → [thought] → [tool_call + tool_result]×N → done | error。
func (m *MiniReAct) Run(ctx context.Context, userMessage string) (*MiniReActResult, error) {
	msgs := []llm.Message{{Role: "user", Content: userMessage}}
	result := &MiniReActResult{}
	m.toolCallLog = nil
	m.clientIdx = clientToolIndex(m.subAgent.ClientTools, m.toolRegistry)
	m.clientToolsUnavailable = false

	if m.subAgent.MsgSink != nil {
		m.subAgent.MsgSink.SaveUserMsg(userMessage)
	}

	m.emit(&llm.StreamEvent{
		Type:    MiniReactEventStatus,
		Content: fmt.Sprintf("开始执行（最多 %d 轮）", m.maxIterations),
	})

	// 主循环最多跑 maxIterations-1 轮，预留最后一轮给强制收敛
	for iter := 0; iter < m.maxIterations-1; iter++ {
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
			Thinking:     m.thinking,
			UserID:       m.userID,
		})
		if err != nil {
			result.Err = err
			result.TerminatedEarly = true
			result.ToolCallLog = m.toolCallLog
			m.logToolCallLog(result.ToolCallLog)
			m.emit(&llm.StreamEvent{Type: MiniReactEventError, Error: err.Error()})
			return result, fmt.Errorf("mini_react[%s] chat iter=%d: %w", m.caller, iter, err)
		}

		result.Iterations = iter + 1

		if resp.ReasoningContent != "" {
			m.emit(&llm.StreamEvent{Type: MiniReactEventThought, Content: resp.ReasoningContent})
		}

		if m.subAgent.MsgSink != nil {
			m.subAgent.MsgSink.SaveAssistantMsg(llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		}

		if len(resp.ToolCalls) > 0 {
			log.Printf("mini_react[%s] iter=%d/%d: tool_calls=%d", m.caller, iter+1, m.maxIterations, len(resp.ToolCalls))
			result.ToolCalls += len(resp.ToolCalls)
			msgs = append(msgs, llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
			msgs = m.executeToolCalls(ctx, msgs, resp.ToolCalls)
			continue
		}

		// 无工具调用 → 视为完成
		log.Printf("mini_react[%s] iter=%d/%d: no tool_calls, final content len=%d", m.caller, iter+1, m.maxIterations, len(resp.Content))
		result.FinalContent = resp.Content
		result.ToolCallLog = m.toolCallLog
		m.emitDone(result)
		return result, nil
	}

	// 主循环耗尽 → 强制收敛轮（计入 maxIterations）
	log.Printf("mini_react[%s] iter=%d/%d: max search rounds reached, forcing final round", m.caller, m.maxIterations, m.maxIterations)
	m.emit(&llm.StreamEvent{
		Type:    MiniReactEventStatus,
		Content: fmt.Sprintf("第 %d/%d 轮（强制收敛）", m.maxIterations, m.maxIterations),
	})

	msgs = append(msgs, llm.Message{Role: "assistant", Content: m.finalRoundPrompt})
	resp, err := m.client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: m.systemPrompt,
		Messages:     msgs,
		Tools:        nil, // 强制轮禁工具，逼出最终内容
		Thinking:     m.thinking,
		UserID:       m.userID,
	})
	result.Iterations = m.maxIterations
	if err != nil {
		result.Err = err
		result.TerminatedEarly = true
		result.ToolCallLog = m.toolCallLog
		m.logToolCallLog(result.ToolCallLog)
		m.emit(&llm.StreamEvent{Type: MiniReactEventError, Error: err.Error()})
		return result, fmt.Errorf("mini_react[%s] final round: %w", m.caller, err)
	}
	if m.subAgent.MsgSink != nil {
		m.subAgent.MsgSink.SaveAssistantMsg(llm.Message{Role: "assistant", Content: resp.Content})
	}

	result.FinalContent = resp.Content
	result.ToolCallLog = m.toolCallLog
	log.Printf("mini_react[%s] final round done, content len=%d", m.caller, len(resp.Content))
	m.emitDone(result)
	return result, nil
}

// logToolCallLog 打印工具调用历史摘要（出错/提前结束时调用，便于诊断）。
func (m *MiniReAct) logToolCallLog(logs []ToolCallLog) {
	if len(logs) == 0 {
		log.Printf("mini_react[%s] terminated, tool_calls=0", m.caller)
		return
	}
	log.Printf("mini_react[%s] terminated, tool_calls=%d, log:", m.caller, len(logs))
	for i, tc := range logs {
		ok := "fail"
		if tc.Result != nil && tc.Result.Success {
			ok = "success"
		}
		log.Printf("mini_react[%s]   [%d] %s.%s args=%s result=%s", m.caller, i+1, tc.Tool, tc.Function, truncateStr(string(tc.Args), 120), ok)
	}
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
// 分发顺序：白名单 guard → 客户端工具（clientIdx）→ 服务端 registry。
func (m *MiniReAct) executeToolCalls(ctx context.Context, msgs []llm.Message, nativeTCs []llm.ToolCall) []llm.Message {
	internal := nativeToolCallsToInternal(nativeTCs)
	for i, tc := range internal {
		m.toolRegistry.FillDisplay(&tc)
		if tcJSON, err := json.Marshal(tc); err == nil {
			m.emit(&llm.StreamEvent{Type: MiniReactEventToolCall, Content: string(tcJSON)})
		}

		var result *tool.Result
		if m.subAgent.AllowedTools != nil && !m.subAgent.AllowedTools[tc.Tool] {
			// 白名单 guard：LLM 幻觉出未授权工具（原生 FC 下本不应发生，防御性拦截）
			log.Printf("mini_react[%s] whitelist guard: %s not allowed", m.caller, tc.Tool)
			result = &tool.Result{Success: false, Error: fmt.Sprintf("子任务无权调用工具 '%s'（不在白名单中）", tc.Tool)}
		} else if m.clientIdx[tc.Tool] {
			result, _ = m.execClientTool(ctx, tc)
		} else {
			func() {
				defer func() {
					if p := recover(); p != nil {
						log.Printf("mini_react[%s] tool exec panic: %s.%s panic=%v", m.caller, tc.Tool, tc.Function, p)
						result = &tool.Result{Success: false, Error: fmt.Sprintf("工具执行异常: %v", p)}
					}
				}()
				ec := &tool.ExecuteContext{Ctx: ctx, UserID: m.userID, ConvID: m.convID, Mode: model.ConvModeWork}
				result = m.toolRegistry.ExecuteByName(ec, tc.Tool, tc.Function, tc.Args)
			}()
		}

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
		if m.subAgent.MsgSink != nil {
			m.subAgent.MsgSink.SaveToolMsg(nativeTCs[i].ID, string(resJSON))
		}
		log.Printf("mini_react[%s]   exec %s.%s success=%v result_bytes=%d", m.caller, tc.Tool, tc.Function, result.Success, len(resJSON))
		m.emit(&llm.StreamEvent{Type: MiniReactEventToolResult, Content: string(resJSON)})
		msgs = append(msgs, llm.Message{Role: "tool", Content: string(resJSON), ToolCallID: nativeTCs[i].ID})
	}
	return msgs
}

// clientToolBudgetFor 计算客户端工具的执行预算（clientToolCaller 的等待超时）。
// ui server（ask_user 用户介入）特判：用户思考时间不计入 120s 默认预算，
// 用 ctx 剩余时间（subAgent 600s 总预算自然封顶）；无 deadline 时回退默认值。
func (m *MiniReAct) clientToolBudgetFor(ctx context.Context, toolName string) time.Duration {
	if toolName == "ui" {
		if dl, ok := ctx.Deadline(); ok {
			if remaining := time.Until(dl); remaining > 0 {
				return remaining
			}
		}
	}
	return m.subAgent.ClientToolTimeout
}

// execClientTool 经共享 clientToolCaller 分派客户端工具。
// 超时/断连后置位 clientToolsUnavailable，本次 Run 内后续客户端工具快速失败。
// 例外：ui 工具（ask_user 用户介入）豁免 fast-fail——它只依赖客户端 UI 展示，
// 不依赖浏览器执行链路；客户端真断连时由 client_tool_call 5s 送达超时兜底报错。
func (m *MiniReAct) execClientTool(ctx context.Context, tc tool.ToolCall) (*tool.Result, bool) {
	if m.clientToolsUnavailable && tc.Tool != "ui" {
		return &tool.Result{Success: false,
			Error: "客户端本地工具不可用（本轮已发生断连或超时），已跳过 " + tc.Tool + "." + tc.Function + "。"}, false
	}
	cc := &clientToolCaller{bridge: m.subAgent.ClientBridge, timeout: m.clientToolBudgetFor(ctx, tc.Tool)}
	r, unavailable := cc.Call(ctx,
		// MiniReAct 无内部 channel：事件经 onEvent 回调转发（RunSubAgent 的 forwarder 提供阻塞送达语义）
		func(evt *llm.StreamEvent) bool {
			m.emit(evt)
			return true
		},
		func(evt *llm.StreamEvent) { m.emit(evt) },
		m.userID, tc, model.ConvModeWork)
	if unavailable {
		m.clientToolsUnavailable = true
	}
	return r, unavailable
}
