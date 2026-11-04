package tool

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"sort"
	"sync"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/app"
	"xhan-helper/internal/service"
)

// ==================== 输出结构 ====================

type unifiedDomainResult struct {
	Description string      `json:"description"`
	Data        interface{} `json:"data"`
}

type searchResult struct {
	Memories  *unifiedDomainResult `json:"memories,omitempty"`
	Notes     *unifiedDomainResult `json:"notes,omitempty"`
	Summaries *unifiedDomainResult `json:"summaries,omitempty"`
	Events    *unifiedDomainResult `json:"events,omitempty"`
	History   *unifiedDomainResult `json:"history,omitempty"`
}

// memoryItem memory 和 note 共用（EntityMeta 字段一致）
type memoryItem struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Topic     string  `json:"topic,omitempty"`
	CreatedAt string  `json:"created_at"`
	Score     float64 `json:"score,omitempty"`
}

type summaryItem struct {
	ID              string  `json:"id"`
	SummaryText     string  `json:"summary_text"`
	Topics          string  `json:"topics,omitempty"`
	CoverFromMsgID  string  `json:"cover_from_msg_id,omitempty"`
	CoverToMsgID    string  `json:"cover_to_msg_id,omitempty"`
	ParentSummaryID string  `json:"parent_summary_id,omitempty"`
	Score           float64 `json:"score,omitempty"`
}

type historyItem struct {
	ID             string  `json:"id"`
	Content        string  `json:"content"`
	CreatedAt      string  `json:"created_at"`
	Score          float64 `json:"score,omitempty"`
	ConversationID string  `json:"conversation_id,omitempty"`
}

// roundScore 保留 4 位小数
func roundScore(s float64) float64 {
	return math.Round(s*10000) / 10000
}

// ==================== 工具 ====================

// UnifiedSearchTool 统一搜索工具，支持语义搜索和关键词搜索，覆盖记忆、笔记、摘要、事件
type UnifiedSearchTool struct {
	sources  map[string]retrieval.ContextSource
	embedder app.Embedder

	msgRepo     *service.MessageRepo
	memRepo     *service.UserMemoryRepo
	noteRepo    *service.NoteRepo
	summaryRepo *service.ConversationSummaryRepo
	eventRepo   *service.EventRepo
}

// NewUnifiedSearchTool 创建统一搜索工具
func NewUnifiedSearchTool(
	sources map[string]retrieval.ContextSource,
	embedder app.Embedder,
	msgRepo *service.MessageRepo,
	memRepo *service.UserMemoryRepo,
	noteRepo *service.NoteRepo,
	summaryRepo *service.ConversationSummaryRepo,
	eventRepo *service.EventRepo,
) *UnifiedSearchTool {
	return &UnifiedSearchTool{
		sources:     sources,
		embedder:    embedder,
		msgRepo:     msgRepo,
		memRepo:     memRepo,
		noteRepo:    noteRepo,
		summaryRepo: summaryRepo,
		eventRepo:   eventRepo,
	}
}

// Name 工具名
func (t *UnifiedSearchTool) Name() string { return "unified_search" }

// Description 工具描述
func (t *UnifiedSearchTool) Description() string {
	return "统一搜索工具，支持语义搜索和关键词搜索，覆盖记忆、笔记、摘要、事件、历史消息五个域，返回结构化 JSON"
}

func (t *UnifiedSearchTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "统一检索。语义/关键词搜记忆、笔记、摘要、事件、历史消息。",
		Funcs:     []string{"semantic_search", "keyword_search"},
	}, true
}

// Functions 工具函数定义
func (t *UnifiedSearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "semantic_search",
			Display:     "语义搜索",
			Description: "用自然语言语义搜索记忆、笔记、摘要、主题和历史消息，返回结构化 JSON。适合「查找相关...」「搜索关于...」等意图模糊的查询",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type": "string", "description": "自然语言搜索查询",
					},
					"types": map[string]interface{}{
						"type":        "array",
						"items":       map[string]string{"type": "string"},
						"description": "限定搜索域，可选值: memory/note/summary/event/history，默认全部",
					},
					"top_k": map[string]interface{}{
						"type": "integer", "description": "每类结果数上限，默认 5，最大 10",
					},
				},
				"required": []interface{}{"query"},
			},
		},
		{
			Name:        "keyword_search",
			Display:     "关键词搜索",
			Description: "用关键词精确搜索记忆、笔记、摘要、事件和历史消息，返回结构化 JSON。适合查找包含特定词汇的内容",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"keywords": map[string]interface{}{
						"type": "string", "description": "关键词，空格/逗号/顿号分隔，OR 语义",
					},
					"types": map[string]interface{}{
						"type":        "array",
						"items":       map[string]string{"type": "string"},
						"description": "限定搜索域，可选值: memory/note/summary/event/history，默认全部",
					},
					"topic": map[string]interface{}{
						"type": "string", "description": "主题过滤，仅对记忆和笔记生效",
					},
					"top_k": map[string]interface{}{
						"type": "integer", "description": "每类结果数上限，默认 5，最大 10",
					},
				},
				"required": []interface{}{"keywords"},
			},
		},
	}
}

func (t *UnifiedSearchTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

// Execute 路由函数
func (t *UnifiedSearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "semantic_search":
		return t.semanticSearch(ec, args)
	case "keyword_search":
		return t.keywordSearch(ec, args)
	default:
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}
}

// ==================== 语义搜索 ====================

func (t *UnifiedSearchTool) semanticSearch(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query string   `json:"query"`
		Types []string `json:"types"`
		TopK  int      `json:"top_k"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.Query == "" {
		return &Result{Success: false, Error: "query is required"}
	}
	if a.TopK <= 0 {
		a.TopK = 5
	}
	if a.TopK > 10 {
		a.TopK = 10
	}
	types := a.Types
	if len(types) == 0 {
		types = []string{"memory", "note", "summary", "event"}
	}

	subQueries := splitSubQueries(a.Query)
	if len(subQueries) == 0 {
		subQueries = []string{a.Query}
	}

	var packets map[string][]retrieval.ContextPacket
	if len(subQueries) == 1 {
		emb, err := t.embedder.Embed(ec.Ctx, subQueries[0])
		if err != nil {
			log.Printf("[unified_search] embed error: %v", err)
			return &Result{Success: false, Error: "embed query failed"}
		}
		opts := t.buildSearchOptions(ec.UserID, subQueries[0], emb, a.TopK)
		packets = t.fetchAll(ec.Ctx, types, opts)
	} else {
		var batches []map[string][]retrieval.ContextPacket
		for _, sub := range subQueries {
			emb, err := t.embedder.Embed(ec.Ctx, sub)
			if err != nil {
				log.Printf("[unified_search] embed sub-query %q: %v", sub, err)
				continue
			}
			opts := t.buildSearchOptions(ec.UserID, sub, emb, a.TopK)
			batch := t.fetchAll(ec.Ctx, types, opts)
			if len(batch) > 0 {
				batches = append(batches, batch)
			}
		}
		packets = mergePacketBatches(batches, a.TopK)
	}

	result := t.buildSearchResult(packets, true)
	data, _ := json.Marshal(result)
	return &Result{Success: true, Data: data}
}

// buildSearchOptions 构造 SearchOptions，提取公共逻辑
func (t *UnifiedSearchTool) buildSearchOptions(userID, query string, emb []float64, topK int) retrieval.SearchOptions {
	return retrieval.SearchOptions{
		Query:     query,
		UserID:    userID,
		Embedding: emb,
		Memory:    retrieval.SourceConfig{Threshold: 0.5, Limit: topK},
		Note:      retrieval.SourceConfig{Threshold: 0.5, Limit: topK},
		Summary:   retrieval.SourceConfig{Threshold: 0.55, Limit: topK},
		Event:     retrieval.SourceConfig{Threshold: 0.5, Limit: topK},
		History:   retrieval.SourceConfig{Threshold: 0.55, Limit: topK},
	}
}

// splitSubQueries 按空格/逗号/顿号拆分 query，最多取前 5 个片段
func splitSubQueries(query string) []string {
	parts := splitKeywords(query)
	if len(parts) > 5 {
		parts = parts[:5]
	}
	return parts
}

// ==================== 关键词搜索 ====================

func (t *UnifiedSearchTool) keywordSearch(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Keywords string   `json:"keywords"`
		Types    []string `json:"types"`
		Topic    string   `json:"topic"`
		TopK     int      `json:"top_k"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.Keywords == "" {
		return &Result{Success: false, Error: "keywords is required"}
	}
	if a.TopK <= 0 {
		a.TopK = 5
	}
	if a.TopK > 10 {
		a.TopK = 10
	}
	types := a.Types
	if len(types) == 0 {
		types = []string{"memory", "note", "summary", "event"}
	}

	keywords := splitKeywords(a.Keywords)
	if len(keywords) == 0 {
		return &Result{Success: false, Error: "no valid keywords extracted from input"}
	}

	packets := make(map[string][]retrieval.ContextPacket)
	var eventHits []eventHit
	for _, typ := range types {
		switch typ {
		case "memory":
			pkts := t.searchMemoriesByKeyword(ec, keywords, a.Topic, a.TopK)
			if len(pkts) > 0 {
				packets["memory"] = pkts
			}
		case "note":
			pkts := t.searchNotesByKeyword(ec, keywords, a.Topic, a.TopK)
			if len(pkts) > 0 {
				packets["note"] = pkts
			}
		case "summary":
			pkts := t.searchSummariesByKeyword(ec, keywords, a.TopK)
			if len(pkts) > 0 {
				packets["summary"] = pkts
			}
		case "event":
			hits, err := searchEventsByKeyword(ec.Ctx, t.eventRepo, t.memRepo, ec.UserID, keywords, a.TopK)
			if err != nil {
				log.Printf("[unified_search] event keyword: %v", err)
			} else if len(hits) > 0 {
				eventHits = hits
			}
		case "history":
			pkts := t.searchHistoryByKeyword(ec, keywords, a.TopK)
			if len(pkts) > 0 {
				packets["history"] = pkts
			}
		}
	}

	result := t.buildSearchResult(packets, false)
	if len(eventHits) > 0 {
		result.Events = &unifiedDomainResult{Description: retrieval.DescEvents, Data: eventHits}
	}
	data, _ := json.Marshal(result)
	return &Result{Success: true, Data: data}
}

// ==================== buildSearchResult ====================

func (t *UnifiedSearchTool) buildSearchResult(packets map[string][]retrieval.ContextPacket, includeScore bool) searchResult {
	var result searchResult
	for typ, pkts := range packets {
		if len(pkts) == 0 {
			continue
		}
		switch typ {
		case "memory":
			result.Memories = &unifiedDomainResult{
				Description: retrieval.DescMemory,
				Data:        t.convertEntityPackets(pkts, includeScore),
			}
		case "note":
			result.Notes = &unifiedDomainResult{
				Description: retrieval.DescNotes,
				Data:        t.convertEntityPackets(pkts, includeScore),
			}
		case "summary":
			result.Summaries = &unifiedDomainResult{
				Description: retrieval.DescSummary,
				Data:        t.convertSummaryPackets(pkts, includeScore),
			}
		case "event":
			result.Events = &unifiedDomainResult{
				Description: retrieval.DescEvents,
				Data:        t.convertEventPackets(pkts, includeScore),
			}
		case "history":
			result.History = &unifiedDomainResult{
				Description: retrieval.DescMessages,
				Data:        t.convertHistoryPackets(pkts, includeScore),
			}
		}
	}
	return result
}

// convertEntityPackets memory 和 note 共用（都使用 EntityMeta）
func (t *UnifiedSearchTool) convertEntityPackets(pkts []retrieval.ContextPacket, includeScore bool) []memoryItem {
	items := make([]memoryItem, 0, len(pkts))
	for _, p := range pkts {
		it := memoryItem{ID: p.ID}
		if meta, ok := p.Meta.(retrieval.EntityMeta); ok {
			it.Content = meta.Content
			it.Topic = meta.Topic
			if !meta.CreatedAt.IsZero() {
				it.CreatedAt = formatMsgTime(meta.CreatedAt)
			}
		}
		if includeScore {
			it.Score = roundScore(p.Score)
		}
		items = append(items, it)
	}
	return items
}

func (t *UnifiedSearchTool) convertSummaryPackets(pkts []retrieval.ContextPacket, includeScore bool) []summaryItem {
	items := make([]summaryItem, 0, len(pkts))
	for _, p := range pkts {
		it := summaryItem{ID: p.ID}
		if meta, ok := p.Meta.(retrieval.SummaryMeta); ok {
			it.SummaryText = meta.SummaryText
			it.Topics = meta.Topics
			it.CoverFromMsgID = meta.CoverFromMsgID
			it.CoverToMsgID = meta.CoverToMsgID
			it.ParentSummaryID = meta.ParentSummaryID
		}
		if includeScore {
			it.Score = roundScore(p.Score)
		}
		items = append(items, it)
	}
	return items
}

func (t *UnifiedSearchTool) convertEventPackets(pkts []retrieval.ContextPacket, includeScore bool) []eventHit {
	items := make([]eventHit, 0, len(pkts))
	for _, p := range pkts {
		meta, ok := p.Meta.(retrieval.EventMeta)
		if !ok {
			continue
		}
		it := eventHit{
			ID:          meta.EventID,
			Name:        meta.Title,
			Summary:     meta.Summary,
			Mode:        meta.Mode,
			MemoryCount: meta.MemoryCount,
		}
		if !meta.UpdatedAt.IsZero() {
			it.LastMemoryAt = formatMsgTime(meta.UpdatedAt)
		}
		if includeScore {
			it.Score = roundScore(p.Score)
		}
		items = append(items, it)
	}
	return items
}

// ==================== 各域关键词搜索（直调 repo） ====================

func (t *UnifiedSearchTool) searchMemoriesByKeyword(ec *ExecuteContext, keywords []string, topic string, limit int) []retrieval.ContextPacket {
	msgs, _, err := t.memRepo.SearchMemoriesByCursor(ec.Ctx, ec.UserID, keywords, topic, nil, nil, nil, limit)
	if err != nil {
		log.Printf("[unified_search] memory keyword: %v", err)
		return nil
	}
	pkts := make([]retrieval.ContextPacket, len(msgs))
	for i, m := range msgs {
		pkts[i] = retrieval.ContextPacket{
			ID:   m.ID,
			Type: "memory",
			Meta: retrieval.EntityMeta{Content: m.MemoryValue, Topic: m.Topic, CreatedAt: m.CreatedAt},
		}
	}
	return pkts
}

func (t *UnifiedSearchTool) searchNotesByKeyword(ec *ExecuteContext, keywords []string, topic string, limit int) []retrieval.ContextPacket {
	notes, _, err := t.noteRepo.SearchNotesByCursor(ec.Ctx, ec.UserID, keywords, topic, nil, nil, nil, limit)
	if err != nil {
		log.Printf("[unified_search] note keyword: %v", err)
		return nil
	}
	pkts := make([]retrieval.ContextPacket, len(notes))
	for i, n := range notes {
		pkts[i] = retrieval.ContextPacket{
			ID:   n.ID,
			Type: "note",
			Meta: retrieval.EntityMeta{Content: n.Content, Topic: n.Topic, CreatedAt: n.CreatedAt},
		}
	}
	return pkts
}

func (t *UnifiedSearchTool) searchSummariesByKeyword(ec *ExecuteContext, keywords []string, limit int) []retrieval.ContextPacket {
	summaries, err := t.summaryRepo.SearchByKeyword(ec.Ctx, ec.UserID, keywords, limit)
	if err != nil {
		log.Printf("[unified_search] summary keyword: %v", err)
		return nil
	}
	pkts := make([]retrieval.ContextPacket, 0, len(summaries))
	for _, s := range summaries {
		parentID := ""
		if s.ParentSummaryID != nil {
			parentID = *s.ParentSummaryID
		}
		pkts = append(pkts, retrieval.ContextPacket{
			ID:   s.ID,
			Type: "summary",
			Meta: retrieval.SummaryMeta{
				SummaryID:       s.ID,
				SummaryText:     s.SummaryText,
				CoverFromMsgID:  s.CoverFromMsgID,
				CoverToMsgID:    s.CoverToMsgID,
				ParentSummaryID: parentID,
				Topics:          s.Topics,
				CreatedAt:       s.CreatedAt,
			},
		})
	}
	return pkts
}

func (t *UnifiedSearchTool) searchHistoryByKeyword(ec *ExecuteContext, keywords []string, limit int) []retrieval.ContextPacket {
	msgs, _, err := t.msgRepo.SearchMessagesByCursor(ec.Ctx, ec.UserID, keywords, "user", "", nil, limit)
	if err != nil {
		log.Printf("[unified_search] history keyword: %v", err)
		return nil
	}
	pkts := make([]retrieval.ContextPacket, 0, len(msgs))
	for _, m := range msgs {
		pkts = append(pkts, retrieval.ContextPacket{
			ID:   m.ID,
			Type: "history",
			Meta: retrieval.MessageMeta{
				Content:        m.Content,
				CreatedAt:      m.CreatedAt,
				ConversationID: m.ConversationID,
			},
		})
	}
	return pkts
}

func (t *UnifiedSearchTool) convertHistoryPackets(pkts []retrieval.ContextPacket, includeScore bool) []historyItem {
	items := make([]historyItem, 0, len(pkts))
	for _, p := range pkts {
		it := historyItem{ID: p.ID}
		if meta, ok := p.Meta.(retrieval.MessageMeta); ok {
			it.Content = meta.Content
			it.ConversationID = meta.ConversationID
			if !meta.CreatedAt.IsZero() {
				it.CreatedAt = formatMsgTime(meta.CreatedAt)
			}
		}
		if includeScore {
			it.Score = roundScore(p.Score)
		}
		items = append(items, it)
	}
	return items
}

// ==================== 并发 Fetch ====================

func (t *UnifiedSearchTool) fetchAll(ctx context.Context, types []string, opts retrieval.SearchOptions) map[string][]retrieval.ContextPacket {
	type result struct {
		typ  string
		pkts []retrieval.ContextPacket
	}
	ch := make(chan result, len(types))
	var wg sync.WaitGroup

	for _, typ := range types {
		src, ok := t.sources[typ]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(typ string, src retrieval.ContextSource) {
			defer wg.Done()
			pkts, _, err := src.Fetch(ctx, opts)
			if err != nil {
				log.Printf("[unified_search] %s semantic: %v", typ, err)
				return
			}
			if len(pkts) > 0 {
				ch <- result{typ: typ, pkts: pkts}
			}
		}(typ, src)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	packets := make(map[string][]retrieval.ContextPacket)
	for r := range ch {
		packets[r.typ] = r.pkts
	}
	return packets
}

// mergePacketBatches 合并多 sub-query 的检索结果，同 ID 保留最高 Score。
// maxPerType 限制每域返回条数（防 sub-query 拆分放大：5 子查询 × topK 会超量）。
func mergePacketBatches(batches []map[string][]retrieval.ContextPacket, maxPerType int) map[string][]retrieval.ContextPacket {
	typeKey := make(map[string]map[string]retrieval.ContextPacket)
	for _, batch := range batches {
		for typ, pkts := range batch {
			if typeKey[typ] == nil {
				typeKey[typ] = make(map[string]retrieval.ContextPacket)
			}
			for _, p := range pkts {
				if existing, ok := typeKey[typ][p.ID]; !ok || p.Score > existing.Score {
					typeKey[typ][p.ID] = p
				}
			}
		}
	}
	if maxPerType <= 0 {
		maxPerType = 5
	}
	result := make(map[string][]retrieval.ContextPacket)
	for typ, idMap := range typeKey {
		pkts := make([]retrieval.ContextPacket, 0, len(idMap))
		for _, p := range idMap {
			pkts = append(pkts, p)
		}
		// 按 Score 降序，截断到 maxPerType
		sort.Slice(pkts, func(i, j int) bool { return pkts[i].Score > pkts[j].Score })
		if len(pkts) > maxPerType {
			pkts = pkts[:maxPerType]
		}
		result[typ] = pkts
	}
	return result
}
