package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

	// 检测配置变更（新购套餐 / 跨天）：total_quota 或 expires_at 变了则重置计数。
	// 更新带旧值条件：并发下只有一个请求真正重置，其余以 DB 最新状态继续（避免各自清零丢计数）。
	if su.TotalQuota != totalQuota || !su.ExpiresAt.Equal(expiresAt) {
		if err := r.db.WithContext(ctx).Model(&model.SearchUsage{}).
			Where("id = ? AND expires_at = ? AND total_quota = ?", su.ID, su.ExpiresAt, su.TotalQuota).
			Updates(map[string]interface{}{
				"total_quota": totalQuota,
				"expires_at":  expiresAt,
				"used_count":  0,
			}).Error; err != nil {
			return 0, fmt.Errorf("更新搜索配额配置失败: %w", err)
		}
		var cur model.SearchUsage
		if err := r.db.WithContext(ctx).First(&cur, "id = ?", su.ID).Error; err != nil {
			return 0, fmt.Errorf("查询搜索用量失败: %w", err)
		}
		su = cur
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
	// 并发窗口：多个请求可能同时发现「无记录」，插入必须幂等，否则撞 provider 唯一索引（1062）。
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&su).Error; err != nil {
		return 0, fmt.Errorf("创建搜索用量记录失败: %w", err)
	}
	// 统一重读取真实行：插入可能被别的请求抢先，本地 su 也不一定是最新计数。
	var cur model.SearchUsage
	if err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&cur).Error; err != nil {
		return 0, fmt.Errorf("查询搜索用量失败: %w", err)
	}
	return r.atomicIncrement(ctx, &cur)
}

func (r *SearchUsageRepo) atomicIncrement(ctx context.Context, su *model.SearchUsage) (int, error) {
	if time.Now().After(su.ExpiresAt) {
		return su.UsedCount, fmt.Errorf("搜索配额已过期（到期日 %s），请续费",
			su.ExpiresAt.Format("2006-01-02"))
	}

	result := r.db.WithContext(ctx).Model(&model.SearchUsage{}).
		Where("id = ? AND used_count < total_quota", su.ID).
		Update("used_count", gorm.Expr("used_count + 1"))
	if result.Error != nil {
		return 0, fmt.Errorf("更新搜索用量失败: %w", result.Error)
	}
	// 回读真实计数：递增是原子的，本地的 su.UsedCount 在并发下已过期。
	var cur model.SearchUsage
	if err := r.db.WithContext(ctx).First(&cur, "id = ?", su.ID).Error; err != nil {
		return 0, fmt.Errorf("查询搜索用量失败: %w", err)
	}
	if result.RowsAffected == 0 {
		return cur.UsedCount, fmt.Errorf("搜索次数已用完（%d/%d）",
			cur.UsedCount, cur.TotalQuota)
	}
	return cur.UsedCount, nil
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
