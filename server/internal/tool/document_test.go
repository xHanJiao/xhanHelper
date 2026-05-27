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

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
