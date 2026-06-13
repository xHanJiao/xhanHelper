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
}

func NewThemeTool(repo *service.ThemeStateRepo, embedWriter app.ThemeVectorWriter) *ThemeTool {
	return &ThemeTool{themeRepo: repo, embedWriter: embedWriter}
}

func (t *ThemeTool) Name() string { return "theme" }
func (t *ThemeTool) Description() string {
	return "管理追踪主题：创建、关闭、重新激活"
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
