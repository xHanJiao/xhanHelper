package tool

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/config"
	"xhan-helper/internal/service"
)

// GetSummaryTool — 按 ID 获取对话摘要，支持沿 parent_summary_id 链表回溯
type GetSummaryTool struct {
	summaryRepo *service.ConversationSummaryRepo
}

func NewGetSummaryTool(summaryRepo *service.ConversationSummaryRepo) *GetSummaryTool {
	return &GetSummaryTool{summaryRepo: summaryRepo}
}

func (t *GetSummaryTool) Name() string { return "get_summary" }

func (t *GetSummaryTool) Description() string {
	return "根据摘要ID获取对话摘要的内容和元数据。可用于沿 parent_summary_id 链表回溯历史摘要，了解更早的对话脉络"
}

func (t *GetSummaryTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "get_summary",
		Display:     "获取摘要",
		Description: "根据摘要ID获取对话摘要。当你需要了解更早的对话脉络时，使用对话摘要中提供的 parent 属性作为 summary_id 来追溯前一段摘要。也可用 cover_from/cover_to 配合 message_search.search_by_range 查找该范围内的具体事实",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"summary_id": map[string]interface{}{"type": "string", "description": "摘要ID，来自 <conversation_summary> 的 id 或 parent 属性"},
			},
			"required": []string{"summary_id"},
		},
	}}
}

func (t *GetSummaryTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var a struct {
		SummaryID string `json:"summary_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.SummaryID == "" {
		return &Result{Success: false, Error: "summary_id 不能为空"}
	}

	summary, err := t.summaryRepo.GetByID(ec.Ctx, a.SummaryID, ec.UserID)
	if err != nil {
		log.Printf("[get_summary] GetByID error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("获取摘要失败: %v", err)}
	}
	if summary == nil {
		return &Result{Success: false, Error: "摘要不存在或无权访问"}
	}

	parent := ""
	if summary.ParentSummaryID != nil {
		parent = *summary.ParentSummaryID
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":                summary.ID,
		"summary_text":      summary.SummaryText,
		"topics":            summary.Topics,
		"parent_summary_id": parent,
		"cover_from_msg_id": summary.CoverFromMsgID,
		"cover_to_msg_id":   summary.CoverToMsgID,
		"created_at":        summary.CreatedAt.In(config.BJ).Format("2006-01-02 15:04"),
	})
	return &Result{Success: true, Data: data}
}
