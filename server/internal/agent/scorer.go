package agent

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

const scorerPrompt = `你是 xhanHelper 的「内容编辑」。你的职责是：从多个主题分析员提交的发现中，选出最值得推送给用户的 Top 3。

## 核心原则

1. 用户的时间宝贵。每天最多看 3 条提醒，必须是最好的 3 条。
2. 宁缺毋滥。如果候选发现整体质量不高，可以只选 1-2 条，剩下的不推送。
3. 区分度第一。如果所有发现都打 4-5 分，等于没打分。

## 评分维度

对每条发现，在以下 3 个维度上打 1-5 分：

| 维度 | 1 分 | 5 分 |
|------|------|------|
| importance | 鸡毛蒜皮 | 涉及用户重大利益、长远规划、重要关系 |
| timeliness | 不着急，下个月看也行 | 今天不处理会有实际损失 |
| actionability | 知道了也做不了什么 | 今天就可以采取具体行动 |

总分 = importance + timeliness + actionability（最高 15 分）

## 排序指南

1. 按总分降序排列，相同分数时优先 urgency=high 和 confidence=high 的
2. 强制约束：同一主题最多占 1 席，每条发现必须来自不同主题
3. 如果多条发现内容重叠，只保留最完整的那条，其余的标为 dropped
4. 所有 score ≤ 6 的发现必须 dropped
5. 候选数量不足 3 条时，有几条排几条

## 需要降分的情况

- 发现内容和上一期简报高度重复 → timeliness 降 1-2 分
- 发现仅基于单一数据点（confidence 为 low，source_refs 只有 1 条）→ importance 可降 1 分
- suggested_action 很模糊（「注意一下」「多关注」等）→ actionability 降 1-2 分
- urgency=low 且 actionable=false → 总分通常不应超过 6

## 输出格式

{
  "ranked": [
    {
      "finding_id": "...",
      "rank": 1,
      "score": 13,
      "scores_detail": {"importance": 5, "timeliness": 4, "actionability": 4},
      "editor_note": "为什么这条排第一，一句话"
    }
  ],
  "dropped": [
    {"finding_id": "...", "drop_reason": "重要性不足 / 时效性弱 / 用户目前无法行动 / 与其他发现重复"}
  ],
  "brief_title": "本期简报标题，10 字以内"
}`

// Scorer — 打分排序子 Agent（实现 tool.Tool 接口，不使用 ReAct 循环）
type Scorer struct {
	flashClient llm.LLMClient
}

func NewScorer(flashClient llm.LLMClient) *Scorer {
	return &Scorer{flashClient: flashClient}
}

func (s *Scorer) Name() string { return "scorer" }
func (s *Scorer) Description() string {
	return "对多条发现打分排序，选出最值得推送的 TopN"
}

type scorerArgs struct {
	Findings []scorerInputFinding `json:"findings"`
}

type scorerInputFinding struct {
	FindingID       string   `json:"finding_id"`
	ThemeName       string   `json:"theme_name"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Urgency         string   `json:"urgency"`
	Confidence      string   `json:"confidence"`
	Actionable      bool     `json:"actionable"`
	SuggestedAction string   `json:"suggested_action"`
	SourceRefs      []string `json:"source_refs"`
}

func (s *Scorer) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{
		Name:        "score",
		Description: "对提供的一组发现进行打分排序，返回 TopN 排名结果和落选原因",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"findings": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"finding_id":       map[string]interface{}{"type": "string"},
							"theme_name":       map[string]interface{}{"type": "string"},
							"title":            map[string]interface{}{"type": "string"},
							"content":          map[string]interface{}{"type": "string"},
							"urgency":          map[string]interface{}{"type": "string"},
							"confidence":       map[string]interface{}{"type": "string"},
							"actionable":       map[string]interface{}{"type": "boolean"},
							"suggested_action": map[string]interface{}{"type": "string"},
							"source_refs":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						},
						"required": []string{"finding_id", "theme_name", "title", "content", "urgency", "confidence"},
					},
				},
			},
			"required": []string{"findings"},
		},
	}}
}

func (s *Scorer) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	var a scorerArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &tool.Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if len(a.Findings) == 0 {
		data, _ := json.Marshal(map[string]interface{}{
			"ranked":      []interface{}{},
			"dropped":     []interface{}{},
			"brief_title": "今日无新发现",
		})
		return &tool.Result{Success: true, Data: data}
	}

	// 构建输入：把 findings 序列化发给 LLM
	findingsJSON, _ := json.Marshal(a.Findings)
	userMsg := fmt.Sprintf("请对以下 %d 条发现打分排序：\n\n%s", len(a.Findings), string(findingsJSON))

	resp, err := s.flashClient.ChatSync(ec.Ctx, &llm.ChatRequest{
		SystemPrompt: scorerPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: userMsg},
		},
		UserID: ec.UserID,
	})
	if err != nil {
		log.Printf("[scorer] ChatSync error: %v", err)
		return &tool.Result{Success: false, Error: fmt.Sprintf("打分失败: %v", err)}
	}

	// 解析输出
	var output struct {
		Ranked     []map[string]interface{} `json:"ranked"`
		Dropped    []map[string]interface{} `json:"dropped"`
		BriefTitle string                   `json:"brief_title"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		log.Printf("[scorer] parse error: %v", parseErr)
		return &tool.Result{Success: false, Error: fmt.Sprintf("解析打分结果失败: %v", parseErr)}
	}

	data, _ := json.Marshal(output)

	log.Printf("[scorer] scored %d findings → ranked %d, dropped %d", len(a.Findings), len(output.Ranked), len(output.Dropped))
	return &tool.Result{Success: true, Data: data}
}
