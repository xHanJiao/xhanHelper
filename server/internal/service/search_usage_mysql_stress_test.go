package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"xhan-helper/internal/model"
)

// TestSearchUsage_MySQLStress — 手工压测，默认跳过（需要外部 MySQL，且不产生任何 API 费用：
// 只压 search_usages 的额度写入，绝不调 Search/Fetch 接口）。
//
// 用法（DSN 用 dev 库，不要用 prod）：
//
//	QUOTA_MYSQL_DSN='xhan:pwd@tcp(127.0.0.1:3306)/xhan_helper_test?charset=utf8mb4&parseTime=true' \
//	  go test ./internal/service -run MySQLStress -count=1 -v
func TestSearchUsage_MySQLStress(t *testing.T) {
	dsn := os.Getenv("QUOTA_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUOTA_MYSQL_DSN 未设置，跳过 MySQL 压测")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchUsage{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewSearchUsageRepo(db)
	expires := tomorrowMidnight()

	// 5 轮「全新 provider + 100 并发首插」：这是 1062 的原始复现场景，一轮不够偶然性太大。
	for round := 0; round < 5; round++ {
		provider := fmt.Sprintf("stress_%d_%d", time.Now().UnixNano(), round)
		func() {
			defer func() {
				if err := db.Where("provider = ?", provider).Delete(&model.SearchUsage{}).Error; err != nil {
					t.Logf("清理压测行失败（可手工删除 provider=%s）: %v", provider, err)
				}
			}()

			total := 0
			for _, n := range []int{100, 50} {
				var wg sync.WaitGroup
				errs := make([]error, n)
				for i := 0; i < n; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						_, errs[i] = repo.TryUse(context.Background(), provider, 10000, expires, 0)
					}(i)
				}
				wg.Wait()

				for i, e := range errs {
					if e == nil {
						continue
					}
					if strings.Contains(e.Error(), "1062") || strings.Contains(e.Error(), "Duplicate entry") {
						t.Fatalf("轮次 %d/%d：goroutine %d 撞唯一索引（并发首插未幂等）: %v", round+1, n, i, e)
					}
					t.Fatalf("轮次 %d/%d：goroutine %d 失败: %v", round+1, n, i, e)
				}
				total += n

				var rows []model.SearchUsage
				if err := db.Where("provider = ?", provider).Find(&rows).Error; err != nil {
					t.Fatalf("query rows: %v", err)
				}
				if len(rows) != 1 {
					t.Fatalf("轮次 %d/%d：同一 provider 应只有 1 行，got %d", round+1, n, len(rows))
				}
				if rows[0].UsedCount != total {
					t.Fatalf("轮次 %d/%d：used_count 应为 %d，got %d", round+1, n, total, rows[0].UsedCount)
				}
			}
		}()
	}
	t.Logf("MySQL 压测通过：5 轮 ×（100 并发首插 + 50 并发递增），零重复键")
}
