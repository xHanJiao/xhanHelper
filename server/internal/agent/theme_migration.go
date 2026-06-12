package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// FactEntry — 一条 JSONL 事实行
type FactEntry struct {
	Ts   string `json:"ts"`
	Fact string `json:"fact"`
}

// blockSep 匹配 history_state_text 中的时间戳分隔符: --- 2026-05-19 20:53 ---
var blockSep = regexp.MustCompile(`\n?--- \d{4}-\d{2}-\d{2} \d{2}:\d{2} ---\n?`)

// factLine 匹配编号事实行: N. [2026-05-19] 事实内容
var factLine = regexp.MustCompile(`^\d+\.\s*\[(\d{4}-\d{2}-\d{2})\]\s*(.+)`)

// MigrateThemeFacts 迁移 history_state_text → state_facts JSONL，启动时调用一次
func MigrateThemeFacts(ctx context.Context, themeRepo *service.ThemeStateRepo, chunkBuilder *ChunkBuilder, flashClient llm.LLMClient) {
	themes, err := themeRepo.ListAllActive(ctx)
	if err != nil {
		log.Printf("[theme_migration] ListAllActive error: %v", err)
		return
	}
	if len(themes) == 0 {
		return
	}

	for _, t := range themes {
		if t.StateFacts != "" {
			continue // 已迁移过
		}
		if t.HistoryStateText == nil || *t.HistoryStateText == "" {
			if t.StateText == "" {
				// 空主题：生成初始摘要
				initialSummary := generateInitialSummary(ctx, flashClient, t.UserID, t.Name, t.Description)
				if initialSummary != "" {
					if err := themeRepo.UpdateSummary(ctx, t.ID, t.UserID, initialSummary); err != nil {
						log.Printf("[theme_migration] update initial summary error: theme=%s err=%v", t.ID[:8], err)
					} else {
						log.Printf("[theme_migration] generated initial summary for theme %s (%s)", t.ID[:8], t.Name)
					}
				}
			}
			continue
		}

		facts := parseHistoryToFacts(*t.HistoryStateText)
		if len(facts) < 3 {
			facts = llmExtractFacts(ctx, flashClient, *t.HistoryStateText)
		}
		facts = deduplicateFacts(facts)

		// 写入 state_facts
		var sb strings.Builder
		for _, f := range facts {
			b, _ := json.Marshal(f)
			sb.Write(b)
			sb.WriteByte('\n')
		}
		factsText := sb.String()
		if err := themeRepo.AppendFact(ctx, t.ID, t.UserID, strings.TrimRight(factsText, "\n")); err != nil {
			log.Printf("[theme_migration] AppendFact error: theme=%s err=%v", t.ID[:8], err)
			continue
		}

		// 全部 facts 拼接后建 chunk
		var factLines []string
		for _, f := range facts {
			factLines = append(factLines, f.Fact)
		}
		allFactsText := strings.Join(factLines, "\n")
		if chunkBuilder != nil {
			n, err := chunkBuilder.BuildChunks(ctx, t.UserID, "theme", t.ID, allFactsText)
			if err != nil {
				log.Printf("[theme_migration] BuildChunks error: theme=%s err=%v", t.ID[:8], err)
			} else {
				log.Printf("[theme_migration] built %d chunks for theme %s (%s)", n, t.ID[:8], t.Name)
			}
		}

		// 重生摘要
		newSummary := regenerateSummary(ctx, flashClient, t.StateText, facts, t.UserID)
		if newSummary != "" {
			if err := themeRepo.UpdateSummary(ctx, t.ID, t.UserID, newSummary); err != nil {
				log.Printf("[theme_migration] UpdateSummary error: theme=%s err=%v", t.ID[:8], err)
			} else {
				log.Printf("[theme_migration] regenerated summary for theme %s (%s)", t.ID[:8], t.Name)
			}
		}

		log.Printf("[theme_migration] migrated theme %s (%s): %d facts", t.ID[:8], t.Name, len(facts))
	}
}

// parseHistoryToFacts 规则解析 history_state_text → []FactEntry
func parseHistoryToFacts(historyText string) []FactEntry {
	blocks := blockSep.Split(historyText, -1)
	seen := make(map[string]bool)
	var facts []FactEntry

	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		for _, line := range lines {
			matches := factLine.FindStringSubmatch(strings.TrimSpace(line))
			if len(matches) != 3 {
				continue
			}
			fact := strings.TrimSpace(matches[2])
			if fact == "" || len(fact) < 4 {
				continue
			}
			key := strings.ToLower(fact)
			if seen[key] {
				continue
			}
			seen[key] = true
			facts = append(facts, FactEntry{
				Ts:   matches[1],
				Fact: fact,
			})
		}
	}
	return facts
}

// deduplicateFacts 按 fact 文本去重
func deduplicateFacts(facts []FactEntry) []FactEntry {
	seen := make(map[string]bool)
	var result []FactEntry
	for _, f := range facts {
		key := strings.ToLower(strings.TrimSpace(f.Fact))
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, f)
	}
	return result
}

// llmExtractFacts LLM 兜底解析 history_state_text → []FactEntry
func llmExtractFacts(ctx context.Context, flashClient llm.LLMClient, historyText string) []FactEntry {
	prompt := `从以下主题状态历史中提取所有原子事实，每条事实一行 JSONL。

## 规则
1. 每条事实自包含（1-2句话）
2. 包含日期、关键细节
3. 输出纯 JSONL，每行 {"ts":"YYYY-MM-DD","fact":"..."}
4. 不要输出其他文字

## 历史状态
` + safeTruncate(historyText, 15000)

	resp, err := flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: prompt,
		Messages:     []llm.Message{{Role: "user", Content: "提取事实"}},
	})
	if err != nil {
		log.Printf("[theme_migration] llmExtractFacts ChatSync error: %v", err)
		return nil
	}

	var facts []FactEntry
	for _, line := range strings.Split(resp.Content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var f FactEntry
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			continue
		}
		if f.Fact != "" {
			facts = append(facts, f)
		}
	}
	return facts
}

// regenerateSummary 基于旧摘要 + facts 生成新摘要
func regenerateSummary(ctx context.Context, flashClient llm.LLMClient, oldSummary string, facts []FactEntry, userID string) string {
	var factList strings.Builder
	for _, f := range facts {
		fmt.Fprintf(&factList, "- [%s] %s\n", f.Ts, f.Fact)
	}

	prompt := fmt.Sprintf(`你是主题状态摘要员。基于旧摘要和最新事实时间线，生成不超过500字的叙事摘要。

## 旧摘要
%s

## 事实时间线
%s

## 规则
1. 保留旧摘要中与新事实不冲突的所有有效信息
2. 过期或被新事实覆盖的信息用新内容替换
3. 每条关键信息标注大致时间
4. 超过3个月且未再出现的旧信息可移除
5. 不超过500字

## 输出
{"summary": "..."}`, safeTruncate(oldSummary, 1000), safeTruncate(factList.String(), 10000))

	resp, err := flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: prompt,
		Messages:     []llm.Message{{Role: "user", Content: "生成摘要"}},
		UserID:       userID,
	})
	if err != nil {
		log.Printf("[theme_migration] regenerateSummary ChatSync error: %v", err)
		return ""
	}

	var output struct {
		Summary string `json:"summary"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		log.Printf("[theme_migration] regenerateSummary parse error: %v, raw=%s", parseErr, safeTruncate(resp.Content, 200))
		return ""
	}
	return output.Summary
}

// generateInitialSummary 为无 history、无 state_text 的新主题生成初始摘要
func generateInitialSummary(ctx context.Context, flashClient llm.LLMClient, userID, name, description string) string {
	prompt := fmt.Sprintf(`你是一个主题状态摘要员。请为主题生成一段简短的初始摘要。

## 主题信息
名称：%s
描述：%s

## 规则
1. 基于名称和描述生成一个自然的开篇状态
2. 不超过200字
3. 标注当前日期

## 输出
{"summary": "..."}`, name, description)

	resp, err := flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: prompt,
		Messages:     []llm.Message{{Role: "user", Content: "生成初始摘要"}},
		UserID:       userID,
	})
	if err != nil {
		log.Printf("[theme_migration] generateInitialSummary ChatSync error: %v", err)
		return ""
	}

	var output struct {
		Summary string `json:"summary"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		return ""
	}
	return output.Summary
}

func safeTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
