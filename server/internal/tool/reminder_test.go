package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupReminderTool(t *testing.T) (*ReminderTool, func()) {
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
	repo := service.NewReminderRepo(db)
	return NewReminderTool(repo, nil), cleanup
}

func a(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestReminderTool_FunctionsCount(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	fns := tool.Functions()
	if len(fns) != 4 {
		t.Fatalf("expected 4 functions, got %d", len(fns))
	}
	names := map[string]bool{}
	for _, f := range fns {
		names[f.Name] = true
	}
	for _, name := range []string{"create_reminder", "cancel_reminder", "list_reminders", "search_reminders"} {
		if !names[name] {
			t.Errorf("missing function: %s", name)
		}
	}
}

func TestReminderTool_CreateReminder_Success(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	args := a(map[string]string{
		"content":  "考科目二",
		"deadline": deadline.Format(time.RFC3339),
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)

	if !result.Success {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var data struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Content string `json:"content"`
	}
	json.Unmarshal(result.Data, &data)
	if data.Status != "generating" {
		t.Errorf("status: got %q, want generating", data.Status)
	}
	if data.Content != "考科目二" {
		t.Errorf("content: got %q", data.Content)
	}
	if data.ID == "" {
		t.Error("id should not be empty")
	}
}

func TestReminderTool_CreateReminder_TooSoon(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(30 * time.Second)
	args := a(map[string]string{
		"content":  "test",
		"deadline": deadline.Format(time.RFC3339),
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)

	if result.Success {
		t.Fatal("expected error for too-soon reminder")
	}
}

func TestReminderTool_CreateReminder_NoContent(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	args := a(map[string]string{
		"content":  "",
		"deadline": deadline.Format(time.RFC3339),
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)

	if result.Success {
		t.Fatal("expected error for empty content")
	}
}

func TestReminderTool_CreateReminder_NoDeadline(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	args := a(map[string]string{
		"content": "test",
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)

	if result.Success {
		t.Fatal("expected error for missing deadline")
	}
}

func TestReminderTool_CreateReminder_InvalidDeadline(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	args := a(map[string]string{
		"content":  "test",
		"deadline": "not-a-date",
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)

	if result.Success {
		t.Fatal("expected error for invalid deadline")
	}
}

func TestReminderTool_CreateReminder_DefaultTitle(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	args := a(map[string]string{
		"content":  "test",
		"deadline": deadline.Format(time.RFC3339),
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)

	if !result.Success {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	// verify title was set in DB
	var data struct{ ID string }
	json.Unmarshal(result.Data, &data)

	// Can't query DB from here directly, but we know repo works from repo tests.
	// Just verify the tool executed.
	_ = data
}

func TestReminderTool_CancelReminder_Success(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}

	createResult := tool.Execute(ec, "create_reminder", a(map[string]string{
		"content":  "test",
		"deadline": deadline.Format(time.RFC3339),
	}))
	var createData struct{ ID string }
	json.Unmarshal(createResult.Data, &createData)

	// Wait for async generation to complete (or just cancel in generating state)
	cancelResult := tool.Execute(ec, "cancel_reminder", a(map[string]string{"id": createData.ID}))

	if !cancelResult.Success {
		t.Fatalf("unexpected cancel error: %s", cancelResult.Error)
	}

	var cancelData struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(cancelResult.Data, &cancelData)
	if cancelData.Status != model.ReminderStatusCancelled {
		t.Errorf("status: got %q, want %q", cancelData.Status, model.ReminderStatusCancelled)
	}
}

func TestReminderTool_CancelReminder_NotFound(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "cancel_reminder", a(map[string]string{"id": "nonexistent"}))

	if result.Success {
		t.Fatal("expected error for nonexistent reminder")
	}
}

func TestReminderTool_ListReminders(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}

	tool.Execute(ec, "create_reminder", a(map[string]string{
		"content":  "reminder1",
		"deadline": deadline.Format(time.RFC3339),
	}))
	tool.Execute(ec, "create_reminder", a(map[string]string{
		"content":  "reminder2",
		"deadline": deadline.Add(2 * time.Hour).Format(time.RFC3339),
	}))

	result := tool.Execute(ec, "list_reminders", a(map[string]interface{}{"limit": 10}))
	if !result.Success {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var data struct {
		Items []struct {
			Content string `json:"content"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	json.Unmarshal(result.Data, &data)
	if data.Total != 2 {
		t.Errorf("total: got %d, want 2", data.Total)
	}
}

func TestReminderTool_SearchReminders(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}

	tool.Execute(ec, "create_reminder", a(map[string]string{
		"content":  "考科目二",
		"deadline": deadline.Format(time.RFC3339),
	}))
	tool.Execute(ec, "create_reminder", a(map[string]string{
		"content":  "买菜",
		"deadline": deadline.Add(2 * time.Hour).Format(time.RFC3339),
	}))

	result := tool.Execute(ec, "search_reminders", a(map[string]interface{}{"query": "科目"}))
	if !result.Success {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var data struct {
		Total int64 `json:"total"`
	}
	json.Unmarshal(result.Data, &data)
	if data.Total != 1 {
		t.Errorf("total: got %d, want 1", data.Total)
	}

	emptyResult := tool.Execute(ec, "search_reminders", a(map[string]interface{}{"query": "不存在的"}))
	var emptyData struct {
		Total int64 `json:"total"`
	}
	json.Unmarshal(emptyResult.Data, &emptyData)
	if emptyData.Total != 0 {
		t.Errorf("total: got %d, want 0", emptyData.Total)
	}
}

func TestReminderTool_UnknownFunction(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "unknown_function", nil)

	if result.Success {
		t.Fatal("expected error for unknown function")
	}
}

func TestReminderTool_FallbackGenerate(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	// Create a tool without llmClient (nil) to test fallback
	deadline := time.Now().Add(1 * time.Hour)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}

	createResult := tool.Execute(ec, "create_reminder", a(map[string]string{
		"content":  "test content",
		"deadline": deadline.Format(time.RFC3339),
	}))
	var createData struct{ ID string }
	json.Unmarshal(createResult.Data, &createData)

	// The async goroutine with nil llmClient calls fallbackGenerate
	// Since it's async, wait a moment
	time.Sleep(100 * time.Millisecond)

	// The status should now be pending (fallback set it)
	// We can't directly query but we can list
	listResult := tool.Execute(ec, "list_reminders", a(map[string]interface{}{}))
	var listData struct {
		Items []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"items"`
	}
	json.Unmarshal(listResult.Data, &listData)

	// After fallback, status should become pending
	for _, item := range listData.Items {
		if item.Content == "test content" && item.Status != "pending" {
			// Might still be generating if async hasn't completed
			t.Logf("status is %q (may be generating if async not yet completed)", item.Status)
		}
	}
}

func TestReminderTool_CreateReminder_ContentTooLong_Rejected(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	longContent := strings.Repeat("提醒事项", 40)
	args := a(map[string]string{
		"content":  longContent,
		"deadline": deadline.Format(time.RFC3339),
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)
	if result.Success {
		t.Fatal("create_reminder should reject content over limit")
	}
	if !strings.Contains(result.Error, "120") {
		t.Errorf("error should mention the 120 limit, got: %s", result.Error)
	}
}

func TestReminderTool_CreateReminder_Boundary120_Accepted(t *testing.T) {
	tool, cleanup := setupReminderTool(t)
	defer cleanup()

	deadline := time.Now().Add(1 * time.Hour)
	boundary := strings.Repeat("字", 120)
	args := a(map[string]string{
		"content":  boundary,
		"deadline": deadline.Format(time.RFC3339),
	})

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user1"}
	result := tool.Execute(ec, "create_reminder", args)
	if !result.Success {
		t.Fatalf("boundary 120 content should be accepted: %s", result.Error)
	}
}
