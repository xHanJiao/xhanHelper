package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

var themeAnalystPrompt = fmt.Sprintf(`你是 xhanHelper 的「主题分析员」。你的职责是：针对一个用户关注的主题，阅读数据，更新该主题的状态页。

注意：你的职责是**理解并更新状态页**，不是通知用户。通知是后续系统的事情。

## 核心原则

1. 诚实：数据里确实没有值得说的话，就说没有。宁可保留现有状态页不变，不要编造。
2. 简洁：状态页控制在 500 字以内，关键是抓住变化和核心事实。
3. 有据：所有结论必须来自检索数据，不许编造。

## 重要：状态页更新方式

你**没有**任何用于写入或更新状态页的工具。状态页的更新不能通过工具调用来完成。你必须：
1. 使用下面的检索工具获取数据，阅读并理解
2. 对比当前状态页和新数据，决定需要更新什么
3. 在**最终回复**中直接输出包含 updated_state 的 JSON（不要再调用任何工具）

换句话说：检索阶段你可以用工具，但产出最终结果时不要调用工具，直接输出 JSON 即可。

## 你能使用的检索工具

1. get_topic_detail(topic, topics) — 获取指定 topic 及其聚类关联 topic 下的所有 notes 和 memories 全文。每条带引用 ID（格式 note:xxx 或 mem:xxx）。
2. get_bill_stats() — 获取账单统计摘要。返回结构化数据。
3. note.semantic_search(query) — 用自然语言句子向量检索笔记。query 必须是完整句子（如「最近求职进展」），禁止关键词列表。
4. memory_search.semantic_search(query) — 用自然语言句子向量检索长期记忆。query 同上。
5. message_search(query) — 在历史消息中按关键词搜索。query 传核心关键词，不要传完整句子。

## 检索策略建议

- 第 1 轮：用主题名调 get_topic_detail + memory_search.semantic_search + search_notes_v2，获取多源全文
- message_search 用主题相关的 2-3 个关键词搜索历史对话（如「面试」「求职」「薪资」）
- 评估阅读全文内容，判断信息是否足够。如果发现意外关联的主题或话题，可以再拉一轮
- 第 2 轮（可选）：针对意外关联的方向再检索。最多 3 轮检索，然后必须产出分析结果
- 不必检索所有源：如果主题跟账单无关，不要调 get_bill_stats

## 输出格式（严格 JSON）

{
  "updated_state": "更新后的状态页文本",
  "summary": "本次分析的一句话总结，30 字以内"
}

## 状态页更新规则

%s`, statePageUpdateRules)

// ThemeAnalyst — 主题分析子 Agent（实现 tool.Tool 接口）
type ThemeAnalyst struct {
	flashClient    llm.LLMClient
	retrievalTools map[string]tool.Tool
	themeStore     *ThemeStore
	traceRepo      *service.ReflectionTraceRepo
	maxIterations  int
	chunkBuilder   *ChunkBuilder
}

func NewThemeAnalyst(flashClient llm.LLMClient, retrievalTools map[string]tool.Tool, themeStore *ThemeStore, traceRepo *service.ReflectionTraceRepo, maxIterations int, chunkBuilder *ChunkBuilder) *ThemeAnalyst {
	return &ThemeAnalyst{
		flashClient:    flashClient,
		retrievalTools: retrievalTools,
		themeStore:     themeStore,
		traceRepo:      traceRepo,
		maxIterations:  maxIterations,
		chunkBuilder:   chunkBuilder,
	}
}

func (ta *ThemeAnalyst) Name() string        { return "theme_analyst" }
func (ta *ThemeAnalyst) Description() string { return "深度分析主题数据，更新状态页" }

func (ta *ThemeAnalyst) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{
		Name:        "analyze",
		Description: "分析指定主题的历史数据，更新状态页",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"theme_name": map[string]interface{}{"type": "string", "description": "要分析的主题名称"},
				"theme_id":   map[string]interface{}{"type": "string", "description": "主题 ID（状态页读写用）"},
			},
			"required": []string{"theme_name", "theme_id"},
		},
	}}
}

type themeAnalystArgs struct {
	ThemeName string `json:"theme_name"`
	ThemeID   string `json:"theme_id"`
}

func (ta *ThemeAnalyst) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	var a themeAnalystArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &tool.Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.ThemeName == "" || a.ThemeID == "" {
		return &tool.Result{Success: false, Error: "theme_name 和 theme_id 不能为空"}
	}

	stateText := ta.themeStore.LoadState(ec.Ctx, a.ThemeID, ec.UserID)
	inputMsg := fmt.Sprintf("## 分析主题\n名称：%s\n\n## 当前状态页\n%s\n\n请检索数据，对比状态页，更新状态页。", a.ThemeName, stateText)

	loop := NewReactLoop(ta.flashClient, ta.retrievalTools, themeAnalystPrompt, ta.maxIterations, ec.UserID, "ThemeAnalyst", ta.traceRepo, CtxTraceID(ec.Ctx))

	result, err := loop.Run(ec.Ctx, inputMsg)
	if err != nil {
		log.Printf("[theme_analyst] ReactLoop error for %s: %v", a.ThemeName, err)
		return &tool.Result{Success: false, Error: fmt.Sprintf("分析失败: %v", err)}
	}

	var output struct {
		UpdatedState string `json:"updated_state"`
		Summary      string `json:"summary"`
	}
	if _, parseErr := ParseFlashJSON(result.FinalResponse, &output); parseErr != nil {
		log.Printf("[theme_analyst] parse output error for %s: %v, raw=%s", a.ThemeName, parseErr, result.FinalResponse[:min(200, len(result.FinalResponse))])
		return &tool.Result{Success: false, Error: fmt.Sprintf("解析分析结果失败: %v", parseErr)}
	}

	stateText = output.UpdatedState
	if stateText == "" {
		stateText = output.Summary
	}
	if stateText == "" {
		log.Printf("[theme_analyst] %s: empty state, skip", a.ThemeName)
		return &tool.Result{Success: true, Data: json.RawMessage(`{"summary":"no update"}`)}
	}

	// 检验 proposed state（Phase 15）
	oldState := ta.themeStore.LoadState(ec.Ctx, a.ThemeID, ec.UserID)
	vr, err := VerifyStateUpdate(ec.Ctx, ta.flashClient, ec.UserID, oldState, result.FinalResponse, stateText)
	if err != nil {
		log.Printf("[theme_analyst] verify error: theme=%s err=%v", a.ThemeName, err)
	}
	if vr != nil && !vr.Complete {
		// 记录检验失败 trace
		recordVerifyDecision(ec.Ctx, ta.traceRepo, a.ThemeName, "rejected", vr)

		log.Printf("[theme_analyst] %s: initial verify failed, running corrective ReAct", a.ThemeName)

		// 矫正 ReAct: 带检验建议 + 检索工具再跑一轮
		correctivePrompt := fmt.Sprintf(`你正在修正「%s」的状态页。原始分析存在以下问题：

%s

请使用检索工具核实并修正状态页。最多 2 轮检索后输出 confirmed_state JSON。`, a.ThemeName, vr.Suggestion)

		correctLoop := NewReactLoop(ta.flashClient, ta.retrievalTools, themeAnalystPrompt, 2, ec.UserID, "ThemeAnalyst:C", ta.traceRepo, CtxTraceID(ec.Ctx))
		correctResult, cErr := correctLoop.Run(ec.Ctx, fmt.Sprintf("## 当前状态页(草案)\n%s\n\n## 旧状态页\n%s\n\n%s", stateText, oldState, correctivePrompt))
		if cErr == nil {
			var corrected struct {
				UpdatedState string `json:"updated_state"`
			}
			if _, parseErr := ParseFlashJSON(correctResult.FinalResponse, &corrected); parseErr == nil && corrected.UpdatedState != "" {
				// 再检验一次
				vr2, err2 := VerifyStateUpdate(ec.Ctx, ta.flashClient, ec.UserID, oldState, correctResult.FinalResponse, corrected.UpdatedState)
				if err2 == nil && vr2 != nil && vr2.Complete {
					recordVerifyDecision(ec.Ctx, ta.traceRepo, a.ThemeName, "corrected", vr2)
					log.Printf("[theme_analyst] %s: corrective ReAct passed", a.ThemeName)
					stateText = corrected.UpdatedState
				} else {
					recordVerifyDecision(ec.Ctx, ta.traceRepo, a.ThemeName, "rejected", vr2)
					log.Printf("[theme_analyst] %s: corrective ReAct still failed", a.ThemeName)
				}
			}
		}
	} else {
		recordVerifyDecision(ec.Ctx, ta.traceRepo, a.ThemeName, "verified", vr)
	}

	if saveErr := ta.themeStore.SaveState(ec.Ctx, a.ThemeID, ec.UserID, stateText); saveErr != nil {
		log.Printf("[theme_analyst] save state error for %s: %v", a.ThemeName, saveErr)
	} else {
		ChunkStateText(ec.Ctx, ta.chunkBuilder, ec.UserID, a.ThemeID, stateText)
	}

	data, _ := json.Marshal(map[string]interface{}{"updated_state": output.UpdatedState, "summary": output.Summary})
	log.Printf("[theme_analyst] %s: state_updated=%v summary=%s", a.ThemeName, output.UpdatedState != "", output.Summary)
	return &tool.Result{Success: true, Data: data}
}

// recordVerifyDecision 记录检验结果到 reflection trace
func recordVerifyDecision(ctx context.Context, traceRepo *service.ReflectionTraceRepo, themeName, verdict string, vr *VerifyResult) {
	traceID := CtxTraceID(ctx)
	if traceID == "" || traceRepo == nil {
		return
	}
	reason := fmt.Sprintf("complete=%v", vr != nil && vr.Complete)
	if vr != nil && !vr.Complete {
		reason = fmt.Sprintf("hallucinations=%d missing=%d", len(vr.Hallucinations), len(vr.Missing))
		if vr.Suggestion != "" {
			reason += " suggestion=" + vr.Suggestion
		}
	}
	detailJSON := ""
	if vr != nil {
		b, _ := json.Marshal(vr)
		detailJSON = string(b)
	}
	createDecisionSafe(traceRepo, ctx, &model.ReflectionDecision{
		ID:         uuid.New().String(),
		TraceID:    traceID,
		Stage:      "theme_verification",
		Subject:    themeName,
		Verdict:    verdict,
		Reason:     reason,
		DetailJSON: &detailJSON,
	})
}
