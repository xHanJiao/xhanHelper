package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/service"
)

// MessageSearchTool — LLM 可主动调用的历史消息检索工具
type MessageSearchTool struct {
	msgRepo  *service.MessageRepo
	convRepo *service.ConversationRepo
}

func NewMessageSearchTool(msgRepo *service.MessageRepo, convRepo *service.ConversationRepo) *MessageSearchTool {
	return &MessageSearchTool{msgRepo: msgRepo, convRepo: convRepo}
}

func (t *MessageSearchTool) Name() string { return "message_search" }

func (t *MessageSearchTool) Description() string {
	return "在用户历史消息中按关键词搜索。当用户询问过去聊过的话题、查找之前讨论过的内容时调用。使用关键词匹配方式"
}

func (t *MessageSearchTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "search",
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
