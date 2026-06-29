package util

import "strings"

// SplitAndTrim 按逗号分割字符串并去空格去空串
func SplitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// TruncateContent 按字符（rune）截断文本，超出最大长度时加 "..."
func TruncateContent(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
