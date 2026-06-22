package tool

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// GetSummaryTool — 获取/搜索对话摘要。
// 支持按 ID 获取单条摘要 + 语义搜索历史摘要。
type GetSummaryTool struct {
	summaryRepo *service.ConversationSummaryRepo
	vectorStore app.VectorStore
	embedder    *llm.Embedder
}

func NewGetSummaryTool(summaryRepo *service.ConversationSummaryRepo, vectorStore app.VectorStore, embedder *llm.Embedder) *GetSummaryTool {
	return &GetSummaryTool{
		summaryRepo: summaryRepo,
		vectorStore: vectorStore,
		embedder:    embedder,
	}
}

func (t *GetSummaryTool) Name() string { return "get_summary" }

func (t *GetSummaryTool) Description() string {
	return "获取或搜索对话摘要。get_summary 按 ID 获取单条摘要并沿 parent 链回溯；search_summaries 用自然语言语义搜索所有历史摘要"
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
			Display:     "搜索摘要",
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
	}
}

func (t *GetSummaryTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "get_summary":
		return t.getSummary(ec, args)
	case "search_summaries":
		return t.searchSummaries(ec, args)
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

	// embed 查询
	vec, err := t.embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		log.Printf("[search_summaries] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询嵌入失败: %v", err)}
	}

	// 向量搜索：拉取足够候选项供后续去重+排序
	topK := a.TopK * 5
	if topK < 10 {
		topK = 10
	}
	results := t.vectorStore.Search(vec, topK, func(id string, vec []float64, meta map[string]string) bool {
		return meta["type"] == "summary" && meta["user_id"] == ec.UserID
	})

	// 按 summary_id 去重，取最高分
	seen := make(map[string]float64)
	for _, r := range results {
		sid := r.Metadata["summary_id"]
		if sid == "" {
			continue
		}
		if old, ok := seen[sid]; !ok || r.Score > old {
			seen[sid] = r.Score
		}
	}
	if len(seen) == 0 {
		return &Result{Success: true, Data: json.RawMessage("[]")}
	}

	// 批量加载摘要（DB 层做 user_id 纵深防御过滤）
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	summaries, err := t.summaryRepo.GetByIDsForUser(ec.Ctx, ids, ec.UserID)
	if err != nil {
		log.Printf("[search_summaries] GetByIDsForUser error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("加载摘要失败: %v", err)}
	}

	// 按 score 排序，截取 topK
	type item struct {
		id    string
		score float64
		text  string
	}
	items := make([]item, 0, len(summaries))
	for _, s := range summaries {
		items = append(items, item{id: s.ID, score: seen[s.ID], text: s.SummaryText})
	}
	// 简单冒泡排序（少量数据）
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].score > items[i].score {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	if len(items) > a.TopK {
		items = items[:a.TopK]
	}

	// 格式化输出
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]interface{}{
			"id":           it.id,
			"summary_text": it.text,
			"score":        it.score,
		})
	}

	data, _ := json.Marshal(out)
	return &Result{Success: true, Data: data}
}
