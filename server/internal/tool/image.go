package tool

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 图片搜索参数
const (
	imageSearchTopKDefault = 10
	imageSearchTopKMax     = 20
	// imageSearchThreshold 语义搜索的相关度下限（与 memory/note 的 0.5 一致）
	imageSearchThreshold = 0.5
)

// ImageTool — 图片工具：列出/搜索用户上传过的图片（含描述），从中挑图写进文档或 HTML。
// 只读；图片由上传产生，本工具不新增/删除图片。
type ImageTool struct {
	repo     *service.ImageAssetRepo
	store    *service.ImageStore
	embedder app.Embedder
	vs       app.VectorStore
}

// NewImageTool 创建图片工具
func NewImageTool(repo *service.ImageAssetRepo, store *service.ImageStore, embedder app.Embedder, vs app.VectorStore) *ImageTool {
	if store == nil {
		panic("image tool: nil image store (wiring bug)")
	}
	if embedder == nil || vs == nil {
		panic("image tool: nil embedder/vector store (wiring bug)")
	}
	return &ImageTool{repo: repo, store: store, embedder: embedder, vs: vs}
}

func (t *ImageTool) Name() string { return "image" }

func (t *ImageTool) Description() string {
	return "查看和搜索用户上传过的图片。需要给文档配图、或把图放进 HTML 页面（手账/图文混排）时，先在这里找到合适的图。" +
		"list_images 按时间浏览（scope=turn 看用户本轮刚发的图，scope=all 看全部）；" +
		"search_images 按描述搜索：关键词（精确词）或语义（「海边那张」这类描述性查询）。" +
		"每条带一句内容描述，按描述挑图，不必让模型逐张识图。" +
		"选中后：文档正文用返回的 markdown 字段原样嵌入；HTML 页面用返回的 html 字段原样嵌入。" +
		"图片一旦被文档引用，会自动变为可公开分享。描述可能暂时为空（尚未生成）。"
}

func (t *ImageTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "图片。浏览/搜索用户上传的图片（含描述），给文档或 HTML 页面配图时用来挑图。",
		Funcs:     []string{"list_images", "search_images"},
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
		{
			Name:        "search_images",
			Display:     "搜索图片",
			Description: "按图片描述搜索用户的图片。mode=keyword 匹配描述里的词（精确、免费）；mode=semantic 按语义找（如「海边那张照片」），只覆盖描述已生成的图。返回结构与 list_images 相同，语义模式每条多一个 score。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "搜索词或自然语言查询"},
					"mode": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"keyword", "semantic"},
						"description": "keyword = 匹配描述里的词（默认）；semantic = 按语义找",
					},
					"top_k": map[string]interface{}{"type": "integer", "description": fmt.Sprintf("返回条数上限，默认 %d，最大 %d", imageSearchTopKDefault, imageSearchTopKMax)},
				},
				"required": []interface{}{"query"},
			},
		},
	}
}

// ReadOnlyFuncs — 图片工具全部只读
func (t *ImageTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{"list_images": true, "search_images": true}
}

func (t *ImageTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "list_images":
		return t.listImages(ec, args)
	case "search_images":
		return t.searchImages(ec, args)
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
			return t.listResult(nil, 0, "本轮没有附件图片；要看用户上传过的其它图，用 scope=all。", nil)
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

	return t.listResult(list, total, "文档正文用 markdown 字段原样嵌入；HTML 页面用 html 字段原样嵌入。描述可能暂时为空（尚未生成）。", nil)
}

// ==================== 搜索 ====================

func (t *ImageTool) searchImages(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Query string `json:"query"`
		Mode  string `json:"mode"`
		TopK  *int   `json:"top_k"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return &Result{Success: false, Error: "query is required"}
	}
	topK := imageSearchTopKDefault
	if req.TopK != nil && *req.TopK > 0 {
		topK = *req.TopK
	}
	if topK > imageSearchTopKMax {
		topK = imageSearchTopKMax
	}

	switch strings.ToLower(strings.TrimSpace(req.Mode)) {
	case "", "keyword":
		return t.searchImagesByKeyword(ec, query, topK)
	case "semantic":
		return t.searchImagesSemantically(ec, query, topK)
	default:
		return &Result{Success: false, Error: "mode 只能是 keyword 或 semantic"}
	}
}

func (t *ImageTool) searchImagesByKeyword(ec *ExecuteContext, query string, topK int) *Result {
	keywords := splitKeywords(query)
	if len(keywords) == 0 {
		return &Result{Success: false, Error: "no valid keywords extracted from input"}
	}
	list, err := t.repo.SearchByDescription(ec.Ctx, ec.UserID, keywords, topK)
	if err != nil {
		return &Result{Success: false, Error: "search images: " + err.Error()}
	}
	return t.listResult(list, int64(len(list)),
		"关键词只匹配图片描述；total 为本次返回条数（已按 top_k 截断）；描述尚未生成的图搜不到，可改用 list_images 按时间浏览。markdown 只用于文档正文，HTML 页面用 html 字段。", nil)
}

func (t *ImageTool) searchImagesSemantically(ec *ExecuteContext, query string, topK int) *Result {
	vec, err := t.embedder.Embed(ec.Ctx, query)
	if err != nil {
		return &Result{Success: false, Error: "embed query failed: " + err.Error()}
	}
	// 取 2 倍候选：阈值过滤和已删除记录都会吃掉一部分名额，留余量给 DB 过滤后再截断。
	matches := t.vs.Search(vec, topK*2, func(_ string, _ []float64, meta map[string]string) bool {
		return meta["type"] == model.VectorTypeImage && meta["user_id"] == ec.UserID
	})

	scores := make(map[string]float64, len(matches))
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		if m.Score < imageSearchThreshold {
			continue
		}
		id, ok := strings.CutPrefix(m.ID, model.VectorPrefixImage)
		if !ok {
			continue
		}
		ids = append(ids, id)
		scores[id] = m.Score
	}
	if len(ids) == 0 {
		return t.listResult(nil, 0,
			"没有匹配的图片描述。可改用 mode=keyword 搜具体词，或用 list_images 按时间浏览。", nil)
	}

	// 向量只产候选，结果一律回 DB 取：已删除的图在这里被自然过滤掉。
	assets, err := t.repo.GetByIDsAndUser(ec.Ctx, ids, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "search images: " + err.Error()}
	}
	byID := make(map[string]model.ImageAsset, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
	}
	list := make([]model.ImageAsset, 0, len(assets))
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			list = append(list, a)
		}
	}
	// DB 过滤后可能仍超过 top_k（取的是 2 倍候选），这里截回契约上限。
	if len(list) > topK {
		list = list[:topK]
	}
	return t.listResult(list, int64(len(list)),
		"按描述语义匹配（score 越高越相关），只覆盖描述已生成的图；total 为本次返回条数（已按 top_k 截断）。markdown 只用于文档正文，HTML 页面用 html 字段。", scores)
}

func (t *ImageTool) listResult(list []model.ImageAsset, total int64, hint string, scores map[string]float64) *Result {
	type item struct {
		ID          string  `json:"id"`
		Description string  `json:"description"`
		Markdown    string  `json:"markdown"`
		HTML        string  `json:"html"`
		Bytes       int64   `json:"bytes"`
		Source      string  `json:"source"`
		Available   bool    `json:"available"`
		CreatedAt   string  `json:"created_at"`
		Score       float64 `json:"score,omitempty"`
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
			Score:       scores[a.ID],
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
