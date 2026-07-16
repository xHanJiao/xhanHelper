package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/service"
)

const summarySearchThreshold = 0.55

// GetSummaryTool — 获取/搜索对话摘要。
// 支持按 ID 获取 + 语义搜索 + 关键词搜索。
type GetSummaryTool struct {
	summaryRepo *service.ConversationSummaryRepo
	vectorStore app.VectorStore
	embedder    app.Embedder
}

func NewGetSummaryTool(summaryRepo *service.ConversationSummaryRepo, vectorStore app.VectorStore, embedder app.Embedder) *GetSummaryTool {
	return &GetSummaryTool{
		summaryRepo: summaryRepo,
		vectorStore: vectorStore,
		embedder:    embedder,
	}
}

func (t *GetSummaryTool) Name() string { return "get_summary" }

func (t *GetSummaryTool) Description() string {
	return "对话摘要由系统在每次对话 session 结束后自动提取，是对话主题和脉络的高层概括，适合快速了解用户过去某段时间聊了什么、发生了什么事。" +
		"query_by_time 按时间范围拉取摘要（回顾过去几天/几周的对话脉络首选）；search_summaries 用自然语言语义搜索；search_by_keyword 按关键词搜索；" +
		"get_summary 按 ID 获取单条摘要并沿 parent 链回溯。" +
		"相比直接查消息（message_search），摘要提供全局视角和浓缩话题，但丢失了细节信息。" +
		"找到感兴趣的话题后，可配合 message_search.search_by_range 查看覆盖范围内的原始消息。"
}

func (t *GetSummaryTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "get_summary",
			Display:     "获取摘要",
			Description: "根据摘要ID获取对话摘要。当你需要了解更早的对话脉络时，使用摘要中提供的 parent 属性作为 summary_id 来追溯前一段摘要。也可用 cover_from/cover_to 配合 message_search.search_by_range 查找该范围内的具体事实",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"summary_id": map[string]interface{}{"type": "string", "description": "摘要ID"},
				},
				"required": []string{"summary_id"},
			},
		},
		{
			Name:        "search_summaries",
			Display:     "语义搜索",
			Description: "用自然语言语义搜索所有历史对话摘要，返回按相关度排序的摘要列表。每个摘要包含文本、话题、时间范围、parent 链。获取感兴趣的摘要后可调用 get_summary 追溯更早的 parent 摘要，或调用 message_search.search_by_range 查看摘要覆盖的原始消息",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "自然语言搜索句子"},
					"top_k": map[string]interface{}{"type": "integer", "description": "返回条数上限，默认 5"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "query_by_time",
			Display:     "按时间查摘要",
			Description: "按时间范围拉取对话摘要，按时间倒序返回，每条带创建时间。用于快速了解用户在某段时间内聊了什么、发生了什么事，是回顾过去几天/几周对话脉络的首选方式。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_time": map[string]interface{}{"type": "string", "description": "起始时间，支持 2026-06-15 14:30:00 或 2026-06-15（纯日期按当日 00:00:00）"},
					"end_time":   map[string]interface{}{"type": "string", "description": "结束时间，格式同上（纯日期含当天全部数据）"},
					"limit":      map[string]interface{}{"type": "integer", "description": "返回条数，默认20，最大50"},
				},
				"required": []string{"start_time", "end_time"},
			},
		},
		{
			Name:        "search_by_keyword",
			Display:     "关键词搜索",
			Description: "按关键词在摘要文本中搜索。多关键词用空格/逗号/顿号分隔为 OR 关系。适合查找包含特定词汇的对话摘要。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "搜索关键词，空格/逗号/顿号分隔多个词"},
					"limit": map[string]interface{}{"type": "integer", "description": "返回条数，默认10"},
				},
				"required": []string{"query"},
			},
		},
	}
}

func (t *GetSummaryTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "get_summary":
		return t.getSummary(ec, args)
	case "search_summaries":
		return t.searchSummaries(ec, args)
	case "query_by_time":
		return t.queryByTime(ec, args)
	case "search_by_keyword":
		return t.searchByKeyword(ec, args)
	}
	return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
}

func (t *GetSummaryTool) getSummary(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		SummaryID string `json:"summary_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.SummaryID == "" {
		return &Result{Success: false, Error: "summary_id 不能为空"}
	}

	summary, err := t.summaryRepo.GetByID(ec.Ctx, a.SummaryID, ec.UserID)
	if err != nil {
		log.Printf("[get_summary] GetByID error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("获取摘要失败: %v", err)}
	}
	if summary == nil {
		return &Result{Success: false, Error: "摘要不存在或无权访问"}
	}

	parent := ""
	if summary.ParentSummaryID != nil {
		parent = *summary.ParentSummaryID
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":                summary.ID,
		"summary_text":      summary.SummaryText,
		"topics":            summary.Topics,
		"parent_summary_id": parent,
		"cover_from_msg_id": summary.CoverFromMsgID,
		"cover_to_msg_id":   summary.CoverToMsgID,
		"created_at":        summary.CreatedAt.In(config.BJ).Format("2006-01-02 15:04"),
	})
	return &Result{Success: true, Data: data}
}

func (t *GetSummaryTool) searchSummaries(ec *ExecuteContext, args json.RawMessage) *Result {
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
		a.TopK = 5
	}

	vec, err := t.embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		log.Printf("[search_summaries] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询嵌入失败: %v", err)}
	}

	topK := a.TopK * 5
	if topK < 10 {
		topK = 10
	}
	results := t.vectorStore.Search(vec, topK, func(_ string, _ []float64, meta map[string]string) bool {
		return meta["type"] == "summary" && meta["user_id"] == ec.UserID
	})

	chunkResults := t.vectorStore.Search(vec, topK, func(_ string, _ []float64, meta map[string]string) bool {
		return meta["type"] == "chunk" &&
			meta["parent_type"] == "summary" &&
			meta["user_id"] == ec.UserID
	})

	seen := make(map[string]float64)
	for _, r := range results {
		sid := r.Metadata["summary_id"]
		if sid == "" || r.Score < summarySearchThreshold {
			continue
		}
		if old, ok := seen[sid]; !ok || r.Score > old {
			seen[sid] = r.Score
		}
	}
	for _, r := range chunkResults {
		sid := r.Metadata["parent_id"]
		if sid == "" || r.Score < summarySearchThreshold {
			continue
		}
		if old, ok := seen[sid]; !ok || r.Score > old {
			seen[sid] = r.Score
		}
	}
	if len(seen) == 0 {
		return &Result{Success: true, Data: json.RawMessage("[]")}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	summaries, err := t.summaryRepo.GetByIDsForUser(ec.Ctx, ids, ec.UserID)
	if err != nil {
		log.Printf("[search_summaries] GetByIDsForUser error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("加载摘要失败: %v", err)}
	}

	textMap := make(map[string]string, len(summaries))
	for _, s := range summaries {
		textMap[s.ID] = s.SummaryText
	}

	var topHits []scoredHit
	for id, score := range seen {
		topHits = append(topHits, scoredHit{ID: id, Score: score})
	}
	sortScoredHits(topHits)
	if len(topHits) > a.TopK {
		topHits = topHits[:a.TopK]
	}

	out := make([]map[string]interface{}, 0, len(topHits))
	for _, h := range topHits {
		out = append(out, map[string]interface{}{
			"id":           h.ID,
			"summary_text": textMap[h.ID],
			"score":        h.Score,
		})
	}

	data, _ := json.Marshal(out)
	return &Result{Success: true, Data: data}
}

func (t *GetSummaryTool) queryByTime(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.StartTime == "" || a.EndTime == "" {
		return &Result{Success: false, Error: "start_time 和 end_time 不能为空"}
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}
	if a.Limit > 50 {
		a.Limit = 50
	}

	start, err := parseTimeString(a.StartTime)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	end, err := parseTimeString(a.EndTime)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	// 纯日期 end_time → 含当日，推到次日 00:00:00
	if isDateOnly(a.EndTime) {
		end = end.Add(24 * time.Hour)
	}
	if !start.Before(end) {
		return &Result{Success: false, Error: "start_time 必须早于 end_time"}
	}

	summaries, err := t.summaryRepo.ListByTimeRange(ec.Ctx, ec.UserID, start, end, a.Limit)
	if err != nil {
		log.Printf("[get_summary] query_by_time error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询摘要失败: %v", err)}
	}

	out := make([]map[string]interface{}, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, map[string]interface{}{
			"id":           s.ID,
			"summary_text": s.SummaryText,
			"topics":       s.Topics,
			"created_at":   s.CreatedAt.In(config.BJ).Format("2006-01-02 15:04:05"),
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"total":     len(out),
		"summaries": out,
	})
	return &Result{Success: true, Data: data}
}

func (t *GetSummaryTool) searchByKeyword(ec *ExecuteContext, args json.RawMessage) *Result {
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

	summaries, err := t.summaryRepo.SearchByKeyword(ec.Ctx, ec.UserID, keywords, a.Limit)
	if err != nil {
		log.Printf("[get_summary] search_by_keyword error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询摘要失败: %v", err)}
	}

	out := make([]map[string]interface{}, 0, len(summaries))
	for _, s := range summaries {
		parent := ""
		if s.ParentSummaryID != nil {
			parent = *s.ParentSummaryID
		}
		out = append(out, map[string]interface{}{
			"id":                s.ID,
			"summary_text":      s.SummaryText,
			"topics":            s.Topics,
			"parent_summary_id": parent,
			"cover_from_msg_id": s.CoverFromMsgID,
			"cover_to_msg_id":   s.CoverToMsgID,
			"created_at":        s.CreatedAt.In(config.BJ).Format("2006-01-02 15:04"),
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"total":     len(out),
		"summaries": out,
	})
	return &Result{Success: true, Data: data}
}
