package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// mockEmbedder returns a fixed dummy vector
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}
func (m *mockEmbedder) ModelName() string { return "mock" }

// mockVectorStore is a simple in-memory vector store
type mockVectorStore struct {
	entries map[string]mockEntry
}

type mockEntry struct {
	vec      []float64
	metadata map[string]string
}

func (vs *mockVectorStore) Upsert(id string, vec []float64, metadata map[string]string) {
	if vs.entries == nil {
		vs.entries = make(map[string]mockEntry)
	}
	vs.entries[id] = mockEntry{vec: vec, metadata: metadata}
}

func (vs *mockVectorStore) Search(query []float64, topK int, filter app.VectorFilter) []app.VectorMatch {
	var results []app.VectorMatch
	for id, e := range vs.entries {
		if filter != nil && !filter(id, e.vec, e.metadata) {
			continue
		}
		results = append(results, app.VectorMatch{ID: id, Score: 0.85})
		if len(results) >= topK {
			break
		}
	}
	return results
}

func (vs *mockVectorStore) GetVector(id string) ([]float64, bool) { return nil, false }
func (vs *mockVectorStore) Delete(id string)                      {}
func (vs *mockVectorStore) Size() int                             { return len(vs.entries) }

// Ensure mocks satisfy interfaces
var _ app.VectorStore = (*mockVectorStore)(nil)
var _ *llm.Embedder = nil // just for type reference

func setupMemorySearchTool(t *testing.T) (*MemorySearchTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	vs := &mockVectorStore{}
	memRepo := service.NewUserMemoryRepo(db)
	return NewMemorySearchTool(vs, memRepo, nil), func() {}
}

func seedMemories(t *testing.T, repo *service.UserMemoryRepo, userID string) {
	t.Helper()
	now := time.Now()
	ctx := context.Background()
	facts1 := "每周三晚上打球"
	facts2 := "后端开发，主要用 Go 和 Python"
	memories := []model.UserMemory{
		{ID: "mem-1", UserID: userID, MemoryValue: "用户喜欢吃辣的食物", Topic: "偏好", Tags: "food,spicy", Facts: &facts1, ContentHash: "h1", MemoryKey: "k1", ConfirmCount: 3, CreatedAt: now.Add(-72 * time.Hour)},
		{ID: "mem-2", UserID: userID, MemoryValue: "用户住在深圳南山区", Topic: "住址", Tags: "", ContentHash: "h2", MemoryKey: "k2", ConfirmCount: 1, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "mem-3", UserID: userID, MemoryValue: "用户是后端开发工程师", Topic: "职业", Tags: "career", Facts: &facts2, ContentHash: "h3", MemoryKey: "k3", ConfirmCount: 5, CreatedAt: now.Add(-36 * time.Hour)},
		{ID: "mem-4", UserID: userID, MemoryValue: "用户喜欢打羽毛球和游泳", Topic: "偏好", Tags: "sports", ContentHash: "h4", MemoryKey: "k4", ConfirmCount: 2, CreatedAt: now.Add(-24 * time.Hour)},
		{ID: "mem-5", UserID: userID, MemoryValue: "另一个用户的记忆不应出现", Topic: "偏好", ContentHash: "h5", MemoryKey: "k5", ConfirmCount: 1, CreatedAt: now.Add(-12 * time.Hour)},
		{ID: "mem-6", UserID: userID, MemoryValue: "SQL优化经验丰富", Topic: "技能", ContentHash: "h6", MemoryKey: "k6", ConfirmCount: 1, CreatedAt: now.Add(-6 * time.Hour)},
	}
	memories[4].UserID = "other-user" // mem-5 is for a different user
	for _, m := range memories {
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatalf("seed memory %s: %v", m.ID, err)
		}
	}
}

// ==================== Schema Tests ====================

func TestMemorySearchTool_Functions_Returns5(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	fns := tool.Functions()
	if len(fns) != 5 {
		t.Fatalf("expected 5 functions, got %d", len(fns))
	}
	names := make(map[string]bool)
	for _, f := range fns {
		names[f.Name] = true
	}
	for _, n := range []string{"semantic_search", "search_by_keyword", "count_memories", "get_by_ids", "list_topics"} {
		if !names[n] {
			t.Errorf("missing function: %s", n)
		}
	}
}

// ==================== Error Handling ====================

func TestMemorySearchTool_Errors(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	t.Run("semantic_search empty query", func(t *testing.T) {
		res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": ""}))
		if res.Success {
			t.Error("expected error for empty query")
		}
	})

	t.Run("search_by_keyword empty query", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": ""}))
		if res.Success {
			t.Error("expected error for empty query")
		}
	})

	t.Run("search_by_keyword bad cursor", func(t *testing.T) {
		res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "test", "cursor": "bad"}))
		if res.Success {
			t.Error("expected error for bad cursor")
		}
	})

	t.Run("get_by_ids empty", func(t *testing.T) {
		res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{"ids": []string{}}))
		if res.Success {
			t.Error("expected error for empty ids")
		}
	})

	t.Run("get_by_ids too many", func(t *testing.T) {
		ids := make([]string, 21)
		for i := range ids {
			ids[i] = "x"
		}
		res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{"ids": ids}))
		if res.Success {
			t.Error("expected error for >20 ids")
		}
	})

	t.Run("unknown function", func(t *testing.T) {
		res := tool.Execute(ec, "nope", mustMarshal(t, map[string]string{}))
		if res.Success {
			t.Error("expected error for unknown function")
		}
	})
}

// ==================== search_by_keyword Tests ====================

func TestMemSearchByKeyword_Basic(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "辣"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	memories := out["memories"].([]interface{})
	if len(memories) == 0 {
		t.Fatal("expected at least 1 result")
	}
	first := memories[0].(map[string]interface{})
	if first["id"] != "mem-1" {
		t.Errorf("expected mem-1, got %v", first["id"])
	}
}

func TestMemSearchByKeyword_MultiKeyword(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "辣 SQL"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	memories := out["memories"].([]interface{})
	if len(memories) < 2 {
		t.Fatalf("expected at least 2 results (OR match), got %d", len(memories))
	}
}

func TestMemSearchByKeyword_TopicFilter(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query": "用户",
		"topic": "偏好",
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	memories := out["memories"].([]interface{})
	for _, m := range memories {
		mm := m.(map[string]interface{})
		if mm["topic"] != "偏好" {
			t.Errorf("expected topic=偏好, got %v", mm["topic"])
		}
	}
}

func TestMemSearchByKeyword_NoMatch(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "外星人"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	memories := out["memories"].([]interface{})
	if len(memories) != 0 {
		t.Errorf("expected 0 results, got %d", len(memories))
	}
}

func TestMemSearchByKeyword_Cursor(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query": "用户",
		"limit": 2,
	}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var page1 map[string]interface{}
	json.Unmarshal(res.Data, &page1)
	msgs1 := page1["memories"].([]interface{})
	if len(msgs1) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs1))
	}
	if page1["has_more"] != true {
		t.Error("expected has_more=true")
	}
	cursor, ok := page1["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("expected next_cursor")
	}

	res = tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]interface{}{
		"query":  "用户",
		"limit":  2,
		"cursor": cursor,
	}))
	if !res.Success {
		t.Fatalf("page 2 failed: %s", res.Error)
	}
	var page2 map[string]interface{}
	json.Unmarshal(res.Data, &page2)
	msgs2 := page2["memories"].([]interface{})
	if len(msgs2) == 0 {
		t.Error("expected messages in page 2")
	}
	if len(msgs2) > 0 && len(msgs1) > 0 {
		if msgs1[0].(map[string]interface{})["id"] == msgs2[0].(map[string]interface{})["id"] {
			t.Error("page 1 and 2 should differ")
		}
	}
}

// ==================== count_memories Tests ====================

func TestCountMemories(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	t.Run("no filter", func(t *testing.T) {
		res := tool.Execute(ec, "count_memories", mustMarshal(t, map[string]interface{}{}))
		if !res.Success {
			t.Fatalf("count failed: %s", res.Error)
		}
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		if out["count"].(float64) < 5 {
			t.Errorf("expected at least 5, got %v", out["count"])
		}
	})

	t.Run("with keyword", func(t *testing.T) {
		res := tool.Execute(ec, "count_memories", mustMarshal(t, map[string]string{"query": "SQL"}))
		if !res.Success {
			t.Fatalf("count failed: %s", res.Error)
		}
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		if out["count"].(float64) < 1 {
			t.Errorf("expected at least 1, got %v", out["count"])
		}
	})

	t.Run("with topic", func(t *testing.T) {
		res := tool.Execute(ec, "count_memories", mustMarshal(t, map[string]string{"topic": "偏好"}))
		if !res.Success {
			t.Fatalf("count failed: %s", res.Error)
		}
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		if out["count"].(float64) < 2 {
			t.Errorf("expected at least 2 偏好, got %v", out["count"])
		}
	})
}

// ==================== get_by_ids Tests ====================

func TestMemGetByIDs(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	t.Run("basic", func(t *testing.T) {
		res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{
			"ids": []string{"mem-1", "mem-3"},
		}))
		if !res.Success {
			t.Fatalf("get_by_ids failed: %s", res.Error)
		}
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		memories := out["memories"].([]interface{})
		if len(memories) != 2 {
			t.Errorf("expected 2, got %d", len(memories))
		}
	})

	t.Run("skip not found", func(t *testing.T) {
		res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{
			"ids": []string{"mem-1", "nonexistent"},
		}))
		if !res.Success {
			t.Fatalf("get_by_ids failed: %s", res.Error)
		}
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		memories := out["memories"].([]interface{})
		if len(memories) != 1 {
			t.Errorf("expected 1 (skip not-found), got %d", len(memories))
		}
	})

	t.Run("skip other user", func(t *testing.T) {
		res := tool.Execute(ec, "get_by_ids", mustMarshal(t, map[string]interface{}{
			"ids": []string{"mem-5"},
		}))
		if !res.Success {
			t.Fatalf("get_by_ids failed: %s", res.Error)
		}
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		memories := out["memories"].([]interface{})
		if len(memories) != 0 {
			t.Error("should not return other user's memory")
		}
	})
}

// ==================== list_topics Tests ====================

func TestListTopics(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "list_topics", mustMarshal(t, map[string]interface{}{}))
	if !res.Success {
		t.Fatalf("list_topics failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	topics := out["topics"].([]interface{})
	if len(topics) < 3 {
		t.Errorf("expected at least 3 topics, got %d", len(topics))
	}
	// First topic should be 偏好 (count=2), sorted DESC
	first := topics[0].(map[string]interface{})
	if first["topic"] != "偏好" {
		t.Errorf("expected first topic=偏好, got %v", first["topic"])
	}
	if first["count"].(float64) < 2 {
		t.Errorf("expected count>=2 for 偏好, got %v", first["count"])
	}
}

// ==================== User Isolation ====================

func TestMemOtherUserIsolation(t *testing.T) {
	tool, cleanup := setupMemorySearchTool(t)
	defer cleanup()
	userID := "user-1"
	seedMemories(t, tool.memRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "other-user"}

	res := tool.Execute(ec, "search_by_keyword", mustMarshal(t, map[string]string{"query": "SQL"}))
	if !res.Success {
		t.Fatalf("search failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	memories := out["memories"].([]interface{})
	if len(memories) != 0 {
		t.Error("other user should see no memories")
	}
}
