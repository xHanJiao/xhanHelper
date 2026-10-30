package tool

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ImageTool — 图片列表工具：让 LLM 看到用户上传过的图片（含描述），从中挑图写进文档。
// 只读；图片由上传产生，本工具不新增/删除图片。
type ImageTool struct {
	repo  *service.ImageAssetRepo
	store *service.ImageStore
}

// NewImageTool 创建图片工具
func NewImageTool(repo *service.ImageAssetRepo, store *service.ImageStore) *ImageTool {
	if store == nil {
		panic("image tool: nil image store (wiring bug)")
	}
	return &ImageTool{repo: repo, store: store}
}

func (t *ImageTool) Name() string { return "image" }

func (t *ImageTool) Description() string {
	return "查看用户上传过的图片列表。需要给文档配图、或把图放进 HTML 页面（手账/图文混排）时，先在这里找到合适的图。" +
		"scope=turn 只看用户本轮刚发的图（最常用）；scope=all 看全部（新的在前）。" +
		"每条带一句内容描述，按描述挑图，不必让模型逐张识图。" +
		"选中后：文档正文用返回的 markdown 字段原样嵌入；HTML 页面用返回的 html 字段原样嵌入。" +
		"图片一旦被文档引用，会自动变为可公开分享。描述可能暂时为空（尚未生成）。"
}

func (t *ImageTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "图片。查看用户上传的图片列表（含描述），给文档或 HTML 页面配图时用来挑图。",
		Funcs:     []string{"list_images"},
	}, true
}

func (t *ImageTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "list_images",
			Display:     "图片列表",
			Description: "列出用户的图片，返回 id、描述、可直接嵌入的片段与来源。scope=turn 只列本轮用户发的图（不分页）；scope=all 列全部（新的在前，默认每页 20 条）。available:false 表示图片文件已丢失（迁移/外部删除遗留），别用，让用户重发。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"turn", "all"},
						"description": "turn = 本轮用户发的图；all = 全部（默认）。",
					},
					"limit":  map[string]interface{}{"type": "integer", "description": fmt.Sprintf("每页条数（仅 scope=all），默认 %d，上限 %d；传 0 只取总数返回空列表", defaultDocPageSize, maxDocPageSize)},
					"offset": map[string]interface{}{"type": "integer", "description": "偏移量（仅 scope=all），默认 0，不能为负"},
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
		Scope  string `json:"scope"`
		Limit  *int   `json:"limit"`
		Offset *int   `json:"offset"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	var list []model.ImageAsset
	var total int64
	if strings.EqualFold(strings.TrimSpace(req.Scope), "turn") {
		// 本轮附件：按消息里的 id 顺序返回，不分页（一次最多几张）。
		if len(ec.UserImageIDs) == 0 {
			return t.listResult(nil, 0, "本轮没有附件图片；要看用户上传过的其它图，用 scope=all。")
		}
		assets, err := t.repo.GetByIDsAndUser(ec.Ctx, ec.UserImageIDs, ec.UserID)
		if err != nil {
			return &Result{Success: false, Error: "list images: " + err.Error()}
		}
		byID := make(map[string]model.ImageAsset, len(assets))
		for _, a := range assets {
			byID[a.ID] = a
		}
		for _, id := range ec.UserImageIDs {
			if a, ok := byID[id]; ok {
				list = append(list, a)
			}
		}
		total = int64(len(list))
	} else {
		limit, offset, errMsg := resolvePaging(req.Limit, req.Offset)
		if errMsg != "" {
			return &Result{Success: false, Error: errMsg}
		}
		var err error
		list, total, err = t.repo.ListByUser(ec.Ctx, ec.UserID, limit, offset)
		if err != nil {
			return &Result{Success: false, Error: "list images: " + err.Error()}
		}
	}

	return t.listResult(list, total, "文档正文用 markdown 字段原样嵌入；HTML 页面用 html 字段原样嵌入。描述可能暂时为空（尚未生成）。")
}

func (t *ImageTool) listResult(list []model.ImageAsset, total int64, hint string) *Result {
	type item struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Markdown    string `json:"markdown"`
		HTML        string `json:"html"`
		Bytes       int64  `json:"bytes"`
		Source      string `json:"source"`
		Available   bool   `json:"available"`
		CreatedAt   string `json:"created_at"`
	}
	items := make([]item, 0, len(list))
	missing := 0
	for i := range list {
		a := &list[i]
		ok := t.store.HasFile(a)
		if !ok {
			missing++
		}
		alt := a.Description
		if alt == "" {
			alt = a.Source
		}
		items = append(items, item{
			ID:          a.ID,
			Description: a.Description,
			Markdown:    fmt.Sprintf("![](/images/%s.%s)", a.ID, a.Ext),
			HTML:        fmt.Sprintf("<img src=\"image:%s\" alt=\"%s\">", a.ID, html.EscapeString(alt)),
			Bytes:       a.Bytes,
			Source:      a.Source,
			Available:   ok,
			CreatedAt:   a.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	if missing > 0 {
		hint += fmt.Sprintf("其中 %d 张的图片文件已丢失（available:false），别用——让用户重发那张图。", missing)
	}
	data, err := json.Marshal(map[string]interface{}{
		"images": items,
		"total":  total,
		"hint":   hint,
	})
	if err != nil {
		return &Result{Success: false, Error: "结果序列化失败: " + err.Error()}
	}
	return &Result{Success: true, Data: data}
}
