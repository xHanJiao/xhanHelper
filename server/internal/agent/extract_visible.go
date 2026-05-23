package agent

import "strings"

// extractAllVisibleText 从累积的 LLM 原始响应中提取用户可见文本。
// LLM 保证返回 JSON 格式 {"reply":"...","tool_calls":[...]}，提取 reply 字段值并反转义。
func extractAllVisibleText(fullText string) string {
	if replyStart := strings.Index(fullText, `"reply":"`); replyStart >= 0 {
		valueStart := replyStart + len(`"reply":"`)
		reply, _ := unescapeJSONString(fullText[valueStart:])
		return reply
	}
	return ""
}

// unescapeJSONString 解析 JSON 字符串转义，返回 (已解析值, 是否闭合)。
// 容错处理：追踪 [] 和 {} 嵌套深度，仅当深度为 0 且 " 后紧跟 , 或 } 时才认为
// 字符串结束，兼容 LLM 在 reply 内输出未转义 JSON 数组/对象的情况。
func unescapeJSONString(s string) (string, bool) {
	var buf strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
			buf.WriteByte('[')
		case ']':
			if depth > 0 {
				depth--
			}
			buf.WriteByte(']')
		case '{':
			depth++
			buf.WriteByte('{')
		case '}':
			if depth > 0 {
				depth--
			}
			buf.WriteByte('}')
		case '"':
			if depth == 0 {
				rest := strings.TrimLeft(s[i+1:], " \t\n\r")
				if len(rest) == 0 || rest[0] == ',' || rest[0] == '}' {
					return buf.String(), true
				}
			}
			buf.WriteByte('"')
		case '\\':
			if i+1 < len(s) {
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
		default:
			buf.WriteByte(s[i])
		}
	}
	return buf.String(), false
}
