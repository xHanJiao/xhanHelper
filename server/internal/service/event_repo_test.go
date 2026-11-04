package service

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
)

func setupEventRepo(t *testing.T) (*EventRepo, *gorm.DB) {
	t.Helper()
	if err := idgen.Init(1); err != nil {
		t.Fatalf("idgen init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.Event{}, &model.UserMemory{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return NewEventRepo(db), db
}

func addMemory(t *testing.T, db *gorm.DB, userID, id, hash string) {
	t.Helper()
	err := db.Create(&model.UserMemory{
		ID:          id,
		UserID:      userID,
		MemoryKey:   "k",
		ContentHash: hash,
		MemoryValue: "v-" + id,
	}).Error
	if err != nil {
		t.Fatalf("insert memory %s: %v", id, err)
	}
}

func memoryEventID(t *testing.T, db *gorm.DB, id string) *string {
	t.Helper()
	var m model.UserMemory
	if err := db.Where("id = ?", id).First(&m).Error; err != nil {
		t.Fatalf("load memory %s: %v", id, err)
	}
	return m.EventID
}

func TestEventRepo_CreateAndGet(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	e := &model.Event{UserID: "u1", Name: "回西安", Summary: "打包中"}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.ID == "" {
		t.Fatal("expected generated id")
	}
	got, err := repo.GetByID(ctx, "u1", e.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Name != "回西安" || got.IsDeleted {
		t.Fatalf("unexpected event: %+v", got)
	}
	// 别的用户看不到
	other, err := repo.GetByID(ctx, "u2", e.ID)
	if err != nil {
		t.Fatalf("get other: %v", err)
	}
	if other != nil {
		t.Fatal("cross-user leak")
	}
}

func TestEventRepo_ListExcludesDeleted(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	for _, name := range []string{"A", "B"} {
		if err := repo.Create(ctx, &model.Event{UserID: "u1", Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	var b model.Event
	if err := repo.db.Where("name = ?", "B").First(&b).Error; err != nil {
		t.Fatalf("load B: %v", err)
	}
	if err := repo.Delete(ctx, "u1", b.ID); err != nil {
		t.Fatalf("delete B: %v", err)
	}

	list, err := repo.List(ctx, "u1", false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "A" {
		t.Fatalf("expected only A, got %+v", list)
	}
	all, err := repo.List(ctx, "u1", true)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 including deleted, got %d", len(all))
	}
}

func TestEventRepo_Merge(t *testing.T) {
	repo, db := setupEventRepo(t)
	ctx := context.Background()

	from := &model.Event{UserID: "u1", Name: "科三考试"}
	to := &model.Event{UserID: "u1", Name: "驾照考取"}
	if err := repo.Create(ctx, from); err != nil {
		t.Fatalf("create from: %v", err)
	}
	if err := repo.Create(ctx, to); err != nil {
		t.Fatalf("create to: %v", err)
	}
	addMemory(t, db, "u1", "m1", "h1")
	addMemory(t, db, "u1", "m2", "h2")
	addMemory(t, db, "u1", "m3", "h3")
	if err := repo.AssignMemories(ctx, "u1", from.ID, []string{"m1", "m2"}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := repo.AssignMemories(ctx, "u1", to.ID, []string{"m3"}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	older := time.Now().Add(-48 * time.Hour)
	if err := repo.SetLastMemoryAt(ctx, "u1", from.ID, older); err != nil {
		t.Fatalf("set from last: %v", err)
	}
	newer := time.Now()
	if err := repo.SetLastMemoryAt(ctx, "u1", to.ID, newer); err != nil {
		t.Fatalf("set to last: %v", err)
	}

	if err := repo.Merge(ctx, "u1", from.ID, to.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}

	// 记忆全部搬到目标
	for _, id := range []string{"m1", "m2", "m3"} {
		eid := memoryEventID(t, db, id)
		if eid == nil || *eid != to.ID {
			t.Fatalf("memory %s not moved to target: %v", id, eid)
		}
	}
	// 源事件软删，行还在
	gotFrom, err := repo.GetByID(ctx, "u1", from.ID)
	if err != nil || gotFrom == nil {
		t.Fatalf("get from: %v %v", gotFrom, err)
	}
	if !gotFrom.IsDeleted {
		t.Fatal("expected source soft-deleted")
	}
	// 目标最后记忆时间取较大值（不被旧的拉回）
	gotTo, err := repo.GetByID(ctx, "u1", to.ID)
	if err != nil || gotTo == nil {
		t.Fatalf("get to: %v %v", gotTo, err)
	}
	if gotTo.LastMemoryAt == nil || gotTo.LastMemoryAt.Before(newer.Add(-time.Second)) {
		t.Fatalf("last_memory_at regressed: %v", gotTo.LastMemoryAt)
	}
	// 幂等
	if err := repo.Merge(ctx, "u1", from.ID, to.ID); err != nil {
		t.Fatalf("idempotent merge: %v", err)
	}
}

func TestEventRepo_MergeGuards(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	a := &model.Event{UserID: "u1", Name: "A"}
	b := &model.Event{UserID: "u1", Name: "B"}
	c := &model.Event{UserID: "u2", Name: "C"}
	for _, e := range []*model.Event{a, b, c} {
		if err := repo.Create(ctx, e); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	if err := repo.Merge(ctx, "u1", a.ID, a.ID); err == nil {
		t.Fatal("expected error merging into itself")
	}
	if err := repo.Merge(ctx, "u1", a.ID, c.ID); err == nil {
		t.Fatal("expected error merging across users")
	}
	if err := repo.Merge(ctx, "u1", a.ID, "missing"); err == nil {
		t.Fatal("expected error merging into missing event")
	}
}

func TestEventRepo_DeleteReturnsMemoriesToPool(t *testing.T) {
	repo, db := setupEventRepo(t)
	ctx := context.Background()

	e := &model.Event{UserID: "u1", Name: "日本行"}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	addMemory(t, db, "u1", "m1", "h1")
	addMemory(t, db, "u1", "m2", "h2")
	if err := repo.AssignMemories(ctx, "u1", e.ID, []string{"m1", "m2"}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	if err := repo.Delete(ctx, "u1", e.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// 记忆回普通池，不能变成隐形数据
	for _, id := range []string{"m1", "m2"} {
		if eid := memoryEventID(t, db, id); eid != nil {
			t.Fatalf("memory %s still attached: %v", id, eid)
		}
	}
	got, err := repo.GetByID(ctx, "u1", e.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if !got.IsDeleted {
		t.Fatal("expected soft-deleted event")
	}
	// 幂等
	if err := repo.Delete(ctx, "u1", e.ID); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestEventRepo_SetLastMemoryAtMonotonic(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	e := &model.Event{UserID: "u1", Name: "A"}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	newer := time.Now()
	older := newer.Add(-time.Hour)
	if err := repo.SetLastMemoryAt(ctx, "u1", e.ID, newer); err != nil {
		t.Fatalf("set newer: %v", err)
	}
	if err := repo.SetLastMemoryAt(ctx, "u1", e.ID, older); err != nil {
		t.Fatalf("set older: %v", err)
	}
	got, err := repo.GetByID(ctx, "u1", e.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.LastMemoryAt == nil || got.LastMemoryAt.Before(newer.Add(-time.Second)) {
		t.Fatalf("expected last_memory_at not to regress, got %v", got.LastMemoryAt)
	}
}
