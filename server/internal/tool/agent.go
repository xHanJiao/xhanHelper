package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// AgentTool — 让 LLM 自主创建和管理智能体
type AgentTool struct {
	agentRepo *service.AgentRepo
}

// NewAgentTool 创建 AgentTool
func NewAgentTool(agentRepo *service.AgentRepo) *AgentTool {
	return &AgentTool{agentRepo: agentRepo}
}

func (t *AgentTool) Name() string { return "agent" }
func (t *AgentTool) Description() string {
	return "管理智能体：创建、查看、删除自定义 AI 助手"
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
			Name:        "list_agents",
			Display:     "智能体列表",
			Description: "列出当前已有的智能体",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (t *AgentTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_agent":
		return t.createAgent(ec, args)
	case "list_agents":
		return t.listAgents(ec, args)
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

func (t *AgentTool) listAgents(ec *ExecuteContext, _ json.RawMessage) *Result {
	agents, _, err := t.agentRepo.ListByUser(context.Background(), ec.UserID, 100, 0)
	if err != nil {
		return &Result{Success: false, Error: "list agents: " + err.Error()}
	}
	type brief struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	list := make([]brief, len(agents))
	for i, a := range agents {
		list[i] = brief{ID: a.ID, Name: a.Name}
	}
	data, _ := json.Marshal(list)
	return &Result{Success: true, Data: data}
}
