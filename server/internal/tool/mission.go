package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MissionEmbedder — 避免循环依赖的嵌入接口（agent.MissionEmbedder 实现）
type MissionEmbedder interface {
	UpsertEmbedding(missionID, userID, goal string) error
	DeleteEmbedding(missionID string)
}

// MissionTool — 对话中 LLM 可调用的 Mission 管理工具
type MissionTool struct {
	missionRepo *service.MissionRepo
	embedder    MissionEmbedder
}

func NewMissionTool(missionRepo *service.MissionRepo, embedder MissionEmbedder) *MissionTool {
	return &MissionTool{missionRepo: missionRepo, embedder: embedder}
}

func (t *MissionTool) Name() string { return "mission" }

func (t *MissionTool) Description() string {
	return "管理后台追踪任务（Mission）。创建、修改、暂停、恢复、完成追踪任务，或列出已有的任务。"
}

func (t *MissionTool) Functions() []FuncSchema {
	commonMissionFields := map[string]interface{}{
		"goal":       map[string]interface{}{"type": "string", "description": "任务目标，用自然语言描述要追踪什么"},
		"tools_hint": map[string]interface{}{"type": "string", "description": "建议使用的工具范围，逗号分隔，如 note,message_search,bill"},
		"check_cron": map[string]interface{}{"type": "string", "description": "检查频率: daily(每天), weekly(每周), 2daily(每两天)"},
		"context":    map[string]interface{}{"type": "string", "description": "背景信息，补充关于此任务的上下文"},
	}

	return []FuncSchema{
		{
			Name:        "create_mission",
			Description: "创建一个新的后台追踪任务。用户说「帮我盯着XX」「定期检查XX」「追踪XX的进展」时调用。创建后系统会自动定期检查并汇报。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"goal":       commonMissionFields["goal"],
					"context":    commonMissionFields["context"],
					"tools_hint": commonMissionFields["tools_hint"],
					"check_cron": commonMissionFields["check_cron"],
				},
				"required": []string{"goal"},
			},
		},
		{
			Name:        "list_missions",
			Description: "列出用户当前的追踪任务。用户问「我有几个任务在追踪」「看看我的missions」时调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{"type": "string", "description": "筛选状态: active, paused, completed。不传默认返回 active + paused"},
				},
			},
		},
		{
			Name:        "update_mission",
			Description: "修改一个追踪任务的细节，如改目标、调频率、补充背景。用户说「把XX任务改成YY」「XX任务改成每周查一次」时调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":         map[string]interface{}{"type": "string", "description": "任务 ID"},
					"goal":       commonMissionFields["goal"],
					"context":    commonMissionFields["context"],
					"tools_hint": commonMissionFields["tools_hint"],
					"check_cron": commonMissionFields["check_cron"],
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "pause_mission",
			Description: "暂停一个追踪任务。用户说「先别盯XX了」「暂停XX任务」时调用。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "description": "任务 ID"}},
				"required":   []string{"id"},
			},
		},
		{
			Name:        "resume_mission",
			Description: "恢复一个暂停的追踪任务。用户说「继续盯XX」「恢复XX任务」时调用。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "description": "任务 ID"}},
				"required":   []string{"id"},
			},
		},
		{
			Name:        "complete_mission",
			Description: "标记一个追踪任务为已完成。用户说「XX学完了」「XX项目做完了，不用盯了」时调用。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "description": "任务 ID"}},
				"required":   []string{"id"},
			},
		},
	}
}

func (t *MissionTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_mission":
		return t.createMission(ec, args)
	case "list_missions":
		return t.listMissions(ec, args)
	case "update_mission":
		return t.updateMission(ec, args)
	case "pause_mission":
		return t.pauseMission(ec, args)
	case "resume_mission":
		return t.resumeMission(ec, args)
	case "complete_mission":
		return t.completeMission(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
}

type missionCreateArgs struct {
	Goal      string `json:"goal"`
	Context   string `json:"context"`
	ToolsHint string `json:"tools_hint"`
	CheckCron string `json:"check_cron"`
}

func (t *MissionTool) createMission(ec *ExecuteContext, args json.RawMessage) *Result {
	var req missionCreateArgs
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if req.Goal == "" {
		return &Result{Success: false, Error: "goal 不能为空"}
	}
	if req.CheckCron == "" {
		req.CheckCron = "daily"
	}

	m := &model.Mission{
		ID:        uuid.New().String(),
		UserID:    ec.UserID,
		Goal:      req.Goal,
		Context:   req.Context,
		ToolsHint: req.ToolsHint,
		CheckCron: req.CheckCron,
		Status:    "active",
		Source:    "user",
	}

	if err := t.missionRepo.Create(ec.Ctx, m); err != nil {
		log.Printf("[mission] create error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("创建任务失败: %v", err)}
	}

	go t.embedder.UpsertEmbedding(m.ID, m.UserID, m.Goal)

	log.Printf("[mission] created: %s goal=%s cron=%s", m.ID[:8], m.Goal[:min(50, len(m.Goal))], m.CheckCron)
	data, _ := json.Marshal(map[string]interface{}{
		"id":         m.ID,
		"goal":       m.Goal,
		"check_cron": m.CheckCron,
		"status":     m.Status,
	})
	return &Result{Success: true, Data: data}
}

type missionListArgs struct {
	Status string `json:"status"`
}

func (t *MissionTool) listMissions(ec *ExecuteContext, args json.RawMessage) *Result {
	var req missionListArgs
	json.Unmarshal(args, &req)

	var statuses []string
	if req.Status != "" {
		statuses = []string{req.Status}
	} else {
		statuses = []string{"active", "paused"}
	}

	list, err := t.missionRepo.ListByStatuses(ec.Ctx, ec.UserID, statuses)
	if err != nil {
		log.Printf("[mission] list error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("查询任务失败: %v", err)}
	}

	type item struct {
		ID        string `json:"id"`
		Goal      string `json:"goal"`
		Status    string `json:"status"`
		CheckCron string `json:"check_cron"`
		Source    string `json:"source"`
	}
	items := make([]item, 0, len(list))
	for _, m := range list {
		items = append(items, item{m.ID, m.Goal, m.Status, m.CheckCron, m.Source})
	}

	data, _ := json.Marshal(map[string]interface{}{"missions": items, "total": len(items)})
	return &Result{Success: true, Data: data}
}

type missionIDArgs struct {
	ID string `json:"id"`
}

func (t *MissionTool) getAndValidate(ec *ExecuteContext, args json.RawMessage, action string) (*model.Mission, error) {
	var req missionIDArgs
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("参数解析失败: %v", err)
	}
	if req.ID == "" {
		return nil, fmt.Errorf("id 不能为空")
	}
	m, err := t.missionRepo.GetByID(ec.Ctx, req.ID, ec.UserID)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %v", err)
	}
	if m == nil {
		return nil, fmt.Errorf("任务不存在: %s", req.ID)
	}
	return m, nil
}

type missionUpdateArgs struct {
	ID        string `json:"id"`
	Goal      string `json:"goal"`
	Context   string `json:"context"`
	ToolsHint string `json:"tools_hint"`
	CheckCron string `json:"check_cron"`
}

func (t *MissionTool) updateMission(ec *ExecuteContext, args json.RawMessage) *Result {
	var req missionUpdateArgs
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id 不能为空"}
	}

	m, err := t.missionRepo.GetByID(ec.Ctx, req.ID, ec.UserID)
	if err != nil || m == nil {
		return &Result{Success: false, Error: fmt.Sprintf("任务不存在: %s", req.ID)}
	}
	if m.Status != "active" && m.Status != "paused" {
		return &Result{Success: false, Error: fmt.Sprintf("只能修改 active/paused 状态的任务，当前状态: %s", m.Status)}
	}

	if req.Goal != "" {
		m.Goal = req.Goal
	}
	if req.Context != "" {
		m.Context = req.Context
	}
	if req.ToolsHint != "" {
		m.ToolsHint = req.ToolsHint
	}
	if req.CheckCron != "" {
		m.CheckCron = req.CheckCron
	}

	if err := t.missionRepo.Update(ec.Ctx, m); err != nil {
		log.Printf("[mission] update error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("更新任务失败: %v", err)}
	}

	log.Printf("[mission] updated: %s goal=%s", m.ID[:8], m.Goal[:min(50, len(m.Goal))])
	data, _ := json.Marshal(map[string]interface{}{"id": m.ID, "status": "updated"})
	return &Result{Success: true, Data: data}
}

func (t *MissionTool) pauseMission(ec *ExecuteContext, args json.RawMessage) *Result {
	m, err := t.getAndValidate(ec, args, "pause")
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if m.Status != "active" {
		return &Result{Success: false, Error: fmt.Sprintf("只能暂停 active 状态的任务，当前状态: %s", m.Status)}
	}
	if err := t.missionRepo.UpdateStatus(ec.Ctx, m.ID, ec.UserID, "paused"); err != nil {
		log.Printf("[mission] pause error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("暂停任务失败: %v", err)}
	}
	log.Printf("[mission] paused: %s", m.ID[:8])
	data, _ := json.Marshal(map[string]interface{}{"id": m.ID, "status": "paused"})
	return &Result{Success: true, Data: data}
}

func (t *MissionTool) resumeMission(ec *ExecuteContext, args json.RawMessage) *Result {
	m, err := t.getAndValidate(ec, args, "resume")
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if m.Status != "paused" {
		return &Result{Success: false, Error: fmt.Sprintf("只能恢复 paused 状态的任务，当前状态: %s", m.Status)}
	}

	// 重设 next_check_at 为当前时间 + cron 偏移
	next := nextCheckTime(m.CheckCron)
	m.NextCheckAt = &next
	if err := t.missionRepo.UpdateStatus(ec.Ctx, m.ID, ec.UserID, "active"); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("恢复任务失败: %v", err)}
	}
	// 单独更新 next_check_at
	t.missionRepo.UpdateAfterCheck(ec.Ctx, m.ID, m.LastSummary, next)
	log.Printf("[mission] resumed: %s next=%s", m.ID[:8], next.Format("2006-01-02 15:04"))
	data, _ := json.Marshal(map[string]interface{}{"id": m.ID, "status": "active"})
	return &Result{Success: true, Data: data}
}

func (t *MissionTool) completeMission(ec *ExecuteContext, args json.RawMessage) *Result {
	m, err := t.getAndValidate(ec, args, "complete")
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if m.Status != "active" && m.Status != "paused" {
		return &Result{Success: false, Error: fmt.Sprintf("无法完成当前状态的任务: %s", m.Status)}
	}
	if err := t.missionRepo.UpdateStatus(ec.Ctx, m.ID, ec.UserID, "completed"); err != nil {
		log.Printf("[mission] complete error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("完成任务失败: %v", err)}
	}
	go t.embedder.DeleteEmbedding(m.ID)
	log.Printf("[mission] completed: %s", m.ID[:8])
	data, _ := json.Marshal(map[string]interface{}{"id": m.ID, "status": "completed"})
	return &Result{Success: true, Data: data}
}

func nextCheckTime(cron string) time.Time {
	now := time.Now()
	switch cron {
	case "weekly":
		return now.Add(7 * 24 * time.Hour)
	case "2daily":
		return now.Add(2 * 24 * time.Hour)
	default: // daily
		return now.Add(24 * time.Hour)
	}
}
