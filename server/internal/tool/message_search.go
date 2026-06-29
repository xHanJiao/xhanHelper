package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const assistantTruncateChars = 100

// ToolMessageOutput — 结构化 JSON 中的单条消息
type ToolMessageOutput struct {
	ID              string              `json:"id"`
	Role            string              `json:"role"`
	Content         string              `json:"content"`
	CreatedAt       string              `json:"created_at"`
	ConversationID  string              `json:"conversation_id"`
	Truncated       bool                `json:"truncated"`
	MatchedKeywords []string            `json:"matched_keywords,omitempty"`
	RoundMessages   []ToolMessageOutput `json:"round_messages,omitempty"`
}

// MessageSearchTool — LLM 可主动调用的历史消息检索工具
type MessageSearchTool struct {
	msgRepo          *service.MessageRepo
	convRepo         *service.ConversationRepo
	maxSummaryRounds int
}

func NewMessageSearchTool(msgRepo *service.MessageRepo, convRepo *service.ConversationRepo, historyRounds, maxSummaryRounds int) *MessageSearchTool {
	if maxSummaryRounds <= 0 {
		maxSummaryRounds = historyRounds / 2
		if maxSummaryRounds < 1 {
			maxSummaryRounds = 1
		}
	}
	return &MessageSearchTool{msgRepo: msgRepo, convRepo: convRepo, maxSummaryRounds: maxSummaryRounds}
}

func (t *MessageSearchTool) Name() string { return "message_search" }

func (t *MessageSearchTool) Description() string {
	return "在用户历史消息中检索。支持关键词搜索、时间范围搜索、按ID获取、统计数量。" +
		"重要提示：做时间范围搜索前先调用 count_messages 评估量级；" +
		"assistant 消息在时间搜索中会被截断，需要完整内容请用 get_by_ids"
}

func (t *MessageSearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "search_by_keyword",
			Display:     "关键词搜索",
			Description: "在用户历史消息中按关键词搜索。从查询中提取核心关键词（如'羽毛球'、'SQL优化'）作为 query，空格/逗号/顿号分隔的多个词为 OR 关系。默认展开命中消息所在对话轮上下文的完整内容。大量结果时用 cursor 翻页。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":           map[string]interface{}{"type": "string", "description": "搜索关键词，空格/逗号/顿号分隔多个词"},
					"conversation_id": map[string]interface{}{"type": "string", "description": "限定会话 ID，不传则跨所有会话搜索"},
					"role":            map[string]interface{}{"type": "string", "description": "消息角色过滤，默认 user，可传 assistant 或 user,assistant"},
					"limit":           map[string]interface{}{"type": "integer", "description": "返回条数，默认20，最大50"},
					"cursor":          map[string]interface{}{"type": "string", "description": "分页游标（上一页返回的 next_cursor），首页不传。格式如 2026-06-15T14:30:00+08:00"},
					"expand_context":  map[string]interface{}{"type": "boolean", "description": "是否展开命中消息所在对话轮上下文，默认 true"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "search_by_time",
			Display:     "时间范围搜索",
			Description: "按时间范围搜索用户历史消息。支持绝对日期时间或相对偏移（last_n_days/last_n_hours）。assistant 消息截断至100字+省略提示，需要完整内容请调 get_by_ids。先调 count_messages 评估量级再决定是否搜索。大量结果时用 cursor 翻页。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start_time":      map[string]interface{}{"type": "string", "description": "起始时间，支持格式: 2026-06-15T14:30:00+08:00 / 2026-06-15 14:30:00 / 2026-06-15 / 2026/06/15"},
					"end_time":        map[string]interface{}{"type": "string", "description": "结束时间，格式同上"},
					"last_n_days":     map[string]interface{}{"type": "integer", "description": "最近 N 天，与 start_time 互斥"},
					"last_n_hours":    map[string]interface{}{"type": "integer", "description": "最近 N 小时，与 start_time 互斥"},
					"conversation_id": map[string]interface{}{"type": "string", "description": "限定会话 ID，不传则跨所有会话搜索"},
					"role":            map[string]interface{}{"type": "string", "description": "消息角色过滤，默认 user，可传 assistant 或 user,assistant"},
					"order":           map[string]interface{}{"type": "string", "description": "排序：asc（最早在前）或 desc（最新在前），默认 desc"},
					"limit":           map[string]interface{}{"type": "integer", "description": "返回条数，默认20，最大50"},
					"cursor":          map[string]interface{}{"type": "string", "description": "分页游标（上一页返回的 next_cursor），首页不传。格式如 2026-06-15T14:30:00+08:00"},
				},
				"required": []string{},
			},
		},
		{
			Name:        "count_messages",
			Display:     "统计消息数量",
			Description: "统计符合条件的消息总数。支持 keyword+time+role+conversation_id 全维度组合过滤。在调用 search_by_keyword 或 search_by_time 之前，先调用此函数评估数据量，帮助决定是否有必要搜索以及是否需要缩小范围。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":           map[string]interface{}{"type": "string", "description": "关键词（空格/逗号分隔，OR 关系）"},
					"start_time":      map[string]interface{}{"type": "string", "description": "起始时间，格式同上"},
					"end_time":        map[string]interface{}{"type": "string", "description": "结束时间，格式同上"},
					"last_n_days":     map[string]interface{}{"type": "integer", "description": "最近 N 天"},
					"last_n_hours":    map[string]interface{}{"type": "integer", "description": "最近 N 小时"},
					"conversation_id": map[string]interface{}{"type": "string", "description": "限定会话 ID"},
					"role":            map[string]interface{}{"type": "string", "description": "消息角色过滤，默认 user"},
				},
				"required": []string{},
			},
		},
		{
			Name:        "get_by_ids",
			Display:     "批量获取消息",
			Description: "根据消息 ID 列表获取完整内容。不受截断限制。查不到的 ID 静默跳过。同一用户的任意会话消息都支持。最多 20 个 ID。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"ids": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "消息 ID 列表，最多 20 个",
					},
				},
				"required": []string{"ids"},
			},
		},
		{
			Name:        "search_by_range",
			Display:     "ID 范围搜索",
			Description: "根据两条消息 ID 之间的范围查找用户发言。典型场景：先通过对话摘要了解话题脉络，摘要的 cover_from/cover_to 属性给出了消息 ID 范围，再用此函数查找该范围内的具体细节。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"msg_id_start": map[string]interface{}{"type": "string", "description": "范围起始消息 ID（摘要的 cover_from）"},
					"msg_id_end":   map[string]interface{}{"type": "string", "description": "范围结束消息 ID（摘要的 cover_to）"},
				},
				"required": []string{"msg_id_start", "msg_id_end"},
			},
		},
	}
}

// Execute — 统一入口
func (t *MessageSearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "search_by_keyword":
		return t.searchByKeyword(ec, args)
	case "search_by_time":
		return t.searchByTime(ec, args)
	case "count_messages":
		return t.countMessages(ec, args)
	case "get_by_ids":
		return t.getByIDs(ec, args)
	case "search_by_range":
		return t.searchByRange(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

// ==================== search_by_keyword ====================

func (t *MessageSearchTool) searchByKeyword(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query          string `json:"query"`
		ConversationID string `json:"conversation_id"`
		Role           string `json:"role"`
		Limit          int    `json:"limit"`
		Cursor         string `json:"cursor"`
		ExpandContext  *bool  `json:"expand_context"`
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
	if a.Role == "" {
		a.Role = "user"
	}
	expandCtx := true
	if a.ExpandContext != nil {
		expandCtx = *a.ExpandContext
	}

	keywords := splitKeywords(a.Query)
	if len(keywords) == 0 {
		return &Result{Success: false, Error: "query 未产生有效关键词，请提供有意义的搜索词"}
	}

	var cursorTime *time.Time
	if a.Cursor != "" {
		parsed, err := parseTimeString(a.Cursor)
		if err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("cursor 解析失败: %v", err)}
		}
		cursorTime = &parsed
	}

	msgs, hasMore, err := t.msgRepo.SearchMessagesByCursor(ec.Ctx, ec.UserID, keywords, a.Role, a.ConversationID, cursorTime, a.Limit)
	if err != nil {
		log.Printf("[message_search] search_by_keyword error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("搜索消息失败: %v", err)}
	}

	total, err := t.msgRepo.CountMessages(ec.Ctx, ec.UserID, keywords, a.Role, a.ConversationID, nil, nil)
	if err != nil {
		log.Printf("[message_search] count error: %v", err)
		total = int64(len(msgs))
	}

	output := make([]ToolMessageOutput, 0, len(msgs))
	for _, m := range msgs {
		mo := t.formatMessage(m, nil)
		mo.MatchedKeywords = findMatchedKeywords(m.Content, keywords)
		if expandCtx && m.Role == "user" {
			mo.RoundMessages = t.getRoundMessages(ec.Ctx, m)
		}
		output = append(output, mo)
	}

	var nextCursor string
	if hasMore && len(msgs) > 0 {
		nextCursor = formatMsgTime(msgs[len(msgs)-1].CreatedAt)
	}

	resp := map[string]interface{}{
		"total":    total,
		"has_more": hasMore,
		"messages": output,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}

	data, _ := json.Marshal(resp)
	return &Result{Success: true, Data: data}
}

// ==================== search_by_time ====================

func (t *MessageSearchTool) searchByTime(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		StartTime      string `json:"start_time"`
		EndTime        string `json:"end_time"`
		LastNDays      int    `json:"last_n_days"`
		LastNHours     int    `json:"last_n_hours"`
		ConversationID string `json:"conversation_id"`
		Role           string `json:"role"`
		Order          string `json:"order"`
		Limit          int    `json:"limit"`
		Cursor         string `json:"cursor"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}
	if a.Limit > 50 {
		a.Limit = 50
	}
	if a.Role == "" {
		a.Role = "user"
	}
	if a.Order == "" {
		a.Order = "desc"
	}
	if a.Order != "asc" && a.Order != "desc" {
		return &Result{Success: false, Error: "order 只能是 asc 或 desc"}
	}

	after, before, err := parseTimeParams(a.StartTime, a.EndTime, a.LastNDays, a.LastNHours)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if after == nil && before == nil {
		return &Result{Success: false, Error: "请提供时间范围（start_time/end_time 或 last_n_days/last_n_hours）"}
	}

	var cursorTime *time.Time
	if a.Cursor != "" {
		parsed, err := parseTimeString(a.Cursor)
		if err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("cursor 解析失败: %v", err)}
		}
		cursorTime = &parsed
	}

	msgs, hasMore, err := t.msgRepo.SearchMessagesByTimeCursor(ec.Ctx, ec.UserID, a.Role, a.ConversationID, after, before, cursorTime, a.Order, a.Limit)
	if err != nil {
		log.Printf("[message_search] search_by_time error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("时间范围搜索失败: %v", err)}
	}

	total, err := t.msgRepo.CountMessages(ec.Ctx, ec.UserID, nil, a.Role, a.ConversationID, after, before)
	if err != nil {
		log.Printf("[message_search] count error: %v", err)
		total = int64(len(msgs))
	}

	output := make([]ToolMessageOutput, 0, len(msgs))
	for _, m := range msgs {
		mo := t.formatMessage(m, nil)
		if m.Role == "assistant" {
			truncated, was := truncateAssistantContent(m.Content, m.ID)
			mo.Content = truncated
			mo.Truncated = was
		}
		output = append(output, mo)
	}

	var nextCursor string
	if hasMore && len(msgs) > 0 {
		nextCursor = formatMsgTime(msgs[len(msgs)-1].CreatedAt)
	}

	resp := map[string]interface{}{
		"total":    total,
		"has_more": hasMore,
		"messages": output,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}

	data, _ := json.Marshal(resp)
	return &Result{Success: true, Data: data}
}

// ==================== count_messages ====================

func (t *MessageSearchTool) countMessages(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Query          string `json:"query"`
		StartTime      string `json:"start_time"`
		EndTime        string `json:"end_time"`
		LastNDays      int    `json:"last_n_days"`
		LastNHours     int    `json:"last_n_hours"`
		ConversationID string `json:"conversation_id"`
		Role           string `json:"role"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Role == "" {
		a.Role = "user"
	}

	after, before, err := parseTimeParams(a.StartTime, a.EndTime, a.LastNDays, a.LastNHours)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	var keywords []string
	if a.Query != "" {
		keywords = splitKeywords(a.Query)
	}

	count, err := t.msgRepo.CountMessages(ec.Ctx, ec.UserID, keywords, a.Role, a.ConversationID, after, before)
	if err != nil {
		log.Printf("[message_search] count_messages error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("统计失败: %v", err)}
	}

	data, _ := json.Marshal(map[string]interface{}{"count": count})
	return &Result{Success: true, Data: data}
}

// ==================== get_by_ids ====================

func (t *MessageSearchTool) getByIDs(ec *ExecuteContext, args json.RawMessage) *Result {
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

	msgs, err := t.msgRepo.GetByIDs(ec.Ctx, a.IDs, ec.UserID)
	if err != nil {
		log.Printf("[message_search] get_by_ids error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("获取消息失败: %v", err)}
	}

	output := make([]ToolMessageOutput, 0, len(msgs))
	for _, m := range msgs {
		output = append(output, t.formatMessage(m, nil))
	}

	data, _ := json.Marshal(map[string]interface{}{"messages": output})
	return &Result{Success: true, Data: data}
}

// ==================== search_by_range ====================

func (t *MessageSearchTool) searchByRange(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		MsgIDStart string `json:"msg_id_start"`
		MsgIDEnd   string `json:"msg_id_end"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.MsgIDStart == "" || a.MsgIDEnd == "" {
		return &Result{Success: false, Error: "msg_id_start 和 msg_id_end 不能为空"}
	}

	startMsg, err := t.msgRepo.GetByIDCols(ec.Ctx, a.MsgIDStart, "id", "conversation_id", "created_at")
	if err != nil || startMsg == nil {
		return &Result{Success: false, Error: fmt.Sprintf("消息 %s 不存在", a.MsgIDStart)}
	}
	endMsg, err := t.msgRepo.GetByIDCols(ec.Ctx, a.MsgIDEnd, "id", "created_at")
	if err != nil || endMsg == nil {
		return &Result{Success: false, Error: fmt.Sprintf("消息 %s 不存在", a.MsgIDEnd)}
	}

	after := startMsg.CreatedAt
	before := endMsg.CreatedAt
	if after.After(before) {
		after, before = before, after
	}

	msgs, err := t.msgRepo.ListUserMessagesBetween(ec.Ctx, startMsg.ConversationID, after.Add(-time.Second), before.Add(time.Second), 0)
	if err != nil {
		log.Printf("[message_search] search_by_range error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("范围查询失败: %v", err)}
	}

	output := make([]ToolMessageOutput, 0, len(msgs))
	for _, m := range msgs {
		output = append(output, t.formatMessage(m, nil))
	}

	data, _ := json.Marshal(map[string]interface{}{
		"total":    len(output),
		"messages": output,
	})
	return &Result{Success: true, Data: data}
}

// ==================== format helpers ====================

// formatMessage 将 model.Message 转为 ToolMessageOutput。
// matchedKeywords 由调用方设置，不在此方法内计算。
func (t *MessageSearchTool) formatMessage(m model.Message, matchedKeywords []string) ToolMessageOutput {
	return ToolMessageOutput{
		ID:              m.ID,
		Role:            m.Role,
		Content:         m.Content,
		CreatedAt:       formatMsgTime(m.CreatedAt),
		ConversationID:  m.ConversationID,
		Truncated:       false,
		MatchedKeywords: matchedKeywords,
	}
}

// getRoundMessages 获取匹配消息所在对话轮的上下文。
// 从该 user 消息的时间开始，向前收集直到遇到下一条不同 ID 的 user 消息或搜索窗口结束。
func (t *MessageSearchTool) getRoundMessages(ctx context.Context, matched model.Message) []ToolMessageOutput {
	msgs, err := t.msgRepo.GetMessagesSince(ctx, matched.ConversationID, matched.CreatedAt, 100)
	if err != nil {
		log.Printf("[message_search] getRoundMessages error: %v", err)
		return nil
	}

	var result []ToolMessageOutput
	for _, m := range msgs {
		if m.Role == "tool" || m.IsInternal {
			continue
		}
		// 遇到下一条不同 ID 的 user 消息，停止
		if m.Role == "user" && m.ID != matched.ID {
			break
		}
		mo := t.formatMessage(m, nil)
		// 不截断 round 内的 assistant 消息（上下文保持完整）
		result = append(result, mo)
	}
	return result
}

// ==================== pure helpers ====================

// splitKeywords 按空格、逗号、顿号分割 query 并去重
func splitKeywords(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	var result []string
	seen := make(map[string]bool)
	for _, part := range strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == ',' || r == '，' || r == '、'
	}) {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			result = append(result, part)
		}
	}
	return result
}

// acceptedTimeFormats — parseTimeString 依次尝试的时间格式列表
var acceptedTimeFormats = []string{
	time.RFC3339,          // 2026-06-15T14:30:00+08:00
	"2006-01-02 15:04:05", // 2026-06-15 14:30:00
	"2006-01-02T15:04:05", // 2026-06-15T14:30:00（无时区）
	"2006/01/02 15:04:05", // 2026/06/15 14:30:00
	"2006-01-02",          // 2026-06-15（默认当日 00:00:00）
	"2006/01/02",          // 2026/06/15（默认当日 00:00:00）
}

// parseTimeString 依次尝试多种常见时间格式，返回东八区时间。
func parseTimeString(s string) (time.Time, error) {
	for _, layout := range acceptedTimeFormats {
		t, err := time.ParseInLocation(layout, s, config.BJ)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间 %q，支持格式: 2026-06-15T14:30:00+08:00 / 2026-06-15 14:30:00 / 2026-06-15", s)
}

// isDateOnly 判断字符串是否为纯日期格式（无时间部分）
func isDateOnly(s string) bool {
	for _, layout := range []string{"2006-01-02", "2006/01/02"} {
		if _, err := time.ParseInLocation(layout, s, config.BJ); err == nil {
			return true
		}
	}
	return false
}

// parseTimeParams 解析时间参数，返回 after/before 的指针（nil 表示无限制）。
// startTime 和 last_n_* 互斥。
func parseTimeParams(startTime, endTime string, lastNDays, lastNHours int) (after, before *time.Time, err error) {
	hasAbsolute := startTime != "" || endTime != ""
	hasRelative := lastNDays > 0 || lastNHours > 0
	if hasAbsolute && hasRelative {
		return nil, nil, fmt.Errorf("start_time/end_time 与 last_n_days/last_n_hours 互斥，请只使用一种方式")
	}

	now := time.Now()

	if hasAbsolute {
		if startTime != "" {
			t, e := parseTimeString(startTime)
			if e != nil {
				return nil, nil, e
			}
			after = &t
		}
		if endTime != "" {
			t, e := parseTimeString(endTime)
			if e != nil {
				return nil, nil, e
			}
			// 纯日期（如 2026-06-27）→ 含当日，推到次日 00:00:00
			if isDateOnly(endTime) {
				t = t.Add(24 * time.Hour)
			}
			before = &t
		}
		return after, before, nil
	}

	if hasRelative {
		var d time.Duration
		if lastNDays > 0 {
			d = time.Duration(lastNDays) * 24 * time.Hour
		} else if lastNHours > 0 {
			d = time.Duration(lastNHours) * time.Hour
		}
		t := now.Add(-d)
		after = &t
		return after, nil, nil
	}

	return nil, nil, nil
}

// formatMsgTime 统一格式化时间为 ISO 8601 +08:00
func formatMsgTime(t time.Time) string {
	return t.In(config.BJ).Format(time.RFC3339)
}

// findMatchedKeywords 大小写不敏感地检查内容中包含哪些关键词
func findMatchedKeywords(content string, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	lower := strings.ToLower(content)
	var matched []string
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			matched = append(matched, kw)
		}
	}
	return matched
}

// truncateAssistantContent 截断超过 100 汉字的内容并加省略提示
func truncateAssistantContent(content, msgID string) (string, bool) {
	runes := []rune(content)
	if len(runes) <= assistantTruncateChars {
		return content, false
	}
	truncated := string(runes[:assistantTruncateChars]) + fmt.Sprintf("（内容已截断，完整内容请用 get_by_ids 查询，消息ID: %s）", msgID)
	return truncated, true
}
