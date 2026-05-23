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
	vectorStore   app.VectorStore
	embedder      *llm.Embedder
	embeddingRepo *service.EmbeddingRepo
	chunkBuilder  app.ChunkEmbedder
	conceptEmbed  app.ConceptEmbedder
}

func NewNoteTool(noteRepo *service.NoteRepo, vs app.VectorStore, emb *llm.Embedder, embeddingRepo *service.EmbeddingRepo, chunkBuilder app.ChunkEmbedder, conceptEmbed app.ConceptEmbedder) *NoteTool {
	return &NoteTool{noteRepo: noteRepo, vectorStore: vs, embedder: emb, embeddingRepo: embeddingRepo, chunkBuilder: chunkBuilder, conceptEmbed: conceptEmbed}
}

func (t *NoteTool) Name() string { return "note" }
func (t *NoteTool) Description() string {
	return "自由笔记工具，记录值得长期追踪的信息——事实、数据、观点、事件、个人状态等"
}

func (t *NoteTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "record_note",
			Description: "保存一条值得长期追踪的笔记。当用户分享事实、数据、观点、事件、个人状态时调用。日常闲聊不需要记录",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":   map[string]interface{}{"type": "string", "description": "主题: 房地产/睡眠/俄乌战争/健康/宏观经济/..."},
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
			Name:        "query_notes",
			Description: "查询已保存的笔记，按主题或关键词检索历史记录",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":   map[string]interface{}{"type": "string", "description": "按主题筛选，可选"},
					"keyword": map[string]interface{}{"type": "string", "description": "在正文和标签中搜索，可选"},
				},
			},
		},
		{
			Name:        "update_note",
			Description: "更新已有笔记的字段。仅传需要修改的字段",
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
	}
}

func (t *NoteTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "record_note":
		return t.recordNote(ec, args)
	case "query_notes":
		return t.queryNotes(ec, args)
	case "update_note":
		return t.updateNote(ec, args)
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
	go func() {
		t.conceptEmbed.EnsureTopicEmbedded(context.Background(), ec.UserID, note.Topic, "note", note.ID)
		if note.Tags != "" {
			t.conceptEmbed.EnsureTagsEmbedded(context.Background(), ec.UserID, note.Tags, "note", note.ID)
		}
		t.embedNote(note)
	}()

	data, _ := json.Marshal(note)
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
	if params.Content != "" {
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
