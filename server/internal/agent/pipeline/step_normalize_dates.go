package pipeline

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"xhan-helper/internal/config"
)

// NormalizeDatesStep 将中文相对日期替换为绝对日期
type NormalizeDatesStep struct {
	now time.Time
}

func NewNormalizeDatesStep() *NormalizeDatesStep {
	return &NormalizeDatesStep{now: time.Now()}
}

func (s *NormalizeDatesStep) Name() string                       { return "normalizeDates" }
func (s *NormalizeDatesStep) Requires() []string                 { return []string{"raw_msg"} }
func (s *NormalizeDatesStep) Produces() []string                 { return []string{"normalized_msg"} }
func (s *NormalizeDatesStep) ShouldRun(ctx context.Context) bool { return true }

func (s *NormalizeDatesStep) Run(ctx context.Context) (context.Context, error) {
	pc := GetPreContext(ctx)
	normalized := pc.RawMsg

	weekdayNames := map[string]time.Weekday{
		"一": time.Monday, "二": time.Tuesday, "三": time.Wednesday,
		"四": time.Thursday, "五": time.Friday, "六": time.Saturday, "日": time.Sunday, "天": time.Sunday,
	}

	now := s.now.In(config.BJ)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, config.BJ)
	dateFmt := "2006-01-02"

	// 具体星期: 上周一、这周三、下周五
	for prefix, weekOffset := range map[string]int{"上": -1, "这": 0, "下": 1} {
		for name, wd := range weekdayNames {
			pattern := prefix + "周" + name
			daysUntil := int(wd) - int(today.Weekday())
			if weekOffset != 0 {
				daysUntil += weekOffset * 7
			}
			target := today.AddDate(0, 0, daysUntil)
			normalized = strings.ReplaceAll(normalized, pattern, pattern+"（"+target.Format(dateFmt)+"）")
		}
	}

	// 相对天: N天前, N天后
	daysAgoRe := regexp.MustCompile(`(\d+)天前`)
	normalized = daysAgoRe.ReplaceAllStringFunc(normalized, func(m string) string {
		nStr := daysAgoRe.FindStringSubmatch(m)[1]
		n, _ := strconv.Atoi(nStr)
		return m + "（" + today.AddDate(0, 0, -n).Format(dateFmt) + "）"
	})
	daysLaterRe := regexp.MustCompile(`(\d+)天后`)
	normalized = daysLaterRe.ReplaceAllStringFunc(normalized, func(m string) string {
		nStr := daysLaterRe.FindStringSubmatch(m)[1]
		n, _ := strconv.Atoi(nStr)
		return m + "（" + today.AddDate(0, 0, n).Format(dateFmt) + "）"
	})

	// 固定替换
	fixed := map[string]string{
		"前天": today.AddDate(0, 0, -2).Format(dateFmt),
		"后天": today.AddDate(0, 0, 2).Format(dateFmt),
		"昨天": today.AddDate(0, 0, -1).Format(dateFmt),
		"明天": today.AddDate(0, 0, 1).Format(dateFmt),
		"今天": today.Format(dateFmt),
	}
	for word, repl := range fixed {
		normalized = strings.ReplaceAll(normalized, word, word+"（"+repl+"）")
	}

	if normalized != pc.RawMsg {
		log.Printf("[pipeline] normalizeDates: raw=%q → normalized=%q", pc.RawMsg, normalized)
	}

	pc.NormalizedMsg = normalized
	return ctx, nil
}
