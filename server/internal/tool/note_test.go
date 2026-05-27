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

func setupNoteTool(t *testing.T) (*NoteTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Note{}, &model.ContentVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	noteRepo := service.NewNoteRepo(db)
	versionRepo := service.NewVersionRepo(db)
	// 仅测试 get/update/rollback/versions，不需要 embedding 依赖
	return NewNoteTool(noteRepo, versionRepo, nil, nil, nil, nil, nil), func() {}
}

func TestNoteGetAndUpdate(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ctx := context.Background()
	userID := "user-1"
	ec := &ExecuteContext{Ctx: ctx, UserID: userID}

	// record_note 需要 embedding 依赖，直接通过 repo 创建
	note := &model.Note{
		ID:      "note-1",
		UserID:  userID,
		Topic:   "测试主题",
		Content: "原始内容",
		Tags:    "a,b",
	}
	if err := tool.noteRepo.Create(ctx, note); err != nil {
		t.Fatalf("create note directly: %v", err)
	}

	// get_note: 返回完整内容
	res := tool.Execute(ec, "get_note", mustMarshal(t, map[string]string{"id": "note-1"}))
	if !res.Success {
		t.Fatalf("get failed: %s", res.Error)
	}
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	if got["content"] != "原始内容" {
		t.Errorf("expected '原始内容', got %v", got["content"])
	}

	// update_note → 创建快照
	res = tool.Execute(ec, "update_note", mustMarshal(t, map[string]string{
		"id": "note-1", "content": "更新后的内容",
	}))
	if !res.Success {
		t.Fatalf("update failed: %s", res.Error)
	}

	// 验证快照已创建
	res = tool.Execute(ec, "get_note_versions", mustMarshal(t, map[string]string{"id": "note-1"}))
	if !res.Success {
		t.Fatalf("get versions failed: %s", res.Error)
	}
	var versResp map[string]interface{}
	json.Unmarshal(res.Data, &versResp)
	versions := versResp["versions"].([]interface{})
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}

	// 再次更新 → 2 个版本
	res = tool.Execute(ec, "update_note", mustMarshal(t, map[string]string{
		"id": "note-1", "topic": "新主题",
	}))
	if !res.Success {
		t.Fatalf("second update failed: %s", res.Error)
	}
	res = tool.Execute(ec, "get_note_versions", mustMarshal(t, map[string]string{"id": "note-1"}))
	json.Unmarshal(res.Data, &versResp)
	versions = versResp["versions"].([]interface{})
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
}

func TestNoteRollback(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ctx := context.Background()
	userID := "user-1"
	ec := &ExecuteContext{Ctx: ctx, UserID: userID}

	note := &model.Note{
		ID:      "note-1",
		UserID:  userID,
		Topic:   "主题v1",
		Content: "内容v1",
		Tags:    "tag1",
	}
	tool.noteRepo.Create(ctx, note)

	// update → 创建快照（v1内容）
	tool.Execute(ec, "update_note", mustMarshal(t, map[string]string{
		"id": "note-1", "content": "内容v2", "tags": "tag2",
	}))

	// rollback 到 version 1
	res := tool.Execute(ec, "rollback_note", mustMarshal(t, map[string]interface{}{
		"id": "note-1", "version": 1,
	}))
	if !res.Success {
		t.Fatalf("rollback failed: %s", res.Error)
	}

	// 验证已恢复
	res = tool.Execute(ec, "get_note", mustMarshal(t, map[string]string{"id": "note-1"}))
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	if got["content"] != "内容v1" {
		t.Errorf("expected '内容v1', got %v", got["content"])
	}
	if got["tags"] != "tag1" {
		t.Errorf("expected 'tag1', got %v", got["tags"])
	}
}

func TestNoteRollbackWithVariables(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	vars := `{"count":10,"active":true}`
	note := &model.Note{
		ID:        "note-var",
		UserID:    "user-1",
		Topic:     "变量笔记",
		Content:   "有变量的内容",
		Variables: &vars,
	}
	tool.noteRepo.Create(context.Background(), note)

	// update → 创建快照
	tool.Execute(ec, "update_note", mustMarshal(t, map[string]string{
		"id": "note-var", "content": "无变量内容",
	}))

	// rollback 到 version 1（应恢复 variables）
	res := tool.Execute(ec, "rollback_note", mustMarshal(t, map[string]interface{}{
		"id": "note-var", "version": 1,
	}))
	if !res.Success {
		t.Fatalf("rollback failed: %s", res.Error)
	}

	res = tool.Execute(ec, "get_note", mustMarshal(t, map[string]string{"id": "note-var"}))
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	if got["content"] != "有变量的内容" {
		t.Errorf("expected '有变量的内容', got %v", got["content"])
	}
	if vars, ok := got["variables"].(map[string]interface{}); !ok {
		t.Error("expected variables to be restored")
	} else if vars["count"].(float64) != 10 {
		t.Errorf("expected count=10, got %v", vars["count"])
	}
}

func TestNoteNotFound(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "get_note", mustMarshal(t, map[string]string{"id": "nonexistent"}))
	if res.Success {
		t.Error("expected failure for nonexistent note")
	}

	res = tool.Execute(ec, "update_note", mustMarshal(t, map[string]string{"id": "nonexistent", "content": "x"}))
	if res.Success {
		t.Error("expected failure for nonexistent note")
	}

	res = tool.Execute(ec, "rollback_note", mustMarshal(t, map[string]interface{}{"id": "nonexistent", "version": 1}))
	if res.Success {
		t.Error("expected failure for nonexistent note")
	}
}
