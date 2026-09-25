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

// IsAllHan 判定字符串是否「非空且只由汉字组成」（忽略内部空白；空白后无汉字视为空）。
// 用于 topic 的纯中文口径，agent（校验/兜底）与 topicclean（存量清洗）共用，避免两处分叉。
func IsAllHan(s string) bool {
	hasContent := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if !unicode.Is(unicode.Han, r) {
			return false
		}
		hasContent = true
	}
	return hasContent
}

// HanOnly 只保留汉字（去掉英文/数字/标点/空白）。可能返回空串，调用方需处理。
func HanOnly(s string) string {
	var r []rune
	for _, c := range strings.TrimSpace(s) {
		if unicode.Is(unicode.Han, c) {
			r = append(r, c)
		}
	}
	return string(r)
}
