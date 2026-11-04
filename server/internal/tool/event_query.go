package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// eventHit — 事件查询结果的公共形状（event 工具与 unified_search 的 event 域共用）。
type eventHit struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Summary      string  `json:"summary,omitempty"`
	Mode         string  `json:"mode,omitempty"`
	LastMemoryAt string  `json:"last_memory_at,omitempty"`
	MemoryCount  int     `json:"memory_count"`
	IsDeleted    bool    `json:"is_deleted,omitempty"`
	Score        float64 `json:"score,omitempty"`
}

// eventMemoryItem — 事件正文里的一条记忆。
type eventMemoryItem struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Topic     string   `json:"topic,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	Facts     []string `json:"facts,omitempty"`
}

// listAllEvents 分页取全量事件（EventRepo.List 单页上限 200，不静默截断）。
func listAllEvents(ctx context.Context, repo *service.EventRepo, userID string, includeDeleted bool) ([]model.Event, error) {
	const pageSize = 200
	var out []model.Event
	for offset := 0; ; offset += pageSize {
		page, err := repo.List(ctx, userID, includeDeleted, pageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("list events (offset %d): %w", offset, err)
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
	}
}

// eventMemoryCounts 一次聚合查询取回多个事件的记忆数（不拉记忆正文）。
func eventMemoryCounts(ctx context.Context, memRepo *service.UserMemoryRepo, userID string, eventIDs []string) (map[string]int, error) {
	counts, err := memRepo.CountByEventIDs(ctx, userID, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("count event memories: %w", err)
	}
	return counts, nil
}

// eventHitsFromModels 把事件模型转成查询结果，附带记忆数。
func eventHitsFromModels(ctx context.Context, memRepo *service.UserMemoryRepo, userID string, events []model.Event) ([]eventHit, error) {
	ids := make([]string, 0, len(events))
	for i := range events {
		ids = append(ids, events[i].ID)
	}
	counts, err := eventMemoryCounts(ctx, memRepo, userID, ids)
	if err != nil {
		return nil, err
	}
	hits := make([]eventHit, 0, len(events))
	for i := range events {
		e := &events[i]
		h := eventHit{
			ID: e.ID, Name: e.Name, Summary: e.Summary,
			MemoryCount: counts[e.ID], IsDeleted: e.IsDeleted,
		}
		if e.LastMemoryAt != nil {
			h.LastMemoryAt = formatMsgTime(*e.LastMemoryAt)
		}
		hits = append(hits, h)
	}
	return hits, nil
}

// searchEventsByKeyword 关键词搜事件：事件名 / summary / 归属记忆文本，OR 语义。
// 返回结果按「命中字段优先级」排（名称 > summary > 记忆），同组保持 last_memory_at 倒序。
func searchEventsByKeyword(
	ctx context.Context,
	eventRepo *service.EventRepo,
	memRepo *service.UserMemoryRepo,
	userID string,
	keywords []string,
	limit int,
) ([]eventHit, error) {
	if len(keywords) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	memLimit := limit * 4
	if memLimit > 100 {
		memLimit = 100
	}
	active, err := listAllEvents(ctx, eventRepo, userID, false)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*model.Event, len(active))
	for i := range active {
		byID[active[i].ID] = &active[i]
	}

	rank := make(map[string]int, len(active))
	lower := make([]string, len(keywords))
	for i, kw := range keywords {
		lower[i] = strings.ToLower(kw)
	}
	containsAny := func(s string) bool {
		s = strings.ToLower(s)
		for _, kw := range lower {
			if strings.Contains(s, kw) {
				return true
			}
		}
		return false
	}
	for i := range active {
		e := &active[i]
		switch {
		case containsAny(e.Name):
			rank[e.ID] = 3
		case containsAny(e.Summary):
			rank[e.ID] = 2
		}
	}

	// 记忆文本命中：把命中的记忆映射回事件。
	mems, _, err := memRepo.SearchMemoriesByCursor(ctx, userID, keywords, "", nil, nil, nil, memLimit)
	if err != nil {
		return nil, fmt.Errorf("search memories by keyword: %w", err)
	}
	for _, m := range mems {
		if m.EventID == nil {
			continue
		}
		if byID[*m.EventID] == nil {
			continue // 已删事件：不返回
		}
		if rank[*m.EventID] < 1 {
			rank[*m.EventID] = 1
		}
	}

	var matched []model.Event
	for i := range active {
		if rank[active[i].ID] > 0 {
			matched = append(matched, active[i])
		}
	}
	// 稳定排序：命中优先级降序；同优先级保持 repo 的 last_memory_at 倒序。
	sortEventHitsByRank(matched, rank)
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return eventHitsFromModels(ctx, memRepo, userID, matched)
}

// sortEventHitsByRank 按命中优先级稳定排序（同优先级不动，保持 repo 的时间序）。
func sortEventHitsByRank(events []model.Event, rank map[string]int) {
	sort.SliceStable(events, func(i, j int) bool {
		return rank[events[i].ID] > rank[events[j].ID]
	})
}

// parseEventFacts 把 facts 的 JSON 数组字符串解析为切片；脏数据只记日志，不阻断查询。
func parseEventFacts(raw *string) []string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	var facts []string
	if err := json.Unmarshal([]byte(*raw), &facts); err != nil {
		log.Printf("[event] parse facts error: %v", err)
		return nil
	}
	return facts
}
