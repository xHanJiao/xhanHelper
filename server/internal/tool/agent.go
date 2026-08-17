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

func (t *AgentTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "智能体。建/查/改自定义 AI 助手，绑定/解绑会话。",
		Funcs:     []string{"create_agent", "find_agent", "list_agents", "bind_agent"},
		Others:    "解绑、更新 等",
	}, true
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
	}
}

// ReadOnlyFuncs — 查询/列表类只读；bind/unbind 会改会话绑定（写，且影响缓存，谨慎使用）
func (t *AgentTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{
		"find_agent":  true,
		"list_agents": true,
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
