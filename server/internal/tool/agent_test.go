package tool

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupAgentTool(t *testing.T) *AgentTool {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Agent{}, &model.Conversation{}, &model.ContentVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	return NewAgentTool(service.NewAgentRepo(db), service.NewVersionRepo(db), service.NewConversationRepo(db))
}

func seedAgent(t *testing.T, at *AgentTool, id, userID, name, summary string) {
	t.Helper()
	if err := at.agentRepo.Create(context.Background(), &model.Agent{
		ID:           id,
		UserID:       userID,
		Name:         name,
		SystemPrompt: "you are " + name,
		Summary:      summary,
	}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func seedConversation(t *testing.T, at *AgentTool, id, userID string) {
	t.Helper()
	if err := at.convRepo.Create(context.Background(), &model.Conversation{
		ID:     id,
		UserID: userID,
		Extra:  "{}",
	}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
}

func TestBindAgent_Success(t *testing.T) {
	at := setupAgentTool(t)
	ctx := context.Background()
	userID := "user-1"
	seedAgent(t, at, "agent-1", userID, "记账助手", "帮你记账")
	seedConversation(t, at, "conv-1", userID)

	ec := &ExecuteContext{Ctx: ctx, UserID: userID, ConvID: "conv-1"}
	res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{"agent_id": "agent-1"}))
	if !res.Success {
		t.Fatalf("bind failed: %s", res.Error)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["agent_name"] != "记账助手" {
		t.Fatalf("agent_name = %v, want 记账助手", got["agent_name"])
	}

	conv, err := at.convRepo.FindByID(ctx, "conv-1")
	if err != nil {
		t.Fatalf("find conv: %v", err)
	}
	if conv.AgentID == nil || *conv.AgentID != "agent-1" {
		t.Fatalf("conv.AgentID = %v, want agent-1", conv.AgentID)
	}
}

func TestBindAgent_NoConvContext(t *testing.T) {
	at := setupAgentTool(t)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}
	res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{"agent_id": "agent-1"}))
	if res.Success {
		t.Fatal("expected failure without conversation context")
	}
	if res.Error != "no conversation context" {
		t.Fatalf("error = %q, want 'no conversation context'", res.Error)
	}
}

func TestBindAgent_MissingAgentID(t *testing.T) {
	at := setupAgentTool(t)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1", ConvID: "conv-1"}
	res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{}))
	if res.Success {
		t.Fatal("expected failure without agent_id")
	}
	if res.Error != "agent_id required" {
		t.Fatalf("error = %q, want 'agent_id required'", res.Error)
	}
}

func TestBindAgent_AgentNotFound(t *testing.T) {
	at := setupAgentTool(t)
	seedConversation(t, at, "conv-1", "user-1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1", ConvID: "conv-1"}
	res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{"agent_id": "nope"}))
	if res.Success {
		t.Fatal("expected failure for missing agent")
	}
	if res.Error != "agent not found" {
		t.Fatalf("error = %q, want 'agent not found'", res.Error)
	}
}

func TestBindAgent_CrossUser(t *testing.T) {
	at := setupAgentTool(t)
	seedAgent(t, at, "agent-a", "user-a", "A的助手", "")
	seedConversation(t, at, "conv-b", "user-b")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-b", ConvID: "conv-b"}
	res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{"agent_id": "agent-a"}))
	if res.Success {
		t.Fatal("expected failure binding another user's agent")
	}
	if res.Error != "agent not found" {
		t.Fatalf("error = %q, want 'agent not found'", res.Error)
	}
}

func TestBindAgent_Switch(t *testing.T) {
	at := setupAgentTool(t)
	ctx := context.Background()
	userID := "user-1"
	seedAgent(t, at, "agent-1", userID, "助手1", "")
	seedAgent(t, at, "agent-2", userID, "助手2", "")
	seedConversation(t, at, "conv-1", userID)
	ec := &ExecuteContext{Ctx: ctx, UserID: userID, ConvID: "conv-1"}

	if res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{"agent_id": "agent-1"})); !res.Success {
		t.Fatalf("bind agent-1 failed: %s", res.Error)
	}
	if res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{"agent_id": "agent-2"})); !res.Success {
		t.Fatalf("switch to agent-2 failed: %s", res.Error)
	}

	conv, err := at.convRepo.FindByID(ctx, "conv-1")
	if err != nil {
		t.Fatalf("find conv: %v", err)
	}
	if conv.AgentID == nil || *conv.AgentID != "agent-2" {
		t.Fatalf("conv.AgentID = %v, want agent-2 (switch should overwrite)", conv.AgentID)
	}
}

func TestUnbindAgent_Success(t *testing.T) {
	at := setupAgentTool(t)
	ctx := context.Background()
	userID := "user-1"
	seedAgent(t, at, "agent-1", userID, "助手", "")
	seedConversation(t, at, "conv-1", userID)
	ec := &ExecuteContext{Ctx: ctx, UserID: userID, ConvID: "conv-1"}

	if res := at.Execute(ec, "bind_agent", mustMarshal(t, map[string]string{"agent_id": "agent-1"})); !res.Success {
		t.Fatalf("bind failed: %s", res.Error)
	}
	if res := at.Execute(ec, "unbind_agent", mustMarshal(t, map[string]string{})); !res.Success {
		t.Fatalf("unbind failed: %s", res.Error)
	}

	conv, err := at.convRepo.FindByID(ctx, "conv-1")
	if err != nil {
		t.Fatalf("find conv: %v", err)
	}
	if conv.AgentID != nil {
		t.Fatalf("conv.AgentID = %v, want nil after unbind", *conv.AgentID)
	}
}

func TestUnbindAgent_NoConvContext(t *testing.T) {
	at := setupAgentTool(t)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}
	res := at.Execute(ec, "unbind_agent", mustMarshal(t, map[string]string{}))
	if res.Success {
		t.Fatal("expected failure without conversation context")
	}
	if res.Error != "no conversation context" {
		t.Fatalf("error = %q, want 'no conversation context'", res.Error)
	}
}

func TestListAgents_ReturnsSummary(t *testing.T) {
	at := setupAgentTool(t)
	userID := "user-1"
	seedAgent(t, at, "agent-1", userID, "记账助手", "帮你管理账单")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := at.Execute(ec, "list_agents", nil)
	if !res.Success {
		t.Fatalf("list failed: %s", res.Error)
	}
	var list []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].ID != "agent-1" || list[0].Name != "记账助手" || list[0].Summary != "帮你管理账单" {
		t.Fatalf("unexpected item: %+v", list[0])
	}
}
