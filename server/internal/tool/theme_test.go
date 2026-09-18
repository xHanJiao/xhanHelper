package tool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupThemeTool(t *testing.T) (*ThemeTool, *service.ThemeStateRepo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ThemeState{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// :memory: 每个连接一个独立库，限制单连接让并发测试共用同一内存库
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	repo := service.NewThemeStateRepo(db)
	return NewThemeTool(repo, nil), repo
}

// 并发追加 state_text：读→追加→整字段写回，无锁时后写者用旧快照覆盖丢条目
func TestThemeConcurrentAppendStateText(t *testing.T) {
	tool, repo := setupThemeTool(t)
	ctx := context.Background()
	ec := &ExecuteContext{Ctx: ctx, UserID: "u1"}

	if err := repo.Create(ctx, &model.ThemeState{ID: "th1", UserID: "u1", Name: "健身"}); err != nil {
		t.Fatalf("create theme: %v", err)
	}

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := tool.Execute(ec, "update_theme", mustMarshal(t, map[string]interface{}{
				"name": "健身", "state_text": fmt.Sprintf("追加%d", i),
			}))
			if !r.Success {
				t.Errorf("update_theme %d failed: %s", i, r.Error)
			}
		}(i)
	}
	wg.Wait()

	th, err := repo.GetByName(ctx, "u1", "健身")
	if err != nil || th == nil {
		t.Fatalf("get theme: %v", err)
	}
	for i := 0; i < n; i++ {
		if !strings.Contains(th.StateText, fmt.Sprintf("追加%d", i)) {
			t.Errorf("并发追加第 %d 条丢失：%s", i, th.StateText)
		}
	}
}
