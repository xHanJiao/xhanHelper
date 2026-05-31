package tool

import (
	"encoding/json"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util"
)

// DocumentTool — 文档管理工具。仅在用户明确要求时使用，不要主动创建。
type DocumentTool struct {
	docRepo     *service.DocumentRepo
	versionRepo *service.VersionRepo
}

func NewDocumentTool(docRepo *service.DocumentRepo, versionRepo *service.VersionRepo) *DocumentTool {
	return &DocumentTool{docRepo: docRepo, versionRepo: versionRepo}
}

func (t *DocumentTool) Name() string { return "document" }

func (t *DocumentTool) Description() string {
	return "管理用户的结构化 Markdown 文档。仅在用户明确要求生成/保存/修改/查询/删除文章、报告、总结等文档时使用。不要主动创建或修改文档。日常的信息记录请用 note 工具。"
}

func (t *DocumentTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_document",
			Description: "创建一篇新的 Markdown 文档。仅当用户明确说写一篇、生成文档、保存为文档、总结成文章等时调用。不要主动调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title":   map[string]interface{}{"type": "string", "description": "文档标题"},
					"content": map[string]interface{}{"type": "string", "description": "Markdown 格式的文档正文"},
					"tags":    map[string]interface{}{"type": "string", "description": "逗号分隔的标签，用于分类检索，如 技术,Go,后端"},
				},
				"required": []string{"title", "content"},
			},
		},
		{
			Name:        "get_document",
			Description: "获取文档完整内容（含版本列表）。修改文档前必须先调用此函数读取当前内容。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "文档 ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "update_document",
			Description: "修改已有文档。修改前请先用 get_document 读取完整内容，在其基础上修改后传入。仅更新传入的非空字段。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      map[string]interface{}{"type": "string", "description": "文档 ID"},
					"title":   map[string]interface{}{"type": "string", "description": "新的文档标题（可选）"},
					"content": map[string]interface{}{"type": "string", "description": "新的 Markdown 正文（可选）"},
					"tags":    map[string]interface{}{"type": "string", "description": "新的标签（可选）"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "rollback_document",
			Description: "将文档回滚到指定历史版本。版本号从 get_document_versions 获取。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      map[string]interface{}{"type": "string", "description": "文档 ID"},
					"version": map[string]interface{}{"type": "integer", "description": "要回滚到的版本号"},
				},
				"required": []string{"id", "version"},
			},
		},
		{
			Name:        "get_document_versions",
			Description: "获取文档的所有历史版本列表。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "文档 ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "query_documents",
			Description: "查询文档列表，可按标签或关键词筛选。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tag":     map[string]interface{}{"type": "string", "description": "按标签筛选（可选）"},
					"keyword": map[string]interface{}{"type": "string", "description": "按标题和内容搜索关键词（可选）"},
				},
			},
		},
		{
			Name:        "list_documents",
			Description: "获取文档列表（按更新时间倒序）。返回 title、tags、preview（正文前50字），不含全文。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit":  map[string]interface{}{"type": "integer", "description": "每页条数，默认 20"},
					"offset": map[string]interface{}{"type": "integer", "description": "偏移量，默认 0"},
				},
			},
		},
		{
			Name:        "list_tags",
			Description: "获取当前用户所有文档中使用过的标签列表。用于了解有哪些标签可供 query_documents 筛选。",
		},
		{
			Name:        "delete_document",
			Description: "删除一篇文档。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "要删除的文档 ID"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *DocumentTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_document":
		return t.createDocument(ec, args)
	case "get_document":
		return t.getDocument(ec, args)
	case "update_document":
		return t.updateDocument(ec, args)
	case "rollback_document":
		return t.rollbackDocument(ec, args)
	case "query_documents":
		return t.queryDocuments(ec, args)
	case "delete_document":
		return t.deleteDocument(ec, args)
	case "list_documents":
		return t.listDocuments(ec, args)
	case "list_tags":
		return t.listTags(ec)
	case "get_document_versions":
		return t.getDocumentVersions(ec, args)
	default:
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}
}

func (t *DocumentTool) createDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		Tags    string `json:"tags"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.Title == "" || req.Content == "" {
		return &Result{Success: false, Error: "title and content required"}
	}

	doc := &model.Document{
		ID:      uuid.New().String(),
		UserID:  ec.UserID,
		Title:   req.Title,
		Content: req.Content,
		Tags:    req.Tags,
	}

	if err := t.docRepo.Create(ec.Ctx, doc); err != nil {
		return &Result{Success: false, Error: "create document: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":      doc.ID,
		"title":   doc.Title,
		"message": "文档已保存",
	})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) getDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}

	doc, err := t.docRepo.GetByID(ec.Ctx, req.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "document not found: " + err.Error()}
	}

	result := map[string]interface{}{
		"id":         doc.ID,
		"title":      doc.Title,
		"content":    doc.Content,
		"tags":       doc.Tags,
		"created_at": doc.CreatedAt,
		"updated_at": doc.UpdatedAt,
	}
	if doc.ConversationID != nil {
		result["conversation_id"] = *doc.ConversationID
	}

	// 附带版本历史列表
	versions, err := t.versionRepo.GetVersions(ec.Ctx, "document", req.ID)
	if err != nil {
		log.Printf("[document] get versions for get_document: %v", err)
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

func (t *DocumentTool) getDocumentVersions(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}

	// 先确认文档存在
	if _, err := t.docRepo.GetByID(ec.Ctx, req.ID, ec.UserID); err != nil {
		return &Result{Success: false, Error: "document not found: " + err.Error()}
	}

	versions, err := t.versionRepo.GetVersions(ec.Ctx, "document", req.ID)
	if err != nil {
		return &Result{Success: false, Error: "get versions: " + err.Error()}
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

func (t *DocumentTool) updateDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
		Tags    string `json:"tags"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}

	// 更新前先创建版本快照
	current, err := t.docRepo.GetByID(ec.Ctx, req.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "document not found: " + err.Error()}
	}
	if err := t.versionRepo.CreateSnapshot(ec.Ctx, "document", req.ID, map[string]interface{}{
		"title":   current.Title,
		"content": current.Content,
		"tags":    current.Tags,
	}); err != nil {
		log.Printf("[document] create snapshot before update %s: %v", req.ID, err)
	}

	updates := map[string]interface{}{}
	if req.Title != "" {
		updates["title"] = req.Title
	}
	if req.Content != "" {
		updates["content"] = req.Content
	}
	if req.Tags != "" {
		updates["tags"] = req.Tags
	}
	if len(updates) == 0 {
		return &Result{Success: true, Data: []byte(`{"message":"nothing to update"}`)}
	}

	if err := t.docRepo.Update(ec.Ctx, req.ID, ec.UserID, updates); err != nil {
		return &Result{Success: false, Error: "update document: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":      req.ID,
		"message": "文档已更新",
	})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) rollbackDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.ID == "" || req.Version <= 0 {
		return &Result{Success: false, Error: "id and version required"}
	}

	// 确保文档存在
	current, err := t.docRepo.GetByID(ec.Ctx, req.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "document not found: " + err.Error()}
	}

	// 先创建当前版本快照
	if err := t.versionRepo.CreateSnapshot(ec.Ctx, "document", req.ID, map[string]interface{}{
		"title":   current.Title,
		"content": current.Content,
		"tags":    current.Tags,
	}); err != nil {
		log.Printf("[document] create snapshot before rollback %s: %v", req.ID, err)
	}

	// 获取目标版本快照
	snap, err := t.versionRepo.GetSnapshot(ec.Ctx, "document", req.ID, req.Version)
	if err != nil {
		return &Result{Success: false, Error: "version not found: " + err.Error()}
	}

	var snapData struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		Tags    string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(snap.Snapshot), &snapData); err != nil {
		return &Result{Success: false, Error: "parse snapshot: " + err.Error()}
	}

	updates := map[string]interface{}{
		"title":   snapData.Title,
		"content": snapData.Content,
		"tags":    snapData.Tags,
	}
	if err := t.docRepo.Update(ec.Ctx, req.ID, ec.UserID, updates); err != nil {
		return &Result{Success: false, Error: "rollback document: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":      req.ID,
		"version": req.Version,
		"message": "文档已回滚",
	})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) queryDocuments(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Tag     string `json:"tag"`
		Keyword string `json:"keyword"`
	}
	json.Unmarshal(args, &req)

	list, total, err := t.docRepo.List(ec.Ctx, ec.UserID, req.Tag, req.Keyword, 20, 0)
	if err != nil {
		return &Result{Success: false, Error: "query documents: " + err.Error()}
	}

	result := make([]map[string]interface{}, 0, len(list))
	for _, d := range list {
		item := map[string]interface{}{
			"id":         d.ID,
			"title":      d.Title,
			"tags":       d.Tags,
			"created_at": d.CreatedAt,
			"updated_at": d.UpdatedAt,
			"preview":    util.TruncateContent(d.Content, 200),
		}
		if d.ConversationID != nil {
			item["conversation_id"] = *d.ConversationID
		}
		result = append(result, item)
	}

	data, _ := json.Marshal(map[string]interface{}{
		"documents": result,
		"total":     total,
	})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) deleteDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}

	if err := t.docRepo.Delete(ec.Ctx, req.ID, ec.UserID); err != nil {
		return &Result{Success: false, Error: "delete document: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{"id": req.ID, "message": "文档已删除"})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) listDocuments(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	json.Unmarshal(args, &req)
	if req.Limit <= 0 {
		req.Limit = 20
	}

	list, total, err := t.docRepo.List(ec.Ctx, ec.UserID, "", "", req.Limit, req.Offset)
	if err != nil {
		return &Result{Success: false, Error: "list documents: " + err.Error()}
	}

	result := make([]map[string]interface{}, 0, len(list))
	for _, d := range list {
		result = append(result, map[string]interface{}{
			"id":         d.ID,
			"title":      d.Title,
			"tags":       d.Tags,
			"preview":    util.TruncateContent(d.Content, 50),
			"created_at": d.CreatedAt,
			"updated_at": d.UpdatedAt,
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"documents": result,
		"total":     total,
	})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) listTags(ec *ExecuteContext) *Result {
	tags, err := t.docRepo.ListTags(ec.Ctx, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "list tags: " + err.Error()}
	}
	data, _ := json.Marshal(map[string]interface{}{"tags": tags})
	return &Result{Success: true, Data: data}
}
