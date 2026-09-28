package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"xhan-helper/internal/activity"
)

// UserActionLogTool — 用户行为日志只读查询。
// 数据来自 events.db 的派生层（day_stats / sessions / event_resources / day_health）与事实层（events）。
// 纪律（实施技术方案 §5.3）：只读、参数固定维度、不给 SQL、不直读主库（唯一的跨库例外：doc 资源取标题）。
// 仅在采集启用时注册（云端关闭则不注册），故 store 非 nil。
type UserActionLogTool struct {
	store     *activity.Store
	docTitles func(ctx context.Context, userID string, ids []string) (map[string]string, error)
}

func NewUserActionLogTool(store *activity.Store, docTitles func(context.Context, string, []string) (map[string]string, error)) *UserActionLogTool {
	return &UserActionLogTool{store: store, docTitles: docTitles}
}

func (t *UserActionLogTool) Name() string { return "user_action_log" }

func (t *UserActionLogTool) Description() string {
	return "查询本机采集的用户行为日志（只读）。day_skeleton 给出某天总览（每小时活动量、在场区间、碰过的资源、数据体检）；" +
		"list_sessions 给出某时间范围内的在场/使用区间列表；list_events 按类型/来源/actor/资源/区间取事件清单（带资源与工具名）；" +
		"time_ranges_by_resource 给出某资源被碰过的时段；facts_by_memory 给出某记忆关联的最近事件。" +
		"用于回答「我那天干了多久」「几点在忙什么」「这段时间用了什么」「这个网页/仓库什么时候看的」这类问题。" +
		"只提供时间去重后的计数、区间与资源标识，不含聊天/文档正文（明细级另设）。"
}

func (t *UserActionLogTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "用户行为日志。某天总览 / 区间列表 / 事件清单 / 资源时段 / 记忆关联（只读）。",
		Funcs:     []string{"day_skeleton", "list_sessions", "list_events", "time_ranges_by_resource", "facts_by_memory"},
	}, true
}

func (t *UserActionLogTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *UserActionLogTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "day_skeleton",
			Display:     "当日总览",
			Description: "取某一天（本地日）的用户行为总览：每小时×来源的活动计数、当天在场/使用区间列表、当天碰过的资源去重清单、数据体检摘要。回答「那天大概干了什么、忙了多久」首选。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": map[string]interface{}{"type": "string", "description": "本地日期，格式 YYYY-MM-DD"},
				},
				"required": []string{"date"},
			},
		},
		{
			Name:        "list_sessions",
			Display:     "区间列表",
			Description: "取某时间范围内的在场/使用区间列表（起止、时长、闭合方式、父区间、锚）。可指定层级 level（0=系统唤醒，1=应用，2=焦点，4=组件）；不填则全部层级。from/to 为本地时间，格式 2006-01-02T15:04:05。limit 为返回条数上限（默认 500，硬上限 500）。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from":  map[string]interface{}{"type": "string", "description": "起始时间（本地），格式 2006-01-02T15:04:05"},
					"to":    map[string]interface{}{"type": "string", "description": "结束时间（本地，不含），同上格式"},
					"level": map[string]interface{}{"type": "integer", "description": "层级过滤，-1 或省略=全部；可选 0/1/2/4"},
					"limit": map[string]interface{}{"type": "integer", "description": "返回条数上限，默认 500，最大 500"},
				},
				"required": []string{"from", "to"},
			},
		},
		{
			Name:        "list_events",
			Display:     "事件清单",
			Description: "按条件取某时间范围内的事件清单（只读）。过滤维度可任意组合：type（精确事件类型）、source（域前缀，如 browser/tool/chat）、actor（user/assistant/system）、session_id（所属区间）、resource_kind+resource_id（同一资源，两者必须同给）。返回每条事件的类型、来源、时间、actor、工具名与碰过的资源。from/to 为本地时间。limit 默认 500，硬上限 500。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from":          map[string]interface{}{"type": "string", "description": "起始时间（本地），格式 2006-01-02T15:04:05"},
					"to":            map[string]interface{}{"type": "string", "description": "结束时间（本地，不含），同上格式"},
					"type":          map[string]interface{}{"type": "string", "description": "事件类型精确匹配，如 browser.nav.started"},
					"source":        map[string]interface{}{"type": "string", "description": "域前缀，如 browser / tool / chat / user"},
					"actor":         map[string]interface{}{"type": "string", "description": "user / assistant / system"},
					"session_id":    map[string]interface{}{"type": "string", "description": "所属区间 id（level=1）"},
					"resource_kind": map[string]interface{}{"type": "string", "description": "资源类别：url/repo/file/doc/memory/bill/reminder；须与 resource_id 同给"},
					"resource_id":   map[string]interface{}{"type": "string", "description": "资源 id（与 resource_kind 同给）"},
					"limit":         map[string]interface{}{"type": "integer", "description": "返回条数上限，默认 500，最大 500"},
				},
				"required": []string{"from", "to"},
			},
		},
		{
			Name:        "time_ranges_by_resource",
			Display:     "资源时段",
			Description: "取某资源在某时间范围内被碰过的时段（相邻事件间隔 ≤5 分钟并成一段）。返回每段的起止、时长与事件数。用于回答「这个网页/仓库/文档什么时候看的、看了几段」。from/to 为本地时间。limit 为段数上限，默认 500，最大 500。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"resource_kind": map[string]interface{}{"type": "string", "description": "资源类别：url/repo/file/doc/memory/bill/reminder"},
					"resource_id":   map[string]interface{}{"type": "string", "description": "资源 id"},
					"from":          map[string]interface{}{"type": "string", "description": "起始时间（本地），格式 2006-01-02T15:04:05"},
					"to":            map[string]interface{}{"type": "string", "description": "结束时间（本地，不含），同上格式"},
					"limit":         map[string]interface{}{"type": "integer", "description": "返回段数上限，默认 500，最大 500"},
				},
				"required": []string{"resource_kind", "resource_id", "from", "to"},
			},
		},
		{
			Name:        "facts_by_memory",
			Display:     "记忆关联事件",
			Description: "取某条记忆关联的最近事件（沿事件-记忆引用倒序）。用于回答「这条记忆是什么时候、在干什么的时候记下的」。limit 默认 100，最大 100。引用尚未填充时返回空清单（不是错误）。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"memory_id": map[string]interface{}{"type": "string", "description": "记忆 id"},
					"limit":     map[string]interface{}{"type": "integer", "description": "返回条数上限，默认 100，最大 100"},
				},
				"required": []string{"memory_id"},
			},
		},
	}
}

func (t *UserActionLogTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "day_skeleton":
		return t.daySkeleton(t.store, args)
	case "list_sessions":
		return t.listSessions(t.store, args)
	case "list_events":
		return t.listEvents(ec, t.store, args)
	case "time_ranges_by_resource":
		return t.timeRangesByResource(t.store, args)
	case "facts_by_memory":
		return t.factsByMemory(t.store, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

func (t *UserActionLogTool) daySkeleton(store *activity.Store, args json.RawMessage) *Result {
	var in struct {
		Date string `json:"date"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if in.Date == "" {
		return &Result{Success: false, Error: "date 不能为空"}
	}
	sk, err := store.DaySkeleton(in.Date)
	if err != nil {
		log.Printf("[user_action_log] day_skeleton(%s) error: %v", in.Date, err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	data, err := json.Marshal(sk)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}
	return &Result{Success: true, Data: data}
}

func (t *UserActionLogTool) listSessions(store *activity.Store, args json.RawMessage) *Result {
	var in struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Level *int   `json:"level"`
		Limit *int   `json:"limit"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	from, err := time.ParseInLocation("2006-01-02T15:04:05", in.From, time.Local)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("from 格式错误（要 2006-01-02T15:04:05）: %v", err)}
	}
	to, err := time.ParseInLocation("2006-01-02T15:04:05", in.To, time.Local)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("to 格式错误（要 2006-01-02T15:04:05）: %v", err)}
	}
	if !to.After(from) {
		return &Result{Success: false, Error: "to 必须晚于 from"}
	}
	level := -1
	if in.Level != nil {
		level = *in.Level
	}
	limit := 0
	if in.Limit != nil {
		limit = *in.Limit
	}
	sessions, err := store.ListSessions(from, to, level, limit)
	if err != nil {
		log.Printf("[user_action_log] list_sessions(%s, %s, %d) error: %v", in.From, in.To, level, err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	data, err := json.Marshal(map[string]any{"sessions": sessions, "count": len(sessions)})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}
	return &Result{Success: true, Data: data}
}

// parseRange 解析 from/to 本地时间并校验先后。
func parseRange(fromStr, toStr string) (time.Time, time.Time, string) {
	from, err := time.ParseInLocation("2006-01-02T15:04:05", fromStr, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Sprintf("from 格式错误（要 2006-01-02T15:04:05）: %v", err)
	}
	to, err := time.ParseInLocation("2006-01-02T15:04:05", toStr, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Sprintf("to 格式错误（要 2006-01-02T15:04:05）: %v", err)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, "to 必须晚于 from"
	}
	return from, to, ""
}

func (t *UserActionLogTool) listEvents(ec *ExecuteContext, store *activity.Store, args json.RawMessage) *Result {
	var in struct {
		From         string `json:"from"`
		To           string `json:"to"`
		Type         string `json:"type"`
		Source       string `json:"source"`
		Actor        string `json:"actor"`
		SessionID    string `json:"session_id"`
		ResourceKind string `json:"resource_kind"`
		ResourceID   string `json:"resource_id"`
		Limit        *int   `json:"limit"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	from, to, msg := parseRange(in.From, in.To)
	if msg != "" {
		return &Result{Success: false, Error: msg}
	}
	if (in.ResourceKind == "") != (in.ResourceID == "") {
		return &Result{Success: false, Error: "resource_kind 与 resource_id 必须同时提供"}
	}
	limit := 0
	if in.Limit != nil {
		limit = *in.Limit
	}
	events, err := store.ListEvents(from, to, activity.EventFilter{
		Type: in.Type, Source: in.Source, Actor: in.Actor, SessionID: in.SessionID,
		ResourceKind: in.ResourceKind, ResourceID: in.ResourceID,
	}, limit)
	if err != nil {
		log.Printf("[user_action_log] list_events(%s, %s) error: %v", in.From, in.To, err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	// 仅 doc 资源需要主库标题；其余 kind 的"标题"就是 canonical / 工具名。
	if t.docTitles != nil {
		t.attachDocTitles(ec.Ctx, ec.UserID, events)
	}
	return marshalEvents(events)
}

func (t *UserActionLogTool) timeRangesByResource(store *activity.Store, args json.RawMessage) *Result {
	var in struct {
		ResourceKind string `json:"resource_kind"`
		ResourceID   string `json:"resource_id"`
		From         string `json:"from"`
		To           string `json:"to"`
		Limit        *int   `json:"limit"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if in.ResourceKind == "" || in.ResourceID == "" {
		return &Result{Success: false, Error: "resource_kind 与 resource_id 不能为空"}
	}
	from, to, msg := parseRange(in.From, in.To)
	if msg != "" {
		return &Result{Success: false, Error: msg}
	}
	limit := 0
	if in.Limit != nil {
		limit = *in.Limit
	}
	ranges, err := store.TimeRangesByResource(in.ResourceKind, in.ResourceID, from, to, limit)
	if err != nil {
		log.Printf("[user_action_log] time_ranges_by_resource(%s/%s) error: %v", in.ResourceKind, in.ResourceID, err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	data, err := json.Marshal(map[string]any{"ranges": ranges, "count": len(ranges)})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}
	return &Result{Success: true, Data: data}
}

func (t *UserActionLogTool) factsByMemory(store *activity.Store, args json.RawMessage) *Result {
	var in struct {
		MemoryID string `json:"memory_id"`
		Limit    *int   `json:"limit"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if in.MemoryID == "" {
		return &Result{Success: false, Error: "memory_id 不能为空"}
	}
	limit := 0
	if in.Limit != nil {
		limit = *in.Limit
	}
	events, err := store.FactsByMemory(in.MemoryID, limit)
	if err != nil {
		log.Printf("[user_action_log] facts_by_memory(%s) error: %v", in.MemoryID, err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	return marshalEvents(events)
}

// attachDocTitles 把事件里 doc 资源的 canonical（=doc id）批量换成标题；查不到保留 id。
func (t *UserActionLogTool) attachDocTitles(ctx context.Context, userID string, events []activity.EventSummary) {
	ids := []string{}
	seen := map[string]bool{}
	for i := range events {
		for _, r := range events[i].Resources {
			if r.Kind == activity.ResourceKindDoc && !seen[r.ResourceID] {
				seen[r.ResourceID] = true
				ids = append(ids, r.ResourceID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	titles, err := t.docTitles(ctx, userID, ids)
	if err != nil {
		// 降级：拿不到标题不影响事件清单，保留 id 即可，记录一次原因。
		log.Printf("[user_action_log] 取文档标题失败，保留 id: %v", err)
		return
	}
	for i := range events {
		for j := range events[i].Resources {
			r := &events[i].Resources[j]
			if r.Kind == activity.ResourceKindDoc {
				if title, ok := titles[r.ResourceID]; ok {
					r.Canonical = title
				}
			}
		}
	}
}

func marshalEvents(events []activity.EventSummary) *Result {
	data, err := json.Marshal(map[string]any{"events": events, "count": len(events)})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}
	return &Result{Success: true, Data: data}
}
