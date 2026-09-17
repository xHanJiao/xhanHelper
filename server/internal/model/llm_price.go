package model

import "time"

// LlmModelPrice 模型单价记录（元 / 1M tokens），按生效日期分段。
// 某天某模型的有效价格 = effective_date <= 当天 的最新一条记录。
// 峰谷：时间落在 PeakSegments 任一段内（开始含、结束不含）用峰价，其余用谷价；峰段为空 = 全天谷价。
// WeekendAllOff 为 true 时，周六/周日恒按谷价（PeakSegments 仅作用于周一~周五），用于供应商「周末全天谷」的定价规则。
// 所有时段按北京时间（Asia/Shanghai）判定。
type LlmModelPrice struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	Model          string    `gorm:"type:varchar(50);not null;index:idx_model_date,priority:1" json:"model"`
	EffectiveDate  string    `gorm:"type:date;not null;index:idx_model_date,priority:2" json:"effective_date"`
	PriceHit       float64   `gorm:"not null" json:"price_hit"`                     // 峰值：缓存命中，元/1M tokens
	PriceMiss      float64   `gorm:"not null" json:"price_miss"`                    // 峰值：缓存未命中
	PriceOutput    float64   `gorm:"not null" json:"price_output"`                  // 峰值：生成
	PriceHitOff    float64   `gorm:"not null" json:"price_hit_off"`                 // 谷值：缓存命中
	PriceMissOff   float64   `gorm:"not null" json:"price_miss_off"`                // 谷值：缓存未命中
	PriceOutputOff float64   `gorm:"not null" json:"price_output_off"`              // 谷值：生成
	PeakSegments   string    `gorm:"type:text" json:"peak_segments"`                // 峰时段 JSON：[["08:30","12:00"],["14:00","17:00"]]；空串 = 全天谷
	WeekendAllOff  bool      `gorm:"not null;default:false" json:"weekend_all_off"` // true = 周六/周日全天谷价
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}
