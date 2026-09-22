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

func setupNoteTool(t *testing.T) (*NoteTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Note{}, &model.ContentVersion{}, &model.EmbeddingChunk{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	noteRepo := service.NewNoteRepo(db)
	versionRepo := service.NewVersionRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	vs := &mockVS{}
	purger := service.NewEntityPurger(embeddingRepo, chunkRepo, versionRepo, vs)
	return NewNoteTool(noteRepo, versionRepo, vs, &mockNoteEmbedder{}, embeddingRepo, &mockChunkEmbedder{}, &mockConceptEmbedder{}, purger), func() {}
}

func TestNoteGetAndUpdate(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ctx := context.Background()
	userID := "user-1"
	ec := &ExecuteContext{Ctx: ctx, UserID: userID}

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

type mockChunkEmbedder struct{}

func (m *mockChunkEmbedder) BuildChunks(_ context.Context, _, _, _, _ string, _ map[string]string) (int, error) {
	return 0, nil
}

type mockConceptEmbedder struct{}

func (m *mockConceptEmbedder) EnsureTopicEmbedded(_ context.Context, _, _, _, _ string) {}
func (m *mockConceptEmbedder) EnsureTagsEmbedded(_ context.Context, _, _, _, _ string)  {}

type mockNoteEmbedder struct{}

func (m *mockNoteEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return []float64{0.1}, nil
}
func (m *mockNoteEmbedder) ModelName() string { return "mock-embedder" }

// 用例：软删除笔记时，其派生数据（chunk 行、chunk 向量行、全文向量行、版本快照、内存向量）全部清掉
func TestNoteSoftDelete_PurgesDerivedData(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Note{}, &model.EmbeddingChunk{}, &model.Embedding{}, &model.ContentVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	noteRepo := service.NewNoteRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	versionRepo := service.NewVersionRepo(db)
	vs := &mockVS{}
	purger := service.NewEntityPurger(embeddingRepo, chunkRepo, versionRepo, vs)

	tool := NewNoteTool(noteRepo, versionRepo, vs, nil, embeddingRepo, nil, nil, purger)
	ctx := context.Background()
	ec := &ExecuteContext{Ctx: ctx, UserID: "user-1"}

	if err := noteRepo.Create(ctx, &model.Note{ID: "n1", UserID: "user-1", Topic: "测试", Content: "内容"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	if err := chunkRepo.BatchCreate(ctx, []model.EmbeddingChunk{
		{ID: "c0", ParentType: "note", ParentID: "n1", ChunkIndex: 0, Content: "s1"},
		{ID: "c1", ParentType: "note", ParentID: "n1", ChunkIndex: 1, Content: "s2"},
	}); err != nil {
		t.Fatalf("create chunks: %v", err)
	}
	emb := "[]"
	if err := embeddingRepo.BatchUpsert(ctx, []model.Embedding{
		{ID: "n1", EntityType: "note", EntityID: "n1", UserID: "user-1", Model: "m", Embedding: &emb},
		{ID: "chunk:note:n1/0", EntityType: "chunk", EntityID: "chunk:note:n1/0", UserID: "user-1", Model: "m", Embedding: &emb},
		{ID: "chunk:note:n1/1", EntityType: "chunk", EntityID: "chunk:note:n1/1", UserID: "user-1", Model: "m", Embedding: &emb},
	}); err != nil {
		t.Fatalf("create embeddings: %v", err)
	}
	if err := versionRepo.CreateSnapshot(ctx, "note", "n1", map[string]interface{}{"content": "内容"}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	res := tool.Execute(ec, "delete_note", mustMarshal(t, map[string]string{"id": "n1"}))
	if !res.Success {
		t.Fatalf("delete_note failed: %s", res.Error)
	}

	// 内存向量：两个 chunk + 全文 note:n1
	want := map[string]bool{"chunk:note:n1/0": true, "chunk:note:n1/1": true, "note:n1": true}
	if len(vs.deleted) != len(want) {
		t.Errorf("向量库应删除 %d 项，实际 %d: %v", len(want), len(vs.deleted), vs.deleted)
	}
	for _, id := range vs.deleted {
		if !want[id] {
			t.Errorf("删除了预期外的向量键: %s", id)
		}
	}

	assertCount := func(name string, model interface{}, query string, args ...interface{}) {
		var n int64
		if err := db.Model(model).Where(query, args...).Count(&n).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if n != 0 {
			t.Errorf("%s 应清零，实际剩 %d 行", name, n)
		}
	}
	assertCount("chunk 行", &model.EmbeddingChunk{}, "parent_id = ?", "n1")
	assertCount("chunk 向量行", &model.Embedding{}, "entity_type = ? AND entity_id LIKE ?", "chunk", "chunk:note:n1/%")
	assertCount("全文向量行", &model.Embedding{}, "entity_type = ? AND entity_id = ?", "note", "n1")
	assertCount("版本快照", &model.ContentVersion{}, "entity_type = ? AND entity_id = ?", "note", "n1")
}

// seedNotes 为查询测试创建多条笔记
func seedNotes(t *testing.T, repo *service.NoteRepo, userID string) {
	t.Helper()
	notes := []model.Note{
		{ID: "n1", UserID: userID, Topic: "Go开发", Content: "Go语言后端开发最佳实践包括错误处理、并发模式", Tags: "go,backend", CreatedAt: time.Now().Add(-72 * time.Hour)},
		{ID: "n2", UserID: userID, Topic: "Go开发", Content: "Go 1.22新增的泛型特性使用指南", Tags: "go,generics", CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ID: "n3", UserID: userID, Topic: "Python", Content: "Python异步编程asyncio详解", Tags: "python,async", CreatedAt: time.Now().Add(-36 * time.Hour)},
		{ID: "n4", UserID: userID, Topic: "数据库", Content: "MySQL索引优化策略和SQL调优经验", Tags: "mysql,sql", CreatedAt: time.Now().Add(-24 * time.Hour)},
		{ID: "n5", UserID: userID, Topic: "数据库", Content: "PostgreSQL分区表实践", Tags: "postgres,partition", CreatedAt: time.Now().Add(-12 * time.Hour)},
		{ID: "n6", UserID: "other-user", Topic: "私密", Content: "不应出现的笔记", Tags: "private", CreatedAt: time.Now().Add(-6 * time.Hour)},
	}
	for _, n := range notes {
		if err := repo.Create(context.Background(), &n); err != nil {
			t.Fatalf("seed note %s: %v", n.ID, err)
		}
	}
}

func TestNoteQueryBasic(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	userID := "user-1"
	seedNotes(t, tool.noteRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "query_notes", mustMarshal(t, map[string]string{"query": "Go"}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	notes := out["notes"].([]interface{})
	if len(notes) < 2 {
		t.Fatalf("expected at least 2 Go notes, got %d", len(notes))
	}
	if out["total"] == nil {
		t.Error("expected total field")
	}
}

func TestNoteQueryTopicFilter(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	userID := "user-1"
	seedNotes(t, tool.noteRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "query_notes", mustMarshal(t, map[string]interface{}{"topic": "Python"}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	notes := out["notes"].([]interface{})
	if len(notes) != 1 {
		t.Fatalf("expected 1 Python note, got %d", len(notes))
	}
}

func TestNoteQueryCursorPagination(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	userID := "user-1"
	seedNotes(t, tool.noteRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "query_notes", mustMarshal(t, map[string]interface{}{"limit": 2}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var p1 map[string]interface{}
	json.Unmarshal(res.Data, &p1)
	n1 := p1["notes"].([]interface{})
	if len(n1) != 2 {
		t.Fatalf("expected 2, got %d", len(n1))
	}
	cursor, ok := p1["next_cursor"].(string)
	if !ok || cursor == "" || p1["has_more"] != true {
		t.Fatal("expected has_more=true and next_cursor")
	}

	res = tool.Execute(ec, "query_notes", mustMarshal(t, map[string]interface{}{"limit": 2, "cursor": cursor}))
	if !res.Success {
		t.Fatalf("page 2 failed: %s", res.Error)
	}
	var p2 map[string]interface{}
	json.Unmarshal(res.Data, &p2)
	n2 := p2["notes"].([]interface{})
	if len(n2) == 0 {
		t.Error("expected messages in page 2")
	}
}

func TestNoteCount(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	userID := "user-1"
	seedNotes(t, tool.noteRepo, userID)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "count_notes", mustMarshal(t, map[string]interface{}{"query": "SQL"}))
	if !res.Success {
		t.Fatalf("count failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	if out["count"].(float64) < 1 {
		t.Errorf("expected at least 1, got %v", out["count"])
	}
}

func TestNoteListTopics(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	userID := "user-1"
	seedNotes(t, tool.noteRepo, userID)
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
}

func TestNoteOtherUserIsolation(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	seedNotes(t, tool.noteRepo, "user-1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "other-user"}

	res := tool.Execute(ec, "query_notes", mustMarshal(t, map[string]string{}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	notes := out["notes"].([]interface{})
	// n6 belongs to other-user, so they should see exactly 1 note
	if len(notes) != 1 {
		t.Errorf("other user should see only their own note, got %d", len(notes))
	}
}

func TestNoteQueryNotes_DefaultLimit5(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	seedNotes(t, tool.noteRepo, "user-1")
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "query_notes", mustMarshal(t, map[string]string{}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	notes := out["notes"].([]interface{})
	if len(notes) > 5 {
		t.Errorf("default limit should be 5, got %d", len(notes))
	}
}

func TestNoteRecordNote_ContentTooLong_Rejected(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	longContent := strings.Repeat("很长的内容", 40)
	res := tool.Execute(ec, "record_note", mustMarshal(t, map[string]string{
		"topic": "测试", "content": longContent,
	}))
	if res.Success {
		t.Fatal("record_note should reject content over limit")
	}
	if !strings.Contains(res.Error, "150") {
		t.Errorf("error should mention the 150 limit, got: %s", res.Error)
	}
}

func TestNoteUpdateNote_ContentTooLong_Rejected(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}
	seedNotes(t, tool.noteRepo, "user-1")

	longContent := strings.Repeat("超长内容", 40)
	res := tool.Execute(ec, "update_note", mustMarshal(t, map[string]string{
		"id": "n1", "content": longContent,
	}))
	if res.Success {
		t.Fatal("update_note should reject content over limit")
	}

	// 原内容不应被改动
	note, err := tool.noteRepo.GetByID(context.Background(), "n1", "user-1")
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if note.Content == longContent {
		t.Error("note content should not be modified on rejected update")
	}
}

func TestNoteRecordNote_Boundary150_Accepted(t *testing.T) {
	tool, cleanup := setupNoteTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 恰好 150 字应通过
	boundary := strings.Repeat("字", 150)
	res := tool.Execute(ec, "record_note", mustMarshal(t, map[string]string{
		"topic": "测试", "content": boundary,
	}))
	if !res.Success {
		t.Fatalf("boundary 150 content should be accepted: %s", res.Error)
	}
}
