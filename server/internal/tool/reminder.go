package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const reminderNotificationTitle = "辽辽提醒"

type ReminderTool struct {
	reminderRepo *service.ReminderRepo
	llmClient    llm.LLMClient
}

func NewReminderTool(reminderRepo *service.ReminderRepo, llmClient llm.LLMClient) *ReminderTool {
	return &ReminderTool{reminderRepo: reminderRepo, llmClient: llmClient}
}

func (t *ReminderTool) Name() string { return "reminder" }

func (t *ReminderTool) Description() string {
	return "设置、查看、搜索、取消待办提醒。提醒到期后系统会弹通知，点击通知助手会主动在聊天里提起。支持一次性提醒，循环提醒预留中。"
}

func (t *ReminderTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_reminder",
			Display:     "创建提醒",
			Description: "创建一个待办提醒。用户说「提醒我XX」「帮我记一下XX」「别忘了XX」时调用。截止时间必须是具体的日期时间（如 2026-07-21T13:00:00+08:00），不允许模糊。如果用户没有给具体时间，追问确认后再调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content":  map[string]interface{}{"type": "string", "description": "提醒内容"},
					"deadline": map[string]interface{}{"type": "string", "description": "ISO8601 截止时间，如 2026-07-21T13:00:00+08:00"},
				},
				"required": []string{"content", "deadline"},
			},
		},
		{
			Name:        "cancel_reminder",
			Display:     "取消提醒",
			Description: "取消一个待办提醒。用户说「取消所有提醒」「不用提醒我XX了」时调用。如果不确定是哪个提醒，先调 list_reminders 或 search_reminders 找到 id 再取消。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "提醒 ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "list_reminders",
			Display:     "查看提醒列表",
			Description: "列出用户的待办提醒。可按状态筛选。用于用户说「我有哪些提醒」「查看我的提醒」时调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{"type": "string", "description": "筛选状态: pending / acknowledged / cancelled。不传则返回全部"},
					"limit":  map[string]interface{}{"type": "integer", "description": "返回条数，默认 20"},
					"offset": map[string]interface{}{"type": "integer", "description": "偏移量，默认 0"},
				},
			},
		},
		{
			Name:        "search_reminders",
			Display:     "搜索提醒",
			Description: "按关键字搜索提醒内容。用于用户说「关于XX的提醒」「找一下XX提醒」时调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":  map[string]interface{}{"type": "string", "description": "搜索关键字"},
					"status": map[string]interface{}{"type": "string", "description": "筛选状态，不传则返回全部"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "mark_reminder_mentioned",
			Display:     "标记提醒已提起",
			Description: "标记一条过期提醒已被你在对话中提起过，避免重复提起同一个过期提醒。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "提醒 ID"},
				},
				"required": []string{"id"},
			},
		},
	}
}

// ReadOnlyFuncs — 列表/搜索只读，创建/取消/状态变更为写（plan 模式拦截）
func (t *ReminderTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{
		"list_reminders":   true,
		"search_reminders": true,
	}
}

func (t *ReminderTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_reminder":
		return t.createReminder(ec, args)
	case "cancel_reminder":
		return t.cancelReminder(ec, args)
	case "list_reminders":
		return t.listReminders(ec, args)
	case "search_reminders":
		return t.searchReminders(ec, args)
	case "mark_reminder_mentioned":
		return t.markReminderMentioned(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
}

func (t *ReminderTool) createReminder(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Content        string               `json:"content"`
		Deadline       string               `json:"deadline"`
		RecurrenceRule *recurrenceRuleInput `json:"recurrence_rule"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if req.Content == "" {
		return &Result{Success: false, Error: "content 不能为空"}
	}
	if req.Deadline == "" {
		return &Result{Success: false, Error: "deadline 不能为空，请提供具体的提醒时间"}
	}

	deadline, err := time.Parse(time.RFC3339, req.Deadline)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("deadline 格式错误，请使用 ISO8601 格式（如 2026-07-21T13:00:00+08:00）: %v", err)}
	}

	if time.Until(deadline) < 1*time.Minute {
		return &Result{Success: false, Error: "提醒时间太近，请至少提前1分钟创建"}
	}

	rem := &model.Reminder{
		ID:                uuid.New().String(),
		UserID:            ec.UserID,
		Content:           req.Content,
		Deadline:          deadline,
		Status:            model.ReminderStatusGenerating,
		NotificationTitle: reminderNotificationTitle,
		AcknowledgedAt:    model.ReminderZeroTime,
		MentionedAt:       model.ReminderZeroTime,
	}
	if req.RecurrenceRule != nil && req.RecurrenceRule.Type != "" {
		ruleJSON, _ := json.Marshal(req.RecurrenceRule)
		rem.RecurrenceRule = string(ruleJSON)
	}

	if err := t.reminderRepo.Create(ec.Ctx, rem); err != nil {
		log.Printf("[reminder] create error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("创建提醒失败: %v", err)}
	}

	// 通知客户端有新提醒需同步（非阻塞，replay/测试时 Events 为 nil）
	if ec.Events != nil {
		select {
		case ec.Events <- &llm.StreamEvent{Type: "reminders_changed", Content: "{}"}:
		default:
		}
	}

	go t.generateContent(rem.ID, req.Content, deadline)

	log.Printf("[reminder] created: %s deadline=%v", rem.ID[:8], deadline)
	data, _ := json.Marshal(map[string]interface{}{
		"id":       rem.ID,
		"content":  rem.Content,
		"deadline": rem.Deadline,
		"status":   rem.Status,
	})
	return &Result{Success: true, Data: data}
}

func (t *ReminderTool) cancelReminder(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id 不能为空"}
	}

	rem, err := t.reminderRepo.FindByID(ec.Ctx, ec.UserID, req.ID)
	if err != nil {
		log.Printf("[reminder] cancel find error: %v", err)
		return &Result{Success: false, Error: "提醒不存在"}
	}
	if rem.Status != model.ReminderStatusPending && rem.Status != model.ReminderStatusGenerating {
		return &Result{Success: false, Error: fmt.Sprintf("提醒状态为 %s，无法取消", rem.Status)}
	}

	if err := t.reminderRepo.Cancel(ec.Ctx, ec.UserID, req.ID); err != nil {
		log.Printf("[reminder] cancel error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("取消提醒失败: %v", err)}
	}

	log.Printf("[reminder] cancelled: %s", req.ID[:8])
	data, _ := json.Marshal(map[string]interface{}{
		"id":     req.ID,
		"status": model.ReminderStatusCancelled,
	})
	return &Result{Success: true, Data: data}
}

func (t *ReminderTool) listReminders(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Status string `json:"status"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	json.Unmarshal(args, &req)
	if req.Limit <= 0 {
		req.Limit = 20
	}

	list, total, err := t.reminderRepo.List(ec.Ctx, ec.UserID, req.Status, req.Limit, req.Offset)
	if err != nil {
		log.Printf("[reminder] list error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询提醒失败: %v", err)}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"items": list,
		"total": total,
	})
	return &Result{Success: true, Data: data}
}

func (t *ReminderTool) searchReminders(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Query  string `json:"query"`
		Status string `json:"status"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	json.Unmarshal(args, &req)
	if req.Query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	list, total, err := t.reminderRepo.Search(ec.Ctx, ec.UserID, req.Query, req.Status, req.Limit, req.Offset)
	if err != nil {
		log.Printf("[reminder] search error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("搜索提醒失败: %v", err)}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"items": list,
		"total": total,
	})
	return &Result{Success: true, Data: data}
}

func (t *ReminderTool) markReminderMentioned(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id 不能为空"}
	}

	rem, err := t.reminderRepo.FindByID(ec.Ctx, ec.UserID, req.ID)
	if err != nil {
		log.Printf("[reminder] mark_mentioned find error: %v", err)
		return &Result{Success: false, Error: "提醒不存在"}
	}

	if err := t.reminderRepo.MarkMentioned(ec.Ctx, ec.UserID, req.ID); err != nil {
		log.Printf("[reminder] mark_mentioned error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("标记失败: %v", err)}
	}

	log.Printf("[reminder] mentioned: %s", req.ID[:8])
	data, _ := json.Marshal(map[string]interface{}{
		"id":           rem.ID,
		"mentioned_at": time.Now(),
	})
	return &Result{Success: true, Data: data}
}

// generateContent 异步调用 LLM 生成 notification_body 和 trigger_message，
// 校验不与 content 重复且彼此不同，失败重试最多 3 次，最终兜底。
func (t *ReminderTool) generateContent(id, content string, deadline time.Time) {
	if t.llmClient == nil {
		t.fallbackGenerate(id, content)
		return
	}

	deadlineStr := deadline.Format("2006-01-02 15:04")

	systemPrompt := `你是辽辽，用户的个人 AI 助手。你简洁、体贴、敢直言。现在需要你为一条已创建的提醒生成通知文案。

请输出 JSON，包含 2 个字段：
1. notification_body — 系统通知弹窗文案，40 字以内，让用户一眼就知道该做什么
2. trigger_message — 用户点击通知后你在聊天里主动说的第一句话，80 字以内。融入这条提醒的背景（为什么重要、和用户什么关系），末尾留钩子引导用户回复或行动（如"需要帮你准备什么吗？""看看还有什么需要调整的？"）

要求：
- notification_body 和 trigger_message 必须不同，且都不能和提醒原文完全相同
- trigger_message 要像你平时说话的风格——不谄媚，不啰嗦，有温度

输出示例：
{"notification_body":"明天下午1点考科目二，别忘了带身份证","trigger_message":"嘿，明天下午就考科目二了。练了那么久，关键是把心态稳住——正常发挥就好。检查一下身份证和驾校报名材料都带齐了吗？"}

只输出 JSON，不要其他内容。`

	userMsg := fmt.Sprintf("提醒内容：%s\n提醒时间：%s", content, deadlineStr)

	for attempt := 1; attempt <= 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		resp, err := t.llmClient.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: systemPrompt,
			Messages:     []llm.Message{{Role: "user", Content: userMsg}},
		})
		cancel()

		if err != nil {
			log.Printf("[reminder] generate attempt %d LLM error: %v", attempt, err)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		var gen struct {
			NotificationBody string `json:"notification_body"`
			TriggerMessage   string `json:"trigger_message"`
		}
		if err := json.Unmarshal([]byte(resp.Content), &gen); err != nil {
			log.Printf("[reminder] generate attempt %d parse error: %v, content=%s", attempt, err, resp.Content)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		body := strings.TrimSpace(gen.NotificationBody)
		trigger := strings.TrimSpace(gen.TriggerMessage)

		if body == "" || trigger == "" {
			log.Printf("[reminder] generate attempt %d empty fields: body=%q trigger=%q", attempt, body, trigger)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}
		if body == content || trigger == content || body == trigger {
			log.Printf("[reminder] generate attempt %d duplicate: body=%q trigger=%q content=%q", attempt, body, trigger, content)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		if err := t.reminderRepo.UpdateGeneratedContent(context.Background(), id, body, trigger); err != nil {
			log.Printf("[reminder] update generated content error: %v", err)
		}
		log.Printf("[reminder] generated content for %s", id[:8])
		return
	}

	t.fallbackGenerate(id, content)
}

func (t *ReminderTool) fallbackGenerate(id, content string) {
	body := fmt.Sprintf("别忘了: %s", content)
	trigger := fmt.Sprintf("到时间啦：%s，搞定了吗？", content)
	if err := t.reminderRepo.UpdateGeneratedContent(context.Background(), id, body, trigger); err != nil {
		log.Printf("[reminder] fallback update error: %v", err)
	}
	log.Printf("[reminder] fallback content for %s", id[:8])
}

type recurrenceRuleInput struct {
	Type      string        `json:"type"`
	Time      string        `json:"time"`
	Days      []string      `json:"days,omitempty"`
	MonthDays []interface{} `json:"month_days,omitempty"`
}
