package activity_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

type evtRow struct {
	Type     string  `gorm:"column:type"`
	Actor    string  `gorm:"column:actor"`
	Trigger  string  `gorm:"column:trigger"`
	ParentID *string `gorm:"column:parent_event_id"`
	ConvID   *string `gorm:"column:conversation_id"`
	BizKey   *string `gorm:"column:biz_key"`
	Attrs    *string `gorm:"column:attrs"`
}

func setup(t *testing.T) (*gorm.DB, *activity.Store) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main db: %v", err)
	}
	if err := db.AutoMigrate(&model.Document{}, &model.DocumentAnnotation{}, &model.DocumentAnnotationReply{}, &model.DocumentShare{}, &model.UserMemory{}, &model.Bill{}, &model.Reminder{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	activity.RegisterChangeCallbacks(db, store)
	return db, store
}

func rows(t *testing.T, s *activity.Store, typ string) []evtRow {
	t.Helper()
	var r []evtRow
	if err := s.DB().Raw(`SELECT * FROM events WHERE type=? ORDER BY rowid`, typ).Scan(&r).Error; err != nil {
		t.Fatalf("scan %s: %v", typ, err)
	}
	return r
}

func attrs(t *testing.T, r evtRow) map[string]any {
	t.Helper()
	m := map[string]any{}
	if r.Attrs != nil {
		if err := json.Unmarshal([]byte(*r.Attrs), &m); err != nil {
			t.Fatalf("attrs: %v", err)
		}
	}
	return m
}

// 工具触发的文档增改删 + 恢复过滤 + size_delta。
func TestCallbackDocumentFlow(t *testing.T) {
	db, store := setup(t)
	repo := service.NewDocumentRepo(db)

	ctx := activity.WithTurn(context.Background(), activity.TurnCtx{TurnID: "t1", StartEventID: "s1", ConvID: "c1"})
	ctx = activity.WithToolCall(ctx, "tc1")

	if err := repo.Create(ctx, &model.Document{ID: "d1", UserID: "u1", Content: "hello"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Update(ctx, "d1", "u1", map[string]any{"content": "hello world"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if n, err := repo.Delete(ctx, "d1", "u1"); err != nil || n != 1 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	// 恢复：status → active，不发事件。
	if err := db.Model(&model.Document{}).Where("id = ?", "d1").Update("status", 0).Error; err != nil {
		t.Fatalf("restore: %v", err)
	}
	store.Flush()

	created, updated, deleted := rows(t, store, activity.TypeDocCreated), rows(t, store, activity.TypeDocUpdated), rows(t, store, activity.TypeDocDeleted)
	if len(created) != 1 || len(updated) != 1 || len(deleted) != 1 {
		t.Fatalf("counts c=%d u=%d d=%d, want 1/1/1（恢复不应再发）", len(created), len(updated), len(deleted))
	}
	c := created[0]
	if c.Actor != "assistant" || c.Trigger != "manual" || c.ParentID == nil || *c.ParentID != "tc1" || c.ConvID == nil || *c.ConvID != "c1" {
		t.Errorf("created row = %+v", c)
	}
	if c.BizKey == nil || *c.BizKey != "d1" || attrs(t, c)["size_delta"].(float64) != 5 {
		t.Errorf("created biz/attrs = %v %v", c.BizKey, attrs(t, c))
	}
	u := updated[0]
	if u.BizKey != nil || attrs(t, u)["doc_id"] != "d1" || attrs(t, u)["size_delta"].(float64) != 6 {
		t.Errorf("updated = %+v attrs=%v", u, attrs(t, u))
	}
	if attrs(t, deleted[0])["doc_id"] != "d1" {
		t.Errorf("deleted attrs = %v", attrs(t, deleted[0]))
	}
}

// 记忆四类变更 + confirm 过滤 + BatchDelete 逐条（不重复）。
func TestCallbackMemoryFlow(t *testing.T) {
	db, store := setup(t)
	repo := service.NewUserMemoryRepo(db)
	repo.SetActivity(store)

	ctx := activity.WithUserActor(context.Background())
	if err := repo.Create(ctx, &model.UserMemory{ID: "m1", UserID: "u1", MemoryKey: "k", ContentHash: "h1", MemoryValue: "v", Topic: "old"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Update(ctx, "m1", "new", "v2", nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := repo.SetTopicByID(ctx, "u1", "m1", "t2"); err != nil {
		t.Fatalf("retopic: %v", err)
	}
	// confirm 计数：不该发
	if err := repo.BatchUpdateConfirm(ctx, []string{"m1"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// 后台批量改名 → topic.cleaned
	if n, err := repo.RenameTopic(context.Background(), "u1", []string{"t2"}, "merged"); err != nil || n != 1 {
		t.Fatalf("rename = %d, %v", n, err)
	}
	// 批量删除 → 逐条 delete，且不因回调重复
	if err := repo.BatchDelete(ctx, []string{"m1"}, "u1"); err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	store.Flush()

	changed := rows(t, store, activity.TypeMemoryEntityChanged)
	var ops []string
	for _, r := range changed {
		ops = append(ops, attrs(t, r)["op"].(string))
	}
	want := []string{"create", "update", "retopic", "delete"}
	if len(ops) != len(want) {
		t.Fatalf("entity.changed ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("ops = %v, want %v", ops, want)
		}
	}
	if changed[0].Actor != "user" || changed[0].Trigger != "manual" || changed[0].BizKey == nil || *changed[0].BizKey != "m1" {
		t.Errorf("create row = %+v", changed[0])
	}
	cleaned := rows(t, store, activity.TypeMemoryTopicCleaned)
	if len(cleaned) != 1 || cleaned[0].Trigger != "batch" || attrs(t, cleaned[0])["count"].(float64) != 1 {
		t.Errorf("topic.cleaned = %+v %v", cleaned, cleaned[0])
	}
}

// 账单/提醒：create 记，无事件的更新吞掉。
func TestCallbackBillReminder(t *testing.T) {
	db, store := setup(t)

	if err := service.NewBillRepo(db).Create(context.Background(), &model.Bill{ID: "b1", UserID: "u1", Item: "x", Amount: 12.5, Category1: "food"}); err != nil {
		t.Fatalf("bill create: %v", err)
	}
	billRepo := service.NewBillRepo(db)
	if err := billRepo.UpdateFields(context.Background(), "b1", "u1", map[string]any{"amount": 20.0}); err != nil {
		t.Fatalf("bill update: %v", err)
	}
	if err := billRepo.SoftDelete(context.Background(), "b1"); err != nil {
		t.Fatalf("bill softdelete: %v", err)
	}
	if err := billRepo.Create(context.Background(), &model.Bill{ID: "b2", UserID: "u1", Item: "y", Amount: 1, Category1: "food"}); err != nil {
		t.Fatalf("bill create2: %v", err)
	}
	// 批量创建（切片）应逐行发
	if err := db.Create(&[]model.Bill{{ID: "b3", UserID: "u1", Category1: "food"}, {ID: "b4", UserID: "u1", Category1: "food"}}).Error; err != nil {
		t.Fatalf("bill slice create: %v", err)
	}

	remRepo := service.NewReminderRepo(db)
	if err := remRepo.Create(context.Background(), &model.Reminder{ID: "r1", UserID: "u1", Content: "c"}); err != nil {
		t.Fatalf("rem create: %v", err)
	}
	if err := remRepo.Ack(context.Background(), "u1", "r1", "msg1"); err != nil {
		t.Fatalf("rem ack: %v", err)
	}
	if err := remRepo.Cancel(context.Background(), "u1", "r1"); err != nil {
		t.Fatalf("rem cancel: %v", err)
	}
	store.Flush()

	bills := rows(t, store, activity.TypeBillCreated)
	if len(bills) != 4 {
		t.Fatalf("bill.created = %d, want 4（含切片两条）", len(bills))
	}
	if bills[0].BizKey == nil || *bills[0].BizKey != "b1" || attrs(t, bills[0])["amount"].(float64) != 12.5 {
		t.Errorf("bill row = %+v %v", bills[0], attrs(t, bills[0]))
	}
	if got := len(rows(t, store, activity.TypeReminderCreated)); got != 1 {
		t.Errorf("reminder.created = %d, want 1", got)
	}
	ack := rows(t, store, activity.TypeReminderAcknowledged)
	if len(ack) != 1 || attrs(t, ack[0])["reminder_id"] != "r1" {
		t.Errorf("reminder.acknowledged = %+v", ack)
	}
}

// 回收站物理清理（purge）是硬删 + 事务，必须抑制，不得再补一条 doc.deleted。
func TestCallbackPurgeNoEvent(t *testing.T) {
	db, store := setup(t)
	repo := service.NewDocumentRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, &model.Document{ID: "d1", UserID: "u1", Content: "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Delete(ctx, "d1", "u1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n, err := repo.PurgeAll(ctx, "u1"); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	store.Flush()

	if got := len(rows(t, store, activity.TypeDocDeleted)); got != 1 {
		t.Fatalf("doc.deleted = %d, want 1（purge 不应补发）", got)
	}
}

// 命中 0 行（wrong user / 已删 / 不存在）时不得发幻影事件。
func TestCallbackZeroRowsNoEvent(t *testing.T) {
	db, store := setup(t)
	ctx := context.Background()

	if err := service.NewDocumentRepo(db).Create(ctx, &model.Document{ID: "d1", UserID: "u1", Content: "x"}); err != nil {
		t.Fatalf("doc create: %v", err)
	}
	if n, err := service.NewDocumentRepo(db).Delete(ctx, "d1", "other"); err != nil || n != 0 {
		t.Fatalf("wrong-user doc delete = %d, %v", n, err)
	}

	if err := service.NewReminderRepo(db).Create(ctx, &model.Reminder{ID: "r1", UserID: "u1", Content: "c"}); err != nil {
		t.Fatalf("rem create: %v", err)
	}
	if err := service.NewReminderRepo(db).Ack(ctx, "u1", "missing", "m"); err != nil {
		t.Fatalf("ack missing: %v", err)
	}

	if err := service.NewUserMemoryRepo(db).Create(ctx, &model.UserMemory{ID: "m1", UserID: "u1", MemoryValue: "v"}); err != nil {
		t.Fatalf("mem create: %v", err)
	}
	if n, err := service.NewUserMemoryRepo(db).SetTopicByID(ctx, "u1", "missing", "t"); err != nil || n != 0 {
		t.Fatalf("retopic missing = %d, %v", n, err)
	}
	store.Flush()

	if n := len(rows(t, store, activity.TypeDocDeleted)); n != 0 {
		t.Errorf("doc.deleted = %d, want 0", n)
	}
	if n := len(rows(t, store, activity.TypeReminderAcknowledged)); n != 0 {
		t.Errorf("reminder.acknowledged = %d, want 0", n)
	}
	// memory 只应有 create 那条，没有 retopic。
	changed := rows(t, store, activity.TypeMemoryEntityChanged)
	if len(changed) != 1 || attrs(t, changed[0])["op"] != "create" {
		t.Errorf("memory.entity.changed = %+v, want 仅 create", changed)
	}
}

// 仅改 audience 也应发 doc.updated（size_delta 0）。
func TestCallbackDocumentAudienceUpdate(t *testing.T) {
	db, store := setup(t)
	repo := service.NewDocumentRepo(db)
	ctx := activity.WithTurn(context.Background(), activity.TurnCtx{TurnID: "t1", StartEventID: "s1", ConvID: "c1"})

	if err := repo.Create(ctx, &model.Document{ID: "d1", UserID: "u1", Content: "x", Audience: model.DocumentAudienceFinished}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Update(ctx, "d1", "u1", map[string]any{"audience": model.DocumentAudienceInternal}); err != nil {
		t.Fatalf("update: %v", err)
	}
	store.Flush()

	updated := rows(t, store, activity.TypeDocUpdated)
	if len(updated) != 1 {
		t.Fatalf("audience-only update should emit 1 doc.updated, got %d", len(updated))
	}
	if attrs(t, updated[0])["doc_id"] != "d1" || attrs(t, updated[0])["size_delta"].(float64) != 0 {
		t.Errorf("attrs = %v", attrs(t, updated[0]))
	}
}
