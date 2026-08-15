package tool

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/app"
	"xhan-helper/internal/service"
)

const themeSearchThreshold = 0.55

// ThemeSearchTool — 对话 LLM 可调用的主题检索工具
type ThemeSearchTool struct {
	themeRepo   *service.ThemeStateRepo
	embedder    app.Embedder
	vectorStore app.VectorStore
}

func NewThemeSearchTool(repo *service.ThemeStateRepo, embedder app.Embedder, vectorStore app.VectorStore) *ThemeSearchTool {
	return &ThemeSearchTool{themeRepo: repo, embedder: embedder, vectorStore: vectorStore}
}

func (t *ThemeSearchTool) Name() string { return "theme_search" }

func (t *ThemeSearchTool) Description() string {
	return "主题代表用户身上正在发生或追踪的事件（如\"戒酒计划\"\"求职\"\"健身\"等），由系统自动发现和维护状态页。此工具提供主题管理能力：列出活跃主题、获取主题详情（语义/关键词检索统一走 unified_search）。当你想了解用户近况、某个领域的状态变化时，先查看主题是最高效的入口，比直接翻消息或笔记更能抓住全局。"
}

func (t *ThemeSearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "list_active_themes",
			Display:     "列出活跃主题",
			Description: "列出用户当前活跃主题（即系统正在追踪的事件），返回简表（id/name/status，不含详情）。默认 20 条。需要完整状态页用 get_detail。用于了解用户身上正在发生哪些事。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "description": "返回条数，默认20，最大50"},
				},
				"required": []string{},
			},
		},
		{
			Name:        "get_detail",
			Display:     "获取主题详情",
			Description: "根据主题 id 获取完整状态页，包含 name、description、status、state_text（完整状态文本）、duration、created_at、updated_at。用于深入了解某个主题的完整进展历史。id 可从上下文中的 <topic id=\"...\"> 属性获取。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "主题 ID，来自上下文中 <topic> 的 id 属性"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *ThemeSearchTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *ThemeSearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "list_active_themes":
		return t.listActiveThemes(ec, args)
	case "get_detail":
		return t.getDetail(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

func (t *ThemeSearchTool) getDetail(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.ID == "" {
		return &Result{Success: false, Error: "id 不能为空"}
	}

	th, err := t.themeRepo.GetByID(ec.Ctx, a.ID, ec.UserID)
	if err != nil {
		log.Printf("[theme_search] get_detail GetByID error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}
	if th == nil {
		return &Result{Success: false, Error: "主题不存在"}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":          th.ID,
		"name":        th.Name,
		"description": th.Description,
		"status":      th.Status,
		"duration":    th.Duration,
		"state_text":  th.StateText,
		"created_at":  th.CreatedAt,
		"updated_at":  th.UpdatedAt,
	})
	return &Result{Success: true, Data: data}
}

func (t *ThemeSearchTool) listActiveThemes(ec *ExecuteContext, args json.RawMessage) *Result {
	var a struct {
		Limit int `json:"limit"`
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

	themes, err := t.themeRepo.ListActive(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[theme_search] list_active_themes error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询主题失败: %v", err)}
	}

	// 简表：只给 id/name/status，详情走 get_detail（主题多时避免 description/state_text 撑爆上下文）
	if len(themes) > a.Limit {
		themes = themes[:a.Limit]
	}
	output := make([]map[string]interface{}, 0, len(themes))
	for _, th := range themes {
		output = append(output, map[string]interface{}{
			"id":     th.ID,
			"name":   th.Name,
			"status": th.Status,
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"total":  len(themes),
		"themes": output,
	})
	return &Result{Success: true, Data: data}
}
