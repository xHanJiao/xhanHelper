package tool

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MailboxTool — 信箱投递工具
type MailboxTool struct {
	mailboxRepo *service.MailboxRepo
}

func NewMailboxTool(repo *service.MailboxRepo) *MailboxTool {
	return &MailboxTool{mailboxRepo: repo}
}

func (t *MailboxTool) Name() string        { return "mailbox" }
func (t *MailboxTool) Description() string { return "向用户信箱投递消息" }

type mailboxDeliverArgs struct {
	Items []struct {
		Title      string `json:"title"`
		Content    string `json:"content"`
		Greeting   string `json:"greeting"`
		Type       string `json:"type"`
		Source     string `json:"source"`
		EntityType string `json:"entity_type"`
		EntityID   string `json:"entity_id"`
	} `json:"items"`
}

func (t *MailboxTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "deliver",
		Description: "向用户信箱投递一条或多条消息。type: daily_brief（日报）/ theme_finding（主题发现）/ candidate_theme（候选主题）。source 为来源主题名或 system",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"items": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title":       map[string]interface{}{"type": "string", "description": "消息标题"},
							"content":     map[string]interface{}{"type": "string", "description": "消息正文"},
							"type":        map[string]interface{}{"type": "string", "description": "消息类型", "enum": []string{"daily_brief", "theme_finding", "candidate_theme"}},
							"source":      map[string]interface{}{"type": "string", "description": "来源主题名"},
							"entity_type": map[string]interface{}{"type": "string", "description": "关联实体类型，theme_finding 必填 'theme'"},
							"entity_id":   map[string]interface{}{"type": "string", "description": "关联实体 ID"},
						},
						"required": []string{"title", "content", "type"},
					},
				},
			},
			"required": []string{"items"},
		},
	}}
}

func (t *MailboxTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var a mailboxDeliverArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if len(a.Items) == 0 {
		return &Result{Success: false, Error: "items 不能为空"}
	}

	delivered := 0
	for _, item := range a.Items {
		mailItem := &model.MailboxItem{
			ID:         uuid.New().String(),
			UserID:     ec.UserID,
			Title:      item.Title,
			Content:    item.Content,
			Type:       item.Type,
			Greeting:   item.Greeting,
			Source:     item.Source,
			EntityType: item.EntityType,
			EntityID:   item.EntityID,
		}
		if err := t.mailboxRepo.Create(ec.Ctx, mailItem); err != nil {
			log.Printf("[mailbox] create error: %v", err)
			continue
		}
		delivered++
	}

	data, _ := json.Marshal(map[string]interface{}{
		"delivered": delivered,
	})

	return &Result{Success: true, Data: data}
}
