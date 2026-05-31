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

func setupDocumentTool(t *testing.T) (*DocumentTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Document{}, &model.ContentVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	docRepo := service.NewDocumentRepo(db)
	versionRepo := service.NewVersionRepo(db)
	return NewDocumentTool(docRepo, versionRepo), func() {}
}

func TestDocumentGetAndUpdate(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ctx := context.Background()
	userID := "user-1"

	// create
	createArgs := mustMarshal(t, map[string]interface{}{
		"title": "Title", "content": "Hello World", "tags": "go",
	})
	ec := &ExecuteContext{Ctx: ctx, UserID: userID}
	res := tool.Execute(ec, "create_document", createArgs)
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	// get_document: 应该返回完整内容
	res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
	if !res.Success {
		t.Fatalf("get failed: %s", res.Error)
	}
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	if got["content"] != "Hello World" {
		t.Errorf("expected content 'Hello World', got %v", got["content"])
	}

	// update: 更新后应创建快照
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "content": "Updated Content",
	}))
	if !res.Success {
		t.Fatalf("update failed: %s", res.Error)
	}

	// 验证快照已创建
	res = tool.Execute(ec, "get_document_versions", mustMarshal(t, map[string]string{"id": docID}))
	if !res.Success {
		t.Fatalf("get versions failed: %s", res.Error)
	}
	var versResp map[string]interface{}
	json.Unmarshal(res.Data, &versResp)
	versions := versResp["versions"].([]interface{})
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	v1 := versions[0].(map[string]interface{})
	if v1["version"].(float64) != 1 {
		t.Errorf("expected version 1, got %v", v1["version"])
	}

	// 再更新一次，应该有2个版本
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "title": "New Title",
	}))
	if !res.Success {
		t.Fatalf("second update failed: %s", res.Error)
	}
	res = tool.Execute(ec, "get_document_versions", mustMarshal(t, map[string]string{"id": docID}))
	json.Unmarshal(res.Data, &versResp)
	versions = versResp["versions"].([]interface{})
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
}

func TestDocumentRollback(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ctx := context.Background()
	userID := "user-1"
	ec := &ExecuteContext{Ctx: ctx, UserID: userID}

	// create
	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
		"title": "v1", "content": "content v1", "tags": "a",
	}))
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	// update → 创建 v1 快照（内容是 content v1）
	tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "content": "content v2", "tags": "b",
	}))

	// rollback 到 version 1
	res = tool.Execute(ec, "rollback_document", mustMarshal(t, map[string]interface{}{
		"id": docID, "version": 1,
	}))
	if !res.Success {
		t.Fatalf("rollback failed: %s", res.Error)
	}

	// 验证内容已恢复
	res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	if got["content"] != "content v1" {
		t.Errorf("expected content 'content v1', got %v", got["content"])
	}
	if got["tags"] != "a" {
		t.Errorf("expected tags 'a', got %v", got["tags"])
	}
}

func TestDocumentNotFound(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": "nonexistent"}))
	if res.Success {
		t.Error("expected failure for nonexistent document")
	}

	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{"id": "nonexistent", "content": "x"}))
	if res.Success {
		t.Error("expected failure for nonexistent document")
	}

	res = tool.Execute(ec, "rollback_document", mustMarshal(t, map[string]interface{}{"id": "nonexistent", "version": 1}))
	if res.Success {
		t.Error("expected failure for nonexistent document")
	}
}

func TestDocumentCreateValidation(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 缺 title
	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{"content": "c"}))
	if res.Success {
		t.Error("expected failure without title")
	}

	// 缺 content
	res = tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{"title": "t"}))
	if res.Success {
		t.Error("expected failure without content")
	}
}

func TestDocumentListAndQuery(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 创建 3 篇文档
	for i, item := range []struct{ title, content, tags string }{
		{"项目管理笔记", "记录了一些项目管理的方法论和实践经验", "项目管理,笔记"},
		{"Go 后端设计", "后端架构设计文档，包括 API 规范", "技术,Go,后端"},
		{"周报 2026-05", "本周完成了默认会话和历史检索两个大功能", "周报"},
	} {
		res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
			"title": item.title, "content": item.content, "tags": item.tags,
		}))
		if !res.Success {
			t.Fatalf("create doc %d failed: %s", i, res.Error)
		}
	}

	t.Run("list_documents", func(t *testing.T) {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{}))
		if !res.Success {
			t.Fatalf("list_documents failed: %s", res.Error)
		}
		var resp struct {
			Documents []struct {
				ID      string `json:"id"`
				Title   string `json:"title"`
				Tags    string `json:"tags"`
				Preview string `json:"preview"`
				Total   int    `json:"-"`
			} `json:"documents"`
			Total int `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 3 {
			t.Errorf("expected total 3, got %d", resp.Total)
		}
		if len(resp.Documents) != 3 {
			t.Fatalf("expected 3 documents, got %d", len(resp.Documents))
		}
		// preview 应截断在 50 字以内
		if len([]rune(resp.Documents[0].Preview)) > 50 {
			t.Errorf("preview too long: %d chars", len([]rune(resp.Documents[0].Preview)))
		}
	})

	t.Run("list_documents_pagination", func(t *testing.T) {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
			"limit": 2, "offset": 0,
		}))
		var resp struct {
			Documents []struct{} `json:"documents"`
			Total     float64    `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if len(resp.Documents) != 2 {
			t.Errorf("expected 2 docs, got %d", len(resp.Documents))
		}
		if int(resp.Total) != 3 {
			t.Errorf("total should still be 3, got %v", resp.Total)
		}
	})

	t.Run("query_documents_by_tag", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]string{"tag": "项目管理"}))
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
			Total     int                      `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 1 {
			t.Errorf("expected 1 doc with tag '项目管理', got %d", resp.Total)
		}
	})

	t.Run("query_documents_by_keyword", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]string{"keyword": "后端"}))
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
			Total     int                      `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 1 {
			t.Errorf("expected 1 doc with keyword '后端', got %d", resp.Total)
		}
	})

	t.Run("query_no_match", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]string{"keyword": "不存在的内容xyz"}))
		var resp struct {
			Documents []interface{} `json:"documents"`
			Total     int           `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 0 {
			t.Errorf("expected 0 matches, got %d", resp.Total)
		}
	})
}

func TestDocumentListTags(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 初始无标签
	res := tool.Execute(ec, "list_tags", mustMarshal(t, map[string]interface{}{}))
	var resp struct {
		Tags []string `json:"tags"`
	}
	json.Unmarshal(res.Data, &resp)
	if len(resp.Tags) != 0 {
		t.Errorf("expected 0 tags initially, got %d: %v", len(resp.Tags), resp.Tags)
	}

	// 创建带标签的文档
	tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "doc1", "content": "c1", "tags": "Go, 后端, 技术",
	}))
	tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "doc2", "content": "c2", "tags": "项目管理, 技术",
	}))

	res = tool.Execute(ec, "list_tags", mustMarshal(t, map[string]interface{}{}))
	json.Unmarshal(res.Data, &resp)
	// 应去重：Go, 后端, 技术, 项目管理 = 4 个
	if len(resp.Tags) != 4 {
		t.Errorf("expected 4 unique tags, got %d: %v", len(resp.Tags), resp.Tags)
	}
	expected := map[string]bool{"Go": true, "后端": true, "技术": true, "项目管理": true}
	for _, tag := range resp.Tags {
		if !expected[tag] {
			t.Errorf("unexpected tag: %s", tag)
		}
	}
}

func TestDocumentDelete(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// create
	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "to-delete", "content": "content",
	}))
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	// delete
	res = tool.Execute(ec, "delete_document", mustMarshal(t, map[string]string{"id": docID}))
	if !res.Success {
		t.Fatalf("delete failed: %s", res.Error)
	}

	// 验证不存在
	res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
	if res.Success {
		t.Error("expected failure after delete")
	}

	// delete 不存在的也应正常
	res = tool.Execute(ec, "delete_document", mustMarshal(t, map[string]string{"id": "nonexistent"}))
	if !res.Success {
		t.Errorf("expected success for deleting nonexistent doc, got: %s", res.Error)
	}
}

func TestDocumentUnknownFunction(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "nonexistent_func", mustMarshal(t, map[string]interface{}{}))
	if res.Success {
		t.Error("expected failure for unknown function")
	}
}

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
