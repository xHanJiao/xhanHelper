package tool

import (
	"encoding/json"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util"
)

// DocumentTool — 文档管理工具。仅在用户明确要求时使用，不要主动创建。
type DocumentTool struct {
	docRepo *service.DocumentRepo
}

func NewDocumentTool(docRepo *service.DocumentRepo) *DocumentTool {
	return &DocumentTool{docRepo: docRepo}
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
			Name:        "update_document",
			Description: "修改已有文档的标题、内容或标签。仅更新传入的非空字段。",
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
	case "update_document":
		return t.updateDocument(ec, args)
	case "query_documents":
		return t.queryDocuments(ec, args)
	case "delete_document":
		return t.deleteDocument(ec, args)
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
