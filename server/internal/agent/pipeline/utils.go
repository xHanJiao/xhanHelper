package pipeline

import "strings"

// extractJSON 从混合文本中提取 JSON 对象
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// truncateSummary 截断文本到指定长度
func truncateSummary(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + "..."
}
