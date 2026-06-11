package retrieval

import (
	"math"
	"time"
)

// TimeDecay 时间衰减计算器，用于对检索结果施加新鲜度过滤
type TimeDecay struct {
	Rate float64 // 时间衰减速率，>0 时启用
}

// NewTimeDecay 创建指定衰减速率的时间衰减器
func NewTimeDecay(rate float64) *TimeDecay {
	return &TimeDecay{Rate: rate}
}

// Freshness 计算给定时间点的鲜度值，范围 (0, 1]
// 公式: 1 / (1 + daysAgo * Rate)
func (d *TimeDecay) Freshness(createdAt time.Time) float64 {
	if d.Rate <= 0 {
		return 1.0
	}
	daysAgo := time.Since(createdAt).Hours() / 24
	f := 1.0 / (1.0 + daysAgo*d.Rate)
	return math.Round(f*10000) / 10000
}
