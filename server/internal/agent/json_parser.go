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

	// 用 json.Decoder 解码，它会正确跳过 JSON 字符串内的 {} 字符
	dec := json.NewDecoder(strings.NewReader(cleaned[start:]))
	if err := dec.Decode(target); err == nil {
		return prefix, nil
	}

	// 截断恢复：从末尾逐步截断，用 json.Unmarshal 重试
	for i := len(cleaned) - 1; i > start; i-- {
		if cleaned[i] != '}' && cleaned[i] != '"' && cleaned[i] != ']' {
			continue
		}
		if json.Unmarshal([]byte(cleaned[start:i+1]), target) == nil {
			return prefix, nil
		}
	}
	return "", fmt.Errorf("parse: invalid JSON, excerpt: %s", cleaned[start:min(start+80, len(cleaned))])
}
