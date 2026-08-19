package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// LlmPriceRepo 模型定价数据访问层
type LlmPriceRepo struct {
	db *gorm.DB
}

// NewLlmPriceRepo 创建 LlmPriceRepo
func NewLlmPriceRepo(db *gorm.DB) *LlmPriceRepo {
	return &LlmPriceRepo{db: db}
}

// List 全量价格记录，按 model, effective_date 升序。
// 价格表是小表（模型数 × 调价次数），全量加载后在内存匹配，不做分页。
func (r *LlmPriceRepo) List(ctx context.Context) ([]model.LlmModelPrice, error) {
	var rows []model.LlmModelPrice
	if err := r.db.WithContext(ctx).Order("model, effective_date").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list model prices: %w", err)
	}
	for i := range rows {
		// MySQL DATE 列被 GORM 扫回 RFC3339（"2026-08-17T00:00:00Z"），统一截断为 YYYY-MM-DD
		rows[i].EffectiveDate = normalizeDateKey(rows[i].EffectiveDate)
	}
	if rows == nil {
		rows = []model.LlmModelPrice{}
	}
	return rows, nil
}

// Save Upsert：同 (model, effective_date) 存在则更新单价，否则插入。
func (r *LlmPriceRepo) Save(ctx context.Context, p *model.LlmModelPrice) error {
	var existing model.LlmModelPrice
	err := r.db.WithContext(ctx).Where("model = ? AND effective_date = ?", p.Model, p.EffectiveDate).
		First(&existing).Error
	switch {
	case err == nil:
		existing.PriceHit = p.PriceHit
		existing.PriceMiss = p.PriceMiss
		existing.PriceOutput = p.PriceOutput
		existing.PriceHitOff = p.PriceHitOff
		existing.PriceMissOff = p.PriceMissOff
		existing.PriceOutputOff = p.PriceOutputOff
		existing.PeakSegments = p.PeakSegments
		// 用用户输入的干净日期覆盖读回的 RFC3339 值，保证 DB 与响应都是 YYYY-MM-DD
		existing.EffectiveDate = p.EffectiveDate
		*p = existing
		return r.db.WithContext(ctx).Save(&existing).Error
	case err == gorm.ErrRecordNotFound:
		if p.EffectiveDate == "" {
			p.EffectiveDate = time.Now().Format("2006-01-02")
		}
		return r.db.WithContext(ctx).Create(p).Error
	default:
		return fmt.Errorf("find model price: %w", err)
	}
}

// Delete 删除单条记录。
func (r *LlmPriceRepo) Delete(ctx context.Context, id uint) error {
	if err := r.db.WithContext(ctx).Delete(&model.LlmModelPrice{}, id).Error; err != nil {
		return fmt.Errorf("delete model price: %w", err)
	}
	return nil
}

// ============================================================
// 峰段解析与判定（所有时段为北京时间 HH:MM）
// ============================================================

// validHM 校验 HH:MM 格式（严格 5 字符）。
func validHM(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", s)
	return err == nil
}

// NormalizePeakSegments 将用户输入的峰段文本规范化。
// 输入格式："08:30-12:00,14:00-17:00"（逗号分隔多段，每段 start-end）。
// 空串/纯空白 = 全天谷，返回 ""。返回存储用的 compact JSON。
func NormalizePeakSegments(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	segs, err := parsePeakSegmentsText(text)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(segs)
	if err != nil {
		return "", fmt.Errorf("marshal peak segments: %w", err)
	}
	return string(b), nil
}

// parsePeakSegmentsText 解析用户输入的峰段文本为段列表。
func parsePeakSegmentsText(text string) ([][]string, error) {
	parts := strings.Split(text, ",")
	segs := make([][]string, 0, len(parts))
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		hs := strings.SplitN(p, "-", 2)
		if len(hs) != 2 || !validHM(hs[0]) || !validHM(hs[1]) {
			return nil, fmt.Errorf("无效峰段 %q，格式应为 08:30-12:00（HH:MM-HH:MM），多段用逗号分隔", p)
		}
		if hs[0] >= hs[1] {
			return nil, fmt.Errorf("峰段 %q 的开始时间必须早于结束时间", p)
		}
		segs = append(segs, []string{hs[0], hs[1]})
	}
	return segs, nil
}

// parsePeakSegmentsJSON 解析存储的峰段 JSON（空串 = 无峰段 = 全天谷）。
func parsePeakSegmentsJSON(s string) ([][]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var segs [][]string
	if err := json.Unmarshal([]byte(s), &segs); err != nil {
		return nil, fmt.Errorf("parse peak segments json: %w", err)
	}
	return segs, nil
}

// isPeak 判断北京时间 HH:MM 是否落在任一峰段内（段开始含、结束不含）。
// 峰段为 nil/空时返回 false（全天谷）。
func isPeak(hm string, segs [][]string) bool {
	for _, seg := range segs {
		if len(seg) == 2 && hm >= seg[0] && hm < seg[1] {
			return true
		}
	}
	return false
}
