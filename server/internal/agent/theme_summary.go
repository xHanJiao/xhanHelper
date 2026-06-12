package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// RegenerateThemeSummary 基于旧摘要 + 全部 facts 异步重生主题摘要
func RegenerateThemeSummary(ctx context.Context, flashClient llm.LLMClient, themeRepo *service.ThemeStateRepo, userID, themeID string) {
	theme, err := themeRepo.GetByID(ctx, themeID, userID)
	if err != nil || theme == nil {
		log.Printf("[theme_summary] GetByID error: theme=%s err=%v", themeID[:8], err)
		return
	}

	oldSummary := theme.StateText
	facts := parseThemeFacts(theme.StateFacts)
	if len(facts) == 0 {
		return
	}

	var factList strings.Builder
	for _, f := range facts {
		fmt.Fprintf(&factList, "- %s\n", f.Fact)
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
{"summary": "..."}`,
		safeTruncate(oldSummary, 1000),
		safeTruncate(factList.String(), 10000))

	resp, err := flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: prompt,
		Messages:     []llm.Message{{Role: "user", Content: "生成摘要"}},
		UserID:       userID,
	})
	if err != nil {
		log.Printf("[theme_summary] ChatSync error: theme=%s err=%v", themeID[:8], err)
		return
	}

	var output struct {
		Summary string `json:"summary"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		log.Printf("[theme_summary] parse error: theme=%s err=%v", themeID[:8], parseErr)
		return
	}
	if output.Summary == "" {
		return
	}

	if err := themeRepo.UpdateSummary(ctx, themeID, userID, output.Summary); err != nil {
		log.Printf("[theme_summary] UpdateSummary error: theme=%s err=%v", themeID[:8], err)
		return
	}

	log.Printf("[theme_summary] regenerated summary for theme %s", themeID[:8])
}

// parseThemeFacts 解析 state_facts JSONL
func parseThemeFacts(factsJSONL string) []FactEntry {
	var facts []FactEntry
	lines := strings.Split(strings.TrimSpace(factsJSONL), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
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
