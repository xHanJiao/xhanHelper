package agent

import (
	"context"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

var bootstrapSummaryPrompt = `你是主题状态摘要员。基于以下主题的状态历史，生成不超过500字的当前状态叙事摘要。

## 规则
1. 提取最近1-2周最关键的进展、决策、发现
2. 过时或已被覆盖的旧信息可压缩或省略，不要逐条复述所有历史记录
3. 保留重要日期和关键细节
4. 不超过500字

## 历史状态
%s

## 输出（纯 JSON，无其他文字）
{"updated_state": "叙事摘要文本"}`

// BootstrapThemeState 用 LLM 修复空 state_text + 初始化 state_text chunk
func BootstrapThemeState(ctx context.Context, themeRepo *service.ThemeStateRepo, chunkBuilder *ChunkBuilder, flashClient llm.LLMClient) {
	themes, err := themeRepo.ListAllActive(ctx)
	if err != nil {
		log.Printf("[theme_bootstrap] list themes error: %v", err)
		return
	}

	for _, t := range themes {
		// 修复空 state_text：用 LLM 从 history 生成摘要
		if t.StateText == "" && t.HistoryStateText != nil && *t.HistoryStateText != "" {
			history := *t.HistoryStateText
			// 取最后 15K 字符，足够覆盖最近 1-2 周的历史
			truncated := history
			if len(history) > 15000 {
				truncated = history[len(history)-15000:]
			}

			prompt := safeSprintf(bootstrapSummaryPrompt, truncated)
			resp, err := flashClient.ChatSync(ctx, &llm.ChatRequest{
				SystemPrompt: prompt,
				Messages:     []llm.Message{{Role: "user", Content: "生成状态摘要"}},
				UserID:       t.UserID,
			})
			if err != nil {
				log.Printf("[theme_bootstrap] LLM error for %s (%s): %v", t.ID[:8], t.Name, err)
				continue
			}

			var output struct {
				UpdatedState string `json:"updated_state"`
			}
			if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil || output.UpdatedState == "" {
				log.Printf("[theme_bootstrap] parse error for %s (%s): %v", t.ID[:8], t.Name, parseErr)
				continue
			}

			if err := themeRepo.UpdateState(ctx, t.ID, t.UserID, output.UpdatedState); err != nil {
				log.Printf("[theme_bootstrap] save state error for %s (%s): %v", t.ID[:8], t.Name, err)
				continue
			}
			log.Printf("[theme_bootstrap] generated state_text for theme %s (%s), %d→%d chars",
				t.ID[:8], t.Name, len(t.StateText), len(output.UpdatedState))
			t.StateText = output.UpdatedState
		}

		// 初始化 state_text chunk 嵌入
		if t.StateText != "" && chunkBuilder != nil {
			ChunkStateText(ctx, chunkBuilder, t.UserID, t.ID, t.StateText)
		}
	}

	log.Printf("[theme_bootstrap] done: %d themes processed", len(themes))
}

func safeSprintf(format, arg string) string {
	result := format
	for i := 0; i <= len(result)-2; i++ {
		if result[i:i+2] == "%s" {
			result = result[:i] + arg + result[i+2:]
			break
		}
	}
	return result
}
