package agent

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

const themeAnalystPrompt = `你是 xhanHelper 的「主题分析员」。你的职责是：针对一个用户关注的主题，阅读提供给你的数据，从中发现值得提醒用户的规律、变化或信号。

## 核心原则

1. 诚实：数据里确实没有值得说的话，就说没有。宁可返回空结果，不要编造发现。
2. 有据：source_refs 的每一条都必须是你刚才调用 search_* 工具返回结果里的**原句**，不许改写、不许概括、不许用你自己的知识补全。如果检索结果里没有「褪黑素」这个词，你就不能写「褪黑素」。
3. 禁止使用你的训练数据中的常识来填补——你只能使用检索工具返回的具体信息。睡眠健康常识（如褪黑素、深度睡眠标准）、医疗建议、通用方法论等，除非检索结果明确提到，否则一律不能出现在发现中。
4. 简洁：3 条高质量发现远好于 10 条凑数的。只报告真正有意义的事。

## 你能使用的检索工具

1. search_notes(query, top_k) — 向量检索笔记。每条带引用 ID（格式 note:xxx）。
2. search_memories(query, top_k) — 向量检索长期记忆。每条带引用 ID（格式 mem:xxx）。
3. get_bill_stats() — 获取账单统计摘要。返回结构化数据。

## 检索策略建议

- **query 必须是自然语言完整句子，捕捉语义意图。禁止使用空格分隔的关键词列表。** 例如「用户最近一个月关于睡眠质量的具体记录、困扰和改善尝试」而非「睡眠 质量 早醒 褪黑素」
- 第 1 轮：用主题名称 + 描述做首次检索，各源取 top 5-8 条
- 评估：查看结果，判断信息是否足够形成发现。如果有意外线索，考虑第 2 轮
- 第 2 轮（可选）：针对意外线索用新关键词检索。最多 2 轮检索，然后必须产出分析结果
- 不必检索所有源：如果主题跟账单无关，不要调 get_bill_stats。如果记忆库里显然没有相关内容，跳过 search_memories

## 你需要产出什么

分析完成后，输出一个 JSON 对象：

{
  "findings": [
    {
      "title": "简明标题，15 字以内",
      "content": "详细说明，100 字以内。把发现讲清楚——是什么规律/变化/信号，为什么值得关注",
      "urgency": "high | medium | low",
      "confidence": "high | medium | low",
      "actionable": true,
      "suggested_action": "如果 actionable=true，给出用户可采取的具体行动；否则留空",
      "source_refs": ["检索数据中实际出现的原文片段1", "原文片段2"]
    }
  ],
  "updated_state": "更新后的状态页文本。hot_run 模式下必须输出，cold_start 模式下可选输出。自由文本格式",
  "summary": "本次分析的一句话总结，30 字以内。没有发现时写'未发现需要提醒的内容'"
}

如果确实没有值得报告的内容，返回：
{
  "findings": [],
  "skip_reason": "insufficient_data | no_pattern | no_change | already_known | noise_only",
  "skip_detail": "简要说明为什么没有发现（1-2句话，方便调试和调优）",
  "summary": "未发现需要提醒的内容"
}

## 什么是「值得提醒」的发现

以下类型的发现是有价值的，优先关注：

1. 规律：从重复出现的数据中识别出的模式。
2. 变化：与历史状态相比的偏离。
3. 信号：尚未形成规律但值得关注的早期迹象。
4. 进展：某个持续追踪的事项的状态更新。
5. 提醒：基于数据推断出的即将需要关注的事。

## 什么「不是」值得提醒的

以下情况不要产出发现，并对应到 skip_reason：
- 数据量太少 → insufficient_data
- 数据中只有一次性闲聊或日常流水 → no_pattern
- 与状态页相比没有显著变化 → no_change
- 可发现的内容是常识或用户肯定已经知道的事 → already_known
- 检索到的数据与主题关联度低 → noise_only
- 纯属推测，没有数据支撑 → insufficient_data

## 状态页更新指南（hot_run 模式）

状态页是跨轮次传递的「记忆」。更新时遵循：
1. 保留上一轮状态页中仍然有效的关键信息
2. 过期或已被否定的事实用新发现覆盖
3. 每条关键信息标注大致时间
4. 控制篇幅：状态页文字不超过 500 字
5. 超过 3 个月且本次未再出现的信息，可移除或标记为「可能已过期」

## cold_start 特别说明

只从能确认的内容中产出发现。如果碎片不足以形成任何判断，坦率返回空结果。
**必须输出 updated_state**：即使没有发现，也要基于检索到的数据写一份初始状态总结，供下一轮 hot_run 使用。这是硬性要求，不可省略。`

// ThemeAnalyst — 主题分析子 Agent（实现 tool.Tool 接口）
type ThemeAnalyst struct {
	flashClient    llm.LLMClient
	retrievalTools map[string]tool.Tool
	themeStore     *ThemeStore
	maxIterations  int
}

func NewThemeAnalyst(flashClient llm.LLMClient, retrievalTools map[string]tool.Tool, themeStore *ThemeStore, maxIterations int) *ThemeAnalyst {
	return &ThemeAnalyst{
		flashClient:    flashClient,
		retrievalTools: retrievalTools,
		themeStore:     themeStore,
		maxIterations:  maxIterations,
	}
}

func (ta *ThemeAnalyst) Name() string { return "theme_analyst" }

func (ta *ThemeAnalyst) Description() string {
	return "分析用户关注的一个追踪主题，从历史数据中发现规律、变化或信号，产出值得提醒的发现"
}

func (ta *ThemeAnalyst) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{
		Name: "analyze",
		Description: `分析指定主题。从检索到的历史数据（消息/笔记/记忆/账单）中发现值得提醒用户的内容。
mode 含义：
- cold_start: 首次分析该主题，无历史状态页，纯靠向量检索
- hot_run: 续分析，已有状态页，做增量分析`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"theme_name":        map[string]interface{}{"type": "string", "description": "要分析的主题名称"},
				"theme_description": map[string]interface{}{"type": "string", "description": "主题描述"},
				"theme_id":          map[string]interface{}{"type": "string", "description": "主题 ID（状态页读写用）"},
				"mode":              map[string]interface{}{"type": "string", "enum": []string{"cold_start", "hot_run"}, "description": "分析模式"},
			},
			"required": []string{"theme_name", "theme_id", "mode"},
		},
	}}
}

type themeAnalystArgs struct {
	ThemeName        string `json:"theme_name"`
	ThemeDescription string `json:"theme_description"`
	ThemeID          string `json:"theme_id"`
	Mode             string `json:"mode"`
}

type themeAnalystOutput struct {
	Findings     []themeAnalystFinding `json:"findings"`
	UpdatedState string                `json:"updated_state"`
	Summary      string                `json:"summary"`
	SkipReason   string                `json:"skip_reason"`
	SkipDetail   string                `json:"skip_detail"`
}

type themeAnalystFinding struct {
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Urgency         string   `json:"urgency"`
	Confidence      string   `json:"confidence"`
	Actionable      bool     `json:"actionable"`
	SuggestedAction string   `json:"suggested_action"`
	SourceRefs      []string `json:"source_refs"`
}

func (ta *ThemeAnalyst) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	var a themeAnalystArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &tool.Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.ThemeName == "" || a.ThemeID == "" {
		return &tool.Result{Success: false, Error: "theme_name 和 theme_id 不能为空"}
	}
	if a.Mode == "" {
		a.Mode = "cold_start"
	}

	// 构建输入消息
	inputMsg := fmt.Sprintf("## 分析主题\n名称：%s\n描述：%s\n模式：%s\n", a.ThemeName, a.ThemeDescription, a.Mode)

	if a.Mode == "hot_run" {
		stateText := ta.themeStore.LoadState(ec.Ctx, a.ThemeID, ec.UserID)
		if stateText != "" {
			inputMsg += fmt.Sprintf("\n## 上一轮状态页\n%s\n", stateText)
		} else {
			inputMsg += "\n（无历史状态页，但模式为 hot_run，请按 cold_start 模式处理）\n"
		}
	} else {
		inputMsg += "\n（cold_start：这是首次分析，请先做第1轮检索了解数据情况）\n"
	}

	// 创建轻量 ReAct 循环（仅检索工具，限 2 轮）
	loop := NewReactLoop(ta.flashClient, ta.retrievalTools, themeAnalystPrompt, ta.maxIterations, ec.UserID, "ThemeAnalyst", CtxTraceRepo(ec.Ctx), CtxTraceID(ec.Ctx))

	result, err := loop.Run(ec.Ctx, inputMsg)
	if err != nil {
		log.Printf("[theme_analyst] ReactLoop error for %s: %v", a.ThemeName, err)
		return &tool.Result{Success: false, Error: fmt.Sprintf("分析失败: %v", err)}
	}

	// 解析 LLM 输出
	var output themeAnalystOutput
	if _, parseErr := ParseFlashJSON(result.FinalResponse, &output); parseErr != nil {
		log.Printf("[theme_analyst] parse output error for %s: %v, raw=%s", a.ThemeName, parseErr, result.FinalResponse[:min(200, len(result.FinalResponse))])
		return &tool.Result{Success: false, Error: fmt.Sprintf("解析分析结果失败: %v", parseErr)}
	}

	// 更新状态页（总是执行，确保 last_run_at 更新）
	// LLM 可能忽略 prompt 要求不输出 updated_state，代码兜底
	stateText := output.UpdatedState
	if stateText == "" {
		stateText = fmt.Sprintf("首次分析完成（%s），发现 %d 条内容。%s", a.Mode, len(output.Findings), output.Summary)
	}
	if saveErr := ta.themeStore.SaveState(ec.Ctx, a.ThemeID, ec.UserID, stateText); saveErr != nil {
		log.Printf("[theme_analyst] save state error for %s: %v", a.ThemeName, saveErr)
	}

	// 构造返回
	respData := map[string]interface{}{
		"findings":      output.Findings,
		"summary":       output.Summary,
		"updated_state": output.UpdatedState,
	}
	if len(output.Findings) == 0 {
		respData["skip_reason"] = output.SkipReason
		respData["skip_detail"] = output.SkipDetail
	}

	data, _ := json.Marshal(respData)

	if len(output.Findings) == 0 {
		log.Printf("[theme_analyst] %s: no findings, reason=%s detail=%s", a.ThemeName, output.SkipReason, output.SkipDetail)
	} else {
		log.Printf("[theme_analyst] %s: %d findings, state_updated=%v", a.ThemeName, len(output.Findings), output.UpdatedState != "")
	}

	return &tool.Result{Success: true, Data: data}
}
