package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ThemeTool — 主题管理工具
type ThemeTool struct {
	themeRepo   *service.ThemeStateRepo
	embedWriter app.ThemeVectorWriter
	// mu 按主题 ID 串行化 state_text 的「读→追加→整字段写回」，避免并发追加丢条目。
	mu keyedMutex
}

func NewThemeTool(repo *service.ThemeStateRepo, embedWriter app.ThemeVectorWriter) *ThemeTool {
	return &ThemeTool{themeRepo: repo, embedWriter: embedWriter}
}

func (t *ThemeTool) Name() string { return "theme" }
func (t *ThemeTool) Description() string {
	return "管理用户生活中持续进行的事件主题。仅在用户明确提及持续性的生活事件（如戒酒、健身、备考、求职、旅行计划等有明确目标和时间跨度的活动）时才操作主题。禁止为一次性问题、闲聊话题、情绪表达创建主题。"
}

func (t *ThemeTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "主题管理。建/改/关/重启持续追踪事件（健身、求职）。",
		Funcs:     []string{"create_theme", "update_theme", "close_theme", "reactivate_theme"},
	}, true
}

func (t *ThemeTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_theme",
			Display:     "创建主题",
			Description: "创建一个新的追踪主题。仅当用户明确提及持续性生活事件（有目标、有时间跨度的活动，如戒酒/健身/备考/求职/装修/旅行等）且当前无对应主题时才创建。勿为一次性咨询、闲聊或情绪状态创建。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "主题名，简洁（5字以内）"},
					"description": map[string]interface{}{"type": "string", "description": "主题追踪什么（20字以内），需描述具体目标和时间跨度"},
					"state_text":  map[string]interface{}{"type": "string", "description": "初始状态（可选），如「用户表示计划每天跑步30分钟，尚未开始」。创建后会自动追加时间戳"},
				},
				"required": []string{"name", "description"},
			},
		},
		{
			Name:        "update_theme",
			Display:     "更新主题",
			Description: "更新主题的名称、描述或状态。状态更新采用增量追加模式——新内容会带上时间戳追加到现有状态文本末尾，不会覆盖已有记录。用于用户报告进展、状态变化、新增信息等。state_text 只需写新增信息/变化，勿重复已有内容。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "要更新的主题名称（用于定位）"},
					"new_name":    map[string]interface{}{"type": "string", "description": "新的主题名（可选，5字以内）"},
					"description": map[string]interface{}{"type": "string", "description": "新的描述（可选，20字以内）"},
					"state_text":  map[string]interface{}{"type": "string", "description": "用户报告的最新状态进展（可选）。此文本会被追加到状态页末尾（自动带时间戳），不会覆盖已有内容。只记录新增信息/变化，勿重复已有内容"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "close_theme",
			Display:     "关闭主题",
			Description: "关闭一个主题（不再追踪但不删除）",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "要关闭的主题名"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "reactivate_theme",
			Display:     "激活主题",
			Description: "重新激活一个已关闭的主题",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "要重新激活的主题名"},
				},
				"required": []string{"name"},
			},
		},
	}
}

// ReadOnlyFuncs — 主题状态全部为写操作（plan 模式全拦）；查看走 theme_search（全只读）
func (t *ThemeTool) ReadOnlyFuncs() map[string]bool { return nil }

func (t *ThemeTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_theme":
		return t.createTheme(ec, args)
	case "update_theme":
		return t.updateTheme(ec, args)
	case "close_theme":
		return t.closeTheme(ec, args)
	case "reactivate_theme":
		return t.reactivateTheme(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

func (t *ThemeTool) createTheme(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		StateText   string `json:"state_text"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Name == "" {
		return &Result{Success: false, Error: "name 不能为空"}
	}

	// 检查同名主题
	existing, err := t.themeRepo.GetByName(ec.Ctx, ec.UserID, a.Name)
	if err != nil {
		log.Printf("[theme] GetByName error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	if existing != nil {
		if existing.Status == "closed" {
			_ = t.themeRepo.ReactivateDraft(ec.Ctx, existing.ID, ec.UserID)
			return &Result{Success: true, Data: json.RawMessage(`{"status":"reactivated","name":"` + a.Name + `"}`)}
		}
		return &Result{Success: false, Error: fmt.Sprintf("主题 %s 已存在", a.Name)}
	}

	theme := &model.ThemeState{
		ID:          uuid.New().String(),
		UserID:      ec.UserID,
		Name:        a.Name,
		Description: a.Description,
		Status:      "active",
	}
	if a.StateText != "" {
		theme.StateText = fmt.Sprintf("[刚刚创建] %s", a.StateText)
	}
	if err := t.themeRepo.Create(ec.Ctx, theme); err != nil {
		log.Printf("[theme] create error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("创建主题失败: %v", err)}
	}

	// 异步写向量（ThemeEmbedder 先查 concept 表，有则跳过）
	if t.embedWriter != nil {
		go t.embedWriter.EnsureEmbedded(context.Background(), ec.UserID, theme.ID, a.Name, a.Description)
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":     theme.ID,
		"name":   theme.Name,
		"status": "created",
	})
	return &Result{Success: true, Data: data}
}

func (t *ThemeTool) updateTheme(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Name        string `json:"name"`
		NewName     string `json:"new_name"`
		Description string `json:"description"`
		StateText   string `json:"state_text"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.NewName == "" && a.Description == "" && a.StateText == "" {
		return &Result{Success: false, Error: "至少需要提供 new_name、description 或 state_text 之一"}
	}

	existing, err := t.themeRepo.GetByName(ec.Ctx, ec.UserID, a.Name)
	if err != nil {
		log.Printf("[theme] update GetByName error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}
	if existing == nil {
		return &Result{Success: false, Error: fmt.Sprintf("主题 %s 不存在", a.Name)}
	}

	if a.NewName != "" || a.Description != "" {
		if err := t.themeRepo.Update(ec.Ctx, existing.ID, ec.UserID, a.NewName, a.Description); err != nil {
			log.Printf("[theme] update error: %v", err)
			return &Result{Success: false, Error: fmt.Sprintf("更新主题失败: %v", err)}
		}
	}

	if a.StateText != "" {
		// 追加是读全文→尾部拼接→整列写回，并发追加必须串行，否则后写者用旧快照覆盖前一条。
		err := func() error {
			unlock := t.mu.lock(existing.ID)
			defer unlock()
			return t.themeRepo.AppendStateText(ec.Ctx, existing.ID, ec.UserID, a.StateText)
		}()
		if err != nil {
			log.Printf("[theme] AppendStateText error: %v", err)
			return &Result{Success: false, Error: fmt.Sprintf("追加状态失败: %v", err)}
		}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":     existing.ID,
		"name":   a.Name,
		"status": "updated",
	})
	return &Result{Success: true, Data: data}
}

func (t *ThemeTool) closeTheme(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	existing, err := t.themeRepo.GetByName(ec.Ctx, ec.UserID, a.Name)
	if err != nil {
		log.Printf("[theme] close GetByName error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}
	if existing == nil {
		return &Result{Success: false, Error: fmt.Sprintf("主题 %s 不存在", a.Name)}
	}
	if err := t.themeRepo.Close(ec.Ctx, existing.ID, ec.UserID); err != nil {
		log.Printf("[theme] close error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("关闭主题失败: %v", err)}
	}
	return &Result{Success: true, Data: json.RawMessage(`{"status":"closed","name":"` + a.Name + `"}`)}
}

func (t *ThemeTool) reactivateTheme(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	existing, err := t.themeRepo.GetByName(ec.Ctx, ec.UserID, a.Name)
	if err != nil {
		log.Printf("[theme] reactivate GetByName error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}
	if existing == nil {
		return &Result{Success: false, Error: fmt.Sprintf("主题 %s 不存在", a.Name)}
	}
	if err := t.themeRepo.ReactivateDraft(ec.Ctx, existing.ID, ec.UserID); err != nil {
		log.Printf("[theme] reactivate error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("重新激活失败: %v", err)}
	}
	return &Result{Success: true, Data: json.RawMessage(`{"status":"reactivated","name":"` + a.Name + `"}`)}
}
