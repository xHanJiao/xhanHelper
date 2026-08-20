package agent

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupTestDelayedTaskRepo(t *testing.T) (*service.DelayedTaskRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.DelayedTask{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewDelayedTaskRepo(db)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return repo, cleanup
}

func newTestCronRunner(repo *service.DelayedTaskRepo) *CronRunner {
	return &CronRunner{
		delayedTaskRepo:   repo,
		sessionGapMinutes: 15,
	}
}

func TestSessionEndTask_CreatesNewWhenNoPending(t *testing.T) {
	repo, cleanup := setupTestDelayedTaskRepo(t)
	defer cleanup()

	cr := newTestCronRunner(repo)
	cr.NotifySessionEnd("user-0001", "conv-0001", "msg-0004")

	tasks, err := repo.FindPendingByUserAndType(context.Background(), "user-0001", "session_end")
	if err != nil {
		t.Fatalf("FindPendingByUserAndType: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 pending session_end task, got %d", len(tasks))
	}
	meta, err := tasks[0].ParseMeta()
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if meta.ConvID != "conv-0001" || meta.LastMsgID != "msg-0004" {
		t.Errorf("unexpected meta: %+v", meta)
	}

	// 使用 UTC 统一比较
	expected := time.Now().UTC().Add(15 * time.Minute)
	if diff := tasks[0].ProcessAt.Sub(expected); diff > time.Minute || diff < -time.Minute {
		t.Errorf("process_at should be ~15min in future, got %v (diff %v)", tasks[0].ProcessAt, diff)
	}
}

func TestSessionEndTask_DebounceResetsProcessAt(t *testing.T) {
	repo, cleanup := setupTestDelayedTaskRepo(t)
	defer cleanup()

	cr := newTestCronRunner(repo)
	cr.NotifySessionEnd("user-0001", "conv-0001", "msg-0004")
	tasks, _ := repo.FindPendingByUserAndType(context.Background(), "user-0001", "session_end")
	first := tasks[0]
	firstProcessAt := first.ProcessAt

	// 等待很短时间后再次触发同一 conv → 应推迟 process_at 并更新 LastMsgID
	time.Sleep(20 * time.Millisecond)
	cr.NotifySessionEnd("user-0001", "conv-0001", "msg-0008")

	tasks2, _ := repo.FindPendingByUserAndType(context.Background(), "user-0001", "session_end")
	if len(tasks2) != 1 {
		t.Fatalf("expected still 1 pending task (debounced), got %d", len(tasks2))
	}
	if !tasks2[0].ProcessAt.After(firstProcessAt) {
		t.Errorf("expected process_at to be pushed back after debounce, got %v vs original %v", tasks2[0].ProcessAt, firstProcessAt)
	}
	meta, _ := tasks2[0].ParseMeta()
	if meta.LastMsgID != "msg-0008" {
		t.Errorf("expected LastMsgID updated to msg-0008, got %s", meta.LastMsgID)
	}
}

func TestSessionEndTask_ListDueAndMarkDone(t *testing.T) {
	repo, cleanup := setupTestDelayedTaskRepo(t)
	defer cleanup()

	// 手动插入一条已到期任务（process_at 在过去）
	job := &model.DelayedTask{
		ID:        "task-0001-id-00001",
		UserID:    "user-0001",
		TaskType:  "session_end",
		Status:    "pending",
		ProcessAt: time.Now().Add(-1 * time.Minute),
	}
	_ = job.SetMeta(model.DelayedTaskMeta{ConvID: "conv-0001", LastMsgID: "msg-0004"})
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatalf("create: %v", err)
	}

	due, err := repo.ListDue(context.Background(), "session_end", 20)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due task, got %d", len(due))
	}

	// MarkDone 后不再到期
	if err := repo.MarkDone(context.Background(), job.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	due2, _ := repo.ListDue(context.Background(), "session_end", 20)
	if len(due2) != 0 {
		t.Errorf("expected 0 due tasks after MarkDone, got %d", len(due2))
	}
}
