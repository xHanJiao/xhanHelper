package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
)

// ==================== 参数解析 / 字段校验 ====================

// 记忆字段硬限制（不变量，不进配置，见 design.md §6）。
const (
	memoryTopicMaxLen   = 20
	memorySummaryMaxLen = 150
	memoryFactsMaxCount = 8
	memoryFactMaxLen    = 100
)

// decodeRecordArgs 严格解析入参：拒绝未知字段；解析失败一律返回错误，禁止吞掉后继续用零值。
func decodeRecordArgs(args json.RawMessage, dst any) error {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("参数解析失败: %w", err)
	}
	return nil
}

// splitKeywords 按空格/逗号/顿号拆关键词（OR 语义），去重。
func splitKeywords(query string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(query, func(r rune) bool {
		switch r {
		case ' ', ',', '，', '、', '"', '\'', '“', '”', '‘', '’':
			return true
		}
		return false
	}) {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

func clampTopK(k int) int {
	if k <= 0 {
		return 5
	}
	if k > 10 {
		return 10
	}
	return k
}

func roundScore(s float64) float64 {
	return math.Round(s*1000) / 1000
}

func timeNow() time.Time { return time.Now() }

// validateMemoryFields 工具侧字段校验，超限返回错误让模型改写。
func validateMemoryFields(topic, summary string, facts []string) error {
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("summary 不能为空")
	}
	if len([]rune(topic)) > memoryTopicMaxLen {
		return fmt.Errorf("topic 超长（≤%d 字）", memoryTopicMaxLen)
	}
	if len([]rune(summary)) > memorySummaryMaxLen {
		return fmt.Errorf("summary 超长（≤%d 字）", memorySummaryMaxLen)
	}
	if len(facts) > memoryFactsMaxCount {
		return fmt.Errorf("facts 最多 %d 条", memoryFactsMaxCount)
	}
	for _, f := range facts {
		if len([]rune(f)) > memoryFactMaxLen {
			return fmt.Errorf("单条 fact 超长（≤%d 字）", memoryFactMaxLen)
		}
	}
	return nil
}

// factsPtr 把 facts 序列化为 JSON 字符串指针（空则 nil）。
func factsPtr(facts []string) *string {
	if len(facts) == 0 {
		return nil
	}
	b, err := json.Marshal(facts)
	if err != nil {
		log.Printf("[record_loop] marshal facts error: %v", err)
		return nil
	}
	s := string(b)
	return &s
}
