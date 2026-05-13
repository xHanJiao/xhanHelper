package agent

import "strings"

// extractAllVisibleText 从累积的 LLM 原始响应中提取用户可见文本。
// 纯文本 → 直接返回原文；JSON → 提取 "reply" 字段值并反转义。
func extractAllVisibleText(fullText string) string {
	// 优先从 JSON 的 reply 字段提取
	if replyStart := strings.Index(fullText, `"reply":"`); replyStart >= 0 {
		valueStart := replyStart + len(`"reply":"`)
		reply, _ := unescapeJSONString(fullText[valueStart:])
		return reply
	}

	// 没有 reply 字段，检查是否以 JSON 开头
	trimmed := strings.TrimLeft(fullText, " \t\n\r")
	if strings.HasPrefix(trimmed, "{") {
		return "" // JSON 但 reply 还没出现
	}

	// 有前导文本 + 后续 JSON 块
	if jsonStart := strings.Index(fullText, "{"); jsonStart >= 0 {
		return fullText[:jsonStart]
	}

	return fullText
}

// unescapeJSONString 解析 JSON 字符串转义，返回 (已解析值, 是否闭合)
func unescapeJSONString(s string) (string, bool) {
	var buf strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			return buf.String(), true
		}
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '"', '\\', '/':
				buf.WriteByte(s[i+1])
				i++
			case 'n':
				buf.WriteByte('\n')
				i++
			case 't':
				buf.WriteByte('\t')
				i++
			case 'r':
				buf.WriteByte('\r')
				i++
			default:
				buf.WriteByte(s[i])
			}
		} else {
			buf.WriteByte(s[i])
		}
	}
	return buf.String(), false
}
