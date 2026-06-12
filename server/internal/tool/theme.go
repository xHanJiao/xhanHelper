package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// OnThemeFactAppended 主题事实追加后的异步回调，用于触发摘要重生 + chunk 构建
type OnThemeFactAppended func(ctx context.Context, userID, themeID, factText string)

// ThemeTool — 主题管理工具
type ThemeTool struct {
	themeRepo   *service.ThemeStateRepo
	embedWriter app.ThemeVectorWriter
	onAppend    OnThemeFactAppended
}

func NewThemeTool(repo *service.ThemeStateRepo, embedWriter app.ThemeVectorWriter) *ThemeTool {
	return &ThemeTool{themeRepo: repo, embedWriter: embedWriter}
}

// SetOnFactAppended 设置事实追加后的异步回调
func (t *ThemeTool) SetOnFactAppended(fn OnThemeFactAppended) {
	t.onAppend = fn
}

func (t *ThemeTool) Name() string { return "theme" }
func (t *ThemeTool) Description() string {
	return "管理追踪主题：创建、关闭、重新激活、获取状态、追加事实"
}

func (t *ThemeTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_theme",
			Display:     "创建主题",
			Description: "创建一个新的追踪主题",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "主题名，简洁（5字以内）"},
					"description": map[string]interface{}{"type": "string", "description": "主题追踪什么（20字以内）"},
				},
				"required": []string{"name", "description"},
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
		{
			Name:        "get_theme_state",
			Display:     "获取主题状态",
			Description: "获取主题完整状态（摘要+全部事实列表）。建议在追加事实前调用，了解已有记录避免重复。若主题不存在，返回当前所有 active 主题列表",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "主题名"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "append_theme_state",
			Display:     "追加主题事实",
			Description: "向主题追加一条新进展事实。每次只追加一条（1-2句话）。适用场景：进度更新、状态变化、新发现。日常闲聊不调用",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "主题名"},
					"fact": map[string]interface{}{"type": "string", "description": "单条事实，1-2句话，包含日期"},
				},
				"required": []string{"name", "fact"},
			},
		},
		{
			Name:        "list_active_themes",
			Display:     "列出活跃主题",
			Description: "列出所有活跃追踪主题。不确定有哪些主题时调用",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (t *ThemeTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_theme":
		return t.createTheme(ec, args)
	case "close_theme":
		return t.closeTheme(ec, args)
	case "reactivate_theme":
		return t.reactivateTheme(ec, args)
	case "get_theme_state":
		return t.getThemeState(ec, args)
	case "append_theme_state":
		return t.appendThemeState(ec, args)
	case "list_active_themes":
		return t.listActiveThemes(ec)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

func (t *ThemeTool) createTheme(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Name        string `json:"name"`
		Description string `json:"description"`
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

func (t *ThemeTool) getThemeState(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Name == "" {
		return &Result{Success: false, Error: "name 不能为空"}
	}

	theme, err := t.themeRepo.GetByName(ec.Ctx, ec.UserID, a.Name)
	if err != nil {
		log.Printf("[theme] getThemeState GetByName error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	if theme == nil {
		actives, err := t.themeRepo.ListActive(ec.Ctx, ec.UserID)
		if err == nil && len(actives) > 0 {
			var names []string
			for _, th := range actives {
				names = append(names, th.Name)
			}
			return &Result{Success: false, Error: fmt.Sprintf("主题「%s」不存在。当前 active 主题：%s", a.Name, strings.Join(names, "、"))}
		}
		return &Result{Success: false, Error: fmt.Sprintf("主题 %s 不存在", a.Name)}
	}

	var facts []json.RawMessage
	if theme.StateFacts != "" {
		lines := strings.Split(strings.TrimSpace(theme.StateFacts), "\n")
		for _, line := range lines {
			if line = strings.TrimSpace(line); line != "" {
				facts = append(facts, json.RawMessage(line))
			}
		}
	}
	if facts == nil {
		facts = []json.RawMessage{}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":          theme.ID,
		"name":        theme.Name,
		"description": theme.Description,
		"state_text":  theme.StateText,
		"state_facts": facts,
		"status":      theme.Status,
		"updated_at":  theme.UpdatedAt,
	})
	return &Result{Success: true, Data: data}
}

func (t *ThemeTool) appendThemeState(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Name string `json:"name"`
		Fact string `json:"fact"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Name == "" || a.Fact == "" {
		return &Result{Success: false, Error: "name 和 fact 不能为空"}
	}

	theme, err := t.themeRepo.GetByName(ec.Ctx, ec.UserID, a.Name)
	if err != nil {
		log.Printf("[theme] appendThemeState GetByName error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}
	if theme == nil {
		return &Result{Success: false, Error: fmt.Sprintf("主题 %s 不存在，请先创建或使用 list_active_themes 查看可用主题", a.Name)}
	}

	b, _ := json.Marshal(map[string]string{"ts": "", "fact": a.Fact})
	if err := t.themeRepo.AppendFact(ec.Ctx, theme.ID, ec.UserID, string(b)); err != nil {
		log.Printf("[theme] AppendFact error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("追加事实失败: %v", err)}
	}

	if t.onAppend != nil {
		go t.onAppend(context.Background(), ec.UserID, theme.ID, a.Fact)
	}

	log.Printf("[theme] appended fact to theme %s (%s): %s", theme.ID[:8], a.Name, a.Fact)
	return &Result{Success: true, Data: json.RawMessage(`{"status":"appended","name":"` + a.Name + `"}`)}
}

func (t *ThemeTool) listActiveThemes(ec *ExecuteContext) *Result {
	actives, err := t.themeRepo.ListActive(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[theme] listActiveThemes error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询失败: %v", err)}
	}

	list := make([]map[string]interface{}, 0, len(actives))
	for _, th := range actives {
		if th.Status == "closed" {
			continue
		}
		list = append(list, map[string]interface{}{
			"name":        th.Name,
			"description": th.Description,
			"status":      th.Status,
			"updated_at":  th.UpdatedAt,
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"themes": list,
		"count":  len(list),
	})
	return &Result{Success: true, Data: data}
}
