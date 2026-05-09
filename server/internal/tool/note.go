package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NoteTool — 变量追踪笔记工具
type NoteTool struct {
	noteRepo    *service.NoteRepo
	vectorStore VectorStorer
	embedder    *llm.Embedder
}

// VectorStorer — 向量存储接口（避免循环依赖）
type VectorStorer interface {
	Upsert(id string, vec []float64, metadata map[string]string)
}

func NewNoteTool(noteRepo *service.NoteRepo, vs VectorStorer, emb *llm.Embedder) *NoteTool {
	return &NoteTool{noteRepo: noteRepo, vectorStore: vs, embedder: emb}
}

func (t *NoteTool) Name() string { return "note" }
func (t *NoteTool) Description() string {
	return "自由笔记工具，记录值得长期追踪的信息——事实、数据、观点、事件、个人状态等"
}

func (t *NoteTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name: "record_note",
			Description: `保存一条值得追踪的笔记。当用户提供了值得长期追踪的信息时调用。日常闲聊和简单记账不需要记录。

topic: 主题，从用户消息推断，用一个词概括（房地产/睡眠/俄乌战争/健康...）
content: 笔记正文。忠实保留用户原话中的关键信息，不要过度总结。长文章全文原样保存。
tags: 逗号分隔的标签，辅助分类检索
variables: JSON对象，从消息中提取的结构化变量。二值用true/false，数值直接填。没有则留空`,
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
			Name: "query_notes",
			Description: `查询已保存的笔记。用于检索历史记录后做回顾或分析。

topic: 按主题筛选，可选
keyword: 在正文和标签中模糊搜索，可选`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":   map[string]interface{}{"type": "string", "description": "按主题筛选，可选"},
					"keyword": map[string]interface{}{"type": "string", "description": "在正文和标签中搜索，可选"},
				},
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

	// 异步 embedding
	if t.vectorStore != nil && t.embedder != nil {
		go t.embedNote(note)
	}

	data, _ := json.Marshal(note)
	return &Result{Success: true, Data: data}
}

func (t *NoteTool) embedNote(note *model.Note) {
	text := note.Content
	vec, err := t.embedder.Embed(context.Background(), text)
	if err != nil {
		log.Printf("[note] embed error: %v", err)
		return
	}
	t.vectorStore.Upsert("note:"+note.ID, vec, map[string]string{"type": "note", "user_id": note.UserID})
	embStr := llm.EmbeddingToString(vec)
	t.noteRepo.UpdateEmbedding(context.Background(), note.ID, &embStr)
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
