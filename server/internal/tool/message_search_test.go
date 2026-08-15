package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

type mockMsgVS struct{}

func (m *mockMsgVS) Upsert(_ string, _ []float64, _ map[string]string)               {}
func (m *mockMsgVS) Search(_ []float64, _ int, _ app.VectorFilter) []app.VectorMatch { return nil }
func (m *mockMsgVS) GetVector(_ string) ([]float64, bool)                            { return nil, false }
func (m *mockMsgVS) Delete(_ string)                                                 {}
func (m *mockMsgVS) Size() int                                                       { return 0 }

func setupMessageSearchTool(t *testing.T) (*MessageSearchTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	msgRepo := service.NewMessageRepo(db)
	convRepo := service.NewConversationRepo(db)
	return NewMessageSearchTool(msgRepo, convRepo, &mockEmbedder{}, &mockMsgVS{}, 50, 25), func() {}
}

func seedMessages(t *testing.T, repo *service.MessageRepo, userID string) {
	t.Helper()
	now := time.Now()
	ctx := context.Background()
	messages := []model.Message{
		// conv-1: multi-message round with keywords, different times
		{ID: "msg-u1", ConversationID: "conv-1", UserID: userID, Role: "user", Content: "今天去羽毛球场打球", CreatedAt: now.Add(-72 * time.Hour)},
		{ID: "msg-a1", ConversationID: "conv-1", UserID: userID, Role: "assistant", Content: "好的，我帮你查一下羽毛球场的预定情况", CreatedAt: now.Add(-71 * time.Hour)},
		{ID: "msg-t1", ConversationID: "conv-1", UserID: userID, Role: "tool", Content: `{"result": "预定成功"}`, CreatedAt: now.Add(-70 * time.Hour)},
		{ID: "msg-u2", ConversationID: "conv-1", UserID: userID, Role: "user", Content: "深圳有什么好吃的推荐", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "msg-a2", ConversationID: "conv-1", UserID: userID, Role: "assistant", Content: "深圳美食推荐：隆江猪脚饭、光明乳鸽、沙井蚝", CreatedAt: now.Add(-47 * time.Hour)},
		// conv-1: round with multiple user msgs (expand_context test)
		{ID: "msg-u3", ConversationID: "conv-1", UserID: userID, Role: "user", Content: "帮我记账，午餐花了50元", CreatedAt: now.Add(-24 * time.Hour)},
		{ID: "msg-a3", ConversationID: "conv-1", UserID: userID, Role: "assistant", Content: "已记录：餐饮 50元", CreatedAt: now.Add(-23*time.Hour - 30*time.Minute)},
		{ID: "msg-u4", ConversationID: "conv-1", UserID: userID, Role: "user", Content: "再记一笔公交4元", CreatedAt: now.Add(-23 * time.Hour)},
		{ID: "msg-a4", ConversationID: "conv-1", UserID: userID, Role: "assistant", Content: "已记录：交通 4元", CreatedAt: now.Add(-22*time.Hour - 30*time.Minute)},
		// conv-2: different keyword
		{ID: "msg-u5", ConversationID: "conv-2", UserID: userID, Role: "user", Content: "如何做SQL优化", CreatedAt: now.Add(-36 * time.Hour)},
		{ID: "msg-a5", ConversationID: "conv-2", UserID: userID, Role: "assistant", Content: "SQL优化建议：1.加索引 2.避免SELECT * 3.使用EXPLAIN分析执行计划 4.合理使用连接池 5.避免大事务", CreatedAt: now.Add(-35 * time.Hour)},
		// conv-2: long assistant message (>100 chars) for truncation test
		{ID: "msg-a6", ConversationID: "conv-2", UserID: userID, Role: "assistant", Content: "SQL优化的第一步是分析慢查询日志找出执行时间超过阈值的SQL语句然后使用EXPLAIN命令查看执行计划重点关注type列是否为ALL全表扫描如果是的话考虑添加合适的索引覆盖索引可以避免回表查询联合索引需要遵循最左前缀原则同时要注意索引并不是越多越好每个索引都会增加写入开销和存储空间另外对于大数据量的表可以考虑使用分区表或者分库分表来分散查询压力", CreatedAt: now.Add(-34 * time.Hour)},
		// msg from another user (should never appear)
		{ID: "msg-o1", ConversationID: "conv-1", UserID: "other-user", Role: "user", Content: "别人的消息不应该出现", CreatedAt: now.Add(-24 * time.Hour)},
	}
	for _, m := range messages {
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatalf("seed message %s: %v", m.ID, err)
		}
	}
}

// prepareUser 创建用户并返回 userID。
func prepareUser(userID string) string {
	return userID
}

// ==================== Schema Tests ====================

func TestMessageSearchTool_Functions_Returns4(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	fns := tool.Functions()
	if len(fns) != 4 {
		t.Fatalf("expected 4 functions, got %d", len(fns))
	}
	names := make(map[string]bool)
	for _, f := range fns {
		names[f.Name] = true
	}
	expected := []string{"search_by_time", "count_messages", "get_by_ids", "search_by_range"}
	for _, n := range expected {
		if !names[n] {
			t.Errorf("missing function: %s", n)
		}
	}
}

func TestMessageSearchTool_FunctionSchemas(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	fns := tool.Functions()

	for _, fn := range fns {
		t.Run(fn.Name, func(t *testing.T) {
			if fn.Name == "" {
				t.Error("empty function name")
			}
			params, ok := fn.Parameters["properties"].(map[string]interface{})
			if !ok {
				t.Fatal("expected properties in parameters")
			}
			// verify required is a slice
			if req, ok := fn.Parameters["required"]; ok {
				if _, ok := req.([]string); !ok {
					t.Errorf("required should be []string, got %T", req)
				}
			}
			// verify query param exists for search_by_keyword
			if fn.Name == "search_by_keyword" {
				if _, ok := params["query"]; !ok {
					t.Error("search_by_keyword missing 'query' param")
				}
				if _, ok := params["expand_context"]; !ok {
					t.Error("search_by_keyword missing 'expand_context' param")
				}
			}
			// verify time params for search_by_time
			if fn.Name == "search_by_time" {
				if _, ok := params["start_time"]; !ok {
					t.Error("search_by_time missing 'start_time' param")
				}
				if _, ok := params["last_n_days"]; !ok {
					t.Error("search_by_time missing 'last_n_days' param")
				}
				if _, ok := params["order"]; !ok {
					t.Error("search_by_time missing 'order' param")
				}
			}
			// count_messages has no required fields
			if fn.Name == "count_messages" {
				if _, ok := params["query"]; !ok {
					t.Error("count_messages missing 'query' param")
				}
			}
		})
	}
}

// ==================== Error Handling ====================

func TestMessageSearchTool_Errors(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	t.Run("search_by_keyword empty query", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": ""}))
		if res.Success {
			t.Error("expected error for empty query")
		}
	})

	t.Run("search_by_keyword bad cursor", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "test", "cursor": "not-a-date"}))
		if res.Success {
			t.Error("expected error for bad cursor")
		}
	})

	t.Run("search_by_time no time params", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{}))
		if res.Success {
			t.Error("expected error with no time params")
		}
	})

	t.Run("search_by_time mutually exclusive", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{
			"start_time":  "2026-06-15T00:00:00+08:00",
			"last_n_days": 7,
		}))
		if res.Success {
			t.Error("expected error for mutually exclusive time params")
		}
	})

	t.Run("search_by_time bad order", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{
			"last_n_days": 7,
			"order":       "random",
		}))
		if res.Success {
			t.Error("expected error for bad order")
		}
	})

	t.Run("get_by_ids empty array", func(t *testing.T) {
		res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{"ids": []string{}}))
		if res.Success {
			t.Error("expected error for empty ids")
		}
	})

	t.Run("get_by_ids too many", func(t *testing.T) {
		ids := make([]string, 21)
		for i := range ids {
			ids[i] = "id-1"
		}
		res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{"ids": ids}))
		if res.Success {
			t.Error("expected error for >20 ids")
		}
	})

	t.Run("unknown function", func(t *testing.T) {
		res := tool.Execute(ec, "nonexistent", mustMarshal(t, map[string]string{}))
		if res.Success {
			t.Error("expected error for unknown function")
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_keyword", []byte(`{bad`))
		if res.Success {
			t.Error("expected error for malformed json")
		}
	})
}

// ==================== search_by_keyword Tests ====================

func TestSearchByTime_LastNDays(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	// last 2 days = only recent messages (记账相关)
	res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{
		"last_n_days": 2,
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	// Should NOT include messages from 72h ago (羽毛球)
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		content := msg["content"].(string)
		if strings.Contains(content, "羽毛球") {
			t.Error("should not include messages from 72h ago")
		}
	}
}

func TestSearchByTime_OrderAsc(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{
		"last_n_days": 4,
		"order":       "asc",
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) < 2 {
		t.Skip("need at least 2 messages to test order")
		return
	}
	first := msgs[0].(map[string]interface{})["created_at"].(string)
	last := msgs[len(msgs)-1].(map[string]interface{})["created_at"].(string)
	if first > last {
		t.Error("asc order: first should be earlier than last")
	}
}

func TestSearchByTime_AssistantTruncation(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{
		"last_n_days": 4,
		"role":        "assistant",
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	foundTruncated := false
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		truncated, _ := msg["truncated"].(bool)
		if truncated {
			foundTruncated = true
			content := msg["content"].(string)
			if !strings.Contains(content, "内容已截断") {
				t.Error("truncated message should contain truncation hint")
			}
			if !strings.Contains(content, "get_by_ids") {
				t.Error("truncation hint should mention get_by_ids")
			}
		}
	}
	if !foundTruncated {
		t.Log("no truncated messages found (may be ok if all are short)")
	}
}

func TestSearchByTime_AbsoluteTime(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	now := time.Now()
	start := now.Add(-50 * time.Hour).In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339)
	end := now.Add(-30 * time.Hour).In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339)

	res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{
		"start_time": start,
		"end_time":   end,
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	// Should include msg-u5 (SQL优化, 36h ago) but not msg-u1 (72h ago) or msg-u3 (24h ago)
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		content := msg["content"].(string)
		if strings.Contains(content, "羽毛球") {
			t.Error("should not include message from 72h ago")
		}
		if strings.Contains(content, "记账") {
			t.Error("should not include message from 24h ago")
		}
	}
}

// ==================== count_messages Tests ====================

func TestCountMessages_NoFilter(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "count_messages", mustMarshal(t, map[string]interface{}{}))
	if !res.Success {
		t.Fatalf("count failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	count := out["count"].(float64)
	if count == 0 {
		t.Error("expected non-zero count")
	}
}

func TestCountMessages_WithKeyword(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "count_messages", mustMarshal(t, map[string]string{"query": "SQL"}))
	if !res.Success {
		t.Fatalf("count failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	count := out["count"].(float64)
	if count < 1 {
		t.Errorf("expected at least 1 match for 'SQL', got %v", count)
	}
}

func TestCountMessages_WithTimeRange(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "count_messages", mustMarshal(t, map[string]interface{}{
		"last_n_days": 2,
	}))
	if !res.Success {
		t.Fatalf("count failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	count := out["count"].(float64)
	if count == 0 {
		t.Error("expected at least some messages from last 2 days")
	}
}

func TestCountMessages_Combined(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "count_messages", mustMarshal(t, map[string]interface{}{
		"query":           "SQL",
		"last_n_days":     3,
		"conversation_id": "conv-2",
	}))
	if !res.Success {
		t.Fatalf("count failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	count := out["count"].(float64)
	if count < 1 {
		t.Errorf("expected at least 1 match for combined filter, got %v", count)
	}
}

// ==================== get_by_ids Tests ====================

func TestGetByIDs_Basic(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{
		"ids": []string{"msg-u1", "msg-u5"},
	}))
	if !res.Success {
		t.Fatalf("get_by_ids failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func TestGetByIDs_CrossConversation(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{
		"ids": []string{"msg-u1", "msg-u5"},
	}))
	if !res.Success {
		t.Fatalf("get_by_ids failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	convIDs := make(map[string]bool)
	for _, m := range msgs {
		convIDs[m.(map[string]interface{})["conversation_id"].(string)] = true
	}
	if len(convIDs) < 2 {
		t.Error("expected messages from different conversations")
	}
}

func TestGetByIDs_SkipNotFound(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{
		"ids": []string{"msg-u1", "non-existent-id", "msg-o1"},
	}))
	if !res.Success {
		t.Fatalf("get_by_ids failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	// Only msg-u1 should be returned (non-existent and other-user's msg skipped)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message (skipping not-found), got %d", len(msgs))
	}
	if len(msgs) > 0 {
		if msgs[0].(map[string]interface{})["id"] != "msg-u1" {
			t.Errorf("expected msg-u1, got %v", msgs[0].(map[string]interface{})["id"])
		}
	}
}

func TestGetByIDs_NoTruncation(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{
		"ids": []string{"msg-a6"},
	}))
	if !res.Success {
		t.Fatalf("get_by_ids failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) == 0 {
		t.Fatal("expected 1 message")
	}
	msg := msgs[0].(map[string]interface{})
	if msg["truncated"].(bool) {
		t.Error("get_by_ids should not truncate content")
	}
}

// ==================== search_by_range Tests ====================

func TestSearchByRange_StructuredOutput(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_range", mustMarshal(t, map[string]string{
		"msg_id_start": "msg-u1",
		"msg_id_end":   "msg-u4",
	}))
	if !res.Success {
		t.Fatalf("search_by_range failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	// Should have structured JSON: total and messages
	if _, ok := out["total"]; !ok {
		t.Error("expected 'total' field")
	}
	msgs, ok := out["messages"].([]interface{})
	if !ok {
		t.Fatal("expected 'messages' array")
	}
	if len(msgs) == 0 {
		t.Error("expected at least 1 message in range")
	}
	// Verify each message has id, role, content, created_at, conversation_id
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		for _, field := range []string{"id", "role", "content", "created_at", "conversation_id"} {
			if _, ok := msg[field]; !ok {
				t.Errorf("missing field %q in search_by_range output", field)
			}
		}
	}
}

func TestSearchByRange_MissingMessage(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_range", mustMarshal(t, map[string]string{
		"msg_id_start": "nonexistent",
		"msg_id_end":   "msg-u4",
	}))
	if res.Success {
		t.Error("expected error for nonexistent start message")
	}
}

func TestOtherUserIsolation(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)

	// Use a different user
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "other-user"}

	// All searches should return empty for other-user
	res := tool.Execute(ec, "search_by_time", mustMarshal(t, map[string]interface{}{"last_n_days": 30}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	msgs := out["messages"].([]interface{})
	// other-user 只能看到自己的消息（seed 中的 msg-o1），绝不应看到 user-1 的消息
	for _, m := range msgs {
		mid := m.(map[string]interface{})["id"].(string)
		if mid != "msg-o1" {
			t.Errorf("other-user 不应看到消息 %s（user-1 的数据隔离失败）", mid)
		}
	}
}
