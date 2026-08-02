package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// AgentTool — 让 LLM 自主创建和管理智能体
type AgentTool struct {
	agentRepo   *service.AgentRepo
	versionRepo *service.VersionRepo
	convRepo    *service.ConversationRepo
}

// NewAgentTool 创建 AgentTool
func NewAgentTool(agentRepo *service.AgentRepo, versionRepo *service.VersionRepo, convRepo *service.ConversationRepo) *AgentTool {
	return &AgentTool{agentRepo: agentRepo, versionRepo: versionRepo, convRepo: convRepo}
}

func (t *AgentTool) Name() string { return "agent" }
func (t *AgentTool) Description() string {
	return "管理智能体：创建、查找、更新、删除、回滚自定义 AI 助手"
}

func (t *AgentTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_agent",
			Display:     "创建智能体",
			Description: "创建一个新的智能体（自定义 AI 助手）。当用户要求创建一个具有特定行为或角色的助手时调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "智能体名称",
					},
					"system_prompt": map[string]interface{}{
						"type":        "string",
						"description": "智能体的 system prompt，定义其角色和行为",
					},
				},
				"required": []string{"name", "system_prompt"},
			},
		},
		{
			Name:        "find_agent",
			Display:     "查找智能体",
			Description: "根据名称查找智能体，返回完整信息（含 system prompt）。当用户提及某个智能体名字时调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "智能体名称",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "list_agents",
			Display:     "智能体列表",
			Description: "列出当前已有的智能体",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "bind_agent",
			Display:     "绑定智能体",
			Description: "将指定智能体绑定到当前会话，后续该会话的对话都会使用该智能体的角色设定。当用户要求切换/使用某个智能体时调用。先用 list_agents/find_agent 拿到 agent_id。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent_id": map[string]interface{}{
						"type":        "string",
						"description": "要绑定的智能体 ID",
					},
				},
				"required": []string{"agent_id"},
			},
		},
		{
			Name:        "unbind_agent",
			Display:     "解绑智能体",
			Description: "解除当前会话已绑定的智能体，恢复为默认助手。当用户要求退出/取消当前智能体时调用。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "update_agent",
			Display:     "更新智能体",
			Description: "更新智能体的配置（名称、system prompt、模型等）。更新前自动创建版本快照，支持后续回滚。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "智能体 ID",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "新的智能体名称（可选）",
					},
					"system_prompt": map[string]interface{}{
						"type":        "string",
						"description": "新的 system prompt（可选）",
					},
					"summary": map[string]interface{}{
						"type":        "string",
						"description": "新的摘要（可选）",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "新的模型名称（可选）",
					},
					"params_json": map[string]interface{}{
						"type":        "string",
						"description": "新的 LLM 参数 JSON（可选）",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "get_agent_versions",
			Display:     "智能体版本历史",
			Description: "查看指定智能体的版本历史记录",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "智能体 ID",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "rollback_agent",
			Display:     "回滚智能体",
			Description: "将智能体回滚到指定版本。回滚前会自动保存当前状态为新的版本快照。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "智能体 ID",
					},
					"version": map[string]interface{}{
						"type":        "integer",
						"description": "目标版本号",
					},
				},
				"required": []string{"id", "version"},
			},
		},
	}
}

// ReadOnlyFuncs — 查询/列表/版本类只读；bind/unbind 会改会话绑定（写，且影响缓存，谨慎使用）
func (t *AgentTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{
		"find_agent":         true,
		"list_agents":        true,
		"get_agent_versions": true,
	}
}

func (t *AgentTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_agent":
		return t.createAgent(ec, args)
	case "find_agent":
		return t.findAgent(ec, args)
	case "list_agents":
		return t.listAgents(ec, args)
	case "bind_agent":
		return t.bindAgent(ec, args)
	case "unbind_agent":
		return t.unbindAgent(ec, args)
	case "update_agent":
		return t.updateAgent(ec, args)
	case "get_agent_versions":
		return t.getAgentVersions(ec, args)
	case "rollback_agent":
		return t.rollbackAgent(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
}

func (t *AgentTool) createAgent(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Name         string `json:"name"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "parse args: " + err.Error()}
	}
	if req.Name == "" || req.SystemPrompt == "" {
		return &Result{Success: false, Error: "name and system_prompt required"}
	}

	agent := &model.Agent{
		ID:           uuid.New().String(),
		UserID:       ec.UserID,
		Name:         req.Name,
		SystemPrompt: req.SystemPrompt,
	}
	if err := t.agentRepo.Create(context.Background(), agent); err != nil {
		return &Result{Success: false, Error: "create agent: " + err.Error()}
	}

	data, _ := json.Marshal(agent)
	return &Result{Success: true, Data: data}
}

func (t *AgentTool) findAgent(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "parse args: " + err.Error()}
	}
	if req.Name == "" {
		return &Result{Success: false, Error: "name required"}
	}

	agent, err := t.agentRepo.FindByName(context.Background(), req.Name, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "find agent: " + err.Error()}
	}
	if agent == nil {
		return &Result{Success: false, Error: "agent not found"}
	}

	data, _ := json.Marshal(agent)
	return &Result{Success: true, Data: data}
}

func (t *AgentTool) listAgents(ec *ExecuteContext, _ json.RawMessage) *Result {
	agents, _, err := t.agentRepo.ListByUser(context.Background(), ec.UserID, 100, 0)
	if err != nil {
		return &Result{Success: false, Error: "list agents: " + err.Error()}
	}
	type brief struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
	}
	list := make([]brief, len(agents))
	for i, a := range agents {
		list[i] = brief{ID: a.ID, Name: a.Name, Summary: a.Summary}
	}
	data, _ := json.Marshal(list)
	return &Result{Success: true, Data: data}
}

func (t *AgentTool) bindAgent(ec *ExecuteContext, args json.RawMessage) *Result {
	if ec.ConvID == "" {
		return &Result{Success: false, Error: "no conversation context"}
	}
	var req struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "parse args: " + err.Error()}
	}
	if req.AgentID == "" {
		return &Result{Success: false, Error: "agent_id required"}
	}

	agent, err := t.agentRepo.FindByID(ec.Ctx, req.AgentID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "find agent: " + err.Error()}
	}
	if agent == nil {
		return &Result{Success: false, Error: "agent not found"}
	}

	if err := t.convRepo.UpdateAgentID(ec.Ctx, ec.ConvID, &req.AgentID); err != nil {
		return &Result{Success: false, Error: "bind agent: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"conversation_id": ec.ConvID,
		"agent_id":        req.AgentID,
		"agent_name":      agent.Name,
		"message":         fmt.Sprintf("已将会话绑定到智能体「%s」", agent.Name),
	})
	return &Result{Success: true, Data: data}
}

func (t *AgentTool) unbindAgent(ec *ExecuteContext, _ json.RawMessage) *Result {
	if ec.ConvID == "" {
		return &Result{Success: false, Error: "no conversation context"}
	}
	if err := t.convRepo.UpdateAgentID(ec.Ctx, ec.ConvID, nil); err != nil {
		return &Result{Success: false, Error: "unbind agent: " + err.Error()}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"conversation_id": ec.ConvID,
		"message":         "已解绑当前会话的智能体",
	})
	return &Result{Success: true, Data: data}
}

func (t *AgentTool) updateAgent(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		SystemPrompt string `json:"system_prompt"`
		Summary      string `json:"summary"`
		Model        string `json:"model"`
		ParamsJSON   string `json:"params_json"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "parse args: " + err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}

	current, err := t.agentRepo.FindByID(context.Background(), req.ID, ec.UserID)
	if err != nil || current == nil {
		return &Result{Success: false, Error: "agent not found"}
	}

	// 更新前创建版本快照
	if err := t.versionRepo.CreateSnapshot(context.Background(), "agent", req.ID, map[string]interface{}{
		"name":          current.Name,
		"system_prompt": current.SystemPrompt,
		"summary":       current.Summary,
		"model":         current.Model,
		"params_json":   current.ParamsJSON,
	}); err != nil {
		log.Printf("[agent] create snapshot before update %s: %v", req.ID, err)
	}

	// 构建更新
	agent := &model.Agent{ID: req.ID, UserID: ec.UserID}
	hasUpdate := false
	if req.Name != "" {
		agent.Name = req.Name
		hasUpdate = true
	}
	if req.SystemPrompt != "" {
		agent.SystemPrompt = req.SystemPrompt
		hasUpdate = true
	}
	if req.Summary != "" {
		agent.Summary = req.Summary
		hasUpdate = true
	}
	if req.Model != "" {
		agent.Model = req.Model
		hasUpdate = true
	}
	if req.ParamsJSON != "" {
		agent.ParamsJSON = req.ParamsJSON
		hasUpdate = true
	}
	if !hasUpdate {
		return &Result{Success: true, Data: []byte(`{"message":"nothing to update"}`)}
	}

	if err := t.agentRepo.Update(context.Background(), agent); err != nil {
		return &Result{Success: false, Error: "update agent: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":      req.ID,
		"message": "智能体已更新",
	})
	return &Result{Success: true, Data: data}
}

func (t *AgentTool) getAgentVersions(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "parse args: " + err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}

	// 确保 agent 存在且属于当前用户
	current, err := t.agentRepo.FindByID(context.Background(), req.ID, ec.UserID)
	if err != nil || current == nil {
		return &Result{Success: false, Error: "agent not found"}
	}

	versions, err := t.versionRepo.GetVersions(context.Background(), "agent", req.ID)
	if err != nil {
		return &Result{Success: false, Error: "get versions: " + err.Error()}
	}

	type versionBrief struct {
		Version   int    `json:"version"`
		CreatedAt string `json:"created_at"`
	}
	list := make([]versionBrief, len(versions))
	for i, v := range versions {
		list[i] = versionBrief{Version: v.Version, CreatedAt: v.CreatedAt.Format("2006-01-02 15:04:05")}
	}

	data, _ := json.Marshal(list)
	return &Result{Success: true, Data: data}
}

func (t *AgentTool) rollbackAgent(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "parse args: " + err.Error()}
	}
	if req.ID == "" || req.Version <= 0 {
		return &Result{Success: false, Error: "id and version required"}
	}

	current, err := t.agentRepo.FindByID(context.Background(), req.ID, ec.UserID)
	if err != nil || current == nil {
		return &Result{Success: false, Error: "agent not found"}
	}

	// 回滚前先存当前状态快照
	if err := t.versionRepo.CreateSnapshot(context.Background(), "agent", req.ID, map[string]interface{}{
		"name":          current.Name,
		"system_prompt": current.SystemPrompt,
		"summary":       current.Summary,
		"model":         current.Model,
		"params_json":   current.ParamsJSON,
	}); err != nil {
		log.Printf("[agent] create snapshot before rollback %s: %v", req.ID, err)
	}

	// 获取目标版本快照
	snap, err := t.versionRepo.GetSnapshot(context.Background(), "agent", req.ID, req.Version)
	if err != nil {
		return &Result{Success: false, Error: "version not found: " + err.Error()}
	}

	var snapData struct {
		Name         string `json:"name"`
		SystemPrompt string `json:"system_prompt"`
		Summary      string `json:"summary"`
		Model        string `json:"model"`
		ParamsJSON   string `json:"params_json"`
	}
	if err := json.Unmarshal([]byte(snap.Snapshot), &snapData); err != nil {
		return &Result{Success: false, Error: "parse snapshot: " + err.Error()}
	}

	agent := &model.Agent{
		ID:           req.ID,
		UserID:       ec.UserID,
		Name:         snapData.Name,
		SystemPrompt: snapData.SystemPrompt,
		Summary:      snapData.Summary,
		Model:        snapData.Model,
		ParamsJSON:   snapData.ParamsJSON,
	}
	if err := t.agentRepo.Update(context.Background(), agent); err != nil {
		return &Result{Success: false, Error: "rollback agent: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":      req.ID,
		"version": req.Version,
		"message": "智能体已回滚",
	})
	return &Result{Success: true, Data: data}
}
