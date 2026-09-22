package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupDestroyer(t *testing.T) (*EphemeralDestroyer, *service.DelayedTaskRepo, *gorm.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "destroyer.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.Message{}, &model.Embedding{},
		&model.ConversationSummary{}, &model.ConversationPlan{}, &model.MessageShare{},
		&model.DelayedTask{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	convRepo := service.NewConversationRepo(db)
	taskRepo := service.NewDelayedTaskRepo(db)
	d := NewEphemeralDestroyer(convRepo, NewVectorStore(), taskRepo)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return d, taskRepo, db, cleanup
}

func TestBeginDestroy_SchedulesPurgeAndClearsVectors(t *testing.T) {
	d, taskRepo, db, cleanup := setupDestroyer(t)
	defer cleanup()
	ctx := context.Background()

	extra, _ := json.Marshal(&model.ConversationExtra{Ephemeral: true})
	db.Create(&model.Conversation{ID: "conv-0001", UserID: "user-0001", Extra: string(extra)})
	db.Create(&model.Message{ID: "msg-0001", ConversationID: "conv-0001", UserID: "user-0001", Role: "user", Status: 1})
	d.vectorStore.Upsert(model.VectorPrefixMessage+"msg-0001", []float64{1, 0}, map[string]string{"type": model.VectorTypeMessage, "conv_id": "conv-0001"})

	if err := d.BeginDestroy(ctx, "conv-0001", "user-0001"); err != nil {
		t.Fatalf("BeginDestroy: %v", err)
	}
	if d.vectorStore.Size() != 0 {
		t.Errorf("expected vectors cleared, got %d", d.vectorStore.Size())
	}
	tasks, _ := taskRepo.FindPendingByUserAndType(ctx, "user-0001", TaskTypeEphemeralPurge)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 purge task, got %d", len(tasks))
	}

	// 幂等：重复调用不重复入队
	if err := d.BeginDestroy(ctx, "conv-0001", "user-0001"); err != nil {
		t.Fatalf("BeginDestroy #2: %v", err)
	}
	tasks, _ = taskRepo.FindPendingByUserAndType(ctx, "user-0001", TaskTypeEphemeralPurge)
	if len(tasks) != 1 {
		t.Errorf("expected still 1 task after duplicate, got %d", len(tasks))
	}
}

func TestBeginDestroy_Nonexistent_NoOp(t *testing.T) {
	d, taskRepo, _, cleanup := setupDestroyer(t)
	defer cleanup()
	ctx := context.Background()

	if err := d.BeginDestroy(ctx, "missing-conv", "user-0001"); err != nil {
		t.Fatalf("BeginDestroy on missing conv should be no-op: %v", err)
	}
	tasks, _ := taskRepo.FindPendingByUserAndType(ctx, "user-0001", TaskTypeEphemeralPurge)
	if len(tasks) != 0 {
		t.Errorf("no task should be enqueued for missing conv, got %d", len(tasks))
	}
}

func TestRunPurge_BatchedAndIdempotent(t *testing.T) {
	d, _, db, cleanup := setupDestroyer(t)
	defer cleanup()
	ctx := context.Background()

	db.Create(&model.Conversation{ID: "conv-0001", UserID: "user-0001"})
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("msg-%04d", i)
		db.Create(&model.Message{ID: id, ConversationID: "conv-0001", UserID: "user-0001", Role: "user", Status: model.MsgStatusDeleted})
		db.Create(&model.Embedding{ID: id, EntityType: model.EntityTypeMessage, EntityID: id, UserID: "user-0001", Model: "test"})
	}

	if err := d.RunPurge(ctx, "conv-0001"); err != nil {
		t.Fatalf("RunPurge: %v", err)
	}
	var n int64
	db.Model(&model.Message{}).Where("conversation_id = ?", "conv-0001").Count(&n)
	if n != 0 {
		t.Errorf("messages remain: %d", n)
	}
	db.Model(&model.Embedding{}).Where("entity_type = ?", model.EntityTypeMessage).Count(&n)
	if n != 0 {
		t.Errorf("embeddings remain: %d", n)
	}
	db.Model(&model.Conversation{}).Where("id = ?", "conv-0001").Count(&n)
	if n != 0 {
		t.Errorf("conversation should be hard-deleted, count=%d", n)
	}

	// 幂等：会话已删，再次运行应安全 no-op
	if err := d.RunPurge(ctx, "conv-0001"); err != nil {
		t.Errorf("second RunPurge should be no-op, got %v", err)
	}
}
