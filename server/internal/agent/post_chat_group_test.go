package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ---- mock PostChatWorker ----

type mockPostChatWorker struct {
	name string
	// 记录被调用的次数和收到的快照
	calls    int
	sessions []*SessionSnapshot
	// 模拟失败
	fail bool
}

func (m *mockPostChatWorker) Name() string { return m.name }
func (m *mockPostChatWorker) Process(_ context.Context, sess *SessionSnapshot) error {
	m.calls++
	m.sessions = append(m.sessions, sess)
	if m.fail {
		return &mockErr{msg: m.name + " failed"}
	}
	return nil
}

type mockErr struct{ msg string }

func (e *mockErr) Error() string { return e.msg }

// fakeEphemeralPurge — 记录 BeginDestroy 调用，供 PostChatWorkerGroup 测试注入。
type fakeEphemeralPurge struct {
	ephemeral  bool
	beginCalls int
	lastConvID string
	lastUserID string
}

func (f *fakeEphemeralPurge) IsEphemeral(_ context.Context, _ string) (bool, error) {
	return f.ephemeral, nil
}

func (f *fakeEphemeralPurge) BeginDestroy(_ context.Context, convID, userID string) error {
	f.beginCalls++
	f.lastConvID = convID
	f.lastUserID = userID
	return nil
}

func (f *fakeEphemeralPurge) RunPurge(_ context.Context, _ string) error { return nil }

// ---- helpers ----

func setupTestMsgRepo(t *testing.T) (*service.MessageRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewMessageRepo(db)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return repo, cleanup
}

// insertTestMsg 插入一条消息，optionally 带 SessionStartMsgID。
func insertTestMsg(t *testing.T, repo *service.MessageRepo, id, convID, role, content string, at time.Time, sessionStart string) {
	t.Helper()
	msg := &model.Message{
		ID:             id,
		ConversationID: convID,
		UserID:         "user-0001",
		Role:           role,
		Content:        content,
		CreatedAt:      at,
		Status:         model.MsgStatusNormal,
	}
	if sessionStart != "" {
		SetSessionStart(msg, sessionStart)
	}
	if err := repo.Create(context.Background(), msg); err != nil {
		t.Fatalf("insert msg %s: %v", id, err)
	}
}

// ---- PostChatWorkerGroup tests ----

func TestPostChatGroup_NoSessionStart_Skip(t *testing.T) {
	repo, cleanup := setupTestMsgRepo(t)
	defer cleanup()

	// lastMsg 无 SessionStart 标记
	now := time.Now()
	insertTestMsg(t, repo, "msg-0001", "conv-0001", "user", "hello", now.Add(-2*time.Minute), "")
	insertTestMsg(t, repo, "msg-0002", "conv-0001", "user", "hi", now, "")

	w1 := &mockPostChatWorker{name: "w1"}
	group := NewPostChatWorkerGroup([]PostChatWorker{w1}, repo, &fakeEphemeralPurge{})

	err := group.Process(context.Background(), "conv-0001", "user-0001", "msg-0002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w1.calls != 0 {
		t.Errorf("expected 0 worker calls (no session start), got %d", w1.calls)
	}
}

func TestPostChatGroup_FetchesCorrectWindow(t *testing.T) {
	repo, cleanup := setupTestMsgRepo(t)
	defer cleanup()

	now := time.Now()
	// session 起始消息（m1 标记自己为起始）
	insertTestMsg(t, repo, "msg-0001", "conv-0001", "user", "start", now.Add(-10*time.Minute), "msg-0001")
	insertTestMsg(t, repo, "msg-0002", "conv-0001", "assistant", "好的", now.Add(-7*time.Minute), "msg-0001")
	insertTestMsg(t, repo, "msg-0003", "conv-0001", "user", "middle", now.Add(-5*time.Minute), "msg-0001")
	insertTestMsg(t, repo, "msg-0004", "conv-0001", "tool", "agent_browser_snapshot 大段HTML", now.Add(-4*time.Minute), "msg-0001")
	insertTestMsg(t, repo, "msg-0005", "conv-0001", "user", "end", now, "msg-0001")

	w1 := &mockPostChatWorker{name: "w1"}
	group := NewPostChatWorkerGroup([]PostChatWorker{w1}, repo, &fakeEphemeralPurge{})

	err := group.Process(context.Background(), "conv-0001", "user-0001", "msg-0005")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w1.calls != 1 {
		t.Fatalf("expected 1 worker call, got %d", w1.calls)
	}
	sess := w1.sessions[0]
	// 快照含 user + assistant；tool 消息被 ListChatMessagesBetween 在 SQL 层排除
	all := sess.GetAllMessages()
	if len(all) != 4 {
		t.Fatalf("expected 4 chat messages, got %d", len(all))
	}
	for _, m := range all {
		if m.Role == "tool" {
			t.Errorf("tool message should be excluded, got %s", m.ID)
		}
	}
	if all[0].ID != "msg-0001" || all[3].ID != "msg-0005" {
		t.Errorf("unexpected message order: first=%s last=%s", all[0].ID, all[3].ID)
	}
	// GetUserMessages 只回 user，行为与旧查询等价
	userMsgs := sess.GetUserMessages()
	if len(userMsgs) != 3 {
		t.Errorf("expected 3 user messages, got %d", len(userMsgs))
	}
	for _, m := range userMsgs {
		if m.Role != "user" {
			t.Errorf("GetUserMessages returned non-user role %q", m.Role)
		}
	}
}

func TestPostChatGroup_PartialWorkerFailure_Continues(t *testing.T) {
	repo, cleanup := setupTestMsgRepo(t)
	defer cleanup()

	now := time.Now()
	insertTestMsg(t, repo, "msg-0001", "conv-0001", "user", "start", now.Add(-10*time.Minute), "msg-0001")
	insertTestMsg(t, repo, "msg-0004", "conv-0001", "user", "end", now, "msg-0001")

	w1 := &mockPostChatWorker{name: "w1", fail: true}
	w2 := &mockPostChatWorker{name: "w2"}
	group := NewPostChatWorkerGroup([]PostChatWorker{w1, w2}, repo, &fakeEphemeralPurge{})

	err := group.Process(context.Background(), "conv-0001", "user-0001", "msg-0004")
	// worker 失败不回滚，group 不返回 error（仅消息拉取失败才返回 error）
	if err != nil {
		t.Fatalf("expected nil error (worker failure not propagated), got %v", err)
	}
	if w1.calls != 1 {
		t.Errorf("expected w1 called once, got %d", w1.calls)
	}
	if w2.calls != 1 {
		t.Errorf("expected w2 called once despite w1 failure, got %d", w2.calls)
	}
}

func TestPostChatGroup_AllWorkersCalled(t *testing.T) {
	repo, cleanup := setupTestMsgRepo(t)
	defer cleanup()

	now := time.Now()
	insertTestMsg(t, repo, "msg-0001", "conv-0001", "user", "start", now.Add(-10*time.Minute), "msg-0001")
	insertTestMsg(t, repo, "msg-0004", "conv-0001", "user", "end", now, "msg-0001")

	w1 := &mockPostChatWorker{name: "summary"}
	w2 := &mockPostChatWorker{name: "memory"}
	w3 := &mockPostChatWorker{name: "state"}
	group := NewPostChatWorkerGroup([]PostChatWorker{w1, w2, w3}, repo, &fakeEphemeralPurge{})

	if err := group.Process(context.Background(), "conv-0001", "user-0001", "msg-0004"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w1.calls != 1 || w2.calls != 1 || w3.calls != 1 {
		t.Errorf("expected all 3 workers called, got summary=%d memory=%d state=%d", w1.calls, w2.calls, w3.calls)
	}
}

func TestPostChatGroup_LastMsgNotFound(t *testing.T) {
	repo, cleanup := setupTestMsgRepo(t)
	defer cleanup()

	w1 := &mockPostChatWorker{name: "w1"}
	group := NewPostChatWorkerGroup([]PostChatWorker{w1}, repo, &fakeEphemeralPurge{})

	err := group.Process(context.Background(), "conv-0001", "user-0001", "nonexistent-id")
	if err != nil {
		t.Fatalf("expected nil error for missing lastMsg, got %v", err)
	}
	if w1.calls != 0 {
		t.Errorf("expected 0 worker calls, got %d", w1.calls)
	}
}

func TestPostChatGroup_Ephemeral_DestroysInsteadOfWorkers(t *testing.T) {
	repo, cleanup := setupTestMsgRepo(t)
	defer cleanup()

	now := time.Now()
	insertTestMsg(t, repo, "msg-0001", "conv-0001", "user", "start", now.Add(-10*time.Minute), "msg-0001")
	insertTestMsg(t, repo, "msg-0004", "conv-0001", "user", "end", now, "msg-0001")

	w1 := &mockPostChatWorker{name: "summary"}
	purge := &fakeEphemeralPurge{ephemeral: true}
	group := NewPostChatWorkerGroup([]PostChatWorker{w1}, repo, purge)

	if err := group.Process(context.Background(), "conv-0001", "user-0001", "msg-0004"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if purge.beginCalls != 1 {
		t.Errorf("expected BeginDestroy called once, got %d", purge.beginCalls)
	}
	if purge.lastConvID != "conv-0001" || purge.lastUserID != "user-0001" {
		t.Errorf("unexpected BeginDestroy args: conv=%s user=%s", purge.lastConvID, purge.lastUserID)
	}
	if w1.calls != 0 {
		t.Errorf("expected 0 worker calls for ephemeral conv, got %d", w1.calls)
	}
}

// ---- MemoryWorker tests ----

func TestMemoryWorker_SkipsWhenDisabled(t *testing.T) {
	// 构造一个不存在的 extractor（nil），但 memoryAutoExtract=false → 直接跳过不 panic
	worker := NewMemoryWorker(&MemoryWorkerConfig{
		Extractor:         nil,
		MemoryAutoExtract: false,
	})
	err := worker.Process(context.Background(), &SessionSnapshot{
		ConvID: "c", UserID: "u", msgs: []model.Message{{ID: "msg-0001", Role: "user"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---- StateWorker tests ----

func TestStateWorker_Name(t *testing.T) {
	if got := (&StateWorker{}).Name(); got != "state" {
		t.Errorf("expected 'state', got '%s'", got)
	}
}

func TestMemoryWorker_Name(t *testing.T) {
	if got := (&MemoryWorker{}).Name(); got != "memory" {
		t.Errorf("expected 'memory', got '%s'", got)
	}
}

// 编排侧：Process 必须把用户消息交给抽取器，assistant 的话不能进（幻觉来源）。
func TestMemoryWorker_SendsOnlyUserTurnsToExtractor(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"extracted":false}`}}}
	worker := NewMemoryWorker(&MemoryWorkerConfig{
		Extractor:         NewMemoryExtractor(&MemoryExtractorConfig{LLMClient: mock}),
		MemoryAutoExtract: true,
	})
	err := worker.Process(context.Background(), &SessionSnapshot{
		ConvID: "c", UserID: "u", msgs: []model.Message{
			{ID: "msg-0001", Role: "user", Content: "用哪个方案"},
			{ID: "msg-0002", Role: "assistant", Content: "建议方案B"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.reqs) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mock.reqs))
	}
	content := mock.reqs[0].Messages[0].Content
	if !strings.Contains(content, "用哪个方案") {
		t.Errorf("user turn must reach the extractor, got: %.400s", content)
	}
	if strings.Contains(content, "建议方案B") || strings.Contains(content, `role="assistant"`) {
		t.Errorf("assistant turn must not reach the extractor, got: %.400s", content)
	}
}

// ---- buildMsgBlock ----

func TestBuildMsgBlock_UserOnlyFormat(t *testing.T) {
	base := time.Date(2026, 8, 4, 14, 30, 0, 0, time.UTC)
	msgs := []model.Message{
		{ID: "msg-0001", Role: "user", Content: "查下北京天气", CreatedAt: base},
		{ID: "msg-0002", Role: "user", Content: "记得带伞", CreatedAt: base.Add(2 * time.Minute)},
	}
	out := buildMsgBlock(msgs)
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
	if containsStr(out, "查下北京天气") == false || containsStr(out, "记得带伞") == false {
		t.Errorf("expected both messages in output, got: %s", out)
	}
}

// containsStr 简单包含判断
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
