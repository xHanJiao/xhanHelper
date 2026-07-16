package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupSummaryRepo(t *testing.T) (*ConversationSummaryRepo, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ConversationSummary{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewConversationSummaryRepo(db), cleanup
}

// seedSummaries 从 baseTime 开始每隔 1 小时创建一条摘要，返回创建顺序（时间正序）。
func seedSummaries(t *testing.T, repo *ConversationSummaryRepo, userID string, n int, baseTime time.Time, status string) []model.ConversationSummary {
	t.Helper()
	ctx := context.Background()
	var out []model.ConversationSummary
	for i := 0; i < n; i++ {
		s := model.ConversationSummary{
			ID:             fmt.Sprintf("s-%s-%02d", userID, i),
			ConversationID: "conv-1",
			UserID:         userID,
			SummaryText:    fmt.Sprintf("summary %d", i),
			Status:         status,
			CreatedAt:      baseTime.Add(time.Duration(i) * time.Hour),
		}
		if err := repo.Create(ctx, &s); err != nil {
			t.Fatalf("seed summary %d: %v", i, err)
		}
		out = append(out, s)
	}
	return out
}

func TestListByTimeRange_WithinRangeOnly(t *testing.T) {
	repo, cleanup := setupSummaryRepo(t)
	defer cleanup()

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	// 6 条：00:00 ~ 05:00
	seedSummaries(t, repo, "u1", 6, base, "active")

	// 范围 [02:00, 05:00) → 应命中 02:00、03:00、04:00 三条
	start := base.Add(2 * time.Hour)
	end := base.Add(5 * time.Hour)
	got, err := repo.ListByTimeRange(context.Background(), "u1", start, end, 10)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(got))
	}
	for _, s := range got {
		if s.CreatedAt.Before(start) || !s.CreatedAt.Before(end) {
			t.Errorf("summary %s created_at %v out of range [%v, %v)", s.ID, s.CreatedAt, start, end)
		}
	}
}

func TestListByTimeRange_OrderDesc(t *testing.T) {
	repo, cleanup := setupSummaryRepo(t)
	defer cleanup()

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	seedSummaries(t, repo, "u1", 5, base, "active")

	got, err := repo.ListByTimeRange(context.Background(), "u1", base, base.Add(24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 summaries, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("expected created_at DESC order, got[%d]=%v after got[%d]=%v",
				i, got[i].CreatedAt, i-1, got[i-1].CreatedAt)
		}
	}
}

func TestListByTimeRange_LimitKeepsNewest(t *testing.T) {
	repo, cleanup := setupSummaryRepo(t)
	defer cleanup()

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	seeded := seedSummaries(t, repo, "u1", 8, base, "active")

	got, err := repo.ListByTimeRange(context.Background(), "u1", base, base.Add(24*time.Hour), 3)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 summaries (limit), got %d", len(got))
	}
	// limit 截断应保留最新的 3 条（seeded 的最后 3 条）
	if got[0].ID != seeded[7].ID || got[1].ID != seeded[6].ID || got[2].ID != seeded[5].ID {
		t.Errorf("expected newest 3 [%s %s %s], got [%s %s %s]",
			seeded[7].ID, seeded[6].ID, seeded[5].ID, got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestListByTimeRange_UserScope(t *testing.T) {
	repo, cleanup := setupSummaryRepo(t)
	defer cleanup()

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	seedSummaries(t, repo, "u1", 3, base, "active")
	seedSummaries(t, repo, "u2", 3, base, "active")

	got, err := repo.ListByTimeRange(context.Background(), "u1", base, base.Add(24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 summaries for u1, got %d", len(got))
	}
	for _, s := range got {
		if s.UserID != "u1" {
			t.Errorf("expected user_id=u1, got %s", s.UserID)
		}
	}
}

func TestListByTimeRange_IncludesSuperseded(t *testing.T) {
	repo, cleanup := setupSummaryRepo(t)
	defer cleanup()

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	seedSummaries(t, repo, "u1", 2, base, "superseded")
	seedSummaries2 := model.ConversationSummary{
		ID: "s-active", ConversationID: "conv-1", UserID: "u1",
		SummaryText: "active one", Status: "active",
		CreatedAt: base.Add(3 * time.Hour),
	}
	if err := repo.Create(context.Background(), &seedSummaries2); err != nil {
		t.Fatalf("seed active: %v", err)
	}

	got, err := repo.ListByTimeRange(context.Background(), "u1", base, base.Add(24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 summaries (superseded included), got %d", len(got))
	}
}

func TestListByTimeRange_EmptyResult(t *testing.T) {
	repo, cleanup := setupSummaryRepo(t)
	defer cleanup()

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	seedSummaries(t, repo, "u1", 3, base, "active")

	// 查询范围在所有数据之前
	got, err := repo.ListByTimeRange(context.Background(), "u1", base.Add(-48*time.Hour), base.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(got))
	}
}

func TestListByTimeRange_DefaultAndMaxLimit(t *testing.T) {
	repo, cleanup := setupSummaryRepo(t)
	defer cleanup()

	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	// 25 条，间隔 1 分钟
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		s := model.ConversationSummary{
			ID: fmt.Sprintf("s-%02d", i), ConversationID: "conv-1", UserID: "u1",
			SummaryText: fmt.Sprintf("summary %d", i), Status: "active",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := repo.Create(ctx, &s); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// limit <= 0 → 默认 20
	got, err := repo.ListByTimeRange(ctx, "u1", base, base.Add(24*time.Hour), 0)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 20 {
		t.Errorf("limit=0: expected default 20, got %d", len(got))
	}

	// limit > 100 → 截到 100（数据只有 25 条，全部返回）
	got, err = repo.ListByTimeRange(ctx, "u1", base, base.Add(24*time.Hour), 200)
	if err != nil {
		t.Fatalf("ListByTimeRange error: %v", err)
	}
	if len(got) != 25 {
		t.Errorf("limit=200: expected 25, got %d", len(got))
	}
}
