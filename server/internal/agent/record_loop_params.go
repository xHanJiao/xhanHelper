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

// keepOrReplaceTopic 决定记忆更新时的 topic：入参非空（trim 后）就用入参，否则保留旧值。
// 见「update 把 topic 抹空」的 bug：模型常只传 summary、省略 topic。
func keepOrReplaceTopic(incoming, old string) string {
	if t := strings.TrimSpace(incoming); t != "" {
		return t
	}
	return old
}

// validateMemoryFields 工具侧字段校验，超限返回错误让模型改写。
// 不含 facts 条数上限：上限只约束「模型单次传入多少」（见 validateFactCount），
// 库里最终能存多少由 update 的合并结果决定，不逐出。
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
// 只用于校验入参，不用于校验合并后的结果——合并结果不设上限。
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

// parseFactsPtr 把库里的 facts JSON 字符串解析回 []string（factsPtr 的反向）。
// 空/坏 JSON 记日志后返回 nil：update 时旧 facts 读不出来不应阻断写入，但必须留痕。
func parseFactsPtr(p *string) []string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(*p), &out); err != nil {
		log.Printf("[record_loop] parse stored facts error: %v (raw %.80s, treat as empty)", err, *p)
		return nil
	}
	return out
}

// mergeFacts 把新增 facts 合并进已有（update 专用）：trim 后完全相等去重、保序。
// 不逐出：合并结果不设条数上限，只增不减。不做模糊/子串匹配（会误吞）。
// 纯确定性逻辑，不交给 LLM。
func mergeFacts(oldFacts, addFacts []string) []string {
	merged := make([]string, 0, len(oldFacts)+len(addFacts))
	seen := make(map[string]bool, len(oldFacts)+len(addFacts))
	appendFact := func(f string) {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			return
		}
		seen[f] = true
		merged = append(merged, f)
	}
	for _, f := range oldFacts {
		appendFact(f)
	}
	for _, f := range addFacts {
		appendFact(f)
	}
	return merged
}
