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

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// ReactLoopResult — ReAct 循环的最终结果
type ReactLoopResult struct {
	FinalResponse   string        // 最后一轮 LLM 的完整响应文本
	ToolCallsLog    []ToolCallLog // 工具调用日志
	Iterations      int
	Error           error // 循环级错误（LLM 调用失败等）
	TerminatedEarly bool  // 是否因 max iterations 提前终止（未自然结束）
}

// ToolCallLog — 一次工具调用的记录
type ToolCallLog struct {
	Tool     string          `json:"tool"`
	Function string          `json:"function"`
	Args     json.RawMessage `json:"args"`
	Result   *tool.Result    `json:"result"`
}

type traceCtxKey struct{}

// WithTraceID 将 traceID 注入 ctx（子 Agent 通过 CtxTraceID 读取）
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, traceID)
}

// CtxTraceID 从 ctx 取出 traceID
func CtxTraceID(ctx context.Context) string {
	if id, ok := ctx.Value(traceCtxKey{}).(string); ok {
		return id
	}
	return ""
}

type ReactLoop struct {
	client        llm.LLMClient
	tools         map[string]tool.Tool
	toolReg       *tool.Registry
	systemPrompt  string
	maxIterations int
	userID        string
	caller        string
	traceRepo     *service.ReflectionTraceRepo // nil = 不写 trace
	traceID       string
}

// NewReactLoop 创建 ReAct 循环（traceRepo 传 nil 则跳过 trace 记录）
func NewReactLoop(client llm.LLMClient, tools map[string]tool.Tool, systemPrompt string, maxIterations int, userID string, caller string, traceRepo *service.ReflectionTraceRepo, traceID string) *ReactLoop {
	reg := tool.NewRegistry()
	for _, t := range tools {
		reg.Register(t)
	}
	if maxIterations <= 0 {
		maxIterations = 4
	}
	return &ReactLoop{
		client:        client,
		tools:         tools,
		toolReg:       reg,
		systemPrompt:  systemPrompt,
		maxIterations: maxIterations,
		userID:        userID,
		caller:        caller,
		traceRepo:     traceRepo,
		traceID:       traceID,
	}
}

// Run 执行 ReAct 循环
func (rl *ReactLoop) Run(ctx context.Context, userMessage string) (*ReactLoopResult, error) {
	result := &ReactLoopResult{}

	msgs := []llm.Message{
		{Role: "user", Content: userMessage},
	}

	for iter := 0; iter < rl.maxIterations; iter++ {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		result.Iterations = iter + 1

		// 构建本轮 system prompt（含工具定义）
		iterSysPrompt := rl.systemPrompt + rl.toolReg.GeneratePrompt()

		llmStart := time.Now()
		resp, err := rl.client.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: iterSysPrompt,
			Messages:     msgs,
			Tools:        nil, // prompt engineering 模式
			UserID:       rl.userID,
		})
		latencyMs := time.Since(llmStart).Milliseconds()

		if err != nil {
			log.Printf("[react_loop] LLM call error (iter %d): %v", iter, err)
			result.Error = fmt.Errorf("LLM call (iter %d): %w", iter, err)

			return result, nil
		}

		var responseJSON string
		if resp.ResponseJSON != nil {
			responseJSON = *resp.ResponseJSON
		}
		pt, ct := extractUsage(responseJSON)

		// 自动写入 ReflectionLLMCall
		var reqJSON, respJSON *string
		if resp.RequestJSON != nil {
			s := compactJSON(*resp.RequestJSON)
			reqJSON = &s
		}
		if resp.ResponseJSON != nil {
			s := compactJSON(*resp.ResponseJSON)
			respJSON = &s
		}
		call := &model.ReflectionLLMCall{
			ID:               uuid.New().String(),
			TraceID:          rl.traceID,
			Caller:           rl.caller,
			RequestJSON:      reqJSON,
			ResponseJSON:     respJSON,
			PromptTokens:     pt,
			CompletionTokens: ct,
			LatencyMs:        latencyMs,
		}
		if err := rl.traceRepo.CreateLLMCall(ctx, call); err != nil {
			log.Printf("[react_loop] create llm call error: %v", err)
		}

		// 解析 LLM 响应中的 JSON
		parsed, parseErr := ParseLLMResponse(resp.Content)
		if parseErr != nil {
			// 非法 JSON → 注入重试消息，继续循环
			log.Printf("[react_loop] iter %d parse error: %v, retrying", iter, parseErr)
			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: "[系统] 你上一轮的回复不是有效的 JSON 格式。请严格按照 {\"reply\":\"文字回复\",\"tool_calls\":[{\"tool\":\"工具名\",\"function\":\"函数名\",\"args\":{...}}]} 格式重新生成。",
			})
			continue
		}
		if parsed == nil || len(parsed.ToolCalls) == 0 {
			// 纯文本回复或无工具调用 → 结束
			result.FinalResponse = resp.Content
			msgs = append(msgs, llm.Message{Role: "assistant", Content: resp.Content})

			return result, nil
		}

		// 有工具调用 → 执行
		log.Printf("[react_loop] iter %d: %d tool calls: %s", iter, len(parsed.ToolCalls), tool.ToolCallNames(parsed.ToolCalls))

		msgs = append(msgs, llm.Message{Role: "assistant", Content: resp.Content})

		results := rl.executeConcurrently(ctx, parsed.ToolCalls, result)

		// 将工具结果反馈到消息中 + 记录 skip_reason 决策
		for i := range parsed.ToolCalls {
			resJSON, err := json.Marshal(results[i].Result)
			if err != nil {
				log.Printf("[react_loop] marshal tool result error: %v", err)
				resJSON = []byte(`{"error":"marshal failed"}`)
			}
			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: string(resJSON),
			})

			// ThemeAnalyst 返回 skip_reason 时记录业务层决策
			tcLog := results[i]
			if tcLog.Tool == "theme_analyst" && tcLog.Result != nil && tcLog.Result.Success && tcLog.Result.Data != nil {
				var taData struct {
					SkipReason string `json:"skip_reason"`
					SkipDetail string `json:"skip_detail"`
				}
				if json.Unmarshal(tcLog.Result.Data, &taData) == nil && taData.SkipReason != "" {
					createDecisionSafe(rl.traceRepo, ctx, &model.ReflectionDecision{
						ID:      uuid.New().String(),
						TraceID: rl.traceID,
						Stage:   "theme_analysis",
						Subject: "theme_analyst:" + shortThemeName(string(tcLog.Args)),
						Verdict: "skipped",
						Reason:  taData.SkipReason + ": " + taData.SkipDetail,
					})
				}
			}
		}

		result.FinalResponse = resp.Content
	}

	log.Printf("[react_loop] max iterations (%d) reached", rl.maxIterations)

	result.TerminatedEarly = true
	return result, nil
}

func (rl *ReactLoop) executeConcurrently(ctx context.Context, toolCalls []tool.ToolCall, result *ReactLoopResult) []ToolCallLog {
	results := make([]ToolCallLog, len(toolCalls))

	var wg sync.WaitGroup
	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, tc tool.ToolCall) {
			defer wg.Done()
			ec := &tool.ExecuteContext{Ctx: ctx, UserID: rl.userID}
			// 工具 panic 不能掀翻整个进程（gin.Recovery 只覆盖 HTTP handler）。
			var res *tool.Result
			func() {
				defer func() {
					if p := recover(); p != nil {
						log.Printf("[react_loop] tool exec panic: %s.%s panic=%v", tc.Tool, tc.Function, p)
						res = &tool.Result{Success: false, Error: fmt.Sprintf("工具执行异常: %v", p)}
					}
				}()
				res = rl.toolReg.ExecuteByName(ec, tc.Tool, tc.Function, tc.Args)
			}()
			results[idx] = ToolCallLog{
				Tool:     tc.Tool,
				Function: tc.Function,
				Args:     tc.Args,
				Result:   res,
			}
			// 工具执行完立即写 trace
			detailB, err := json.Marshal(results[idx])
			if err != nil {
				log.Printf("[react_loop] marshal tool call log error: %v", err)
				return
			}
			detailStr := string(detailB)
			verdict := "success"
			reason := fmt.Sprintf("%s.%s: ok", tc.Tool, tc.Function)
			if !res.Success {
				verdict = "error"
				reason = fmt.Sprintf("%s.%s: %s", tc.Tool, tc.Function, res.Error)
			}
			createDecisionSafe(rl.traceRepo, ctx, &model.ReflectionDecision{
				ID:         uuid.New().String(),
				TraceID:    rl.traceID,
				Stage:      "tool_execution",
				Subject:    tc.Tool + "." + tc.Function,
				Verdict:    verdict,
				Reason:     reason,
				DetailJSON: &detailStr,
			})
		}(i, tc)
	}
	wg.Wait()

	result.ToolCallsLog = append(result.ToolCallsLog, results...)
	return results
}

// compactJSON 压缩 JSON（parse → 递归展开内嵌 JSON 字符串 → 单行序列化）
func compactJSON(raw string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	unrolled := unrollJSONStrings(v)
	b, err := json.Marshal(unrolled)
	if err != nil {
		return raw
	}
	return string(b)
}

// unrollJSONStrings 递归展开对象中的 JSON 字符串值
func unrollJSONStrings(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, vv := range val {
			out[k] = unrollJSONStrings(vv)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, vv := range val {
			out[i] = unrollJSONStrings(vv)
		}
		return out
	case string:
		trimmed := strings.TrimSpace(val)
		if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && len(trimmed) > 2 {
			var nested interface{}
			if err := json.Unmarshal([]byte(trimmed), &nested); err == nil {
				return unrollJSONStrings(nested)
			}
		}
		return val
	default:
		return v
	}
}

// shortThemeName 从 theme_analyst 的 args JSON 中提取主题名
func shortThemeName(argsJSON string) string {
	var a struct {
		ThemeName string `json:"theme_name"`
	}
	if json.Unmarshal([]byte(argsJSON), &a) == nil && a.ThemeName != "" {
		return a.ThemeName
	}
	return "unknown"
}

// extractUsage 从 ResponseJSON 中解析 token 用量
func extractUsage(responseJSON string) (promptTokens, completionTokens int) {
	var v struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(responseJSON), &v) == nil {
		return v.Usage.PromptTokens, v.Usage.CompletionTokens
	}
	return 0, 0
}
