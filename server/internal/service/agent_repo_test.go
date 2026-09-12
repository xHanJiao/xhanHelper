package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupAgentRepo(t *testing.T) (*AgentRepo, *ConversationRepo, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Agent{}, &model.Conversation{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewAgentRepo(db), NewConversationRepo(db), cleanup
}

func ptrStr(s string) *string { return &s }

func agentIDs(agents []model.Agent) []string {
	out := make([]string, len(agents))
	for i := range agents {
		out[i] = agents[i].ID
	}
	return out
}

// 智能体列表按绑定会话数（使用频率）倒序；软删会话与子会话不计入。
func TestAgentRepo_OrderByUsageFrequency(t *testing.T) {
	agentRepo, convRepo, cleanup := setupAgentRepo(t)
	defer cleanup()
	ctx := context.Background()

	for _, a := range []model.Agent{
		{ID: "a", UserID: "u1", Name: "A", SystemPrompt: "a"},
		{ID: "b", UserID: "u1", Name: "B", SystemPrompt: "b"},
		{ID: "c", UserID: "u1", Name: "C", SystemPrompt: "c"},
	} {
		ag := a
		if err := agentRepo.Create(ctx, &ag); err != nil {
			t.Fatalf("create agent: %v", err)
		}
	}

	convs := []model.Conversation{
		{ID: "c1", UserID: "u1", AgentID: ptrStr("b")},
		{ID: "c2", UserID: "u1", AgentID: ptrStr("b")},
		{ID: "c3", UserID: "u1", AgentID: ptrStr("a")},
		{ID: "c4", UserID: "u1", AgentID: ptrStr("a"), IsDeleted: true},       // 软删不计
		{ID: "c5", UserID: "u1", AgentID: ptrStr("c"), ParentID: ptrStr("p")}, // 子会话不计
		{ID: "c6", UserID: "u2", AgentID: ptrStr("b")},                        // 他人会话不计
	}
	for i := range convs {
		if err := convRepo.Create(ctx, &convs[i]); err != nil {
			t.Fatalf("create conv: %v", err)
		}
	}

	want := []string{"b", "a", "c"}
	assertOrder := func(name string, got []model.Agent) {
		if ids := agentIDs(got); len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
			t.Fatalf("%s: order = %v, want %v", name, ids, want)
		}
	}

	list, total, err := agentRepo.ListByUser(ctx, "u1", 10, 0)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if total != 3 {
		t.Fatalf("ListByUser total = %d, want 3", total)
	}
	assertOrder("ListByUser", list)

	dtos, err := agentRepo.ListByCursor(ctx, "u1", 0)
	if err != nil {
		t.Fatalf("ListByCursor: %v", err)
	}
	got := make([]model.Agent, len(dtos))
	for i, d := range dtos {
		got[i] = d.Agent
	}
	assertOrder("ListByCursor", got)
}
