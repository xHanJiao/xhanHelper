package service

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"xhan-helper/internal/model"
)

func setupSearchUsageRepo(t *testing.T) (*SearchUsageRepo, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// 单连接串行化语句：SQLite 并发写会报 database is locked，但语句之间仍可交错，
	// 正好用来复现「两个请求都看到无记录」的竞态。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&model.SearchUsage{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewSearchUsageRepo(db), db
}

func tomorrowMidnight() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
}

// TestSearchUsage_ConcurrentFirstUse — 并发首次调用不能撞 provider 唯一索引（回归：1062 duplicate）。
func TestSearchUsage_ConcurrentFirstUse(t *testing.T) {
	repo, db := setupSearchUsageRepo(t)
	expires := tomorrowMidnight()

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.TryUse(context.Background(), "deepseek_web", 100, expires, 0)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d 失败（并发首插应幂等）: %v", i, err)
		}
	}

	var rows []model.SearchUsage
	if err := db.Where("provider = ?", "deepseek_web").Find(&rows).Error; err != nil {
		t.Fatalf("query rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("同一 provider 应只有 1 行，got %d", len(rows))
	}
	if rows[0].UsedCount != n {
		t.Errorf("used_count 应为 %d，got %d", n, rows[0].UsedCount)
	}
}

// TestSearchUsage_ExhaustedAtLimit — 超过上限返回错误，且计数停在上限（不越界）。
func TestSearchUsage_ExhaustedAtLimit(t *testing.T) {
	repo, _ := setupSearchUsageRepo(t)
	expires := tomorrowMidnight()

	for i := 0; i < 2; i++ {
		if _, err := repo.TryUse(context.Background(), "p", 2, expires, 0); err != nil {
			t.Fatalf("前 2 次应成功，第 %d 次报错: %v", i+1, err)
		}
	}
	used, err := repo.TryUse(context.Background(), "p", 2, expires, 0)
	if err == nil {
		t.Fatal("超限应返回 error")
	}
	if used != 2 {
		t.Errorf("超限时 used 应等于上限 2，got %d", used)
	}
}

// TestSearchUsage_RolloverResets — expires_at 变化（跨天）时计数清零，不靠定时任务。
func TestSearchUsage_RolloverResets(t *testing.T) {
	repo, db := setupSearchUsageRepo(t)
	ctx := context.Background()

	// 直接埋一条「昨天已用 2 次」的记录（走 TryUse 会被过期保护拦住）
	if err := db.Create(&model.SearchUsage{
		ID: "seed", Provider: "p", TotalQuota: 100, UsedCount: 2, ExpiresAt: midnightOffset(-1),
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	used, err := repo.TryUse(ctx, "p", 100, tomorrowMidnight(), 0)
	if err != nil {
		t.Fatalf("跨天调用: %v", err)
	}
	if used != 1 {
		t.Errorf("跨天后 used 应从 1 重新计数，got %d", used)
	}
	var row model.SearchUsage
	if err := db.Where("provider = ?", "p").First(&row).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if row.UsedCount != 1 {
		t.Errorf("DB used_count 应为 1，got %d", row.UsedCount)
	}
}

func midnightOffset(days int) time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, days)
}

// TestSearchUsage_ConcurrentRollover — 跨天重置与递增并发时不能丢计数、不能报错。
func TestSearchUsage_ConcurrentRollover(t *testing.T) {
	repo, db := setupSearchUsageRepo(t)
	ctx := context.Background()

	if err := db.Create(&model.SearchUsage{
		ID: "seed", Provider: "p", TotalQuota: 1000, UsedCount: 5, ExpiresAt: midnightOffset(-1),
	}).Error; err != nil {
		t.Fatalf("打底记录: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.TryUse(ctx, "p", 1000, tomorrowMidnight(), 0)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d 失败: %v", i, err)
		}
	}
	var row model.SearchUsage
	if err := db.Where("provider = ?", "p").First(&row).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if row.UsedCount != n {
		t.Errorf("重置后应精确累计 %d 次，got %d", n, row.UsedCount)
	}
}

// TestSearchUsage_DBErrorPropagates — 底层错误必须上抛（不能静默当 0 用）。
func TestSearchUsage_DBErrorPropagates(t *testing.T) {
	repo, db := setupSearchUsageRepo(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	_ = sqlDB.Close()

	if _, err := repo.TryUse(context.Background(), "p", 10, tomorrowMidnight(), 0); err == nil {
		t.Fatal("DB 关闭后应返回 error")
	}
}
