package retrieval

import (
	"fmt"
	"strings"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/util"
)

const MaxContentLen = 500

// FilterMessageRound 从消息列表中截取以 matchedID 开头的一轮 Q&A
// 规则：保留 user + assistant 消息，跳过 tool/internal，遇到下一个 user 消息时截断
func FilterMessageRound(msgs []model.Message, matchedID string) []model.Message {
	var result []model.Message
	for _, m := range msgs {
		if m.Role == "tool" || m.IsInternal {
			continue
		}
		if m.Role == "user" && m.ID != matchedID {
			break
		}
		if (m.Role == "user" || m.Role == "assistant") && m.Content != "" {
			result = append(result, m)
		}
	}
	return result
}

// FormatMessageRound 将一轮对话格式化为系统提示词文本，包含相关度分数和日期。
// 用于 SystemPromptBuilder 的 history provider 注入。
//
//	### [相关度 0.87] 05月28日 15:32
//	**用户**：帮我记账...
//	**助手**：已记录...
func FormatMessageRound(msgs []model.Message, score float64) string {
	var sb strings.Builder
	if len(msgs) > 0 {
		timeStr := msgs[0].CreatedAt.In(config.BJ).Format("01月02日 15:04")
		sb.WriteString(fmt.Sprintf("### [相关度 %.2f] %s\n", score, timeStr))
	}
	for _, m := range msgs {
		content := util.TruncateContent(m.Content, MaxContentLen)
		switch m.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("**用户**：%s\n", content))
		case "assistant":
			sb.WriteString(fmt.Sprintf("**助手**：%s\n", content))
		}
	}
	return sb.String()
}

// FormatMessageRoundWithTitle 将一轮对话格式化为带标题的文本，用于消息搜索工具。
//
//   - [标题|2026-05-28 15:32] 用户: content
//     → 助手: content
func FormatMessageRoundWithTitle(msgs []model.Message, title string) string {
	var sb strings.Builder
	for _, m := range msgs {
		timeStr := m.CreatedAt.In(config.BJ).Format("2006-01-02 15:04")
		switch m.Role {
		case "user":
			content := util.TruncateContent(m.Content, MaxContentLen)
			sb.WriteString(fmt.Sprintf("- [%s|%s] 用户: %s\n", title, timeStr, content))
		case "assistant":
			content := util.TruncateContent(m.Content, MaxContentLen)
			sb.WriteString(fmt.Sprintf("  → 助手: %s\n", content))
		}
	}
	return sb.String()
}
