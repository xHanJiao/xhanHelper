package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// SearchUsageRepo — 搜索配额用量追踪
type SearchUsageRepo struct {
	db *gorm.DB
}

func NewSearchUsageRepo(db *gorm.DB) *SearchUsageRepo {
	return &SearchUsageRepo{db: db}
}

// TryUse 确保 DB 记录存在、同步配置变更、检查过期、原子递增 used_count。
// totalQuota / expiresAt 来自 config；initialUsed 仅在首次创建记录时生效。
// 返回 (本次使用后的 used_count, error)。超配额或已过期返回 error。
func (r *SearchUsageRepo) TryUse(ctx context.Context, provider string, totalQuota int, expiresAt time.Time, initialUsed int) (int, error) {
	var su model.SearchUsage
	err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&su).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.createAndIncrement(ctx, provider, totalQuota, expiresAt, initialUsed)
	}
	if err != nil {
		return 0, fmt.Errorf("查询搜索用量失败: %w", err)
	}

	// 检测配置变更（新购套餐）：total_quota 或 expires_at 变了则重置计数
	if su.TotalQuota != totalQuota || !su.ExpiresAt.Equal(expiresAt) {
		if err := r.db.WithContext(ctx).Model(&su).Updates(map[string]interface{}{
			"total_quota": totalQuota,
			"expires_at":  expiresAt,
			"used_count":  0,
		}).Error; err != nil {
			return 0, fmt.Errorf("更新搜索配额配置失败: %w", err)
		}
		su.TotalQuota = totalQuota
		su.ExpiresAt = expiresAt
		su.UsedCount = 0
	}

	return r.atomicIncrement(ctx, &su)
}

func (r *SearchUsageRepo) createAndIncrement(ctx context.Context, provider string, totalQuota int, expiresAt time.Time, initialUsed int) (int, error) {
	su := model.SearchUsage{
		ID:         newUUID(),
		Provider:   provider,
		TotalQuota: totalQuota,
		UsedCount:  initialUsed,
		ExpiresAt:  expiresAt,
	}
	if err := r.db.WithContext(ctx).Create(&su).Error; err != nil {
		return 0, fmt.Errorf("创建搜索用量记录失败: %w", err)
	}
	// 首次创建后立即 +1（initialUsed 是创建前的已用量）
	return r.atomicIncrement(ctx, &su)
}

func (r *SearchUsageRepo) atomicIncrement(ctx context.Context, su *model.SearchUsage) (int, error) {
	if time.Now().After(su.ExpiresAt) {
		return su.UsedCount, fmt.Errorf("搜索配额已过期（到期日 %s），请续费",
			su.ExpiresAt.Format("2006-01-02"))
	}

	result := r.db.WithContext(ctx).Model(&model.SearchUsage{}).
		Where("id = ? AND used_count < total_quota", su.ID).
		Update("used_count", gorm.Expr("used_count + 1"))
	if result.RowsAffected == 0 {
		// 重新读取获取当前实际值
		if err := r.db.WithContext(ctx).First(su, "id = ?", su.ID).Error; err != nil {
			return 0, fmt.Errorf("查询搜索用量失败: %w", err)
		}
		return su.UsedCount, fmt.Errorf("搜索次数已用完（%d/%d）",
			su.UsedCount, su.TotalQuota)
	}
	return su.UsedCount + 1, nil
}

// Ensure 确保 DB 中存在该 provider 的记录（不存在则用 config 值创建），不做递增
func (r *SearchUsageRepo) Ensure(ctx context.Context, provider string, totalQuota int, expiresAt time.Time, initialUsed int) error {
	var su model.SearchUsage
	err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&su).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		su = model.SearchUsage{
			ID:         newUUID(),
			Provider:   provider,
			TotalQuota: totalQuota,
			UsedCount:  initialUsed,
			ExpiresAt:  expiresAt,
		}
		return r.db.WithContext(ctx).Create(&su).Error
	}
	if err != nil {
		return err
	}
	if su.TotalQuota != totalQuota || !su.ExpiresAt.Equal(expiresAt) {
		return r.db.WithContext(ctx).Model(&su).Updates(map[string]interface{}{
			"total_quota": totalQuota,
			"expires_at":  expiresAt,
			"used_count":  0,
		}).Error
	}
	return nil
}

// Snapshot 返回当前用量快照，供监控看板使用
func (r *SearchUsageRepo) Snapshot(ctx context.Context, provider string) map[string]interface{} {
	var su model.SearchUsage
	err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&su).Error
	if err != nil {
		return map[string]interface{}{
			"used":        0,
			"total_quota": 0,
			"provider":    provider,
			"expires_at":  "",
		}
	}
	return map[string]interface{}{
		"used":        su.UsedCount,
		"total_quota": su.TotalQuota,
		"provider":    su.Provider,
		"expires_at":  su.ExpiresAt.Format("2006-01-02"),
	}
}

// newUUID 简单 UUID 生成（无需引入外部依赖）
func newUUID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		time.Now().UnixNano()&0xFFFFFFFF,
		time.Now().UnixNano()>>32&0xFFFF,
		time.Now().UnixNano()>>48&0xFFFF,
		time.Now().UnixNano()>>32&0xFFFF|0x4000,
		time.Now().UnixNano()&0xFFFFFFFFFFFF)
}
