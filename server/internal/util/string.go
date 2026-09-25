package util

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"unicode"
)

// MD5Hex 返回文本的 MD5 十六进制摘要（内容判重口径）。
func MD5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

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

// isTopicChar 判定单个 rune 是否为合法 topic 字符：中文、英文（ASCII 字母）、数字。
// 有意收窄到中英文数字——不收其它语种字母（西里尔/假名/谚文）、不收全角/阿拉伯-印度数字。
func isTopicChar(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Latin, r) || ('0' <= r && r <= '9')
}

// IsValidTopicChars 判定 topic 字符是否合规：非空，且只含中文/英文/数字（不许标点、空格、符号）。
// agent（校验/兜底）与 topicclean（存量清洗）共用同一口径，避免两处分叉。
func IsValidTopicChars(s string) bool {
	hasContent := false
	for _, r := range s {
		if !isTopicChar(r) {
			return false
		}
		hasContent = true
	}
	return hasContent
}

// KeepTopicChars 只保留中文/英文/数字（去掉标点、空格、符号）。可能返回空串，调用方需处理。
func KeepTopicChars(s string) string {
	var r []rune
	for _, c := range strings.TrimSpace(s) {
		if isTopicChar(c) {
			r = append(r, c)
		}
	}
	return string(r)
}
