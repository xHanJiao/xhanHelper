package agent

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 隐藏轮（接力等控制轮）：user/assistant/tool 三类消息都落库为 MsgStatusHidden，
// 对常规读取（历史列表/轮次/GetByID）完全不可见。
func TestHiddenRound_MessagesAreHidden(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewMessageRepo(db)
	orch := New(&OrchestratorConfig{MsgRepo: repo})
	ctx := context.Background()
	const conv, user = "c1", "u1"

	um := mkUserMsg(&RunRequest{ConversationID: conv, UserID: user, UserMessageContent: "接力提示词", HiddenRound: true})
	if um.Status != model.MsgStatusHidden {
		t.Fatalf("隐藏轮 user status=%d, want %d", um.Status, model.MsgStatusHidden)
	}
	if err := repo.Create(ctx, um); err != nil {
		t.Fatalf("create user: %v", err)
	}

	amID := orch.saveAssistantMsg(ctx, conv, user, llm.Message{Content: `{"reply":"接力文档标题"}`}, nil, true)
	if amID == "" {
		t.Fatal("assistant id empty")
	}
	orch.saveToolMsg(ctx, conv, user, "tc-1", `{"success":true,"data":{"id":"d1"}}`, true)

	nm := mkUserMsg(&RunRequest{ConversationID: conv, UserID: user, UserMessageContent: "普通消息"})
	if nm.Status != model.MsgStatusNormal {
		t.Fatalf("普通轮 user status=%d, want %d", nm.Status, model.MsgStatusNormal)
	}
	if err := repo.Create(ctx, nm); err != nil {
		t.Fatalf("create normal: %v", err)
	}

	var hidden int64
	db.Model(&model.Message{}).Where("conversation_id = ? AND status = ?", conv, model.MsgStatusHidden).Count(&hidden)
	if hidden != 3 {
		t.Errorf("隐藏消息数=%d, want 3 (user+assistant+tool)", hidden)
	}

	list, err := repo.ListByConversation(ctx, conv, 50)
	if err != nil {
		t.Fatalf("ListByConversation: %v", err)
	}
	if len(list) != 1 || list[0].Content != "普通消息" {
		t.Errorf("ListByConversation 应只返回 1 条普通消息, got %d", len(list))
	}

	rounds, err := repo.ListRecentRounds(ctx, conv, 10)
	if err != nil {
		t.Fatalf("ListRecentRounds: %v", err)
	}
	if len(rounds) != 1 {
		t.Errorf("ListRecentRounds 应只 1 轮, got %d", len(rounds))
	}

	// 隐藏轮的锚点消息不可查：历史检索/调试/后处理都拿不到
	if got, _ := repo.GetByID(ctx, um.ID); got != nil {
		t.Error("隐藏 user 消息不应被 GetByID 读到")
	}
	if got, _ := repo.GetByID(ctx, amID); got != nil {
		t.Error("隐藏 assistant 消息不应被 GetByID 读到")
	}
}

// 隐藏轮不触发后处理：锚点（隐藏 user 消息）读不到 → Process 直接跳过，worker 不被调用。
func TestHiddenRound_PostChatGroupSkips(t *testing.T) {
	repo, cleanup := setupTestMsgRepo(t)
	defer cleanup()
	ctx := context.Background()

	if err := repo.Create(ctx, &model.Message{
		ID: "h1", ConversationID: "conv-1", UserID: "user-0001",
		Role: "user", Content: "接力提示词", Status: model.MsgStatusHidden,
	}); err != nil {
		t.Fatalf("create hidden: %v", err)
	}

	w := &mockPostChatWorker{name: "w"}
	group := NewPostChatWorkerGroup([]PostChatWorker{w}, repo, &fakeEphemeralPurge{})
	if err := group.Process(ctx, "conv-1", "user-0001", "h1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.calls != 0 {
		t.Errorf("隐藏轮不应触发后处理, worker calls=%d", w.calls)
	}
}
