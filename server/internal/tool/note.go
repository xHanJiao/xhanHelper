package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const (
	defaultQueryLimit   = 20
	maxQueryLimit       = 50
	defaultSemanticTopK = 5
	maxSemanticTopK     = 10

	// defaultNoteQueryLimit — query_notes 默认条数（笔记内容长，5 条足够浏览定位，更多靠 limit/cursor）
	defaultNoteQueryLimit = 5
)

// ToolNoteOutput — 结构化 JSON 中的单条笔记
type ToolNoteOutput struct {
	ID              string          `json:"id"`
	Topic           string          `json:"topic"`
	Content         string          `json:"content"`
	Tags            string          `json:"tags,omitempty"`
	Variables       json.RawMessage `json:"variables,omitempty"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	RelevanceScore  *float64        `json:"relevance_score,omitempty"`
	MatchedKeywords []string        `json:"matched_keywords,omitempty"`
}
type NoteTool struct {
	noteRepo      *service.NoteRepo
	versionRepo   *service.VersionRepo
	vectorStore   app.VectorStore
	embedder      app.Embedder
	embeddingRepo *service.EmbeddingRepo
	chunkBuilder  app.ChunkEmbedder
	conceptEmbed  app.ConceptEmbedder
	purger        *service.EntityPurger
}

func NewNoteTool(noteRepo *service.NoteRepo, versionRepo *service.VersionRepo, vs app.VectorStore, emb app.Embedder,
	embeddingRepo *service.EmbeddingRepo, chunkBuilder app.ChunkEmbedder, conceptEmbed app.ConceptEmbedder,
	purger *service.EntityPurger) *NoteTool {
	return &NoteTool{noteRepo: noteRepo, versionRepo: versionRepo, vectorStore: vs, embedder: emb,
		embeddingRepo: embeddingRepo, chunkBuilder: chunkBuilder, conceptEmbed: conceptEmbed, purger: purger}
}

func (t *NoteTool) Name() string { return "note" }
func (t *NoteTool) Description() string {
	// TODO 在这里可以简单介绍一下各个函数
	return "自由笔记工具，记录值得长期追踪的信息——事实、数据、观点、事件、个人状态、人物关系等"
}

func (t *NoteTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "笔记。记录/检索长期追踪信息：事实、观点、事件、状态、关系。",
		Funcs:     []string{"record_note", "semantic_search", "query_notes", "update_note"},
		Others:    "计数、主题列表、版本回滚、删除 等",
	}, true
}

// maxNoteContentLen 单条笔记正文上限（字）。超出请先建文档，笔记只存摘要。
const maxNoteContentLen = 150

func (t *NoteTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "semantic_search",
			Display:     "语义搜索",
			Description: "用向量语义相似度搜索笔记。返回按创建时间倒序排列的结果。适合模糊概念、主题等语义匹配场景。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "搜索关键词或描述（用于语义匹配）"},
					"top_k": map[string]interface{}{"type": "integer", "description": "返回条数，默认5，最大10"},
					"topic": map[string]interface{}{"type": "string", "description": "限定 topic，不传则不限"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "count_notes",
			Display:     "统计笔记数量",
			Description: "统计符合条件的笔记总数。支持 keyword+topic+time 全维度组合过滤。在搜索前先调用此函数评估量级。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]interface{}{"type": "string", "description": "关键词（空格/逗号分隔，OR 关系）"},
					"topic":       map[string]interface{}{"type": "string", "description": "限定 topic"},
					"start_time":  map[string]interface{}{"type": "string", "description": "起始时间"},
					"end_time":    map[string]interface{}{"type": "string", "description": "结束时间"},
					"last_n_days": map[string]interface{}{"type": "integer", "description": "最近 N 天"},
				},
				"required": []string{},
			},
		},
		{
			Name:        "record_note",
			Display:     "记录笔记",
			Description: fmt.Sprintf("保存一条值得长期追踪的笔记。当用户分享事实、数据、观点、事件、个人状态、人物关系时调用。日常闲聊不需要记录。正文 ≤%d 字：如果确实需要超长内容，先建文档（document 工具），笔记只存精简摘要并在 content 末尾注明 document_id", maxNoteContentLen),
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":   map[string]interface{}{"type": "string", "description": "主题: 房地产/睡眠/人物关系/健康/..."},
					"content": map[string]interface{}{"type": "string", "description": fmt.Sprintf("笔记正文，≤%d 字，保留关键事实（日期/人物/金额/决定）。超长会被拒绝，请先建文档再存摘要", maxNoteContentLen)},
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
			Display:     "获取笔记",
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
			Display:     "查询笔记",
			Description: "按关键词或主题搜索笔记。多关键词用空格/逗号/顿号分隔为 OR 关系。支持时间范围过滤、cursor 分页。先调 count_notes 评估量级。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]interface{}{"type": "string", "description": "搜索关键词，空格/逗号/顿号分隔多个词"},
					"topic":       map[string]interface{}{"type": "string", "description": "限定 topic"},
					"start_time":  map[string]interface{}{"type": "string", "description": "起始时间"},
					"end_time":    map[string]interface{}{"type": "string", "description": "结束时间"},
					"last_n_days": map[string]interface{}{"type": "integer", "description": "最近 N 天，与 start_time 互斥"},
					"limit":       map[string]interface{}{"type": "integer", "description": "返回条数，默认5，最大50（更多结果用 cursor 翻页）"},
					"cursor":      map[string]interface{}{"type": "string", "description": "分页游标（上一页返回的 next_cursor），首页不传"},
				},
				"required": []string{},
			},
		},
		{
			Name:        "list_topics",
			Display:     "列出所有主题",
			Description: "列出用户所有笔记中的 topic 及对应数量，按数量倒序排列。用于了解现有笔记覆盖的主题，辅助选择搜索方向。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
				"required":   []string{},
			},
		},
		{
			Name:        "update_note",
			Display:     "修改笔记",
			Description: fmt.Sprintf("更新已有笔记。修改前请先用 get_note 读取完整内容，在其基础上修改后传入。仅传需要修改的字段。正文 ≤%d 字，超长会被拒绝", maxNoteContentLen),
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      map[string]interface{}{"type": "string", "description": "笔记 ID，必填"},
					"topic":   map[string]interface{}{"type": "string", "description": "新主题，可选"},
					"content": map[string]interface{}{"type": "string", "description": fmt.Sprintf("新正文，≤%d 字，可选", maxNoteContentLen)},
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
			Display:     "回滚笔记",
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
			Display:     "版本列表",
			Description: "获取笔记的所有历史版本列表。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "笔记 ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "delete_note",
			Display:     "删除笔记",
			Description: "作废一条笔记（软删除）。用户明确要求删除或内容已过时时调用。如用户未明确指定要删除的笔记 ID，必须先向用户确认",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "笔记 ID（必填）"},
				},
				"required": []string{"id"},
			},
		},
	}
}

// ReadOnlyFuncs — 检索/查询/版本类函数只读，记录/修改/删除类为写（plan 模式拦截）
func (t *NoteTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{
		"semantic_search":   true,
		"count_notes":       true,
		"get_note":          true,
		"query_notes":       true,
		"list_topics":       true,
		"get_note_versions": true,
	}
}

func (t *NoteTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "semantic_search":
		return t.semanticSearch(ec, args)
	case "count_notes":
		return t.countNotes(ec, args)
	case "record_note":
		return t.recordNote(ec, args)
	case "get_note":
		return t.getNote(ec, args)
	case "query_notes":
		return t.queryNotes(ec, args)
	case "update_note":
		return t.updateNote(ec, args)
	case "list_topics":
		return t.listTopics(ec, args)
	case "rollback_note":
		return t.rollbackNote(ec, args)
	case "delete_note":
		return t.deleteNote(ec, args)
	case "get_note_versions":
		return t.getNoteVersions(ec, args)
	default:
		// TODO 这里可以增加上对于已有函数名的返回，另外这种Execute 是否可以用反射做分发？
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
	if utf8.RuneCountInString(params.Content) > maxNoteContentLen {
		return &Result{Success: false, Error: fmt.Sprintf("笔记正文超长（≤%d 字），请精简；长内容请先建文档，笔记只存摘要并记录 document_id", maxNoteContentLen)}
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

	// 异步嵌入：full + concept + chunk
	go func() {
		t.embedAll(context.Background(), note)
	}()

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

func (t *NoteTool) listTopics(ec *ExecuteContext, _ json.RawMessage) *Result {
	topics, err := t.noteRepo.DistinctTopics(ec.Ctx, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("get topics: %v", err)}
	}
	if topics == nil {
		topics = []string{}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"topics": topics,
		"count":  len(topics),
	})
	return &Result{Success: true, Data: data}
}

func (t *NoteTool) deleteNote(ec *ExecuteContext, args json.RawMessage) *Result {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse args: %v", err)}
	}
	if params.ID == "" {
		return &Result{Success: false, Error: "id is required"}
	}

	// 先验证笔记存在且属于当前用户
	if _, err := t.noteRepo.GetByID(ec.Ctx, params.ID, ec.UserID); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("note not found: %v", err)}
	}

	if err := t.noteRepo.SoftDelete(ec.Ctx, params.ID, ec.UserID); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("delete note failed: %v", err)}
	}

	// 清理派生数据（向量行/chunk/版本快照/内存向量）。笔记已删，清理失败只记日志、不翻转结果。
	if err := t.purger.Purge(ec.Ctx, "note", params.ID); err != nil {
		log.Printf("[note] purge derived data for %s error: %v", params.ID, err)
	}

	data, _ := json.Marshal(map[string]string{"deleted_id": params.ID})
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
	if utf8.RuneCountInString(params.Content) > maxNoteContentLen {
		return &Result{Success: false, Error: fmt.Sprintf("笔记正文超长（≤%d 字），请精简；长内容请先建文档，笔记只存摘要并记录 document_id", maxNoteContentLen)}
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
	if params.Content != "" {
		topic := params.Topic
		if topic == "" {
			topic = current.Topic
		}
		tags := params.Tags
		if tags == "" {
			tags = current.Tags
		}
		noteForEmbed := &model.Note{
			ID:      params.ID,
			UserID:  ec.UserID,
			Topic:   topic,
			Content: params.Content,
			Tags:    tags,
		}
		go func() {
			if err := t.purger.PurgeChunks(context.Background(), "note", params.ID); err != nil {
				log.Printf("[note] purge chunks for %s error: %v", params.ID, err)
			}
			t.embedAll(context.Background(), noteForEmbed)
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

func (t *NoteTool) embedAll(ctx context.Context, note *model.Note) {
	t.conceptEmbed.EnsureTopicEmbedded(ctx, note.UserID, note.Topic, "note", note.ID)
	if note.Tags != "" {
		t.conceptEmbed.EnsureTagsEmbedded(ctx, note.UserID, note.Tags, "note", note.ID)
	}

	vec, err := t.embedder.Embed(ctx, note.Content)
	if err != nil {
		log.Printf("[note] embed full note %.8s error: %v", note.ID, err)
	} else {
		t.vectorStore.Upsert("note:"+note.ID, vec, map[string]string{
			"type":    "note",
			"user_id": note.UserID,
			"topic":   note.Topic,
		})
		embStr := llm.EmbeddingToString(vec)
		if err := t.embeddingRepo.Upsert(ctx, &model.Embedding{
			ID:         note.ID,
			EntityType: "note",
			EntityID:   note.ID,
			UserID:     note.UserID,
			Model:      t.embedder.ModelName(),
			Embedding:  &embStr,
		}); err != nil {
			log.Printf("[note] upsert full embedding %.8s error: %v", note.ID, err)
		}
	}

	n, err := t.chunkBuilder.BuildChunks(ctx, note.UserID, "note", note.ID, note.Content, map[string]string{"topic": note.Topic})
	if err != nil {
		log.Printf("[note] chunk build error: %v", err)
	} else {
		log.Printf("[note] chunked note %.8s: %d sentences", note.ID, n)
	}
}

func (t *NoteTool) queryNotes(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query     string `json:"query"`
		Topic     string `json:"topic"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		LastNDays int    `json:"last_n_days"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Limit <= 0 {
		a.Limit = defaultNoteQueryLimit
	}
	if a.Limit > maxQueryLimit {
		a.Limit = maxQueryLimit
	}

	var keywords []string
	if a.Query != "" {
		keywords = splitKeywords(a.Query)
	}

	after, before, err := parseTimeParams(a.StartTime, a.EndTime, a.LastNDays, 0)
	if err != nil {
		log.Printf("[note] query_notes parse time params error: %v", err)
		return &Result{Success: false, Error: err.Error()}
	}

	var cursorTime *time.Time
	if a.Cursor != "" {
		parsed, err := parseTimeString(a.Cursor)
		if err != nil {
			log.Printf("[note] query_notes parse cursor error: %v", err)
			return &Result{Success: false, Error: fmt.Sprintf("cursor 解析失败: %v", err)}
		}
		cursorTime = &parsed
	}

	notes, hasMore, err := t.noteRepo.SearchNotesByCursor(ec.Ctx, ec.UserID, keywords, a.Topic, after, before, cursorTime, a.Limit)
	if err != nil {
		log.Printf("[note] query_notes error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询笔记失败: %v", err)}
	}

	total, err := t.noteRepo.CountNotes(ec.Ctx, ec.UserID, keywords, a.Topic, after, before)
	if err != nil {
		log.Printf("[note] count error: %v", err)
		total = int64(len(notes))
	}

	output := make([]ToolNoteOutput, 0, len(notes))
	for _, n := range notes {
		no := formatNote(n)
		if len(keywords) > 0 {
			no.MatchedKeywords = findMatchedKeywords(n.Content, keywords)
		}
		output = append(output, no)
	}

	var nextCursor string
	if hasMore && len(notes) > 0 {
		nextCursor = formatMsgTime(notes[len(notes)-1].CreatedAt)
	}

	resp := map[string]interface{}{
		"total":    total,
		"has_more": hasMore,
		"notes":    output,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}

	data, _ := json.Marshal(resp)
	return &Result{Success: true, Data: data}
}

// ==================== semantic_search ====================

func (t *NoteTool) semanticSearch(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if a.TopK <= 0 {
		a.TopK = defaultSemanticTopK
	}
	if a.TopK > maxSemanticTopK {
		a.TopK = maxSemanticTopK
	}

	vec, err := t.embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		log.Printf("[note] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("向量化失败: %v", err)}
	}

	// 1. 全量嵌入搜索 (type=note)
	fullHits := t.vectorStore.Search(vec, a.TopK*2, func(id string, _ []float64, metadata map[string]string) bool {
		if metadata["type"] != "note" || metadata["user_id"] != ec.UserID {
			return false
		}
		if metaTopic, ok := metadata["topic"]; ok && metaTopic != "" && a.Topic != "" && metaTopic != a.Topic {
			return false
		}
		return true
	})

	// 2. chunk 搜索 (type=chunk, parent_type=note)
	chunkHits := t.vectorStore.Search(vec, a.TopK*2, func(id string, _ []float64, metadata map[string]string) bool {
		if metadata["type"] != "chunk" || metadata["parent_type"] != "note" || metadata["user_id"] != ec.UserID {
			return false
		}
		if metaTopic, ok := metadata["topic"]; ok && metaTopic != "" && a.Topic != "" && metaTopic != a.Topic {
			return false
		}
		return true
	})

	// TODO 增加fullHits & chunkHits 的长度log

	// 合并两路结果，按 note ID 取最高分
	scoreMap := make(map[string]float64)
	for _, hit := range fullHits {
		// full-note ID 格式: "note:{noteID}"
		id := strings.TrimPrefix(hit.ID, "note:")
		if old, ok := scoreMap[id]; !ok || hit.Score > old {
			scoreMap[id] = hit.Score
		}
	}
	for _, hit := range chunkHits {
		// chunk ID 格式: "chunk:note:{noteID}/{i}"
		id := strings.TrimPrefix(hit.ID, "chunk:note:")
		if idx := strings.Index(id, "/"); idx > 0 {
			id = id[:idx]
		}
		if id == "" {
			continue
		}
		if old, ok := scoreMap[id]; !ok || hit.Score > old {
			scoreMap[id] = hit.Score
		}
	}

	var noteIDs []string
	for id := range scoreMap {
		noteIDs = append(noteIDs, id)
	}

	if len(noteIDs) == 0 {
		data, _ := json.Marshal(map[string]interface{}{"total": 0, "notes": []ToolNoteOutput{}})
		return &Result{Success: true, Data: data}
	}

	notes, err := t.noteRepo.GetByIDsAndUser(ec.Ctx, noteIDs, ec.UserID)
	if err != nil {
		log.Printf("[note] GetByIDsAndUser error: %v", err)
	}

	output := make([]ToolNoteOutput, 0, len(notes))
	for _, n := range notes {
		score := scoreMap[n.ID]
		no := formatNote(n)
		no.RelevanceScore = &score
		output = append(output, no)
	}

	data, _ := json.Marshal(map[string]interface{}{"total": len(output), "notes": output})
	return &Result{Success: true, Data: data}
}

// ==================== count_notes ====================

func (t *NoteTool) countNotes(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query     string `json:"query"`
		Topic     string `json:"topic"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		LastNDays int    `json:"last_n_days"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}

	after, before, err := parseTimeParams(a.StartTime, a.EndTime, a.LastNDays, 0)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	var keywords []string
	if a.Query != "" {
		keywords = splitKeywords(a.Query)
	}

	count, err := t.noteRepo.CountNotes(ec.Ctx, ec.UserID, keywords, a.Topic, after, before)
	if err != nil {
		log.Printf("[note] count_notes error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("统计失败: %v", err)}
	}

	data, _ := json.Marshal(map[string]interface{}{"count": count})
	return &Result{Success: true, Data: data}
}

// ==================== format helpers ====================

func formatNote(n model.Note) ToolNoteOutput {
	no := ToolNoteOutput{
		ID:        n.ID,
		Topic:     n.Topic,
		Content:   n.Content,
		Tags:      n.Tags,
		CreatedAt: formatMsgTime(n.CreatedAt),
		UpdatedAt: formatMsgTime(n.UpdatedAt),
	}
	if n.Variables != nil {
		no.Variables = json.RawMessage(*n.Variables)
	}
	return no
}
