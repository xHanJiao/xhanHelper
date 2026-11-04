package service

import (
	"context"
	"errors"
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

func loadMemory(t *testing.T, db *gorm.DB, id string) *model.UserMemory {
	t.Helper()
	var m model.UserMemory
	if err := db.Where("id = ?", id).First(&m).Error; err != nil {
		t.Fatalf("load memory %s: %v", id, err)
	}
	return &m
}

func memoryEventID(t *testing.T, db *gorm.DB, id string) *string {
	t.Helper()
	return loadMemory(t, db, id).EventID
}

func mustCreate(t *testing.T, repo *EventRepo, userID, name string) *model.Event {
	t.Helper()
	e := &model.Event{UserID: userID, Name: name}
	if err := repo.Create(context.Background(), e); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return e
}

func TestEventRepo_CreateAndGet(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	e := &model.Event{UserID: "u1", Name: "  回西安  ", Summary: "打包中"}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.ID == "" {
		t.Fatal("expected generated id")
	}
	if e.Name != "回西安" {
		t.Fatalf("expected trimmed name, got %q", e.Name)
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

func TestEventRepo_CreateGuards(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	if err := repo.Create(ctx, &model.Event{UserID: "", Name: "x"}); err == nil {
		t.Fatal("expected error on empty user_id")
	}
	if err := repo.Create(ctx, &model.Event{UserID: "u1", Name: "   "}); err == nil {
		t.Fatal("expected error on empty name")
	}
	long := ""
	for i := 0; i < 101; i++ {
		long += "字"
	}
	if err := repo.Create(ctx, &model.Event{UserID: "u1", Name: long}); err == nil {
		t.Fatal("expected error on over-long name")
	}
}

func TestEventRepo_ListExcludesDeletedAndPaginates(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	var all []*model.Event
	for _, name := range []string{"A", "B", "C"} {
		all = append(all, mustCreate(t, repo, "u1", name))
	}
	// 让 C 最新、A 最旧
	now := time.Now()
	if err := repo.SetLastMemoryAt(ctx, "u1", all[0].ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("set A: %v", err)
	}
	if err := repo.SetLastMemoryAt(ctx, "u1", all[1].ID, now.Add(-time.Hour)); err != nil {
		t.Fatalf("set B: %v", err)
	}
	if err := repo.SetLastMemoryAt(ctx, "u1", all[2].ID, now); err != nil {
		t.Fatalf("set C: %v", err)
	}

	if err := repo.Delete(ctx, "u1", all[1].ID); err != nil {
		t.Fatalf("delete B: %v", err)
	}

	list, err := repo.List(ctx, "u1", false, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Name != "C" || list[1].Name != "A" {
		t.Fatalf("expected [C A] sorted by last_memory_at, got %+v", list)
	}

	page, err := repo.List(ctx, "u1", false, 1, 1)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if len(page) != 1 || page[0].Name != "A" {
		t.Fatalf("expected second page [A], got %+v", page)
	}

	all2, err := repo.List(ctx, "u1", true, 10, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all2) != 3 {
		t.Fatalf("expected 3 including deleted, got %d", len(all2))
	}
}

func TestEventRepo_ListUpdatedSince(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	old := mustCreate(t, repo, "u1", "old")
	recent := mustCreate(t, repo, "u1", "recent")
	gone := mustCreate(t, repo, "u1", "gone")
	now := time.Now()
	for id, at := range map[string]time.Time{
		old.ID:    now.Add(-48 * time.Hour),
		recent.ID: now,
		gone.ID:   now,
	} {
		if err := repo.SetLastMemoryAt(ctx, "u1", id, at); err != nil {
			t.Fatalf("set: %v", err)
		}
	}
	if err := repo.Delete(ctx, "u1", gone.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	list, err := repo.ListUpdatedSince(ctx, "u1", now.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("list since: %v", err)
	}
	if len(list) != 1 || list[0].Name != "recent" {
		t.Fatalf("expected only recent, got %+v", list)
	}
}

func TestEventRepo_GetByIDs(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	a := mustCreate(t, repo, "u1", "A")
	b := mustCreate(t, repo, "u1", "B")
	other := mustCreate(t, repo, "u2", "X")

	m, err := repo.GetByIDs(ctx, "u1", []string{a.ID, b.ID, other.ID, "missing"})
	if err != nil {
		t.Fatalf("get by ids: %v", err)
	}
	if len(m) != 2 || m[a.ID] == nil || m[b.ID] == nil {
		t.Fatalf("unexpected map: %+v", m)
	}
}

func TestEventRepo_Merge(t *testing.T) {
	repo, db := setupEventRepo(t)
	ctx := context.Background()

	from := mustCreate(t, repo, "u1", "科三考试")
	to := mustCreate(t, repo, "u1", "驾照考取")
	addMemory(t, db, "u1", "m1", "h1")
	addMemory(t, db, "u1", "m2", "h2")
	addMemory(t, db, "u1", "m3", "h3")
	if err := repo.AssignMemories(ctx, "u1", from.ID, []string{"m1", "m2"}); err != nil {
		t.Fatalf("assign from: %v", err)
	}
	if err := repo.AssignMemories(ctx, "u1", to.ID, []string{"m3"}); err != nil {
		t.Fatalf("assign to: %v", err)
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

	for _, id := range []string{"m1", "m2", "m3"} {
		eid := memoryEventID(t, db, id)
		if eid == nil || *eid != to.ID {
			t.Fatalf("memory %s not moved to target: %v", id, eid)
		}
	}
	gotFrom, err := repo.GetByID(ctx, "u1", from.ID)
	if err != nil || gotFrom == nil {
		t.Fatalf("get from: %v %v", gotFrom, err)
	}
	if !gotFrom.IsDeleted {
		t.Fatal("expected source soft-deleted")
	}
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

	a := mustCreate(t, repo, "u1", "A")
	b := mustCreate(t, repo, "u1", "B")
	c := mustCreate(t, repo, "u2", "C")

	if err := repo.Merge(ctx, "u1", a.ID, a.ID); err == nil {
		t.Fatal("expected error merging into itself")
	}
	if err := repo.Merge(ctx, "u1", a.ID, c.ID); err == nil {
		t.Fatal("expected error merging across users")
	}
	if err := repo.Merge(ctx, "u1", a.ID, "missing"); err == nil {
		t.Fatal("expected error merging into missing event")
	}
	if err := repo.Delete(ctx, "u1", b.ID); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	if err := repo.Merge(ctx, "u1", a.ID, b.ID); err == nil {
		t.Fatal("expected error merging into deleted event")
	}

	// 源已合并、目标后来被删：重试同一 merge 应幂等成功（from 判断在 to 之前）
	d := mustCreate(t, repo, "u1", "D")
	e := mustCreate(t, repo, "u1", "E")
	if err := repo.Merge(ctx, "u1", d.ID, e.ID); err != nil {
		t.Fatalf("merge D→E: %v", err)
	}
	if err := repo.Delete(ctx, "u1", e.ID); err != nil {
		t.Fatalf("delete E: %v", err)
	}
	if err := repo.Merge(ctx, "u1", d.ID, e.ID); err != nil {
		t.Fatalf("retry merged-from should be idempotent, got %v", err)
	}
}

func TestEventRepo_DeleteReturnsMemoriesToPool(t *testing.T) {
	repo, db := setupEventRepo(t)
	ctx := context.Background()

	e := mustCreate(t, repo, "u1", "日本行")
	addMemory(t, db, "u1", "m1", "h1")
	addMemory(t, db, "u1", "m2", "h2")
	if err := repo.AssignMemories(ctx, "u1", e.ID, []string{"m1", "m2"}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	if err := repo.Delete(ctx, "u1", e.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

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
	if err := repo.Delete(ctx, "u1", e.ID); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestEventRepo_DeleteMissing(t *testing.T) {
	repo, _ := setupEventRepo(t)
	if err := repo.Delete(context.Background(), "u1", "missing"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestEventRepo_AssignMemoriesGuards(t *testing.T) {
	repo, db := setupEventRepo(t)
	ctx := context.Background()

	e := mustCreate(t, repo, "u1", "A")
	otherUserEvent := mustCreate(t, repo, "u2", "X")
	addMemory(t, db, "u1", "m1", "h1")
	addMemory(t, db, "u2", "m2", "h2")

	if err := repo.AssignMemories(ctx, "u1", "", []string{"m1"}); err == nil {
		t.Fatal("expected error on empty event id")
	}
	if err := repo.AssignMemories(ctx, "u1", "missing", []string{"m1"}); err == nil {
		t.Fatal("expected error on missing event")
	}
	if err := repo.AssignMemories(ctx, "u1", otherUserEvent.ID, []string{"m1"}); err == nil {
		t.Fatal("expected error assigning to another user's event")
	}
	if err := repo.AssignMemories(ctx, "u1", e.ID, []string{"m2"}); err == nil {
		t.Fatal("expected error assigning another user's memory")
	}
	if err := repo.AssignMemories(ctx, "u1", e.ID, nil); err != nil {
		t.Fatalf("empty list should be no-op: %v", err)
	}
}

func TestEventRepo_AssignDoesNotBumpUpdatedAt(t *testing.T) {
	repo, db := setupEventRepo(t)
	ctx := context.Background()

	e := mustCreate(t, repo, "u1", "A")
	addMemory(t, db, "u1", "m1", "h1")
	old := time.Now().Add(-72 * time.Hour)
	if err := db.Model(&model.UserMemory{ID: "m1"}).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatalf("seed updated_at: %v", err)
	}

	if err := repo.AssignMemories(ctx, "u1", e.ID, []string{"m1"}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := loadMemory(t, db, "m1").UpdatedAt; !got.Equal(old) {
		t.Fatalf("assign must not bump updated_at: want %v, got %v", old, got)
	}

	// merge 搬移同样不能刷
	e2 := mustCreate(t, repo, "u1", "B")
	if err := repo.Merge(ctx, "u1", e.ID, e2.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := loadMemory(t, db, "m1").UpdatedAt; !got.Equal(old) {
		t.Fatalf("merge must not bump updated_at: want %v, got %v", old, got)
	}

	// delete 回池同样不能刷
	if err := repo.Delete(ctx, "u1", e2.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := loadMemory(t, db, "m1").UpdatedAt; !got.Equal(old) {
		t.Fatalf("delete must not bump updated_at: want %v, got %v", old, got)
	}
}

func TestEventRepo_CreateWithMemories(t *testing.T) {
	repo, db := setupEventRepo(t)
	ctx := context.Background()

	addMemory(t, db, "u1", "m1", "h1")
	addMemory(t, db, "u1", "m2", "h2")
	old := time.Now().Add(-10 * time.Hour)
	for _, id := range []string{"m1", "m2"} {
		if err := db.Model(&model.UserMemory{ID: id}).UpdateColumn("updated_at", old).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	e := &model.Event{UserID: "u1", Name: "回西安", Summary: "打包"}
	if err := repo.CreateWithMemories(ctx, e, []string{"m1", "m2", "m2"}); err != nil {
		t.Fatalf("create with memories: %v", err)
	}
	for _, id := range []string{"m1", "m2"} {
		eid := memoryEventID(t, db, id)
		if eid == nil || *eid != e.ID {
			t.Fatalf("memory %s not attached: %v", id, eid)
		}
	}
	got, err := repo.GetByID(ctx, "u1", e.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.LastMemoryAt == nil || !got.LastMemoryAt.Equal(old) {
		t.Fatalf("expected last_memory_at=%v, got %v", old, got.LastMemoryAt)
	}
}

func TestEventRepo_UpdateSummary(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	e := mustCreate(t, repo, "u1", "旧名")
	if err := repo.UpdateSummary(ctx, "u1", e.ID, "新名", "新摘要"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := repo.GetByID(ctx, "u1", e.ID)
	if got.Name != "新名" || got.Summary != "新摘要" {
		t.Fatalf("unexpected: %+v", got)
	}
	// 空 name / 空 summary = 保持原值
	if err := repo.UpdateSummary(ctx, "u1", e.ID, "", ""); err != nil {
		t.Fatalf("no-op update: %v", err)
	}
	got, _ = repo.GetByID(ctx, "u1", e.ID)
	if got.Name != "新名" || got.Summary != "新摘要" {
		t.Fatalf("no-op changed values: %+v", got)
	}
	// 已删事件更新 → 明确报错
	if err := repo.Delete(ctx, "u1", e.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.UpdateSummary(ctx, "u1", e.ID, "x", "y"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestEventRepo_SetLastMemoryAtMonotonic(t *testing.T) {
	repo, _ := setupEventRepo(t)
	ctx := context.Background()

	e := mustCreate(t, repo, "u1", "A")
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
