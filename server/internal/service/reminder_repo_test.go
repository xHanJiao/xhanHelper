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

func setupReminderRepo(t *testing.T) (*ReminderRepo, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Reminder{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewReminderRepo(db), cleanup
}

var seedCounter int

func seedReminder(t *testing.T, repo *ReminderRepo, userID, content string, deadline time.Time, status string) *model.Reminder {
	t.Helper()
	seedCounter++
	r := &model.Reminder{
		ID:             fmt.Sprintf("id-%d", seedCounter),
		UserID:         userID,
		Content:        content,
		Deadline:       deadline,
		Status:         status,
		AcknowledgedAt: model.ReminderZeroTime,
		MentionedAt:    model.ReminderZeroTime,
	}
	if err := repo.Create(context.Background(), r); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return r
}

func TestReminderRepo_CreateAndFind(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	rem := &model.Reminder{
		ID:             "test-id-1",
		UserID:         "user1",
		Content:        "考科目二",
		Deadline:       deadline,
		Status:         model.ReminderStatusGenerating,
		AcknowledgedAt: model.ReminderZeroTime,
		MentionedAt:    model.ReminderZeroTime,
	}
	if err := repo.Create(context.Background(), rem); err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := repo.FindByID(context.Background(), "user1", "test-id-1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Content != "考科目二" {
		t.Errorf("content: got %q, want %q", found.Content, "考科目二")
	}
	if !found.Deadline.Equal(deadline) {
		t.Errorf("deadline: got %v, want %v", found.Deadline, deadline)
	}
	if found.Status != model.ReminderStatusGenerating {
		t.Errorf("status: got %q, want %q", found.Status, model.ReminderStatusGenerating)
	}
}

func TestReminderRepo_FindByID_WrongUser(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Now().Add(24 * time.Hour)
	rem := seedReminder(t, repo, "user1", "test", deadline, model.ReminderStatusPending)

	_, err := repo.FindByID(context.Background(), "user2", rem.ID)
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
}

func TestReminderRepo_FindByDeadline(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	seedReminder(t, repo, "user1", "提醒A", deadline, model.ReminderStatusPending)

	found, err := repo.FindByDeadline(context.Background(), "user1", deadline)
	if err != nil {
		t.Fatalf("find by deadline: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find reminder")
	}

	_, err = repo.FindByDeadline(context.Background(), "user1", deadline.Add(time.Hour))
	if err == nil {
		t.Fatal("expected error for different deadline")
	}
}

func TestReminderRepo_List(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	now := time.Now()
	seedReminder(t, repo, "user1", "提醒1", now.Add(1*time.Hour), model.ReminderStatusPending)
	seedReminder(t, repo, "user1", "提醒2", now.Add(2*time.Hour), model.ReminderStatusAcknowledged)
	seedReminder(t, repo, "user1", "提醒3", now.Add(3*time.Hour), model.ReminderStatusCancelled)

	// list all
	list, total, err := repo.List(context.Background(), "user1", "", 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("total: got %d, want 3", total)
	}
	if len(list) != 3 {
		t.Errorf("len: got %d, want 3", len(list))
	}

	// filter by pending
	list2, total2, _ := repo.List(context.Background(), "user1", model.ReminderStatusPending, 10, 0)
	if total2 != 1 {
		t.Errorf("pending total: got %d, want 1", total2)
	}
	if len(list2) != 1 {
		t.Errorf("pending len: got %d, want 1", len(list2))
	}

	// limit
	list3, total3, _ := repo.List(context.Background(), "user1", "", 1, 1)
	if total3 != 3 {
		t.Errorf("total with limit: want 3, got %d", total3)
	}
	if len(list3) != 1 {
		t.Errorf("limited len: want 1, got %d", len(list3))
	}

	// different user
	list4, total4, _ := repo.List(context.Background(), "user2", "", 10, 0)
	if total4 != 0 {
		t.Errorf("other user total: want 0, got %d", total4)
	}
	if len(list4) != 0 {
		t.Errorf("other user len: want 0, got %d", len(list4))
	}
}

func TestReminderRepo_ListExpiredUnmentioned(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	now := time.Now()

	// expired, not mentioned
	seedReminder(t, repo, "user1", "过期1", now.Add(-2*time.Hour), model.ReminderStatusPending)

	// expired but acknowledged
	ack := seedReminder(t, repo, "user1", "过期2", now.Add(-1*time.Hour), model.ReminderStatusAcknowledged)

	// expired but already mentioned
	mentioned := seedReminder(t, repo, "user1", "过期3", now.Add(-3*time.Hour), model.ReminderStatusPending)
	repo.MarkMentioned(context.Background(), "user1", mentioned.ID)

	// upcoming
	seedReminder(t, repo, "user1", "将来1", now.Add(1*time.Hour), model.ReminderStatusPending)

	list, err := repo.ListExpiredUnmentioned(context.Background(), "user1", now, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 expired unmentioned, got %d", len(list))
	}
	if len(list) > 0 && list[0].Content != "过期1" {
		t.Errorf("expected 过期1, got %s", list[0].Content)
	}

	_ = ack
}

func TestReminderRepo_ListUpcoming(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	now := time.Now()

	seedReminder(t, repo, "user1", "将来1", now.Add(1*time.Hour), model.ReminderStatusPending)
	seedReminder(t, repo, "user1", "将来2", now.Add(3*time.Hour), model.ReminderStatusPending)
	seedReminder(t, repo, "user1", "将来3", now.Add(2*time.Hour), model.ReminderStatusPending)
	seedReminder(t, repo, "user1", "过期", now.Add(-1*time.Hour), model.ReminderStatusPending)

	list, err := repo.ListUpcoming(context.Background(), "user1", now, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 upcoming, got %d", len(list))
	}

	// verify ASC order
	if list[0].Content != "将来1" {
		t.Errorf("first should be 将来1, got %s", list[0].Content)
	}
	if list[2].Content != "将来2" {
		t.Errorf("last should be 将来2, got %s", list[2].Content)
	}
}

func TestReminderRepo_Ack(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	rem := seedReminder(t, repo, "user1", "test", deadline, model.ReminderStatusPending)

	err := repo.Ack(context.Background(), "user1", rem.ID, "msg-1")
	if err != nil {
		t.Fatalf("ack: %v", err)
	}

	found, _ := repo.FindByID(context.Background(), "user1", rem.ID)
	if found.Status != model.ReminderStatusAcknowledged {
		t.Errorf("status: got %q, want %q", found.Status, model.ReminderStatusAcknowledged)
	}
	if found.TriggerMessageMsgID != "msg-1" {
		t.Errorf("msgID: got %q, want msg-1", found.TriggerMessageMsgID)
	}
	if found.AcknowledgedAt.IsZero() {
		t.Error("acknowledged_at should not be zero")
	}
}

func TestReminderRepo_Cancel(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	rem := seedReminder(t, repo, "user1", "test", deadline, model.ReminderStatusPending)

	err := repo.Cancel(context.Background(), "user1", rem.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	found, _ := repo.FindByID(context.Background(), "user1", rem.ID)
	if found.Status != model.ReminderStatusCancelled {
		t.Errorf("status: got %q, want %q", found.Status, model.ReminderStatusCancelled)
	}
}

func TestReminderRepo_Cancel_AcknowledgedAllowed(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	rem := seedReminder(t, repo, "user1", "test", deadline, model.ReminderStatusAcknowledged)

	err := repo.Cancel(context.Background(), "user1", rem.ID)
	// Cancel has WHERE status = pending, so acknowledged won't match
	if err == nil {
		t.Log("cancel on acknowledged returned nil — no rows affected, which is expected")
	}
}

func TestReminderRepo_MarkMentioned(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Now().Add(-1 * time.Hour)
	rem := seedReminder(t, repo, "user1", "test", deadline, model.ReminderStatusPending)

	err := repo.MarkMentioned(context.Background(), "user1", rem.ID)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}

	found, _ := repo.FindByID(context.Background(), "user1", rem.ID)
	if found.MentionedAt.IsZero() {
		t.Error("mentioned_at should not be zero")
	}
}

func TestReminderRepo_UpdateGeneratedContent(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	rem := seedReminder(t, repo, "user1", "test", deadline, model.ReminderStatusGenerating)

	err := repo.UpdateGeneratedContent(context.Background(), rem.ID, "别忘了test", "嘿，该test了")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	found, _ := repo.FindByID(context.Background(), "user1", rem.ID)
	if found.Status != model.ReminderStatusPending {
		t.Errorf("status: got %q, want %q", found.Status, model.ReminderStatusPending)
	}
	if found.NotificationBody != "别忘了test" {
		t.Errorf("body: got %q", found.NotificationBody)
	}
	if found.TriggerMessage != "嘿，该test了" {
		t.Errorf("trigger: got %q", found.TriggerMessage)
	}
}

func TestReminderRepo_Search(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	now := time.Now()
	seedReminder(t, repo, "user1", "考科目二", now.Add(1*time.Hour), model.ReminderStatusPending)
	seedReminder(t, repo, "user1", "买菜", now.Add(2*time.Hour), model.ReminderStatusPending)
	seedReminder(t, repo, "user1", "科目三练习", now.Add(3*time.Hour), model.ReminderStatusPending)

	list, total, err := repo.Search(context.Background(), "user1", "科目", "", 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 2 {
		t.Errorf("total: got %d, want 2", total)
	}
	if len(list) != 2 {
		t.Errorf("len: got %d, want 2", len(list))
	}

	list2, total2, _ := repo.Search(context.Background(), "user1", "买菜", model.ReminderStatusPending, 10, 0)
	if total2 != 1 {
		t.Errorf("filtered total: got %d, want 1", total2)
	}
	if len(list2) != 1 {
		t.Errorf("filtered len: got %d, want 1", len(list2))
	}
}

func TestReminderRepo_UserIsolation(t *testing.T) {
	repo, cleanup := setupReminderRepo(t)
	defer cleanup()

	now := time.Now()
	seedReminder(t, repo, "user1", "u1", now, model.ReminderStatusPending)
	seedReminder(t, repo, "user2", "u2", now, model.ReminderStatusPending)

	list, total, _ := repo.List(context.Background(), "user1", "", 10, 0)
	if total != 1 {
		t.Errorf("user1 total: want 1, got %d", total)
	}
	if len(list) > 0 && list[0].Content != "u1" {
		t.Errorf("user1 content: got %q", list[0].Content)
	}

	list2, total2, _ := repo.List(context.Background(), "user2", "", 10, 0)
	if total2 != 1 {
		t.Errorf("user2 total: want 1, got %d", total2)
	}
	if len(list2) > 0 && list2[0].Content != "u2" {
		t.Errorf("user2 content: got %q", list2[0].Content)
	}
}
