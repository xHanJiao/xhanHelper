package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"

	"xhan-helper/internal/util"
)

// ==================== 参数解析 / 字段校验 ====================

// 记忆字段硬限制（不变量，不进配置，见 design.md §6）。
const (
	memoryTopicMaxLen   = 5
	memorySummaryMaxLen = 150
	memoryFactsMaxCount = 10
	// memoryFactMaxLen 200：facts 是用户原话片段，用户一句话经常超过 100 字；
	// 卡太紧会让模型反复「超长被拒」白烧决策循环的轮数预算。
	memoryFactMaxLen = 200
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

// keepOrReplaceTopic 决定记忆更新时的 topic：入参非空（trim 后）就用入参，否则保留旧值。
// 见「update 把 topic 抹空」的 bug：模型常只传 summary、省略 topic。
func keepOrReplaceTopic(incoming, old string) string {
	if t := strings.TrimSpace(incoming); t != "" {
		return t
	}
	return old
}

// validateMemoryFields 工具侧字段校验，超限返回错误让模型改写。
// 只校验本次入参：update 的 facts 是**整体覆盖**（不是与旧值合并），所以没有「合并结果」要额外校验。
func validateMemoryFields(topic, summary string, facts []string) error {
	topic = strings.TrimSpace(topic) // 与 validTopic 对齐：先 trim 再校验长度/字符
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("summary 不能为空")
	}
	if topic == "" {
		return fmt.Errorf("topic 不能为空")
	}
	if len([]rune(topic)) > memoryTopicMaxLen {
		return fmt.Errorf("topic 超长（≤%d 字）", memoryTopicMaxLen)
	}
	if !util.IsValidTopicChars(topic) {
		return fmt.Errorf("topic 只能含中文/英文/数字（不含标点/空格/符号）")
	}
	if len([]rune(summary)) > memorySummaryMaxLen {
		return fmt.Errorf("summary 超长（≤%d 字）", memorySummaryMaxLen)
	}
	for _, f := range facts {
		if len([]rune(f)) > memoryFactMaxLen {
			return fmt.Errorf("单条 fact 超长（≤%d 字）", memoryFactMaxLen)
		}
	}
	return nil
}

// validateFactCount 约束模型单次传入的 facts 条数（防止一次灌太多）。
// update 是整体覆盖，所以这个上限同时也是库里单条记忆 facts 的上限。
func validateFactCount(facts []string) error {
	if len(facts) > memoryFactsMaxCount {
		return fmt.Errorf("facts 最多 %d 条", memoryFactsMaxCount)
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
