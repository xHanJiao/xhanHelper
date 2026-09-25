package topicclean

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"xhan-helper/internal/llm"
)

// Violation — 一条违规 topic 及其处置。
type Violation struct {
	OldTopic string
	Runes    int
	NewTopic string // LLM 候选；空表示需人工填
	Action   string // rename | skip
}

// PlanRow — 映射文件里的一行（供人工审阅/编辑）。
type PlanRow struct {
	OldTopic string
	NewTopic string
	Action   string
}

// proposeNames 调 LLM 为一批违规 topic 起 ≤5 字短名（同用户一次调用）。
// 失败的条目 NewTopic 留空，交人工；本函数不阻断生成流程，由调用方决定降级。
func proposeNames(ctx context.Context, client llm.LLMClient, userID string, violations []Violation, all []topicRef) error {
	var b strings.Builder
	b.WriteString("下面是一个用户的记忆主题词列表，其中有几个违规（空或超过 5 字）。\n")
	b.WriteString("请为每个违规主题词给一个合规的替代名：非空、5 个字以内、一个词，禁止斜杠/逗号/空格。\n")
	b.WriteString("优先复用列表里已有的合规主题词；确实没有合适的才新建。\n\n")
	b.WriteString("已有（合规）主题词：")
	existing := make([]string, 0, len(all))
	for _, t := range all {
		if strings.TrimSpace(t.Topic) != "" && len([]rune(t.Topic)) <= MaxTopicRunes {
			existing = append(existing, t.Topic)
		}
	}
	if len(existing) == 0 {
		b.WriteString("（无）")
	} else {
		b.WriteString(strings.Join(existing, "、"))
	}
	b.WriteString("\n\n需要改的主题词：\n")
	for _, v := range violations {
		fmt.Fprintf(&b, "- %q（%d 字）\n", v.OldTopic, v.Runes)
	}
	b.WriteString("\n严格按 JSON 数组输出，每项 {\"old\":\"原词\",\"new\":\"新词\"}，不要包含其他内容。")

	msg, err := client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是主题词规范化助手。",
		Messages:     []llm.Message{{Role: "user", Content: b.String()}},
		UserID:       userID,
	})
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}

	var pairs []struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if _, err := parseJSONArray(msg.Content, &pairs); err != nil {
		return fmt.Errorf("parse llm output: %w", err)
	}
	byOld := make(map[string]string, len(pairs))
	for _, p := range pairs {
		byOld[strings.TrimSpace(p.Old)] = strings.TrimSpace(p.New)
	}
	for i := range violations {
		named := byOld[violations[i].OldTopic]
		if err := validateNewTopic(named); err != nil {
			continue // 不合规 → 留空交人工
		}
		violations[i].NewTopic = named
	}
	return nil
}

// parseJSONArray 从可能带包裹文本/代码块的输出里抠出 JSON 数组。
func parseJSONArray(raw string, target any) (string, error) {
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end < start {
		return "", fmt.Errorf("no JSON array in output")
	}
	seg := raw[start : end+1]
	if err := json.Unmarshal([]byte(seg), target); err != nil {
		return "", err
	}
	return raw[:start], nil
}

// renderPlan 生成映射文件（TSV）。人工可改 new_topic / action，或删行。
func renderPlan(userID string, violations []Violation) string {
	var b strings.Builder
	b.WriteString("# topic-clean plan —— 人工审阅后可编辑，勿改列顺序/制表符\n")
	b.WriteString("# user_id: " + userID + "\n")
	b.WriteString("# action=rename：把 old_topic 改为 new_topic；action=skip：跳过；删除整行=跳过\n")
	b.WriteString("# new_topic 必须非空、≤5 字、不含斜杠/逗号/空格\n")
	b.WriteString("# old_topic\tnew_topic\taction\n")
	for _, v := range violations {
		action := v.Action
		if v.NewTopic == "" {
			action = "skip" // 无候选 → 默认跳过，人工填了 new_topic 再改成 rename
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", v.OldTopic, v.NewTopic, action)
	}
	return b.String()
}

// parsePlan 解析映射文件，返回 user_id 与改名列。
func parsePlan(text string) (userID string, rows []PlanRow, err error) {
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if v, ok := strings.CutPrefix(line, "# user_id:"); ok {
				userID = strings.TrimSpace(v)
			}
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return "", nil, fmt.Errorf("第 %d 行字段数不足（需 3 列，用制表符分隔）: %q", i+1, line)
		}
		rows = append(rows, PlanRow{
			OldTopic: strings.TrimSpace(fields[0]),
			NewTopic: strings.TrimSpace(fields[1]),
			Action:   strings.TrimSpace(fields[2]),
		})
	}
	if userID == "" {
		return "", nil, fmt.Errorf("映射文件缺少 '# user_id:' 行")
	}
	return userID, rows, nil
}

// renderReport 生成 Markdown 报告。
func renderReport(userID string, all []topicRef, violations []Violation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# topic 清洗报告\n\n")
	fmt.Fprintf(&b, "- user: `%s`\n", userID)
	fmt.Fprintf(&b, "- 全部 topic: %d 个｜违规（空/超 %d 字）: %d 个\n\n", len(all), MaxTopicRunes, len(violations))
	b.WriteString("## 违规 topic 及候选短名\n\n")
	b.WriteString("| 原 topic | 字数 | LLM 候选 | 引用数 |\n|---|---|---|---|\n")
	count := map[string]int64{}
	for _, t := range all {
		count[t.Topic] = t.Count
	}
	for _, v := range violations {
		cand := v.NewTopic
		if cand == "" {
			cand = "（需人工填）"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %d |\n", v.OldTopic, v.Runes, cand, count[v.OldTopic])
	}
	return b.String()
}
