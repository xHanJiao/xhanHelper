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

// FormatMessageRound 将一轮对话格式化为文本
func FormatMessageRound(msgs []model.Message, convTitle string) string {
	var sb strings.Builder
	for _, m := range msgs {
		timeStr := m.CreatedAt.In(config.BJ).Format("2006-01-02 15:04")
		switch m.Role {
		case "user":
			content := util.TruncateContent(m.Content, MaxContentLen)
			sb.WriteString(fmt.Sprintf("- [%s|%s] 用户: %s\n", convTitle, timeStr, content))
		case "assistant":
			content := util.TruncateContent(m.Content, MaxContentLen)
			sb.WriteString(fmt.Sprintf("  → 助手: %s\n", content))
		}
	}
	return sb.String()
}
