package agent

import (
	"encoding/json"
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

	if err := json.Unmarshal([]byte(cleaned[start:end+1]), target); err != nil {
		return "", err
	}
	return prefix, nil
}
