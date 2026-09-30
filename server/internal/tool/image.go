package tool

import (
	"encoding/json"
	"fmt"

	"xhan-helper/internal/service"
)

// ImageTool — 图片列表工具：让 LLM 看到用户上传过的图片（含描述），从中挑图写进文档。
// 只读；图片由上传产生，本工具不新增/删除图片。
type ImageTool struct {
	repo *service.ImageAssetRepo
}

// NewImageTool 创建图片工具
func NewImageTool(repo *service.ImageAssetRepo) *ImageTool {
	return &ImageTool{repo: repo}
}

func (t *ImageTool) Name() string { return "image" }

func (t *ImageTool) Description() string {
	return "查看用户上传过的图片列表。需要给文档配图（把用户传的图放进正文）时，先在这里找到合适的图。" +
		"每条带一句内容描述，按描述挑图，不必让模型逐张识图。选中后把返回的 markdown 字段原样写进文档正文即可；" +
		"图片一旦被文档引用，会自动变为可公开分享。描述为空表示尚未生成（稍后重试）。"
}

func (t *ImageTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "图片。查看用户上传的图片列表（含描述），给文档配图时用来挑图。",
		Funcs:     []string{"list_images"},
	}, true
}

func (t *ImageTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "list_images",
			Display:     "图片列表",
			Description: "列出用户上传的图片（新的在前），返回 id、描述、可直接嵌入 Markdown 的 markdown 片段（![](/images/<id>.<ext>)）与来源。默认每页 20 条。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit":  map[string]interface{}{"type": "integer", "description": fmt.Sprintf("每页条数，默认 %d，上限 %d；传 0 只取总数返回空列表", defaultDocPageSize, maxDocPageSize)},
					"offset": map[string]interface{}{"type": "integer", "description": "偏移量，默认 0，不能为负"},
				},
			},
		},
	}
}

// ReadOnlyFuncs — 图片工具全部只读
func (t *ImageTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{"list_images": true}
}

func (t *ImageTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "list_images":
		return t.listImages(ec, args)
	default:
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}
}

func (t *ImageTool) listImages(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Limit  *int `json:"limit"`
		Offset *int `json:"offset"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	limit, offset, errMsg := resolvePaging(req.Limit, req.Offset)
	if errMsg != "" {
		return &Result{Success: false, Error: errMsg}
	}

	list, total, err := t.repo.ListByUser(ec.Ctx, ec.UserID, limit, offset)
	if err != nil {
		return &Result{Success: false, Error: "list images: " + err.Error()}
	}

	type item struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Markdown    string `json:"markdown"`
		Source      string `json:"source"`
		CreatedAt   string `json:"created_at"`
	}
	items := make([]item, 0, len(list))
	for _, a := range list {
		items = append(items, item{
			ID:          a.ID,
			Description: a.Description,
			Markdown:    fmt.Sprintf("![](/images/%s.%s)", a.ID, a.Ext),
			Source:      a.Source,
			CreatedAt:   a.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"images": items,
		"total":  total,
		"hint":   "把 markdown 字段原样嵌入文档正文即可配图；描述为空表示尚未生成。",
	})
	return &Result{Success: true, Data: data}
}
