package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ToolMemoryOutput — 结构化 JSON 中的单条记忆
type ToolMemoryOutput struct {
	ID              string   `json:"id"`
	MemoryValue     string   `json:"memory_value"`
	Topic           string   `json:"topic,omitempty"`
	Tags            string   `json:"tags,omitempty"`
	Facts           string   `json:"facts,omitempty"`
	ConfirmCount    int      `json:"confirm_count"`
	CreatedAt       string   `json:"created_at"`
	RelevanceScore  *float64 `json:"relevance_score,omitempty"`
	MatchedKeywords []string `json:"matched_keywords,omitempty"`
}

// MemorySearchTool — LLM 可主动调用的记忆检索工具
type MemorySearchTool struct {
	vectorStore app.VectorStore
	memRepo     *service.UserMemoryRepo
	embedder    *llm.Embedder
}

func NewMemorySearchTool(vs app.VectorStore, memRepo *service.UserMemoryRepo, embedder *llm.Embedder) *MemorySearchTool {
	return &MemorySearchTool{vectorStore: vs, memRepo: memRepo, embedder: embedder}
}

func (t *MemorySearchTool) Name() string { return "memory_search" }

func (t *MemorySearchTool) Description() string {
	return "在用户的长期记忆中检索。支持向量语义搜索、关键词搜索（topic过滤+时间范围+cursor分页）、统计数量、按ID获取、列出所有topic。" +
		"做关键词/时间搜索前先调 count_memories 评估量级"
}

func (t *MemorySearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "semantic_search",
			Display:     "语义搜索",
			Description: "用向量语义相似度在用户长期记忆中搜索。返回按创建时间倒序排列的记忆。适合模糊概念、偏好、习惯等语义匹配场景。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]interface{}{"type": "string", "description": "搜索关键词或描述（用于语义匹配）"},
					"top_k":       map[string]interface{}{"type": "integer", "description": "返回条数，默认5，最大10"},
					"topic":       map[string]interface{}{"type": "string", "description": "限定 topic，不传则不限"},
					"exclude_ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "排除的记忆 ID 列表"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "search_by_keyword",
			Display:     "关键词搜索",
			Description: "在用户记忆中按关键词搜索（DB LIKE 匹配）。支持 topic 过滤、时间范围过滤、cursor 分页。多关键词用空格/逗号/顿号分隔为 OR 关系。先调 count_memories 评估量级。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]interface{}{"type": "string", "description": "搜索关键词，空格/逗号/顿号分隔多个词"},
					"topic":       map[string]interface{}{"type": "string", "description": "限定 topic"},
					"start_time":  map[string]interface{}{"type": "string", "description": "起始时间，格式同上"},
					"end_time":    map[string]interface{}{"type": "string", "description": "结束时间，格式同上"},
					"last_n_days": map[string]interface{}{"type": "integer", "description": "最近 N 天，与 start_time 互斥"},
					"limit":       map[string]interface{}{"type": "integer", "description": "返回条数，默认20，最大50"},
					"cursor":      map[string]interface{}{"type": "string", "description": "分页游标（上一页返回的 next_cursor），首页不传"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "count_memories",
			Display:     "统计记忆数量",
			Description: "统计符合条件的记忆总数。支持 keyword+topic+time 全维度组合过滤。在搜索前先调用此函数评估量级。",
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
			Name:        "get_by_ids",
			Display:     "批量获取记忆",
			Description: "根据记忆 ID 列表获取完整内容。查不到的 ID 静默跳过。同一用户的任意记忆都支持。最多 20 个 ID。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"ids": map[string]interface{}{
						"type": "array", "items": map[string]interface{}{"type": "string"},
						"description": "记忆 ID 列表，最多 20 个",
					},
				},
				"required": []string{"ids"},
			},
		},
		{
			Name:        "list_topics",
			Display:     "列出所有主题",
			Description: "列出用户所有记忆中的 topic 及对应数量，按数量倒序排列。用于了解现有记忆覆盖的主题，辅助选择搜索方向。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
				"required":   []string{},
			},
		},
	}
}

// Execute — 统一入口
func (t *MemorySearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "semantic_search":
		return t.semanticSearch(ec, args)
	case "search_by_keyword":
		return t.searchByKeyword(ec, args)
	case "count_memories":
		return t.countMemories(ec, args)
	case "get_by_ids":
		return t.getByIDs(ec, args)
	case "list_topics":
		return t.listTopics(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

// ==================== semantic_search ====================

func (t *MemorySearchTool) semanticSearch(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query      string   `json:"query"`
		TopK       int      `json:"top_k"`
		Topic      string   `json:"topic"`
		ExcludeIDs []string `json:"exclude_ids"`
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
	if a.TopK > 10 {
		a.TopK = 10
	}

	vec, err := t.embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		log.Printf("[memory_search] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("向量化失败: %v", err)}
	}

	// 1. 全量嵌入搜索 (type=mem)
	fullHits := t.vectorStore.Search(vec, a.TopK*2, func(id string, _ []float64, metadata map[string]string) bool {
		if metadata["type"] != "mem" || metadata["user_id"] != ec.UserID {
			return false
		}
		// 仅当 metadata 有非空 topic 且不匹配时才排除（旧嵌入无 topic 时不误杀）
		if metaTopic, ok := metadata["topic"]; ok && metaTopic != "" && a.Topic != "" && metaTopic != a.Topic {
			return false
		}
		return true
	})

	// 2. chunk 搜索 (type=chunk, parent_type=memory) — 覆盖新创建还未进全量嵌入的记忆
	chunkHits := t.vectorStore.Search(vec, a.TopK*2, func(id string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == "chunk" &&
			metadata["parent_type"] == "memory" &&
			metadata["user_id"] == ec.UserID
	})

	skipIDs := make(map[string]bool, len(a.ExcludeIDs))
	for _, id := range a.ExcludeIDs {
		skipIDs[strings.TrimPrefix(id, "mem:")] = true
		skipIDs[id] = true
	}

	// 合并两路结果，按 memory ID 取最高分
	scoreMap := make(map[string]float64)
	for _, hit := range fullHits {
		id := strings.TrimPrefix(hit.ID, "mem:")
		if skipIDs[id] {
			continue
		}
		if oldSc, ok := scoreMap[id]; !ok || hit.Score > oldSc {
			scoreMap[id] = hit.Score
		}
	}
	for _, hit := range chunkHits {
		// chunk ID 格式: "mem:{memID}/chunk:{n}"
		id := strings.TrimPrefix(hit.ID, "mem:")
		if idx := strings.Index(id, "/"); idx > 0 {
			id = id[:idx]
		}
		if id == "" || skipIDs[id] {
			continue
		}
		if oldSc, ok := scoreMap[id]; !ok || hit.Score > oldSc {
			scoreMap[id] = hit.Score
		}
	}

	var memIDs []string
	for id := range scoreMap {
		memIDs = append(memIDs, id)
	}

	if len(memIDs) == 0 {
		data, _ := json.Marshal(map[string]interface{}{
			"total": 0, "memories": []ToolMemoryOutput{},
		})
		return &Result{Success: true, Data: data}
	}

	memories, err := t.memRepo.GetByIDsAndUser(ec.Ctx, memIDs, ec.UserID)
	if err != nil {
		log.Printf("[memory_search] GetByIDsAndUser error: %v", err)
	}

	output := make([]ToolMemoryOutput, 0, len(memories))
	for _, m := range memories {
		// post-load topic 兜底过滤（处理旧嵌入无 topic metadata 的情况）
		if a.Topic != "" && m.Topic != a.Topic {
			continue
		}
		score := scoreMap[m.ID]
		mo := formatMemory(m)
		mo.RelevanceScore = &score
		output = append(output, mo)
	}

	// 按 created_at 倒序（model 里的时间是 UTC，直接用 time 字段排序）
	sortMemoriesByTime(output)

	data, _ := json.Marshal(map[string]interface{}{
		"total":    len(output),
		"memories": output,
	})
	return &Result{Success: true, Data: data}
}

// ==================== search_by_keyword ====================

func (t *MemorySearchTool) searchByKeyword(ec *ExecuteContext, args json.RawMessage) *Result {
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
	if a.Query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}
	if a.Limit > 50 {
		a.Limit = 50
	}

	keywords := splitKeywords(a.Query)
	if len(keywords) == 0 {
		return &Result{Success: false, Error: "query 未产生有效关键词，请提供有意义的搜索词"}
	}

	after, before, err := parseTimeParams(a.StartTime, a.EndTime, a.LastNDays, 0)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	var cursorTime *time.Time
	if a.Cursor != "" {
		parsed, err := parseTimeString(a.Cursor)
		if err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("cursor 解析失败: %v", err)}
		}
		cursorTime = &parsed
	}

	msgs, hasMore, err := t.memRepo.SearchMemoriesByCursor(ec.Ctx, ec.UserID, keywords, a.Topic, after, before, cursorTime, a.Limit)
	if err != nil {
		log.Printf("[memory_search] search_by_keyword error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("搜索记忆失败: %v", err)}
	}

	total, err := t.memRepo.CountMemories(ec.Ctx, ec.UserID, keywords, a.Topic, after, before)
	if err != nil {
		log.Printf("[memory_search] count error: %v", err)
		total = int64(len(msgs))
	}

	output := make([]ToolMemoryOutput, 0, len(msgs))
	for _, m := range msgs {
		mo := formatMemory(m)
		mo.MatchedKeywords = findMatchedKeywords(m.MemoryValue, keywords)
		output = append(output, mo)
	}

	var nextCursor string
	if hasMore && len(msgs) > 0 {
		nextCursor = formatMsgTime(msgs[len(msgs)-1].CreatedAt)
	}

	resp := map[string]interface{}{
		"total":    total,
		"has_more": hasMore,
		"memories": output,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}

	data, _ := json.Marshal(resp)
	return &Result{Success: true, Data: data}
}

// ==================== count_memories ====================

func (t *MemorySearchTool) countMemories(ec *ExecuteContext, args json.RawMessage) *Result {
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

	count, err := t.memRepo.CountMemories(ec.Ctx, ec.UserID, keywords, a.Topic, after, before)
	if err != nil {
		log.Printf("[memory_search] count_memories error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("统计失败: %v", err)}
	}

	data, _ := json.Marshal(map[string]interface{}{"count": count})
	return &Result{Success: true, Data: data}
}

// ==================== get_by_ids ====================

func (t *MemorySearchTool) getByIDs(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if len(a.IDs) == 0 {
		return &Result{Success: false, Error: "ids 不能为空"}
	}
	if len(a.IDs) > 20 {
		return &Result{Success: false, Error: "ids 最多 20 个"}
	}

	memories, err := t.memRepo.GetByIDsAndUser(ec.Ctx, a.IDs, ec.UserID)
	if err != nil {
		log.Printf("[memory_search] get_by_ids error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("获取记忆失败: %v", err)}
	}

	output := make([]ToolMemoryOutput, 0, len(memories))
	for _, m := range memories {
		output = append(output, formatMemory(m))
	}

	data, _ := json.Marshal(map[string]interface{}{"memories": output})
	return &Result{Success: true, Data: data}
}

// ==================== list_topics ====================

func (t *MemorySearchTool) listTopics(ec *ExecuteContext, _ json.RawMessage) *Result {
	topics, err := t.memRepo.DistinctTopics(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[memory_search] list_topics error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("获取 topic 列表失败: %v", err)}
	}
	if topics == nil {
		topics = []service.TopicCount{}
	}

	output := make([]map[string]interface{}, 0, len(topics))
	for _, tc := range topics {
		output = append(output, map[string]interface{}{"topic": tc.Topic, "count": tc.Count})
	}

	data, _ := json.Marshal(map[string]interface{}{"topics": output})
	return &Result{Success: true, Data: data}
}

// ==================== helpers ====================

// formatMemory 将 UserMemory 转为 ToolMemoryOutput。
func formatMemory(m model.UserMemory) ToolMemoryOutput {
	mo := ToolMemoryOutput{
		ID:           m.ID,
		MemoryValue:  m.MemoryValue,
		Topic:        m.Topic,
		Tags:         m.Tags,
		ConfirmCount: m.ConfirmCount,
		CreatedAt:    formatMsgTime(m.CreatedAt),
	}
	if m.Facts != nil {
		mo.Facts = *m.Facts
	}
	return mo
}

// sortMemoriesByTime 按 created_at 倒序排序（从 ISO 8601 字符串解析）
func sortMemoriesByTime(memories []ToolMemoryOutput) {
	// simple insertion sort by created_at DESC (newest first)
	sort.Slice(memories, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, memories[i].CreatedAt)
		tj, _ := time.Parse(time.RFC3339, memories[j].CreatedAt)
		return ti.After(tj)
	})
}
