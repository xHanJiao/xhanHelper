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

// TruncateMiddle 从中间截断，保留头部 head 与尾部 tail 字符（rune），中间用 "…" 连接。
// 适合保留关键开头与结尾（如 deadline）的长文本注入。截断提示由调用方决定。
func TruncateMiddle(s string, head, tail int) string {
	runes := []rune(s)
	if len(runes) <= head+tail {
		return s
	}
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}
