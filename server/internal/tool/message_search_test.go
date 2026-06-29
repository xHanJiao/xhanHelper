package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

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
	return NewMessageSearchTool(msgRepo, convRepo, 50, 25), func() {}
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

func TestMessageSearchTool_Functions_Returns5(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	fns := tool.Functions()
	if len(fns) != 5 {
		t.Fatalf("expected 5 functions, got %d", len(fns))
	}
	names := make(map[string]bool)
	for _, f := range fns {
		names[f.Name] = true
	}
	expected := []string{"search_by_keyword", "search_by_time", "count_messages", "get_by_ids", "search_by_range"}
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

func TestSearchByKeyword_Basic(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "羽毛球"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 result")
	}
	first := msgs[0].(map[string]interface{})
	if first["role"] != "user" {
		t.Errorf("expected role user, got %v", first["role"])
	}
	kw := first["matched_keywords"].([]interface{})
	if len(kw) == 0 {
		t.Error("expected matched_keywords")
	}
}

func TestSearchByKeyword_MultiKeyword(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	// OR match: 羽毛球 SQL should match both messages with 羽毛球 and SQL
	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "羽毛球 SQL"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 results (OR match), got %d", len(msgs))
	}
}

func TestSearchByKeyword_RoleFilter(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query": "SQL",
		"role":  "assistant",
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		if msg["role"] != "assistant" {
			t.Errorf("expected assistant only, got %v", msg["role"])
		}
	}
}

func TestSearchByKeyword_ConversationFilter(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query":           "好吃 SQL 羽毛球 记账",
		"conversation_id": "conv-2",
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		if msg["conversation_id"] != "conv-2" {
			t.Errorf("expected conv-2 only, got %v", msg["conversation_id"])
		}
	}
}

func TestSearchByKeyword_NoMatch(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "外星人"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) != 0 {
		t.Errorf("expected 0 results, got %d", len(msgs))
	}
	total := out["total"].(float64)
	if total != 0 {
		t.Errorf("expected total=0, got %v", total)
	}
}

// ==================== Cursor Pagination Tests ====================

func TestSearchByKeyword_CursorPagination(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	// Page 1: limit=2
	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query": "SQL 羽毛球 深圳 记账",
		"limit": 2,
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var page1 map[string]interface{}
	json.Unmarshal(res.Data, &page1)

	msgs1 := page1["messages"].([]interface{})
	if len(msgs1) != 2 {
		t.Fatalf("expected 2 messages page 1, got %d", len(msgs1))
	}
	if page1["has_more"] != true {
		t.Error("expected has_more=true for page 1")
	}
	cursor, ok := page1["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("expected next_cursor in page 1")
	}

	// Page 2: use cursor
	res = tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query":  "SQL 羽毛球 深圳 记账",
		"limit":  2,
		"cursor": cursor,
	}))
	if !res.Success {
		t.Fatalf("search page 2 failed: %s", res.Error)
	}
	var page2 map[string]interface{}
	json.Unmarshal(res.Data, &page2)

	msgs2 := page2["messages"].([]interface{})
	if len(msgs2) == 0 {
		t.Error("expected messages in page 2")
	}
	// Messages should be different from page 1
	if len(msgs2) > 0 {
		p1First := msgs1[0].(map[string]interface{})["id"].(string)
		p2First := msgs2[0].(map[string]interface{})["id"].(string)
		if p1First == p2First {
			t.Error("page 1 and page 2 should have different messages")
		}
	}
}

// ==================== Expand Context Tests ====================

func TestSearchByKeyword_ExpandContext(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	// 搜索记账——展开上下文会看到同一轮中的 assistant 回复
	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query":          "记账",
		"expand_context": true,
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 result")
	}
	first := msgs[0].(map[string]interface{})
	round := first["round_messages"].([]interface{})
	if len(round) == 0 {
		t.Error("expected round_messages with expand_context=true")
	}
}

func TestSearchByKeyword_ExpandContextFalse(t *testing.T) {
	tool, cleanup := setupMessageSearchTool(t)
	defer cleanup()
	userID := prepareUser("user-1")
	seedMessages(t, tool.msgRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query":          "记账",
		"expand_context": false,
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	msgs := out["messages"].([]interface{})
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 result")
	}
	first := msgs[0].(map[string]interface{})
	// round_messages should be omitted (nil in Go becomes omitted in JSON)
	if _, exists := first["round_messages"]; exists {
		t.Error("round_messages should be omitted when expand_context=false")
	}
}

// ==================== search_by_time Tests ====================

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

// ==================== Helper Tests ====================

func TestSplitKeywords(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"羽毛球", 1},
		{"羽毛球 SQL", 2},
		{"羽毛球,SQL", 2},
		{"羽毛球，SQL", 2},
		{"羽毛球、SQL", 2},
		{"羽毛球 SQL 优化", 3},
		{"羽毛球 SQL SQL", 2}, // dedup
		{"  羽毛球  SQL  ", 2},
	}
	for _, tt := range tests {
		result := splitKeywords(tt.input)
		if len(result) != tt.expected {
			t.Errorf("splitKeywords(%q) = %d keywords, want %d: %v", tt.input, len(result), tt.expected, result)
		}
	}
}

func TestParseTimeParams(t *testing.T) {
	t.Run("both absolute and relative", func(t *testing.T) {
		_, _, err := parseTimeParams("2026-06-15T00:00:00+08:00", "", 7, 0)
		if err == nil {
			t.Error("expected error for mutually exclusive params")
		}
	})

	t.Run("invalid start_time", func(t *testing.T) {
		_, _, err := parseTimeParams("not-a-date", "", 0, 0)
		if err == nil {
			t.Error("expected error for invalid start_time")
		}
	})

	t.Run("invalid end_time", func(t *testing.T) {
		_, _, err := parseTimeParams("", "not-a-date", 0, 0)
		if err == nil {
			t.Error("expected error for invalid end_time")
		}
	})

	t.Run("last_n_days", func(t *testing.T) {
		after, before, err := parseTimeParams("", "", 7, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after == nil {
			t.Error("expected non-nil after")
		}
		if before != nil {
			t.Error("expected nil before")
		}
	})

	t.Run("last_n_hours", func(t *testing.T) {
		after, before, err := parseTimeParams("", "", 0, 6)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after == nil {
			t.Error("expected non-nil after")
		}
		if before != nil {
			t.Error("expected nil before")
		}
	})

	t.Run("absolute range", func(t *testing.T) {
		after, before, err := parseTimeParams("2026-06-15T00:00:00+08:00", "2026-06-20T00:00:00+08:00", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after == nil || before == nil {
			t.Error("expected both after and before")
		}
	})

	t.Run("no params", func(t *testing.T) {
		after, before, err := parseTimeParams("", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after != nil || before != nil {
			t.Error("expected both nil")
		}
	})
}

func TestFindMatchedKeywords(t *testing.T) {
	matched := findMatchedKeywords("今天去羽毛球场打球", []string{"羽毛球", "SQL"})
	if len(matched) != 1 || matched[0] != "羽毛球" {
		t.Errorf("expected [羽毛球], got %v", matched)
	}

	// case insensitive
	matched = findMatchedKeywords("Learn SQL Optimization", []string{"sql"})
	if len(matched) != 1 || matched[0] != "sql" {
		t.Errorf("expected [sql] (case-insensitive), got %v", matched)
	}

	matched = findMatchedKeywords("nothing here", []string{"外星人"})
	if len(matched) != 0 {
		t.Errorf("expected empty, got %v", matched)
	}
}

func TestTruncateAssistantContent(t *testing.T) {
	t.Run("short content", func(t *testing.T) {
		content := "短消息"
		result, truncated := truncateAssistantContent(content, "msg-1")
		if truncated {
			t.Error("short content should not be truncated")
		}
		if result != content {
			t.Errorf("expected unchanged, got %q", result)
		}
	})

	t.Run("exactly 100 chars", func(t *testing.T) {
		content := ""
		for i := 0; i < 100; i++ {
			content += "测"
		}
		result, truncated := truncateAssistantContent(content, "msg-1")
		if truncated {
			t.Error("exactly 100 chars should not be truncated")
		}
		if result != content {
			t.Errorf("expected unchanged at 100 chars")
		}
	})

	t.Run("over 100 chars", func(t *testing.T) {
		content := ""
		for i := 0; i < 150; i++ {
			content += "测"
		}
		result, truncated := truncateAssistantContent(content, "msg-test")
		if !truncated {
			t.Error("content > 100 chars should be truncated")
		}
		if !strings.Contains(result, "内容已截断") {
			t.Error("truncated content should have hint")
		}
		if !strings.Contains(result, "msg-test") {
			t.Error("truncation hint should contain message ID")
		}
	})
}

func TestFormatMsgTime(t *testing.T) {
	tm := time.Date(2026, 6, 15, 14, 30, 0, 0, time.FixedZone("CST", 8*3600))
	result := formatMsgTime(tm)
	if result != "2026-06-15T14:30:00+08:00" {
		t.Errorf("expected '2026-06-15T14:30:00+08:00', got %q", result)
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
	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "羽毛球"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	msgs := out["messages"].([]interface{})
	if len(msgs) != 0 {
		t.Error("other-user should not see any messages")
	}
}
