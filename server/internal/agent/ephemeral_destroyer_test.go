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
		&model.DelayedTask{}, &model.Artifact{},
		&model.UserMemory{}, &model.EmbeddingChunk{}, &model.ContentVersion{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	convRepo := service.NewConversationRepo(db)
	memRepo := service.NewUserMemoryRepo(db)
	taskRepo := service.NewDelayedTaskRepo(db)
	vs := NewVectorStore()
	purger := service.NewEntityPurger(service.NewEmbeddingRepo(db), service.NewEmbeddingChunkRepo(db), service.NewVersionRepo(db), vs)
	d := NewEphemeralDestroyer(convRepo, memRepo, purger, vs, taskRepo)
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

// 无痕销毁必须清掉这些会话写下的记忆：会话从普通切无痕时，那些记忆是靠 source_conv_id 回溯的；
// 其他会话的记忆、以及没出处的存量记忆不能误删。
func TestBeginDestroy_PurgesMemoriesOfDestroyedConv(t *testing.T) {
	d, _, db, cleanup := setupDestroyer(t)
	defer cleanup()
	ctx := context.Background()

	extra, _ := json.Marshal(&model.ConversationExtra{Ephemeral: true})
	db.Create(&model.Conversation{ID: "conv-0001", UserID: "user-0001", Extra: string(extra)})
	db.Create(&model.Conversation{ID: "conv-keep", UserID: "user-0001"})

	mine, other := "conv-0001", "conv-keep"
	inline := model.MemorySourceInline
	db.Create(&model.UserMemory{ID: "mem-1", UserID: "user-0001", MemoryKey: "k", ContentHash: "h1",
		MemoryValue: "无痕会话里的隐私", Source: inline, SourceConvID: &mine})
	db.Create(&model.UserMemory{ID: "mem-2", UserID: "user-0001", MemoryKey: "k", ContentHash: "h2",
		MemoryValue: "别的会话的记忆", Source: inline, SourceConvID: &other})
	db.Create(&model.UserMemory{ID: "mem-3", UserID: "user-0001", MemoryKey: "k", ContentHash: "h3",
		MemoryValue: "存量无出处", Source: inline})

	// 派生数据：向量行 + chunk 行 + 内存向量
	db.Create(&model.Embedding{ID: "mem-1", EntityType: model.EntityTypeMemory, EntityID: "mem-1", UserID: "user-0001", Model: "test"})
	db.Create(&model.EmbeddingChunk{ID: "chunk-1", ParentType: model.EntityTypeMemory, ParentID: "mem-1",
		UserID: "user-0001", ChunkIndex: 0, Content: "无痕会话里的隐私"})
	d.vectorStore.Upsert(model.VectorPrefixMemory+"mem-1", []float64{1, 0}, map[string]string{"type": model.VectorTypeMemory})
	d.vectorStore.Upsert(model.VectorPrefixMemory+"mem-2", []float64{0, 1}, map[string]string{"type": model.VectorTypeMemory})

	if err := d.BeginDestroy(ctx, "conv-0001", "user-0001"); err != nil {
		t.Fatalf("BeginDestroy: %v", err)
	}

	var n int64
	db.Model(&model.UserMemory{}).Where("id = ?", "mem-1").Count(&n)
	if n != 0 {
		t.Errorf("本会话写下的记忆应被删除")
	}
	db.Model(&model.UserMemory{}).Where("id IN ?", []string{"mem-2", "mem-3"}).Count(&n)
	if n != 2 {
		t.Errorf("其他会话与无出处的记忆不该被删，剩 %d 条", n)
	}
	db.Model(&model.Embedding{}).Where("entity_type = ? AND entity_id = ?", model.EntityTypeMemory, "mem-1").Count(&n)
	if n != 0 {
		t.Errorf("记忆向量行应被清，剩 %d", n)
	}
	db.Model(&model.EmbeddingChunk{}).Where("parent_id = ?", "mem-1").Count(&n)
	if n != 0 {
		t.Errorf("记忆 chunk 应被清，剩 %d", n)
	}
	if _, ok := d.vectorStore.GetVector(model.VectorPrefixMemory + "mem-1"); ok {
		t.Error("内存向量应被清")
	}
	if _, ok := d.vectorStore.GetVector(model.VectorPrefixMemory + "mem-2"); !ok {
		t.Error("别的会话的内存向量不该被清")
	}
}
