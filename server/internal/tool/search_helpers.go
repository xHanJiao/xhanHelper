package tool

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"xhan-helper/internal/config"
)

// ==================== 关键词处理 ====================

// quoteRunes 是需要从关键词首尾剥离的引号字符集合。
var quoteRunes = []rune{
	'"', '\'', '“', '”', '‘', '’', '「', '」',
}

// splitKeywords 按空格、逗号（半角/全角）、顿号分割 query 并去重去空。
// 分割后的每个关键词会剥离首尾的引号（中英文/单双/直角）。
func splitKeywords(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	var result []string
	seen := make(map[string]bool)
	for _, part := range strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == ',' || r == '，' || r == '、' || r == '"' || r == '\'' || r == '“' || r == '”' || r == '‘' || r == '’'
	}) {
		part = strings.TrimSpace(part)
		// 移除所有引号
		for _, qr := range quoteRunes {
			part = strings.ReplaceAll(part, string(qr), "")
		}
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			result = append(result, part)
		}
	}
	return result
}

// findMatchedKeywords 大小写不敏感地检查内容中包含哪些关键词。
func findMatchedKeywords(content string, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	lower := strings.ToLower(content)
	var matched []string
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			matched = append(matched, kw)
		}
	}
	return matched
}

// ==================== 时间处理 ====================

// acceptedTimeFormats 是 parseTimeString 依次尝试的时间格式列表。
var acceptedTimeFormats = []string{
	time.RFC3339,          // 2026-06-15T14:30:00+08:00
	"2006-01-02 15:04:05", // 2026-06-15 14:30:00
	"2006-01-02T15:04:05", // 2026-06-15T14:30:00（无时区）
	"2006/01/02 15:04:05", // 2026/06/15 14:30:00
	"2006-01-02",          // 2026-06-15（默认当日 00:00:00）
	"2006/01/02",          // 2026/06/15（默认当日 00:00:00）
}

// parseTimeString 依次尝试多种常见时间格式，返回东八区时间。
func parseTimeString(s string) (time.Time, error) {
	for _, layout := range acceptedTimeFormats {
		t, err := time.ParseInLocation(layout, s, config.BJ)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间 %q，支持格式: 2026-06-15T14:30:00+08:00 / 2026-06-15 14:30:00 / 2026-06-15", s)
}

// isDateOnly 判断字符串是否为纯日期格式（无时间部分）。
func isDateOnly(s string) bool {
	for _, layout := range []string{"2006-01-02", "2006/01/02"} {
		if _, err := time.ParseInLocation(layout, s, config.BJ); err == nil {
			return true
		}
	}
	return false
}

// parseTimeParams 解析时间参数，返回 after/before 的指针（nil 表示无限制）。
// start_time/end_time 与 last_n_days/last_n_hours 互斥。
// 纯日期 end_time（如 2026-06-27）自动 +24h 以覆盖当天全部数据。
func parseTimeParams(startTime, endTime string, lastNDays, lastNHours int) (after, before *time.Time, err error) {
	hasAbsolute := startTime != "" || endTime != ""
	hasRelative := lastNDays > 0 || lastNHours > 0
	if hasAbsolute && hasRelative {
		return nil, nil, fmt.Errorf("start_time/end_time 与 last_n_days/last_n_hours 互斥，请只使用一种方式")
	}

	now := time.Now()

	if hasAbsolute {
		if startTime != "" {
			t, e := parseTimeString(startTime)
			if e != nil {
				return nil, nil, e
			}
			after = &t
		}
		if endTime != "" {
			t, e := parseTimeString(endTime)
			if e != nil {
				return nil, nil, e
			}
			// 纯日期（如 2026-06-27）→ 含当日，推到次日 00:00:00
			if isDateOnly(endTime) {
				t = t.Add(24 * time.Hour)
			}
			before = &t
		}
		return after, before, nil
	}

	if hasRelative {
		var d time.Duration
		if lastNDays > 0 {
			d = time.Duration(lastNDays) * 24 * time.Hour
		} else if lastNHours > 0 {
			d = time.Duration(lastNHours) * time.Hour
		}
		t := now.Add(-d)
		after = &t
		return after, nil, nil
	}

	return nil, nil, nil
}

// formatMsgTime 统一格式化为 ISO 8601 +08:00 时间。
func formatMsgTime(t time.Time) string {
	return t.In(config.BJ).Format(time.RFC3339)
}

// scoredHit 向量搜索结果，带 ID 和相关性分数。
type scoredHit struct {
	ID    string
	Score float64
}

// sortScoredHits 按 Score 降序排列。
func sortScoredHits(hits []scoredHit) {
	sort.Slice(hits, func(i, j int) bool {
		return hits[i].Score > hits[j].Score
	})
}
