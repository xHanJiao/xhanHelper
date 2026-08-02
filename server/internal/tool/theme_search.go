package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/service"
)

const themeSearchThreshold = 0.55

// ThemeSearchTool — 对话 LLM 可调用的主题检索工具
type ThemeSearchTool struct {
	themeRepo   *service.ThemeStateRepo
	embedder    app.Embedder
	vectorStore app.VectorStore
}

func NewThemeSearchTool(repo *service.ThemeStateRepo, embedder app.Embedder, vectorStore app.VectorStore) *ThemeSearchTool {
	return &ThemeSearchTool{themeRepo: repo, embedder: embedder, vectorStore: vectorStore}
}

func (t *ThemeSearchTool) Name() string { return "theme_search" }

func (t *ThemeSearchTool) Description() string {
	return "主题代表用户身上正在发生或追踪的事件（如\"戒酒计划\"\"求职\"\"健身\"等），由系统自动发现和维护状态页。通过此工具可语义搜索或关键词搜索现有主题，了解用户当前生活中有哪些正在发生的事及其进展。当你想了解用户近况、某个领域的状态变化时，先搜索主题是最高效的入口，比直接翻消息或笔记更能抓住全局。"
}

func (t *ThemeSearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "semantic_search",
			Display:     "语义搜索",
			Description: "用向量语义相似度搜索主题。输入应为自然语言描述句（如'最近用户戒酒的情况'），而非空格分隔的关键词组。搜索覆盖主题的名称、描述和状态文本。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "自然语言搜索描述"},
					"top_k": map[string]interface{}{"type": "integer", "description": "返回条数，默认5，最大10"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "search_by_keyword",
			Display:     "关键词搜索",
			Description: "按关键词搜索主题的名称、描述和状态文本。多关键词用空格/逗号/顿号分隔为 OR 关系。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "搜索关键词，空格/逗号/顿号分隔多个词"},
					"limit": map[string]interface{}{"type": "integer", "description": "返回条数，默认10"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "list_active_themes",
			Display:     "列出活跃主题",
			Description: "列出用户当前所有活跃主题（即系统正在追踪的事件），返回每个主题的 id、name、description、status 和 state_text（如有）。用于了解用户身上正在发生哪些事。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
				"required":   []string{},
			},
		},
		{
			Name:        "get_detail",
			Display:     "获取主题详情",
			Description: "根据主题 id 获取完整状态页，包含 name、description、status、state_text（完整状态文本）、duration、created_at、updated_at。用于深入了解某个主题的完整进展历史。id 可从上下文中的 <topic id=\"...\"> 属性获取。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "主题 ID，来自上下文中 <topic> 的 id 属性"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *ThemeSearchTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *ThemeSearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "semantic_search":
		return t.semanticSearch(ec, args)
	case "search_by_keyword":
		return t.searchByKeyword(ec, args)
	case "list_active_themes":
		return t.listActiveThemes(ec, args)
	case "get_detail":
		return t.getDetail(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

func (t *ThemeSearchTool) semanticSearch(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
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
		log.Printf("[theme_search] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("向量化失败: %v", err)}
	}

	fullHits := t.vectorStore.Search(vec, a.TopK*2, func(_ string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == "theme" && metadata["user_id"] == ec.UserID
	})

	chunkHits := t.vectorStore.Search(vec, a.TopK*2, func(_ string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == "chunk" &&
			metadata["parent_type"] == "theme" &&
			metadata["user_id"] == ec.UserID
	})

	scoreMap := make(map[string]float64)
	for _, hit := range fullHits {
		themeID := hit.Metadata["theme_id"]
		if themeID == "" {
			continue
		}
		if old, ok := scoreMap[themeID]; !ok || hit.Score > old {
			scoreMap[themeID] = hit.Score
		}
	}
	for _, hit := range chunkHits {
		themeID := hit.Metadata["parent_id"]
		if themeID == "" {
			continue
		}
		if old, ok := scoreMap[themeID]; !ok || hit.Score > old {
			scoreMap[themeID] = hit.Score
		}
	}

	var topHits []scoredHit
	for id, score := range scoreMap {
		if score < themeSearchThreshold {
			continue
		}
		topHits = append(topHits, scoredHit{ID: id, Score: score})
	}

	if len(topHits) == 0 {
		log.Printf("[theme_search] semantic_search zero results: hits=%d threshold=%.2f", len(scoreMap), themeSearchThreshold)
		data, _ := json.Marshal(map[string]interface{}{"total": 0, "themes": []interface{}{}})
		return &Result{Success: true, Data: data}
	}

	sortScoredHits(topHits)
	if len(topHits) > a.TopK {
		topHits = topHits[:a.TopK]
	}

	ids := make([]string, len(topHits))
	for i, h := range topHits {
		ids[i] = h.ID
		scoreMap[h.ID] = h.Score
	}

	themeMap, err := t.themeRepo.GetByIDs(ec.Ctx, ids, ec.UserID)
	if err != nil {
		log.Printf("[theme_search] GetByIDs error: %v", err)
	}
	log.Printf("[theme_search] semantic_search: requested=%d got=%d", len(ids), len(themeMap))

	output := make([]map[string]interface{}, 0, len(themeMap))
	for id, th := range themeMap {
		score := scoreMap[id]
		m := map[string]interface{}{
			"id":              th.ID,
			"name":            th.Name,
			"description":     th.Description,
			"status":          th.Status,
			"relevance_score": score,
		}
		if th.StateText != "" {
			m["state_text"] = th.StateText
		}
		output = append(output, m)
	}

	data, _ := json.Marshal(map[string]interface{}{
		"total":  len(output),
		"themes": output,
	})
	return &Result{Success: true, Data: data}
}

func (t *ThemeSearchTool) searchByKeyword(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if a.Limit <= 0 {
		a.Limit = 10
	}

	keywords := splitKeywords(a.Query)
	if len(keywords) == 0 {
		return &Result{Success: false, Error: "query 未产生有效关键词"}
	}

	themes, err := t.themeRepo.ListActive(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[theme_search] search_by_keyword ListActive error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}

	matched := make([]map[string]interface{}, 0)
	for _, th := range themes {
		searchText := th.Name + " " + th.Description + " " + th.StateText
		found := false
		for _, kw := range keywords {
			if strings.Contains(searchText, kw) {
				found = true
				break
			}
		}
		if found {
			m := map[string]interface{}{
				"id":          th.ID,
				"name":        th.Name,
				"description": th.Description,
				"status":      th.Status,
			}
			if th.StateText != "" {
				m["state_text"] = th.StateText
			}
			matched = append(matched, m)
		}
	}

	if len(matched) > a.Limit {
		matched = matched[:a.Limit]
	}

	data, _ := json.Marshal(map[string]interface{}{
		"total":  len(matched),
		"themes": matched,
	})
	return &Result{Success: true, Data: data}
}

func (t *ThemeSearchTool) getDetail(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.ID == "" {
		return &Result{Success: false, Error: "id 不能为空"}
	}

	th, err := t.themeRepo.GetByID(ec.Ctx, a.ID, ec.UserID)
	if err != nil {
		log.Printf("[theme_search] get_detail GetByID error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}
	if th == nil {
		return &Result{Success: false, Error: "主题不存在"}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":          th.ID,
		"name":        th.Name,
		"description": th.Description,
		"status":      th.Status,
		"duration":    th.Duration,
		"state_text":  th.StateText,
		"created_at":  th.CreatedAt,
		"updated_at":  th.UpdatedAt,
	})
	return &Result{Success: true, Data: data}
}

func (t *ThemeSearchTool) listActiveThemes(ec *ExecuteContext, _ json.RawMessage) *Result {
	themes, err := t.themeRepo.ListActive(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[theme_search] list_active_themes error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}

	output := make([]map[string]interface{}, 0, len(themes))
	for _, th := range themes {
		m := map[string]interface{}{
			"id":          th.ID,
			"name":        th.Name,
			"description": th.Description,
			"status":      th.Status,
		}
		if th.StateText != "" {
			m["state_text"] = th.StateText
		}
		output = append(output, m)
	}

	data, _ := json.Marshal(map[string]interface{}{
		"total":  len(output),
		"themes": output,
	})
	return &Result{Success: true, Data: data}
}
