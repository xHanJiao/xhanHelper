package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NoteTool — 变量追踪笔记工具
type NoteTool struct {
	noteRepo      *service.NoteRepo
	versionRepo   *service.VersionRepo
	vectorStore   app.VectorStore
	embedder      *llm.Embedder
	embeddingRepo *service.EmbeddingRepo
	chunkBuilder  app.ChunkEmbedder
	conceptEmbed  app.ConceptEmbedder
}

func NewNoteTool(noteRepo *service.NoteRepo, versionRepo *service.VersionRepo, vs app.VectorStore, emb *llm.Embedder, embeddingRepo *service.EmbeddingRepo, chunkBuilder app.ChunkEmbedder, conceptEmbed app.ConceptEmbedder) *NoteTool {
	return &NoteTool{noteRepo: noteRepo, versionRepo: versionRepo, vectorStore: vs, embedder: emb, embeddingRepo: embeddingRepo, chunkBuilder: chunkBuilder, conceptEmbed: conceptEmbed}
}

func (t *NoteTool) Name() string { return "note" }
func (t *NoteTool) Description() string {
	return "自由笔记工具，记录值得长期追踪的信息——事实、数据、观点、事件、个人状态、人物关系等"
}

func (t *NoteTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "record_note",
			Description: "保存一条值得长期追踪的笔记。当用户分享事实、数据、观点、事件、个人状态、人物关系时调用。日常闲聊不需要记录",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":   map[string]interface{}{"type": "string", "description": "主题: 房地产/睡眠/人物关系/健康/..."},
					"content": map[string]interface{}{"type": "string", "description": "笔记正文，忠实保留用户原文，不要总结或删减"},
					"tags":    map[string]interface{}{"type": "string", "description": "逗号分隔标签，可选"},
					"variables": map[string]interface{}{
						"type":        "object",
						"description": "结构化键值对。二值用true/false，数值直接填。无量化信息则留空",
					},
				},
				"required": []string{"topic", "content"},
			},
		},
		{
			Name:        "get_note",
			Description: "获取笔记完整内容（含版本列表）。修改笔记前必须先调用此函数读取当前内容。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "笔记 ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "query_notes",
			Description: "查询已保存的笔记，按主题或关键词检索历史记录",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":   map[string]interface{}{"type": "string", "description": "按主题筛选，可选"},
					"keyword": map[string]interface{}{"type": "string", "description": "在正文,主题和标签中搜索，可选"},
				},
			},
		},
		{
			Name:        "update_note",
			Description: "更新已有笔记。修改前请先用 get_note 读取完整内容，在其基础上修改后传入。仅传需要修改的字段",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      map[string]interface{}{"type": "string", "description": "笔记 ID，必填"},
					"topic":   map[string]interface{}{"type": "string", "description": "新主题，可选"},
					"content": map[string]interface{}{"type": "string", "description": "新正文，可选"},
					"tags":    map[string]interface{}{"type": "string", "description": "新标签，可选"},
					"variables": map[string]interface{}{
						"type":        "object",
						"description": "新结构变量，可选",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "rollback_note",
			Description: "将笔记回滚到指定历史版本。版本号从 get_note_versions 获取。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      map[string]interface{}{"type": "string", "description": "笔记 ID"},
					"version": map[string]interface{}{"type": "integer", "description": "要回滚到的版本号"},
				},
				"required": []string{"id", "version"},
			},
		},
		{
			Name:        "get_note_versions",
			Description: "获取笔记的所有历史版本列表。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "笔记 ID"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *NoteTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "record_note":
		return t.recordNote(ec, args)
	case "get_note":
		return t.getNote(ec, args)
	case "query_notes":
		return t.queryNotes(ec, args)
	case "update_note":
		return t.updateNote(ec, args)
	case "rollback_note":
		return t.rollbackNote(ec, args)
	case "get_note_versions":
		return t.getNoteVersions(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

func (t *NoteTool) recordNote(ec *ExecuteContext, args json.RawMessage) *Result {
	var params struct {
		Topic     string                 `json:"topic"`
		Content   string                 `json:"content"`
		Tags      string                 `json:"tags"`
		Variables map[string]interface{} `json:"variables"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse args: %v", err)}
	}
	if params.Topic == "" || params.Content == "" {
		return &Result{Success: false, Error: "topic and content are required"}
	}

	note := &model.Note{
		ID:      uuid.New().String(),
		UserID:  ec.UserID,
		Topic:   params.Topic,
		Content: params.Content,
		Tags:    params.Tags,
	}
	if len(params.Variables) > 0 {
		b, _ := json.Marshal(params.Variables)
		s := string(b)
		note.Variables = &s
	}

	if err := t.noteRepo.Create(ec.Ctx, note); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("create note: %v", err)}
	}

	// 异步嵌入：concept + chunk
	if t.chunkBuilder != nil {
		go func() {
			t.conceptEmbed.EnsureTopicEmbedded(context.Background(), ec.UserID, note.Topic, "note", note.ID)
			if note.Tags != "" {
				t.conceptEmbed.EnsureTagsEmbedded(context.Background(), ec.UserID, note.Tags, "note", note.ID)
			}
			t.embedNote(note)
		}()
	}

	data, _ := json.Marshal(note)
	return &Result{Success: true, Data: data}
}

func (t *NoteTool) getNote(ec *ExecuteContext, args json.RawMessage) *Result {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse args: %v", err)}
	}
	if params.ID == "" {
		return &Result{Success: false, Error: "id is required"}
	}

	note, err := t.noteRepo.GetByID(ec.Ctx, params.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("note not found: %v", err)}
	}

	result := map[string]interface{}{
		"id":         note.ID,
		"topic":      note.Topic,
		"content":    note.Content,
		"tags":       note.Tags,
		"created_at": note.CreatedAt,
		"updated_at": note.UpdatedAt,
	}
	if note.Variables != nil {
		var vars map[string]interface{}
		if json.Unmarshal([]byte(*note.Variables), &vars) == nil {
			result["variables"] = vars
		}
	}

	// 附带版本历史列表
	versions, err := t.versionRepo.GetVersions(ec.Ctx, "note", params.ID)
	if err != nil {
		log.Printf("[note] get versions for get_note: %v", err)
	}
	if len(versions) > 0 {
		vlist := make([]map[string]interface{}, len(versions))
		for i, v := range versions {
			vlist[i] = map[string]interface{}{
				"version":    v.Version,
				"created_at": v.CreatedAt,
			}
		}
		result["versions"] = vlist
	}

	data, _ := json.Marshal(result)
	return &Result{Success: true, Data: data}
}

func (t *NoteTool) getNoteVersions(ec *ExecuteContext, args json.RawMessage) *Result {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse args: %v", err)}
	}
	if params.ID == "" {
		return &Result{Success: false, Error: "id is required"}
	}

	if _, err := t.noteRepo.GetByID(ec.Ctx, params.ID, ec.UserID); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("note not found: %v", err)}
	}

	versions, err := t.versionRepo.GetVersions(ec.Ctx, "note", params.ID)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("get versions: %v", err)}
	}

	vlist := make([]map[string]interface{}, 0, len(versions))
	for _, v := range versions {
		vlist = append(vlist, map[string]interface{}{
			"version":    v.Version,
			"snapshot":   v.Snapshot,
			"created_at": v.CreatedAt,
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"versions": vlist,
	})
	return &Result{Success: true, Data: data}
}

func (t *NoteTool) updateNote(ec *ExecuteContext, args json.RawMessage) *Result {
	var params struct {
		ID        string                 `json:"id"`
		Topic     string                 `json:"topic"`
		Content   string                 `json:"content"`
		Tags      string                 `json:"tags"`
		Variables map[string]interface{} `json:"variables"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse args: %v", err)}
	}
	if params.ID == "" {
		return &Result{Success: false, Error: "id is required"}
	}

	// 更新前先创建版本快照
	current, err := t.noteRepo.GetByID(ec.Ctx, params.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("note not found: %v", err)}
	}
	snapData := map[string]interface{}{
		"topic":   current.Topic,
		"content": current.Content,
		"tags":    current.Tags,
	}
	if current.Variables != nil {
		snapData["variables"] = *current.Variables
	}
	if err := t.versionRepo.CreateSnapshot(ec.Ctx, "note", params.ID, snapData); err != nil {
		log.Printf("[note] create snapshot before update %s: %v", params.ID, err)
	}

	var varsStr *string
	if len(params.Variables) > 0 {
		b, _ := json.Marshal(params.Variables)
		s := string(b)
		varsStr = &s
	}

	if err := t.noteRepo.Update(ec.Ctx, params.ID, ec.UserID, params.Topic, params.Content, params.Tags, varsStr); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("update note: %v", err)}
	}

	// 更新了 content 则重新嵌入
	// FIXME 重新嵌入，如果不删除之前的内容，会有幽灵空搜吧
	if params.Content != "" && t.chunkBuilder != nil {
		go func() {
			if params.Topic != "" {
				t.conceptEmbed.EnsureTopicEmbedded(context.Background(), ec.UserID, params.Topic, "note", params.ID)
			}
			if params.Tags != "" {
				t.conceptEmbed.EnsureTagsEmbedded(context.Background(), ec.UserID, params.Tags, "note", params.ID)
			}
			t.embedNote(&model.Note{ID: params.ID, Content: params.Content, UserID: ec.UserID})
		}()
	}

	return &Result{Success: true}
}

func (t *NoteTool) rollbackNote(ec *ExecuteContext, args json.RawMessage) *Result {
	var params struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse args: %v", err)}
	}
	if params.ID == "" || params.Version <= 0 {
		return &Result{Success: false, Error: "id and version are required"}
	}

	current, err := t.noteRepo.GetByID(ec.Ctx, params.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("note not found: %v", err)}
	}

	snapData := map[string]interface{}{
		"topic":   current.Topic,
		"content": current.Content,
		"tags":    current.Tags,
	}
	if current.Variables != nil {
		snapData["variables"] = *current.Variables
	}
	if err := t.versionRepo.CreateSnapshot(ec.Ctx, "note", params.ID, snapData); err != nil {
		log.Printf("[note] create snapshot before rollback %s: %v", params.ID, err)
	}

	snap, err := t.versionRepo.GetSnapshot(ec.Ctx, "note", params.ID, params.Version)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("version not found: %v", err)}
	}

	var snapData2 struct {
		Topic     string `json:"topic"`
		Content   string `json:"content"`
		Tags      string `json:"tags"`
		Variables string `json:"variables"`
	}
	if err := json.Unmarshal([]byte(snap.Snapshot), &snapData2); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse snapshot: %v", err)}
	}

	var varsPtr *string
	if snapData2.Variables != "" {
		varsPtr = &snapData2.Variables
	}
	if err := t.noteRepo.Update(ec.Ctx, params.ID, ec.UserID, snapData2.Topic, snapData2.Content, snapData2.Tags, varsPtr); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("rollback note: %v", err)}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":      params.ID,
		"version": params.Version,
		"message": "笔记已回滚",
	})
	return &Result{Success: true, Data: data}
}

func (t *NoteTool) embedNote(note *model.Note) {
	n, err := t.chunkBuilder.BuildChunks(context.Background(), note.UserID, "note", note.ID, note.Content)
	if err != nil {
		log.Printf("[note] chunk build error: %v", err)
	} else {
		log.Printf("[note] chunked note %s: %d sentences", note.ID[:8], n)
	}
}

func (t *NoteTool) queryNotes(ec *ExecuteContext, args json.RawMessage) *Result {
	var params struct {
		Topic   string `json:"topic"`
		Keyword string `json:"keyword"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse args: %v", err)}
	}

	notes, _, err := t.noteRepo.List(ec.Ctx, ec.UserID, params.Topic, params.Keyword, 50, 0)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("query notes: %v", err)}
	}
	if notes == nil {
		notes = []model.Note{}
	}
	data, _ := json.Marshal(notes)
	return &Result{Success: true, Data: data}
}
