package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
)

func stripMarkdownFence(s string) string {
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

const prelabelSystemPrompt = `你是一个检索相关性标注助手。给定一条用户 query 和一组语料候选，对每个候选与 query 的相关性进行 0-4 分评分：

0 (完全无关) — 与 query 毫无关联。
1 (话题相近) — 属于同一领域但无实质帮助。
2 (弱相关) — 有一定线索但不足以回答问题。
3 (相关) — 直接相关，内容可回答该问题。
4 (完美命中) — 最优答案，精准匹配。

只输出一个 JSON 数组，每个元素包含 "corpus_id" 和 "relevance"，不要输出其他内容。
示例：[{"corpus_id":"abc","relevance":3},{"corpus_id":"def","relevance":0}]`

func (s *DefaultLabelStrategy) PreLabel(ctx context.Context, client ChatLLM, query string, candidates []CorpusRecord) ([]LabelResult, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	for _, c := range candidates {
		sb.WriteString(c.ID)
		sb.WriteString(": ")
		sb.WriteString(c.Content)
		sb.WriteString("\n\n")
	}

	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryAdmin,
		SubCategory: llmcost.SubEvalPreLabel,
	})
	req := &llm.ChatRequest{
		SystemPrompt: prelabelSystemPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: fmt.Sprintf(
				"[Query]\n%s\n\n[语料候选]\n%s\n对每个候选评分 0-4。只输出 JSON。",
				query, sb.String(),
			)},
		},
	}

	msg, err := client.ChatSync(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("prelabel: %w", err)
	}

	content := strings.TrimSpace(msg.Content)
	content = stripMarkdownFence(content)

	var results []LabelResult
	if err := json.Unmarshal([]byte(content), &results); err != nil {
		return nil, fmt.Errorf("parse prelabel results: %w\nraw: %s", err, truncate(content, 300))
	}

	return results, nil
}
