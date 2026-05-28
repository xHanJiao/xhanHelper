package tool

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupNoteTool(t *testing.T) (*NoteTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Note{}, &model.ContentVersion{}, &model.EmbeddingChunk{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	noteRepo := service.NewNoteRepo(db)
	versionRepo := service.NewVersionRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)
	return NewNoteTool(noteRepo, versionRepo, nil, nil, nil, chunkRepo, nil, nil), func() {}
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

// 用例：软删除笔记成功后，get_note 查不到
func TestNoteSoftDelete_Success(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	note := &model.Note{ID: "n1", UserID: "user-1", Topic: "测试", Content: "内容"}
	tool.noteRepo.Create(context.Background(), note)

	res := tool.Execute(ec, "delete_note", mustMarshal(t, map[string]string{"id": "n1"}))
	if !res.Success {
		t.Fatalf("delete_note failed: %s", res.Error)
	}

	// 删后 get_note 应查不到
	res = tool.Execute(ec, "get_note", mustMarshal(t, map[string]string{"id": "n1"}))
	if res.Success {
		t.Error("软删除后 get_note 应失败")
	}
}

// 用例：软删除不存在的笔记
func TestNoteSoftDelete_NotFound(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "delete_note", mustMarshal(t, map[string]string{"id": "nonexistent"}))
	if res.Success {
		t.Error("删除不存在的笔记应失败")
	}
}

// 用例：不能软删除其他用户的笔记
func TestNoteSoftDelete_WrongUser(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()

	note := &model.Note{ID: "n1", UserID: "user-1", Topic: "测试", Content: "内容"}
	tool.noteRepo.Create(context.Background(), note)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-2"}
	res := tool.Execute(ec, "delete_note", mustMarshal(t, map[string]string{"id": "n1"}))
	if res.Success {
		t.Error("不应能删除其他用户的笔记")
	}
}

// 用例：软删除后 List 查不到，DistinctTopics 也不包含
func TestNoteSoftDelete_ExcludedFromQueries(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	note := &model.Note{ID: "n1", UserID: "user-1", Topic: "饮酒", Content: "啤酒50元"}
	tool.noteRepo.Create(context.Background(), note)

	// 删除前 List 有结果
	notes, _, _ := tool.noteRepo.List(context.Background(), "user-1", "", "", 10, 0)
	if len(notes) != 1 {
		t.Fatalf("删除前应有 1 条，实际 %d", len(notes))
	}

	// 软删除
	tool.Execute(ec, "delete_note", mustMarshal(t, map[string]string{"id": "n1"}))

	// 删除后 List 无结果
	notes, _, _ = tool.noteRepo.List(context.Background(), "user-1", "", "", 10, 0)
	if len(notes) != 0 {
		t.Errorf("删除后 List 应为空，实际 %d 条", len(notes))
	}

	// DistinctTopics 也不包含
	topics, _ := tool.noteRepo.DistinctTopics(context.Background(), "user-1")
	for _, topic := range topics {
		if topic == "饮酒" {
			t.Error("DistinctTopics 不应包含已删除笔记的主题")
		}
	}
}

// mockVS 记录被删除的 ID 列表
type mockVS struct {
	deleted []string
}

func (m *mockVS) Upsert(id string, vec []float64, metadata map[string]string) {}
func (m *mockVS) Search(query []float64, topK int, filter app.VectorFilter) []app.VectorMatch {
	return nil
}
func (m *mockVS) GetVector(id string) ([]float64, bool) { return nil, false }
func (m *mockVS) Delete(id string)                      { m.deleted = append(m.deleted, id) }
func (m *mockVS) Size() int                             { return 0 }

// 用例：软删除笔记时向量库中的 chunk 也被清掉
func TestNoteSoftDelete_VectorStoreCleanedUp(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Note{}, &model.EmbeddingChunk{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	noteRepo := service.NewNoteRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)
	vs := &mockVS{}

	tool := NewNoteTool(noteRepo, nil, vs, nil, nil, chunkRepo, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	note := &model.Note{ID: "n1", UserID: "user-1", Topic: "测试", Content: "内容"}
	noteRepo.Create(context.Background(), note)

	chunkRepo.BatchCreate(context.Background(), []model.EmbeddingChunk{
		{ID: "chunk:note:n1/0", ParentType: "note", ParentID: "n1", ChunkIndex: 0, Content: "s1"},
		{ID: "chunk:note:n1/1", ParentType: "note", ParentID: "n1", ChunkIndex: 1, Content: "s2"},
	})

	res := tool.Execute(ec, "delete_note", mustMarshal(t, map[string]string{"id": "n1"}))
	if !res.Success {
		t.Fatalf("delete_note failed: %s", res.Error)
	}

	if len(vs.deleted) != 2 {
		t.Errorf("向量库应删除 2 个 chunk，实际 %d: %v", len(vs.deleted), vs.deleted)
	}
	if vs.deleted[0] != "chunk:note:n1/0" || vs.deleted[1] != "chunk:note:n1/1" {
		t.Errorf("删除的 chunk ID 不对: %v", vs.deleted)
	}
}

// 用例：软删除笔记时连带删除其 chunk
func TestNoteSoftDelete_ChunksCleanedUp(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	note := &model.Note{ID: "n1", UserID: "user-1", Topic: "测试", Content: "内容"}
	tool.noteRepo.Create(context.Background(), note)

	// 创建 2 个 chunk
	tool.chunkRepo.BatchCreate(context.Background(), []model.EmbeddingChunk{
		{ID: "chunk:note:n1/0", ParentType: "note", ParentID: "n1", ChunkIndex: 0, Content: "句子1"},
		{ID: "chunk:note:n1/1", ParentType: "note", ParentID: "n1", ChunkIndex: 1, Content: "句子2"},
	})

	// 删除前 chunk 存在
	chunks, _ := tool.chunkRepo.ListByParent(context.Background(), "note", "n1")
	if len(chunks) != 2 {
		t.Fatalf("删除前应有 2 个 chunk，实际 %d", len(chunks))
	}

	// 软删除
	res := tool.Execute(ec, "delete_note", mustMarshal(t, map[string]string{"id": "n1"}))
	if !res.Success {
		t.Fatalf("delete_note failed: %s", res.Error)
	}

	// 删除后 chunk 清空
	chunks, _ = tool.chunkRepo.ListByParent(context.Background(), "note", "n1")
	if len(chunks) != 0 {
		t.Errorf("删除后 chunk 应被清空，实际 %d 条", len(chunks))
	}
}
