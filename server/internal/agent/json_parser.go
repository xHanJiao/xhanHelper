package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseFlashJSON 从 Flash LLM 的输出中提取 JSON 并反序列化到 target。
// 返回 JSON 前的纯文本前缀。JSON 之后的任何文字会被忽略。
func ParseFlashJSON(raw string, target interface{}) (prefix string, err error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	start := strings.Index(cleaned, "{")
	if start < 0 {
		return "", nil
	}

	prefix = strings.TrimSpace(cleaned[:start])

	end := start
	depth := 0
loop:
	for i := start; i < len(cleaned); i++ {
		switch cleaned[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				break loop
			}
		}
	}

	if end == start {
		end = len(cleaned) - 1 // 未找到匹配闭括号，从末尾恢复
	}
	jsonStr := cleaned[start : end+1]
	if err := json.Unmarshal([]byte(jsonStr), target); err == nil {
		return prefix, nil
	}

	// 截断恢复：从末尾逐步截断无效 UTF-8 或不完整 JSON，重试
	for i := len(jsonStr) - 1; i > start; i-- {
		if jsonStr[i] != '}' && jsonStr[i] != '"' && jsonStr[i] != ']' {
			continue
		}
		if err := json.Unmarshal([]byte(jsonStr[:i+1]), target); err == nil {
			return prefix, nil
		}
	}
	return "", fmt.Errorf("parse: json truncated, raw excerpt: %s", cleaned[start:min(start+80, len(cleaned))])
}
