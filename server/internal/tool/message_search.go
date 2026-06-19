package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/service"
)

// MessageSearchTool — LLM 可主动调用的历史消息检索工具
type MessageSearchTool struct {
	msgRepo          *service.MessageRepo
	convRepo         *service.ConversationRepo
	maxSummaryRounds int // 2*M 上限，用于 search_by_range
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
	return "在用户历史消息中按关键词搜索。当用户询问过去聊过的话题、查找之前讨论过的内容时调用。使用关键词匹配方式"
}

func (t *MessageSearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "search_by_range",
			Display:     "按消息ID范围搜索",
			Description: "根据两条消息ID之间的范围查找用户发言。典型场景：先通过对话摘要了解话题脉络，摘要的 cover_from/cover_to 属性给出了消息ID范围，再用此函数查找该范围内的具体细节、数据或决策。只返回 role=user 的消息，最多 " + fmt.Sprintf("%d", t.maxSummaryRounds*2) + " 条",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"msg_id_start": map[string]interface{}{"type": "string", "description": "范围起始消息ID（摘要的 cover_from）"},
					"msg_id_end":   map[string]interface{}{"type": "string", "description": "范围结束消息ID（摘要的 cover_to）"},
				},
				"required": []string{"msg_id_start", "msg_id_end"},
			},
		},
		{
			Name:        "search",
			Display:     "搜索消息",
			Description: "在用户历史消息中按关键词搜索。从查询中提取核心关键词（如'羽毛球'、'SQL优化'、'深圳'）作为 query 参数，不要传完整句子。支持跨会话检索",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":           map[string]interface{}{"type": "string", "description": "搜索关键词，从用户问题中提取核心名词或实体，不要传完整问句"},
					"limit":           map[string]interface{}{"type": "integer", "description": "返回条数上限，默认20，最大50"},
					"conversation_id": map[string]interface{}{"type": "string", "description": "限定在指定会话中搜索，不传则跨所有会话搜索"},
				},
				"required": []string{"query"},
			},
		}}
}

func (t *MessageSearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName == "search_by_range" {
		return t.executeSearchByRange(ec, args)
	}

	var a struct {
		Query          string `json:"query"`
		Limit          int    `json:"limit"`
		ConversationID string `json:"conversation_id"`
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

	matched, _, err := t.msgRepo.SearchUserMessages(ec.Ctx, ec.UserID, a.Query, a.ConversationID, a.Limit, 0)
	if err != nil {
		log.Printf("[message_search] SearchUserMessages error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("搜索消息失败: %v", err)}
	}

	if len(matched) == 0 {
		data, _ := json.Marshal(map[string]string{
			"results": "（未找到相关历史消息）\n",
		})
		return &Result{Success: true, Data: data}
	}

	// 缓存会话标题
	titleCache := make(map[string]string)

	var sb strings.Builder
	sb.WriteString("### 历史消息检索结果\n")

	shown := 0
	for _, m := range matched {
		roundMsgs, err := t.msgRepo.GetMessagesSince(ec.Ctx, m.ConversationID, m.CreatedAt, 100)
		if err != nil {
			log.Printf("[message_search] GetMessagesSince error: %v", err)
			continue
		}

		filtered := retrieval.FilterMessageRound(roundMsgs, m.ID)
		if len(filtered) == 0 {
			continue
		}

		// 获取会话标题（缓存）
		title, ok := titleCache[m.ConversationID]
		if !ok {
			if cv, err := t.convRepo.FindByID(ec.Ctx, m.ConversationID); err == nil && cv != nil {
				title = cv.Title
			} else {
				title = m.ConversationID
			}
			titleCache[m.ConversationID] = title
		}

		sb.WriteString(retrieval.FormatMessageRoundWithTitle(filtered, title))
		sb.WriteString("\n")
		shown++
	}

	if shown == 0 {
		data, _ := json.Marshal(map[string]string{
			"results": "（未找到相关历史消息）\n",
		})
		return &Result{Success: true, Data: data}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"results": sb.String(),
	})
	return &Result{Success: true, Data: data}
}

// executeSearchByRange 根据两条消息 ID 范围查找用户发言
func (t *MessageSearchTool) executeSearchByRange(ec *ExecuteContext, args json.RawMessage) *Result {
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

	// 找两条消息的 created_at
	startMsg, err := t.msgRepo.GetByIDCols(ec.Ctx, a.MsgIDStart, "id", "created_at")
	if err != nil || startMsg == nil {
		return &Result{Success: false, Error: fmt.Sprintf("消息 %s 不存在", a.MsgIDStart)}
	}
	endMsg, err := t.msgRepo.GetByIDCols(ec.Ctx, a.MsgIDEnd, "id", "created_at")
	if err != nil || endMsg == nil {
		return &Result{Success: false, Error: fmt.Sprintf("消息 %s 不存在", a.MsgIDEnd)}
	}

	// 确保时间顺序
	after := startMsg.CreatedAt
	before := endMsg.CreatedAt
	if after.After(before) {
		after, before = before, after
	}

	limit := t.maxSummaryRounds * 2
	msgs, err := t.msgRepo.ListUserMessagesBetween(ec.Ctx, startMsg.ConversationID, after.Add(-time.Second), before.Add(time.Second), limit)
	if err != nil {
		log.Printf("[message_search] search_by_range error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("范围查询失败: %v", err)}
	}

	if len(msgs) == 0 {
		data, _ := json.Marshal(map[string]string{
			"results": fmt.Sprintf("（消息 %s 到 %s 之间未找到用户消息）", a.MsgIDStart[:8], a.MsgIDEnd[:8]),
		})
		return &Result{Success: true, Data: data}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### 消息范围检索结果 (%d条)\n\n", len(msgs)))
	for _, m := range msgs {
		timeStr := m.CreatedAt.Format("01-02 15:04")
		sb.WriteString(fmt.Sprintf("[%s] %s\n", timeStr, m.Content))
	}

	data, _ := json.Marshal(map[string]interface{}{
		"results": sb.String(),
	})
	return &Result{Success: true, Data: data}
}
