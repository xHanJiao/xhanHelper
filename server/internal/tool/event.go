package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// eventPinnedMaxDefault — 钉住上限的兜底值（配置 quota_event_pinned 优先）。
const eventPinnedMaxDefault = 3

// EventTool — 事件管理工具组（设计 §7.1）。
//
// 事件由后台拼装自动生长；本工具只在用户明确要求时介入（查看/钉住/改名/删除/合并）。
type EventTool struct {
	eventRepo *service.EventRepo
	memRepo   *service.UserMemoryRepo
	convRepo  *service.ConversationRepo
	embedder  app.Embedder
	source    retrieval.ContextSource
	maxPinned int
}

func NewEventTool(
	eventRepo *service.EventRepo,
	memRepo *service.UserMemoryRepo,
	convRepo *service.ConversationRepo,
	embedder app.Embedder,
	source retrieval.ContextSource,
	maxPinned int,
) *EventTool {
	if maxPinned <= 0 {
		maxPinned = eventPinnedMaxDefault
	}
	return &EventTool{
		eventRepo: eventRepo,
		memRepo:   memRepo,
		convRepo:  convRepo,
		embedder:  embedder,
		source:    source,
		maxPinned: maxPinned,
	}
}

func (t *EventTool) Name() string { return "event" }

func (t *EventTool) Description() string {
	return "管理由记忆自动聚合出的「事件」（现实里持续在跟进的一件事，如「回西安准备」「驾照考取」）。" +
		"只在用户明确要求查看、钉住、改名、删除或合并事件时使用；不要主动创建事件（后台拼装会自动生长）。"
}

func (t *EventTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "事件管理。查事件全文、钉住、改名、删除、合并重复事件。",
		Funcs:     []string{"list", "get", "search", "enable"},
		Others:    "取消钉住、改名/改摘要、删除、合并 等",
	}, true
}

func (t *EventTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "list",
			Display:     "列出事件",
			Description: "列出事件（默认只列未删除的），按最后一条记忆时间倒序。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"include_deleted": map[string]interface{}{"type": "boolean", "description": "是否包含已删除事件，默认 false"},
					"limit":           map[string]interface{}{"type": "integer", "description": "返回条数，默认 100，最大 200"},
				},
			},
		},
		{
			Name:        "get",
			Display:     "查看事件",
			Description: "返回事件 summary + 全部记忆（含逐字 facts）。已删除事件会标注 is_deleted。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"event_id": map[string]interface{}{"type": "string", "description": "事件 ID"},
					"name":     map[string]interface{}{"type": "string", "description": "事件名（没有 ID 时用，精确匹配）"},
				},
			},
		},
		{
			Name:        "search",
			Display:     "搜索事件",
			Description: "搜索事件：默认语义搜索（事件 summary + 聚合记忆）；传 keyword 时按关键词搜事件名/summary/记忆文本。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":   map[string]interface{}{"type": "string", "description": "自然语言查询（语义搜索）"},
					"keyword": map[string]interface{}{"type": "string", "description": "关键词，空格/逗号分隔（OR 语义）；传了就走关键词搜索"},
					"top_k":   map[string]interface{}{"type": "integer", "description": "返回条数，默认 5，最大 20"},
				},
			},
		},
		{
			Name:        "enable",
			Display:     "钉住事件",
			Description: "钉住事件（最多 3 个）：工具结果直接返回完整事件，之后每轮自动 full 注入，跨窗口保留。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"event_id": map[string]interface{}{"type": "string", "description": "事件 ID"},
					"name":     map[string]interface{}{"type": "string", "description": "事件名（没有 ID 时用，精确匹配）"},
				},
			},
		},
		{
			Name:        "disable",
			Display:     "取消钉住",
			Description: "取消钉住事件，之后按普通检索决定是否注入。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"event_id": map[string]interface{}{"type": "string", "description": "事件 ID"},
					"name":     map[string]interface{}{"type": "string", "description": "事件名（没有 ID 时用，精确匹配）"},
				},
			},
		},
		{
			Name:        "update",
			Display:     "修改事件",
			Description: "修改事件名 / summary，或把已有记忆追加到事件。仅在用户明确说「记一下 / 改一下」时使用；只传要改的字段。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"event_id": map[string]interface{}{"type": "string", "description": "事件 ID"},
					"name":     map[string]interface{}{"type": "string", "description": "事件名（没有 ID 时用，精确匹配）"},
					"new_name": map[string]interface{}{"type": "string", "description": "新事件名（可选）"},
					"summary":  map[string]interface{}{"type": "string", "description": "新的当前状态 summary，≤200 字（可选）"},
					"memory_ids": map[string]interface{}{
						"type":        "array",
						"items":       map[string]string{"type": "string"},
						"description": "要追加到该事件的记忆 ID（通常来自 record_memory 的返回）",
					},
				},
			},
		},
		{
			Name:        "delete",
			Display:     "删除事件",
			Description: "删除事件（软删），其记忆退回普通记忆池继续参与注入。用户明确要求删除时才用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"event_id": map[string]interface{}{"type": "string", "description": "事件 ID"},
					"name":     map[string]interface{}{"type": "string", "description": "事件名（没有 ID 时用，精确匹配）"},
				},
			},
		},
		{
			Name:        "merge",
			Display:     "合并事件",
			Description: "合并两个重复事件：from 的记忆全部搬到 to，from 软删。用户明确说「这两个是一件事」时才用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from_id": map[string]interface{}{"type": "string", "description": "被合并（会消失）的事件 ID"},
					"to_id":   map[string]interface{}{"type": "string", "description": "保留的目标事件 ID"},
				},
				"required": []string{"from_id", "to_id"},
			},
		},
	}
}

// ReadOnlyFuncs — 查询类只读；钉住/改名/删除/合并是写操作（plan 模式拦截）。
func (t *EventTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{"list": true, "get": true, "search": true}
}

func (t *EventTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "list":
		return t.list(ec, args)
	case "get":
		return t.get(ec, args)
	case "search":
		return t.search(ec, args)
	case "enable":
		return t.enable(ec, args)
	case "disable":
		return t.disable(ec, args)
	case "update":
		return t.update(ec, args)
	case "delete":
		return t.delete(ec, args)
	case "merge":
		return t.merge(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

// ReadOnly 返回只暴露 get 的只读视图，供事件拼装循环使用（结构上杜绝写操作，设计 §7.3）。
func (t *EventTool) ReadOnly() Tool { return &eventReadTool{EventTool: t} }

type eventReadTool struct{ *EventTool }

func (r *eventReadTool) Functions() []FuncSchema {
	for _, f := range r.EventTool.Functions() {
		if f.Name == "get" {
			return []FuncSchema{f}
		}
	}
	return nil
}

func (r *eventReadTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{ShortDesc: "读取事件全文（只读）。", Funcs: []string{"get"}}, true
}

func (r *eventReadTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(r) }

func (r *eventReadTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName != "get" {
		return &Result{Success: false, Error: fmt.Sprintf("拼装循环只允许读取事件: %s", funcName)}
	}
	return r.EventTool.Execute(ec, funcName, args)
}

// ==================== 查询 ====================

type eventRefArgs struct {
	EventID string `json:"event_id"`
	Name    string `json:"name"`
}

func (t *EventTool) list(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		IncludeDeleted bool `json:"include_deleted"`
		Limit          int  `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	events, err := listAllEvents(ec.Ctx, t.eventRepo, ec.UserID, a.IncludeDeleted)
	if err != nil {
		log.Printf("[event] list error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("列出事件失败: %v", err)}
	}
	if a.Limit <= 0 {
		a.Limit = 100
	}
	if a.Limit > 200 {
		a.Limit = 200
	}
	if len(events) > a.Limit {
		events = events[:a.Limit]
	}
	hits, err := eventHitsFromModels(ec.Ctx, t.memRepo, ec.UserID, events)
	if err != nil {
		log.Printf("[event] list memory counts error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("统计事件记忆失败: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{"events": hits})
	return &Result{Success: true, Data: data}
}

func (t *EventTool) get(ec *ExecuteContext, args json.RawMessage) *Result {
	var a eventRefArgs
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	e, err := t.resolveEvent(ec, a)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	detail, err := t.eventDetail(ec.Ctx, ec.UserID, e)
	if err != nil {
		log.Printf("[event] get detail error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("读取事件失败: %v", err)}
	}
	data, _ := json.Marshal(detail)
	return &Result{Success: true, Data: data}
}

func (t *EventTool) search(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query   string `json:"query"`
		Keyword string `json:"keyword"`
		TopK    int    `json:"top_k"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if a.TopK <= 0 {
		a.TopK = 5
	}
	if a.TopK > 20 {
		a.TopK = 20
	}

	if strings.TrimSpace(a.Keyword) != "" {
		keywords := splitKeywords(a.Keyword)
		if len(keywords) == 0 {
			return &Result{Success: false, Error: "keyword 中没有有效关键词"}
		}
		hits, err := searchEventsByKeyword(ec.Ctx, t.eventRepo, t.memRepo, ec.UserID, keywords, a.TopK)
		if err != nil {
			log.Printf("[event] keyword search error: %v", err)
			return &Result{Success: false, Error: fmt.Sprintf("搜索事件失败: %v", err)}
		}
		data, _ := json.Marshal(map[string]interface{}{"events": hits})
		return &Result{Success: true, Data: data}
	}

	if strings.TrimSpace(a.Query) == "" {
		return &Result{Success: false, Error: "query 或 keyword 至少提供一个"}
	}
	if t.embedder == nil || t.source == nil {
		return &Result{Success: false, Error: "语义搜索不可用，请改用 keyword"}
	}
	emb, err := t.embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		log.Printf("[event] embed query error: %v", err)
		return &Result{Success: false, Error: "生成查询向量失败"}
	}
	pkts, _, err := t.source.Fetch(ec.Ctx, retrieval.SearchOptions{
		Query:     a.Query,
		UserID:    ec.UserID,
		Embedding: emb,
		Event:     retrieval.SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		log.Printf("[event] semantic search error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("搜索事件失败: %v", err)}
	}
	hits := make([]eventHit, 0, len(pkts))
	for _, p := range pkts {
		meta, ok := p.Meta.(retrieval.EventMeta)
		if !ok {
			continue
		}
		hits = append(hits, eventHit{
			ID: meta.EventID, Name: meta.Title, Summary: meta.Summary,
			MemoryCount: meta.MemoryCount, Score: roundScore(p.Score),
		})
	}
	if len(hits) > a.TopK {
		hits = hits[:a.TopK]
	}
	data, _ := json.Marshal(map[string]interface{}{"events": hits})
	return &Result{Success: true, Data: data}
}

// ==================== 钉住 ====================

func (t *EventTool) enable(ec *ExecuteContext, args json.RawMessage) *Result {
	var a eventRefArgs
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	e, err := t.resolveEvent(ec, a)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if e.IsDeleted {
		return &Result{Success: false, Error: "事件已删除，无法钉住"}
	}
	if ec.ConvID == "" {
		return &Result{Success: false, Error: "当前上下文没有会话，无法钉住事件"}
	}

	// 钉住列表里的旧 ID（事件已删/已合并）会白占名额：写入前顺手剔除。
	activeEvents, err := listAllEvents(ec.Ctx, t.eventRepo, ec.UserID, false)
	if err != nil {
		log.Printf("[event] enable list active error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询事件失败: %v", err)}
	}
	activeSet := make(map[string]struct{}, len(activeEvents))
	for i := range activeEvents {
		activeSet[activeEvents[i].ID] = struct{}{}
	}

	var capErr error
	err = t.convRepo.UpdateExtraField(ec.Ctx, ec.ConvID, func(extra *model.ConversationExtra) {
		if extra.EventFocus == nil {
			extra.EventFocus = &model.EventFocus{}
		}
		ids := slices.DeleteFunc(append([]string(nil), extra.EventFocus.Enabled...), func(id string) bool {
			_, ok := activeSet[id]
			return !ok
		})
		if slices.Contains(ids, e.ID) {
			extra.EventFocus.Enabled = ids
			return
		}
		if len(ids) >= t.maxPinned {
			capErr = fmt.Errorf("最多同时钉住 %d 个事件，请先 disable 一个", t.maxPinned)
			return
		}
		extra.EventFocus.Enabled = append(ids, e.ID)
	})
	if err != nil {
		log.Printf("[event] enable update extra error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("保存钉住状态失败: %v", err)}
	}
	if capErr != nil {
		return &Result{Success: false, Error: capErr.Error()}
	}

	detail, err := t.eventDetail(ec.Ctx, ec.UserID, e)
	if err != nil {
		log.Printf("[event] enable detail error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("钉住成功但读取事件失败: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{"status": "enabled", "event": detail})
	return &Result{Success: true, Data: data}
}

func (t *EventTool) disable(ec *ExecuteContext, args json.RawMessage) *Result {
	var a eventRefArgs
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	e, err := t.resolveEvent(ec, a)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if ec.ConvID == "" {
		return &Result{Success: false, Error: "当前上下文没有会话，无法取消钉住"}
	}
	err = t.convRepo.UpdateExtraField(ec.Ctx, ec.ConvID, func(extra *model.ConversationExtra) {
		if extra.EventFocus == nil {
			return
		}
		extra.EventFocus.Enabled = slices.DeleteFunc(extra.EventFocus.Enabled, func(id string) bool {
			return id == e.ID
		})
	})
	if err != nil {
		log.Printf("[event] disable update extra error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("保存钉住状态失败: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{"status": "disabled", "event_id": e.ID})
	return &Result{Success: true, Data: data}
}

// ==================== 修改 ====================

func (t *EventTool) update(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		eventRefArgs
		NewName   string   `json:"new_name"`
		Summary   string   `json:"summary"`
		MemoryIDs []string `json:"memory_ids"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if strings.TrimSpace(a.NewName) == "" && strings.TrimSpace(a.Summary) == "" && len(a.MemoryIDs) == 0 {
		return &Result{Success: false, Error: "至少提供 new_name、summary 或 memory_ids 之一"}
	}
	e, err := t.resolveEvent(ec, a.eventRefArgs)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if e.IsDeleted {
		return &Result{Success: false, Error: "事件已删除，无法修改"}
	}
	if err := t.eventRepo.UpdateSummary(ec.Ctx, ec.UserID, e.ID, a.NewName, a.Summary); err != nil {
		log.Printf("[event] update error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("修改事件失败: %v", err)}
	}
	if len(a.MemoryIDs) > 0 {
		if err := t.eventRepo.AssignMemories(ec.Ctx, ec.UserID, e.ID, a.MemoryIDs); err != nil {
			log.Printf("[event] update assign memories error: %v", err)
			return &Result{Success: false, Error: fmt.Sprintf("追加记忆失败: %v", err)}
		}
	}
	updated, err := t.eventRepo.GetByID(ec.Ctx, ec.UserID, e.ID)
	if err != nil {
		log.Printf("[event] update reload error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("修改成功但重新读取失败: %v", err)}
	}
	detail, err := t.eventDetail(ec.Ctx, ec.UserID, updated)
	if err != nil {
		log.Printf("[event] update detail error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("修改成功但读取事件失败: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{"status": "updated", "event": detail})
	return &Result{Success: true, Data: data}
}

func (t *EventTool) delete(ec *ExecuteContext, args json.RawMessage) *Result {
	var a eventRefArgs
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	e, err := t.resolveEvent(ec, a)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if err := t.eventRepo.Delete(ec.Ctx, ec.UserID, e.ID); err != nil {
		log.Printf("[event] delete error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("删除事件失败: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{"status": "deleted", "event_id": e.ID})
	return &Result{Success: true, Data: data}
}

func (t *EventTool) merge(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		FromID string `json:"from_id"`
		ToID   string `json:"to_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if a.FromID == "" || a.ToID == "" {
		return &Result{Success: false, Error: "from_id 与 to_id 都不能为空"}
	}
	if err := t.eventRepo.Merge(ec.Ctx, ec.UserID, a.FromID, a.ToID); err != nil {
		log.Printf("[event] merge error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("合并事件失败: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{"status": "merged", "from": a.FromID, "into": a.ToID})
	return &Result{Success: true, Data: data}
}

// ==================== 内部 ====================

// resolveEvent 按 event_id 优先、否则按事件名精确匹配（含已删除，供 get 区分「已删除」）。
func (t *EventTool) resolveEvent(ec *ExecuteContext, a eventRefArgs) (*model.Event, error) {
	if id := strings.TrimSpace(a.EventID); id != "" {
		e, err := t.eventRepo.GetByID(ec.Ctx, ec.UserID, id)
		if err != nil {
			return nil, fmt.Errorf("查询事件失败: %w", err)
		}
		if e == nil {
			return nil, fmt.Errorf("事件不存在: %s", id)
		}
		return e, nil
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return nil, fmt.Errorf("需要提供 event_id 或 name")
	}
	events, err := listAllEvents(ec.Ctx, t.eventRepo, ec.UserID, true)
	if err != nil {
		return nil, fmt.Errorf("查询事件失败: %w", err)
	}
	for i := range events {
		if strings.EqualFold(strings.TrimSpace(events[i].Name), name) {
			return &events[i], nil
		}
	}
	return nil, fmt.Errorf("事件不存在: %s", name)
}

// eventDetail 返回事件 summary + 全部记忆（updated_at DESC，含 facts）。
func (t *EventTool) eventDetail(ctx context.Context, userID string, e *model.Event) (map[string]interface{}, error) {
	mems, err := t.memRepo.ListByEventIDs(ctx, userID, []string{e.ID})
	if err != nil {
		return nil, fmt.Errorf("list event memories: %w", err)
	}
	sort.Slice(mems, func(i, j int) bool {
		if !mems[i].UpdatedAt.Equal(mems[j].UpdatedAt) {
			return mems[i].UpdatedAt.After(mems[j].UpdatedAt)
		}
		return mems[i].ID < mems[j].ID
	})
	items := make([]eventMemoryItem, 0, len(mems))
	for _, m := range mems {
		items = append(items, eventMemoryItem{
			ID:        m.ID,
			Content:   m.MemoryValue,
			Topic:     m.Topic,
			CreatedAt: formatMsgTime(m.CreatedAt),
			UpdatedAt: formatMsgTime(m.UpdatedAt),
			Facts:     parseEventFacts(m.Facts),
		})
	}
	detail := map[string]interface{}{
		"id":             e.ID,
		"name":           e.Name,
		"summary":        e.Summary,
		"memory_count":   len(items),
		"memories":       items,
		"is_deleted":     e.IsDeleted,
		"last_memory_at": "",
	}
	if e.LastMemoryAt != nil {
		detail["last_memory_at"] = formatMsgTime(*e.LastMemoryAt)
	}
	return detail, nil
}
