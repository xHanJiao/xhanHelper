package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"xhan-helper/internal/activity"
)

// UserActionLogTool — 用户行为日志只读查询（骨架级）。
// 数据来自 events.db 的派生层（day_stats / sessions / event_resources / day_health）。
// 纪律（实施技术方案 §5.3）：只读、参数固定维度、不给 SQL、不直读主库。
// 仅在采集启用时注册（云端关闭则不注册），故 store 非 nil。
type UserActionLogTool struct {
	store *activity.Store
}

func NewUserActionLogTool(store *activity.Store) *UserActionLogTool {
	return &UserActionLogTool{store: store}
}

func (t *UserActionLogTool) Name() string { return "user_action_log" }

func (t *UserActionLogTool) Description() string {
	return "查询本机采集的用户行为日志（只读）。day_skeleton 给出某天总览（每小时活动量、在场区间、碰过的资源、数据体检）；" +
		"list_sessions 给出某时间范围内的在场/使用区间列表。用于回答「我那天干了多久」「几点在忙什么」「这段时间用了什么」这类问题。" +
		"只提供时间去重后的计数与区间，不含任何聊天/文档正文内容。"
}

func (t *UserActionLogTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "用户行为日志。某天总览 / 时间区间列表（只读）。",
		Funcs:     []string{"day_skeleton", "list_sessions"},
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
	}
}

func (t *UserActionLogTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "day_skeleton":
		return t.daySkeleton(t.store, args)
	case "list_sessions":
		return t.listSessions(t.store, args)
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
