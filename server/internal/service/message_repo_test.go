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

func setupMessageRepo(t *testing.T) (*MessageRepo, func()) {
	t.Helper()
	// 每个测试用独立的临时 SQLite 文件，避免共享内存 DB 的跨测试数据污染。
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Message{}, &model.Conversation{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewMessageRepo(db), cleanup
}

// seedTestRounds 创建 N 轮对话，每轮：1 user + 2 assistant + 1 internal tool + 1 final assistant。
// 返回创建的 user 消息列表（按时间正序）。
// baseTime 用于固定时间起点，每轮间隔 4 秒（每轮 5 条消息，充足间隔）。
// startIdx 用于避免多次调用时 ID 冲突。
func seedTestRounds(t *testing.T, repo *MessageRepo, convID, userID string, n int, baseTime time.Time, startIdx int) []model.Message {
	t.Helper()
	ctx := context.Background()
	var userMsgs []model.Message
	tick := baseTime

	for i := 0; i < n; i++ {
		idx := startIdx + i
		// user
		um := model.Message{
			ID:             fmt.Sprintf("u-%s-%02d", convID, idx),
			ConversationID: convID,
			UserID:         userID,
			Role:           "user",
			Content:        fmt.Sprintf("user message %d", idx),
			CreatedAt:      tick,
		}
		if err := repo.Create(ctx, &um); err != nil {
			t.Fatalf("seed user msg %d: %v", idx, err)
		}
		userMsgs = append(userMsgs, um)
		tick = tick.Add(time.Second)

		// assistant 1 (带 tool_call)
		tcJSON := fmt.Sprintf(`[{"tool":"note","function":"search","args":{"q":"test%d"}}]`, idx)
		am1 := model.Message{
			ID:             fmt.Sprintf("a1-%s-%02d", convID, idx),
			ConversationID: convID,
			UserID:         userID,
			Role:           "assistant",
			Content:        fmt.Sprintf("assistant thinking %d", idx),
			ToolCallsJSON:  &tcJSON,
			CreatedAt:      tick,
		}
		if err := repo.Create(ctx, &am1); err != nil {
			t.Fatalf("seed assistant1 msg %d: %v", idx, err)
		}
		tick = tick.Add(time.Second)

		// tool (IsInternal)
		tm := model.Message{
			ID:             fmt.Sprintf("t-%s-%02d", convID, idx),
			ConversationID: convID,
			UserID:         userID,
			Role:           "tool",
			Content:        fmt.Sprintf(`{"success":true,"data":"result%d"}`, idx),
			IsInternal:     true,
			CreatedAt:      tick,
		}
		if err := repo.Create(ctx, &tm); err != nil {
			t.Fatalf("seed tool msg %d: %v", idx, err)
		}
		tick = tick.Add(time.Second)

		// assistant 2 (final reply)
		am2 := model.Message{
			ID:             fmt.Sprintf("a2-%s-%02d", convID, idx),
			ConversationID: convID,
			UserID:         userID,
			Role:           "assistant",
			Content:        fmt.Sprintf(`{"reply":"here is result %d","tool_calls":[]}`, idx),
			CreatedAt:      tick,
		}
		if err := repo.Create(ctx, &am2); err != nil {
			t.Fatalf("seed assistant2 msg %d: %v", idx, err)
		}
		tick = tick.Add(time.Second)
	}
	return userMsgs
}

// —— Tests ——

func TestCursorByRounds_Normal(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedTestRounds(t, repo, "conv1", "user1", 10, base, 0)

	// 初始加载：取最近 5 轮
	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}

	// 统计 user 消息数（应 >= 5，因为每轮至少一条 user）
	userCount := 0
	for _, m := range result.Messages {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount < 5 {
		t.Errorf("expected at least 5 user messages in 5 rounds, got %d", userCount)
	}

	// cursors 应非空
	if result.OldestCursor == "" || result.NewestCursor == "" {
		t.Errorf("cursors should not be empty: oldest=%q newest=%q", result.OldestCursor, result.NewestCursor)
	}

	// 总共 10 轮，只取了 5 轮，应 has_more_before
	if !result.HasMoreBefore {
		t.Error("expected HasMoreBefore=true for 5 rounds out of 10")
	}
	// 取的是最近 5 轮，后面不应有更多消息
	if result.HasMoreAfter {
		t.Error("expected HasMoreAfter=false for most recent rounds")
	}
}

func TestCursorByRounds_AllAssistantToolBatch(t *testing.T) {
	// 模拟原 bug 场景：紧跟 user 消息后堆积大量 assistant+tool 消息
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// 先创建几轮正常对话
	seedTestRounds(t, repo, "conv1", "user1", 3, base, 0)

	// 最后一轮之后，再创建一轮超长的对话（20+ 条 assistant+tool）
	lastUserTime := base.Add(time.Duration(3*4) * time.Second) // 3 轮后
	longRoundUser := model.Message{
		ID:             "u-conv1-long",
		ConversationID: "conv1",
		UserID:         "user1",
		Role:           "user",
		Content:        "do a very complex task",
		CreatedAt:      lastUserTime,
	}
	if err := repo.Create(ctx, &longRoundUser); err != nil {
		t.Fatalf("seed long round user: %v", err)
	}
	tick := lastUserTime.Add(time.Second)

	// 创建 25 条 assistant+tool 消息紧跟 user 后（模拟大量工具调用）
	for i := 0; i < 25; i++ {
		role := "assistant"
		if i%2 == 1 {
			role = "tool"
		}
		isInternal := role == "tool"
		msg := model.Message{
			ID:             fmt.Sprintf("long-%02d", i),
			ConversationID: "conv1",
			UserID:         "user1",
			Role:           role,
			Content:        fmt.Sprintf(`{"reply":"step %d","tool_calls":[]}`, i),
			IsInternal:     isInternal,
			CreatedAt:      tick,
		}
		if err := repo.Create(ctx, &msg); err != nil {
			t.Fatalf("seed long msg %d: %v", i, err)
		}
		tick = tick.Add(time.Second)
	}

	// 再后面建 2 轮正常对话（使用高 startIdx 避免 ID 冲突）
	seedTestRounds(t, repo, "conv1", "user1", 2, tick, 100)

	// 现在用新方法取最近 4 轮 — 每批都应含 user 消息
	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	userCount := 0
	for _, m := range result.Messages {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount < 4 {
		t.Errorf("expected at least 4 user messages (one per round), got %d", userCount)
	}

	// 旧代码（ListByConversationCursor）取 20 条可能会全是 assistant/tool
	oldResult, err := repo.ListByConversationCursor(context.Background(), "conv1", "", "before", 20)
	if err != nil {
		t.Fatalf("old method error: %v", err)
	}
	oldUserCount := 0
	for _, m := range oldResult.Messages {
		if m.Role == "user" {
			oldUserCount++
		}
	}
	// 验证旧方法确实可能断流（20 条内可能只有 1 条 user 甚至 0 条）
	t.Logf("old method: %d messages, %d user; new method: %d messages, %d user",
		len(oldResult.Messages), oldUserCount, len(result.Messages), userCount)
}

func TestCursorByRounds_EmptyConv(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv-empty", "", "before", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("expected empty messages, got %d", len(result.Messages))
	}
}

func TestCursorByRounds_OnlyUserMessages(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// 只创建 user 消息，无 assistant/tool
	for i := 0; i < 10; i++ {
		msg := model.Message{
			ID:             fmt.Sprintf("u-only-%02d", i),
			ConversationID: "conv1",
			UserID:         "user1",
			Role:           "user",
			Content:        fmt.Sprintf("question %d", i),
			CreatedAt:      base.Add(time.Duration(i) * time.Second),
		}
		if err := repo.Create(ctx, &msg); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 每轮只有一条 user 消息，5 轮 = 5 条
	if len(result.Messages) != 5 {
		t.Errorf("expected 5 messages (5 user-only rounds), got %d", len(result.Messages))
	}
	for _, m := range result.Messages {
		if m.Role != "user" {
			t.Errorf("expected only user messages, got role=%s", m.Role)
		}
	}
}

func TestCursorByRounds_SingleLongRound(t *testing.T) {
	// 一轮里有 35 条 assistant/tool 消息 — 验证 buildRoundsFromUserMsgs Limit(30) 截断行为
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	um := model.Message{
		ID:             "u-single",
		ConversationID: "conv1",
		UserID:         "user1",
		Role:           "user",
		Content:        "big task",
		CreatedAt:      base,
	}
	if err := repo.Create(ctx, &um); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	tick := base.Add(time.Second)

	for i := 0; i < 35; i++ {
		role := "assistant"
		if i%2 == 1 {
			role = "tool"
		}
		isInternal := role == "tool"
		msg := model.Message{
			ID:             fmt.Sprintf("long-%02d", i),
			ConversationID: "conv1",
			UserID:         "user1",
			Role:           role,
			Content:        fmt.Sprintf("step %d", i),
			IsInternal:     isInternal,
			CreatedAt:      tick,
		}
		if err := repo.Create(ctx, &msg); err != nil {
			t.Fatalf("seed long msg %d: %v", i, err)
		}
		tick = tick.Add(time.Second)
	}

	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 至少应包含 user 消息
	hasUser := false
	for _, m := range result.Messages {
		if m.Role == "user" {
			hasUser = true
			break
		}
	}
	if !hasUser {
		t.Error("expected at least one user message in result")
	}
}

func TestCursorByRounds_CursorBefore(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	userMsgs := seedTestRounds(t, repo, "conv1", "user1", 10, base, 0)

	// 取第 8 轮 user 消息作为 cursor（index 7），翻页取更早的 3 轮
	// 这样前面有 7 轮（0-6），取 3 轮后还剩 4 轮更早
	cursorID := userMsgs[7].ID
	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", cursorID, "before", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 应返回 cursor 之前（更早）的 3 轮
	userCount := 0
	for _, m := range result.Messages {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount < 3 {
		t.Errorf("expected at least 3 user messages (3 rounds before cursor), got %d", userCount)
	}

	// 前面还有更早的轮（总共 7 轮在 cursor 前，取了 3，还有 4）
	if !result.HasMoreBefore {
		t.Error("expected HasMoreBefore=true (more older rounds exist)")
	}
}

func TestCursorByRounds_HasMoreFlags(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	_ = seedTestRounds(t, repo, "conv1", "user1", 5, base, 0)

	// 取全部 5 轮 — 总共就 5 轮，has_more_before 应为 false
	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasMoreBefore {
		t.Error("expected HasMoreBefore=false (all 5 rounds fetched)")
	}
	if result.HasMoreAfter {
		t.Error("expected HasMoreAfter=false (most recent rounds)")
	}
}

func TestCursorByRounds_RoundsLimit(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedTestRounds(t, repo, "conv1", "user1", 5, base, 0)

	// rounds=1
	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 1)
	if err != nil {
		t.Fatalf("rounds=1: %v", err)
	}
	userCount := 0
	for _, m := range result.Messages {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Errorf("rounds=1 should have 1 user, got %d", userCount)
	}

	// rounds=0 → 应被 clamp 到默认值 20（至少比 5 轮多，不报错）
	result2, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 0)
	if err != nil {
		t.Fatalf("rounds=0: %v", err)
	}
	if len(result2.Messages) == 0 {
		t.Error("rounds=0 should be clamped to default, expected messages")
	}

	// rounds=100 → 应被 clamp 到 50 以内
	result3, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 100)
	if err != nil {
		t.Fatalf("rounds=100: %v", err)
	}
	if len(result3.Messages) == 0 {
		t.Error("rounds=100 should be clamped, expected messages")
	}
}

func TestCursorByRounds_WithCleanMessages(t *testing.T) {
	// 验证 round-based 拉取的数据结构正确，确保给 cleanMessages 的输入有 user 边界。
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedTestRounds(t, repo, "conv1", "user1", 5, base, 0)

	result, err := repo.ListByConversationCursorByRounds(context.Background(), "conv1", "", "before", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasUser, hasAssistant := false, false
	for _, m := range result.Messages {
		switch m.Role {
		case "user":
			hasUser = true
		case "assistant":
			hasAssistant = true
		}
	}
	if !hasUser {
		t.Error("expected at least one user message in round-based fetch")
	}
	if !hasAssistant {
		t.Error("expected at least one assistant message (test data includes them)")
	}

	// cursors 应非空
	if result.OldestCursor == "" || result.NewestCursor == "" {
		t.Error("cursors should not be empty")
	}
}

func TestCursorByRounds_NoDuplicationAcrossPages(t *testing.T) {
	// 验证翻页加载不会出现消息重复：批次之间不应有重叠的 message ID
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedTestRounds(t, repo, "conv1", "user1", 15, base, 0)

	ctx := context.Background()

	// 第一页：最近 5 轮
	page1, err := repo.ListByConversationCursorByRounds(ctx, "conv1", "", "before", 5)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	seen := make(map[string]bool)
	for _, m := range page1.Messages {
		seen[m.ID] = true
	}
	t.Logf("page1: %d messages, oldestCursor=%s", len(page1.Messages), page1.OldestCursor)

	// 第二页：翻页取更早的 5 轮
	page2, err := repo.ListByConversationCursorByRounds(ctx, "conv1", page1.OldestCursor, "before", 5)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	for _, m := range page2.Messages {
		if seen[m.ID] {
			t.Errorf("duplicate message %s (role=%s content=%q) in page2", m.ID, m.Role, m.Content)
		}
		seen[m.ID] = true
	}
	t.Logf("page2: %d messages, oldestCursor=%s", len(page2.Messages), page2.OldestCursor)

	// 第三页：继续翻页
	page3, err := repo.ListByConversationCursorByRounds(ctx, "conv1", page2.OldestCursor, "before", 5)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	for _, m := range page3.Messages {
		if seen[m.ID] {
			t.Errorf("duplicate message %s (role=%s content=%q) in page3", m.ID, m.Role, m.Content)
		}
		seen[m.ID] = true
	}
	t.Logf("page3: %d messages, oldestCursor=%s", len(page3.Messages), page3.OldestCursor)

	// 总共 15 轮，3 页各 5 轮，应无遗漏、无重复
	// 每轮 3 条非 tool 消息 (user + a1 + a2；tool 被前端分页排除)，15 轮 = 45 条
	if len(seen) != 45 {
		t.Errorf("expected 45 unique messages (15 rounds × 3 non-tool msgs), got %d", len(seen))
	}
}

// ==================== ListUserMessagesBetween tests ====================

func seedUserMessages(t *testing.T, repo *MessageRepo, convID, userID string, times []time.Time) []model.Message {
	t.Helper()
	ctx := context.Background()
	var msgs []model.Message
	for i, ts := range times {
		m := model.Message{
			ID:             fmt.Sprintf("ub-%s-%02d", convID, i),
			ConversationID: convID,
			UserID:         userID,
			Role:           "user",
			Content:        fmt.Sprintf("between msg %d for %s", i, convID),
			CreatedAt:      ts,
		}
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatalf("seed between msg %d: %v", i, err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func TestListUserMessagesBetween_IncludesBoundaryMessages(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	times := []time.Time{
		base,                      // 0: 边界 after
		base.Add(1 * time.Second), // 1: 中间
		base.Add(2 * time.Second), // 2: 边界 before
	}
	allMsgs := seedUserMessages(t, repo, "conv1", "user1", times)

	// 闭区间应包含首尾
	msgs, err := repo.ListUserMessagesBetween(context.Background(), "conv1", times[0], times[2], 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (inclusive both ends), got %d", len(msgs))
	}
	for i, expectedID := range []string{allMsgs[0].ID, allMsgs[1].ID, allMsgs[2].ID} {
		if msgs[i].ID != expectedID {
			t.Errorf("expected msg[%d]=%s, got %s", i, expectedID, msgs[i].ID)
		}
	}
}

func TestListUserMessagesBetween_SingleMessageSession(t *testing.T) {
	// 单条消息 session: after == before → 应返回该消息自身
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	times := []time.Time{base}
	allMsgs := seedUserMessages(t, repo, "conv1", "user1", times)

	msgs, err := repo.ListUserMessagesBetween(context.Background(), "conv1", times[0], times[0], 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (self-inclusive), got %d", len(msgs))
	}
	if msgs[0].ID != allMsgs[0].ID {
		t.Errorf("expected msg %s, got %s", allMsgs[0].ID, msgs[0].ID)
	}
}

func TestListUserMessagesBetween_EmptyRange(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	seedUserMessages(t, repo, "conv1", "user1", []time.Time{base})

	// 查询时间范围在消息之前 → 空
	before := base.Add(-2 * time.Second)
	after := base.Add(-1 * time.Second)
	msgs, err := repo.ListUserMessagesBetween(context.Background(), "conv1", after, before, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for empty range, got %d", len(msgs))
	}
}

func TestListUserMessagesBetween_ExcludesOtherConversations(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	seedUserMessages(t, repo, "conv1", "user1", []time.Time{base, base.Add(time.Second)})
	seedUserMessages(t, repo, "conv2", "user1", []time.Time{base, base.Add(time.Second)})

	msgs, err := repo.ListUserMessagesBetween(context.Background(), "conv1", base, base.Add(2*time.Second), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages for conv1, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.ConversationID != "conv1" {
			t.Errorf("unexpected convID: %s", m.ConversationID)
		}
	}
}

func TestListUserMessagesBetween_OnlyUserRole(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	seedUserMessages(t, repo, "conv1", "user1", []time.Time{base, base.Add(time.Second)})

	// 插入一条 assistant 消息（不应被返回）
	assistantMsg := model.Message{
		ID:             "a-test-role",
		ConversationID: "conv1",
		UserID:         "user1",
		Role:           "assistant",
		Content:        "I am an assistant",
		CreatedAt:      base.Add(500 * time.Millisecond),
	}
	if err := repo.Create(context.Background(), &assistantMsg); err != nil {
		t.Fatalf("seed assistant: %v", err)
	}

	msgs, err := repo.ListUserMessagesBetween(context.Background(), "conv1", base, base.Add(2*time.Second), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 user messages (assistant filtered out), got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.Role != "user" {
			t.Errorf("expected role=user, got role=%s (id=%s)", m.Role, m.ID)
		}
	}
}

func TestListUserMessagesBetween_Limit(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	var times []time.Time
	for i := 0; i < 10; i++ {
		times = append(times, base.Add(time.Duration(i)*time.Second))
	}
	seedUserMessages(t, repo, "conv1", "user1", times)

	// limit=3 应只返回 3 条并按时间正序
	msgs, err := repo.ListUserMessagesBetween(context.Background(), "conv1", base, base.Add(10*time.Second), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (limit=3), got %d", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if !msgs[i].CreatedAt.After(msgs[i-1].CreatedAt) {
			t.Errorf("messages not ordered ASC at index %d", i)
		}
	}
}

func TestListUserMessagesBetween_SwappedBounds(t *testing.T) {
	// after > before → 结果应为空（时间窗无效）
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	seedUserMessages(t, repo, "conv1", "user1", []time.Time{base})

	msgs, err := repo.ListUserMessagesBetween(context.Background(), "conv1", base.Add(time.Second), base, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for swapped bounds, got %d", len(msgs))
	}
}

// —— 巨型工具轮场景（前端分页不截断 + LLM 路径单轮完整）——

// TestCursorByRounds_MassiveToolRoundsNoTruncation
// 回归：前端分页（messages-v2）此前拉全量消息（含 tool），巨型 ReAct 轮（150+ 条）
// 超过 Limit(rounds*30) 导致最新消息被截断（用户最后几条消息消失）。
// 修复：loadMessagesInRange 排除 role='tool' → 前端分页只返回 user+assistant，无截断。
func TestCursorByRounds_MassiveToolRoundsNoTruncation(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()
	convID := "conv-massive"
	userID := "u1"
	base := time.Now().Add(-time.Hour)

	// 5 轮正常
	seedTestRounds(t, repo, convID, userID, 5, base, 0)

	// 第 6 轮（最新）：1 user + 1 assistant(tool_calls) + 50 tool + 1 assistant 最终回复
	tick := base.Add(5 * 4 * time.Second)
	um := model.Message{ID: "u-massive-6", ConversationID: convID, UserID: userID, Role: "user", Content: "最新用户消息", CreatedAt: tick}
	if err := repo.Create(ctx, &um); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	tick = tick.Add(time.Second)
	tcJSON := `[{"tool":"note","function":"search","args":{"q":"x"}}]`
	if err := repo.Create(ctx, &model.Message{ID: "a-massive-6a", ConversationID: convID, UserID: userID, Role: "assistant", Content: "", ToolCallsJSON: &tcJSON, CreatedAt: tick}); err != nil {
		t.Fatalf("seed assistant a: %v", err)
	}
	for i := 0; i < 50; i++ {
		tick = tick.Add(50 * time.Millisecond)
		if err := repo.Create(ctx, &model.Message{ID: fmt.Sprintf("t-massive-6-%02d", i), ConversationID: convID, UserID: userID, Role: "tool", Content: `{"success":true}`, IsInternal: true, CreatedAt: tick}); err != nil {
			t.Fatalf("seed tool %d: %v", i, err)
		}
	}
	tick = tick.Add(time.Second)
	if err := repo.Create(ctx, &model.Message{ID: "a-massive-6b", ConversationID: convID, UserID: userID, Role: "assistant", Content: "最终回复", CreatedAt: tick}); err != nil {
		t.Fatalf("seed assistant final: %v", err)
	}

	// 前端分页：最近 7 轮（实际 6 轮）→ 应完整包含最新 user + 最终回复，且不含 tool
	res, err := repo.ListByConversationCursorByRounds(ctx, convID, "", "before", 7)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("不应为空")
	}
	// 不含 role=tool
	for _, m := range res.Messages {
		if m.Role == "tool" {
			t.Errorf("前端分页不应包含 tool 消息: %s", m.ID)
		}
	}
	// 最新 user 消息必须在列（修复前被截断）
	foundLatestUser := false
	foundFinalReply := false
	for _, m := range res.Messages {
		if m.ID == "u-massive-6" {
			foundLatestUser = true
		}
		if m.ID == "a-massive-6b" {
			foundFinalReply = true
		}
	}
	if !foundLatestUser {
		t.Error("最新 user 消息被截断（修复目标）")
	}
	if !foundFinalReply {
		t.Error("最终 assistant 回复被截断")
	}
	// 6 轮全量 user 都应在（u-…-00 ~ u-massive-6）
	userCount := 0
	for _, m := range res.Messages {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 6 {
		t.Errorf("应包含 6 条 user 消息, got %d", userCount)
	}
}

// TestBuildRoundsFromUserMsgs_LongToolRound
// 回归：LLM 上下文路径单轮 >30 条工具消息时，旧 Limit(30) 截断轮尾导致孤儿 tool_call。
// 修复：Limit(30) → 100。
func TestBuildRoundsFromUserMsgs_LongToolRound(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()
	convID := "conv-longround"
	userID := "u1"
	base := time.Now().Add(-time.Hour)

	// 第 1 轮：1 user + 40 tool（> 30）
	tick := base
	um := model.Message{ID: "u-lr-1", ConversationID: convID, UserID: userID, Role: "user", Content: "第一轮", CreatedAt: tick}
	if err := repo.Create(ctx, &um); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	tick = tick.Add(time.Second)
	tcJSON := `[{"tool":"note","function":"search","args":{"q":"x"}}]`
	if err := repo.Create(ctx, &model.Message{ID: "a-lr-1a", ConversationID: convID, UserID: userID, Role: "assistant", Content: "", ToolCallsJSON: &tcJSON, CreatedAt: tick}); err != nil {
		t.Fatalf("seed assistant a: %v", err)
	}
	for i := 0; i < 40; i++ {
		tick = tick.Add(50 * time.Millisecond)
		if err := repo.Create(ctx, &model.Message{ID: fmt.Sprintf("t-lr-1-%02d", i), ConversationID: convID, UserID: userID, Role: "tool", Content: `{"success":true}`, IsInternal: true, CreatedAt: tick}); err != nil {
			t.Fatalf("seed tool %d: %v", i, err)
		}
	}
	tick = tick.Add(time.Second)
	if err := repo.Create(ctx, &model.Message{ID: "a-lr-1b", ConversationID: convID, UserID: userID, Role: "assistant", Content: "第一轮回复", CreatedAt: tick}); err != nil {
		t.Fatalf("seed assistant final: %v", err)
	}

	// 第 2 轮：正常（user + assistant）
	tick = tick.Add(4 * time.Second)
	um2 := model.Message{ID: "u-lr-2", ConversationID: convID, UserID: userID, Role: "user", Content: "第二轮", CreatedAt: tick}
	if err := repo.Create(ctx, &um2); err != nil {
		t.Fatalf("seed user2: %v", err)
	}
	tick = tick.Add(time.Second)
	if err := repo.Create(ctx, &model.Message{ID: "a-lr-2", ConversationID: convID, UserID: userID, Role: "assistant", Content: "第二轮回复", CreatedAt: tick}); err != nil {
		t.Fatalf("seed assistant2: %v", err)
	}

	rounds, err := repo.ListRecentRounds(ctx, convID, 2)
	if err != nil {
		t.Fatalf("ListRecentRounds: %v", err)
	}
	if len(rounds) != 2 {
		t.Fatalf("应返回 2 轮, got %d", len(rounds))
	}
	// 第 1 轮应包含全部 40 条 tool（旧 Limit(30) 会截断）
	toolCount := 0
	for _, m := range rounds[0] {
		if m.Role == "tool" {
			toolCount++
		}
	}
	if toolCount != 40 {
		t.Errorf("第 1 轮 tool 消息应完整（40 条）, got %d（旧 Limit(30) 截断回归）", toolCount)
	}
}

// TestBuildRoundsFromUserMsgs_MassiveReActRound
// 回归：真实长 ReAct 循环在 DB 语义下属于同一轮（user 到下一个 user 之间），
// 一轮可达 150+ 条消息（46 轮循环 ≈ 1 user + 46 assistant + ~100 tool）。
// 此前 Limit(30/100) 会截断轮尾 → 孤儿 tool_call → 后续对话丢失该轮内容。
// 修复：完全去掉每轮 Limit（created_at 紧上界精确查询）。
func TestBuildRoundsFromUserMsgs_MassiveReActRound(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()
	convID := "conv-massive-react"
	userID := "u1"
	base := time.Now().Add(-time.Hour)

	// 第 1 轮：巨型 ReAct 轮（1 user + 50 assistant(tool_calls) + 100 tool + 1 final assistant）
	tick := base
	um := model.Message{ID: "u-mr-1", ConversationID: convID, UserID: userID, Role: "user", Content: "复杂任务", CreatedAt: tick}
	if err := repo.Create(ctx, &um); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	tick = tick.Add(time.Second)
	for i := 0; i < 50; i++ {
		tcJSON := fmt.Sprintf(`[{"tool":"note","function":"search","args":{"q":"x%d"}}]`, i)
		if err := repo.Create(ctx, &model.Message{ID: fmt.Sprintf("a-mr-1-%02d", i), ConversationID: convID, UserID: userID, Role: "assistant", Content: "", ToolCallsJSON: &tcJSON, CreatedAt: tick}); err != nil {
			t.Fatalf("seed assistant %d: %v", i, err)
		}
		tick = tick.Add(50 * time.Millisecond)
		for j := 0; j < 2; j++ {
			if err := repo.Create(ctx, &model.Message{ID: fmt.Sprintf("t-mr-1-%02d-%d", i, j), ConversationID: convID, UserID: userID, Role: "tool", Content: `{"success":true}`, IsInternal: true, CreatedAt: tick}); err != nil {
				t.Fatalf("seed tool %d-%d: %v", i, j, err)
			}
			tick = tick.Add(50 * time.Millisecond)
		}
	}
	tick = tick.Add(time.Second)
	if err := repo.Create(ctx, &model.Message{ID: "a-mr-1-final", ConversationID: convID, UserID: userID, Role: "assistant", Content: "最终回复", CreatedAt: tick}); err != nil {
		t.Fatalf("seed final: %v", err)
	}

	// 第 2 轮：正常（user + assistant）
	tick = tick.Add(4 * time.Second)
	um2 := model.Message{ID: "u-mr-2", ConversationID: convID, UserID: userID, Role: "user", Content: "第二轮", CreatedAt: tick}
	if err := repo.Create(ctx, &um2); err != nil {
		t.Fatalf("seed user2: %v", err)
	}
	tick = tick.Add(time.Second)
	if err := repo.Create(ctx, &model.Message{ID: "a-mr-2", ConversationID: convID, UserID: userID, Role: "assistant", Content: "第二轮回复", CreatedAt: tick}); err != nil {
		t.Fatalf("seed assistant2: %v", err)
	}

	// 断言：第 1 轮完整（1 user + 50 assistant + 100 tool + 1 final = 152 条），无截断
	rounds, err := repo.ListRecentRounds(ctx, convID, 2)
	if err != nil {
		t.Fatalf("ListRecentRounds: %v", err)
	}
	if len(rounds) != 2 {
		t.Fatalf("应返回 2 轮, got %d", len(rounds))
	}
	first := rounds[0]
	if len(first) != 152 {
		t.Fatalf("第 1 轮应完整 152 条, got %d（Limit 截断回归）", len(first))
	}
	// 轮尾完整性：最终回复必须是该轮最后一条（截断时轮尾丢失）
	if first[len(first)-1].ID != "a-mr-1-final" {
		t.Errorf("轮尾最终回复应保留, got %s", first[len(first)-1].ID)
	}
	// tool 数量完整
	toolCount := 0
	for _, m := range first {
		if m.Role == "tool" {
			toolCount++
		}
	}
	if toolCount != 100 {
		t.Errorf("第 1 轮 tool 应完整 100 条, got %d", toolCount)
	}
}

// TestCountRecent_ExcludesSubConversation — 子会话（subAgent）消息不参与用户活动统计
func TestCountRecent_ExcludesSubConversation(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()

	parentID := "parent-1"
	convRepo := NewConversationRepo(repo.db)
	now := time.Now()

	// 主会话 + 子会话
	convRepo.Create(ctx, &model.Conversation{ID: "conv-main", UserID: "u1"})
	convRepo.Create(ctx, &model.Conversation{ID: "conv-sub", UserID: "u1", ParentID: &parentID})

	for _, m := range []model.Message{
		{ID: "m-main-1", ConversationID: "conv-main", UserID: "u1", Role: "user", Content: "主会话消息", CreatedAt: now.Add(-time.Hour)},
		{ID: "m-sub-1", ConversationID: "conv-sub", UserID: "u1", Role: "user", Content: "子会话任务消息", IsSubAgent: true, CreatedAt: now.Add(-time.Hour)},
	} {
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	count, err := repo.CountRecent(ctx, "u1", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("CountRecent 应排除子会话消息, got %d", count)
	}
}

// TestSearchMessages_ExcludesSubConversation — message_search 聚合检索不返回子会话消息
func TestSearchMessages_ExcludesSubConversation(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()

	parentID := "parent-1"
	convRepo := NewConversationRepo(repo.db)
	convRepo.Create(ctx, &model.Conversation{ID: "conv-main", UserID: "u1"})
	convRepo.Create(ctx, &model.Conversation{ID: "conv-sub", UserID: "u1", ParentID: &parentID})

	for _, m := range []model.Message{
		{ID: "m-main-1", ConversationID: "conv-main", UserID: "u1", Role: "user", Content: "聊过上下文缓存优化", CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "m-sub-1", ConversationID: "conv-sub", UserID: "u1", Role: "user", Content: "子任务里也聊上下文缓存", IsSubAgent: true, CreatedAt: time.Now().Add(-time.Hour)},
	} {
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	// 关键词检索
	msgs, _, err := repo.SearchMessagesByCursor(ctx, "u1", []string{"缓存"}, "user", "", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m-main-1" {
		t.Errorf("关键词检索应只返回主会话消息, got %+v", msgs)
	}

	// 统计
	total, err := repo.CountMessages(ctx, "u1", []string{"缓存"}, "user", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("CountMessages 应排除子会话消息, got %d", total)
	}
}

// TestBatchUpdateDebugJSON — 批量异步刷盘 request_json
func TestBatchUpdateDebugJSON(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()

	for _, m := range []model.Message{
		{ID: "m1", ConversationID: "c1", UserID: "u1", Role: "assistant", Content: "a", CreatedAt: time.Now()},
		{ID: "m2", ConversationID: "c1", UserID: "u1", Role: "assistant", Content: "b", CreatedAt: time.Now()},
	} {
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	req1, req2 := `{"req":1}`, `{"req":2}`
	if err := repo.BatchUpdateDebugJSON(ctx, []DebugJSONUpdate{
		{MessageID: "m1", RequestJSON: &req1},
		{MessageID: "m2", RequestJSON: &req2},
	}); err != nil {
		t.Fatalf("BatchUpdateDebugJSON error: %v", err)
	}

	m1, err := repo.GetByID(ctx, "m1")
	if err != nil || m1 == nil || m1.RequestJSON == nil || *m1.RequestJSON != req1 {
		t.Errorf("m1 request_json 应为 %s, got %v", req1, m1.RequestJSON)
	}
	m2, _ := repo.GetByID(ctx, "m2")
	if m2.RequestJSON == nil || *m2.RequestJSON != req2 {
		t.Errorf("m2 request_json 应为 %s, got %v", req2, m2.RequestJSON)
	}
}

// TestBatchUpdateDebugJSON_Empty — 空批次直接返回
func TestBatchUpdateDebugJSON_Empty(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	if err := repo.BatchUpdateDebugJSON(context.Background(), nil); err != nil {
		t.Errorf("空批次应无错误, got %v", err)
	}
}

// TestClearDebugJSONBefore — 保留清理：只清空 cutoff 之前有 request_json 的行，分批生效
func TestClearDebugJSONBefore(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()

	req := `{"req":1}`
	oldMsg := model.Message{ID: "old", ConversationID: "c1", UserID: "u1", Role: "assistant",
		Content: "old", RequestJSON: &req, CreatedAt: now.Add(-48 * time.Hour)}
	newMsg := model.Message{ID: "new", ConversationID: "c1", UserID: "u1", Role: "assistant",
		Content: "new", RequestJSON: &req, CreatedAt: now.Add(-time.Hour)}
	noReq := model.Message{ID: "noreq", ConversationID: "c1", UserID: "u1", Role: "assistant",
		Content: "noreq", CreatedAt: now.Add(-48 * time.Hour)}
	for _, m := range []model.Message{oldMsg, newMsg, noReq} {
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	// 分批清理：limit=1 时一轮只能清 1 行
	cutoff := now.Add(-24 * time.Hour)
	n, err := repo.ClearDebugJSONBefore(ctx, cutoff, 1)
	if err != nil {
		t.Fatalf("ClearDebugJSONBefore error: %v", err)
	}
	if n != 1 {
		t.Errorf("第一轮应清 1 行, got %d", n)
	}
	n, err = repo.ClearDebugJSONBefore(ctx, cutoff, 1)
	if err != nil {
		t.Fatalf("ClearDebugJSONBefore error: %v", err)
	}
	if n != 0 {
		t.Errorf("第二轮应清 0 行（只剩 noReq 无 request_json 不计入）, got %d", n)
	}

	mOld, _ := repo.GetByID(ctx, "old")
	if mOld.RequestJSON != nil {
		t.Errorf("旧消息 request_json 应被清空")
	}
	mNew, _ := repo.GetByID(ctx, "new")
	if mNew.RequestJSON == nil || *mNew.RequestJSON != req {
		t.Errorf("新消息 request_json 应保留")
	}
	mNoReq, _ := repo.GetByID(ctx, "noreq")
	if mNoReq.RequestJSON != nil {
		t.Errorf("无 request_json 的行不应被改动")
	}
}

// 隐藏消息不参与游标 hasMore 边界判断（隐藏轮不进对话记录）。
func TestExistsBeforeAfter_IgnoresHidden(t *testing.T) {
	repo, cleanup := setupMessageRepo(t)
	defer cleanup()
	ctx := context.Background()
	const conv = "conv-hidden"

	base := time.Now()
	visible := &model.Message{ID: "v1", ConversationID: conv, UserID: "u1", Role: "user", Content: "可见", Status: model.MsgStatusNormal, CreatedAt: base}
	hidden := &model.Message{ID: "h1", ConversationID: conv, UserID: "u1", Role: "user", Content: "隐藏", Status: model.MsgStatusHidden, CreatedAt: base.Add(time.Second)}
	for _, m := range []*model.Message{visible, hidden} {
		if err := repo.Create(ctx, m); err != nil {
			t.Fatalf("create %s: %v", m.ID, err)
		}
	}

	if !repo.existsBefore(ctx, conv, base.Add(2*time.Second)) {
		t.Error("存在可见消息，hasBefore 应为 true")
	}
	// 可见消息之后只有隐藏消息 → 不应认为还有更多
	if repo.existsAfter(ctx, conv, base.Add(500*time.Millisecond)) {
		t.Error("可见消息之后仅有隐藏消息，hasAfter 应为 false")
	}
}
