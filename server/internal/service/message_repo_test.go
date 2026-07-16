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
	if err := db.AutoMigrate(&model.Message{}); err != nil {
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
	// 每轮 4 条消息 (user + a1 + tool + a2)，15 轮 = 60 条
	if len(seen) != 60 {
		t.Errorf("expected 60 unique messages (15 rounds × 4 msgs), got %d", len(seen))
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
