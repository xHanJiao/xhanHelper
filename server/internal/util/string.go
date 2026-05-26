package util

// TruncateContent 按字符（rune）截断文本，超出最大长度时加 "..."
func TruncateContent(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
