package controller

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// AgentHandler — 智能体 REST API
type AgentHandler struct {
	agentRepo *service.AgentRepo
	llmClient llm.LLMClient
}

// NewAgentHandler 创建 AgentHandler
func NewAgentHandler(agentRepo *service.AgentRepo, llmClient llm.LLMClient) *AgentHandler {
	return &AgentHandler{agentRepo: agentRepo, llmClient: llmClient}
}

// CreateAgent POST /api/agents
func (h *AgentHandler) CreateAgent(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		Name         string `json:"name"`
		SystemPrompt string `json:"system_prompt"`
		Model        string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid request")
		return
	}
	if req.Name == "" || len([]rune(req.Name)) > 50 {
		view.JSONError(c, 400, "name required, max 50 chars")
		return
	}
	if req.SystemPrompt == "" || len([]rune(req.SystemPrompt)) > 2000 {
		view.JSONError(c, 400, "system_prompt required, max 2000 chars")
		return
	}

	if nsfw, _ := h.checkNSFW(c.Request.Context(), req.SystemPrompt); nsfw {
		view.JSONError(c, 400, "content policy violation")
		return
	}

	agent := &model.Agent{
		ID:           uuid.New().String(),
		UserID:       userID,
		Name:         req.Name,
		SystemPrompt: req.SystemPrompt,
		Model:        req.Model,
	}
	if err := h.agentRepo.Create(c.Request.Context(), agent); err != nil {
		view.JSONError(c, 500, "create agent failed")
		return
	}
	view.JSONOK(c, agent)
}

// ListAgents GET /api/agents?limit=50&offset=0
func (h *AgentHandler) ListAgents(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	agents, err := h.agentRepo.ListByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		view.JSONError(c, 500, "query agents failed")
		return
	}
	view.JSONOK(c, agents)
}

// GetAgent GET /api/agents/:id
func (h *AgentHandler) GetAgent(c *gin.Context) {
	userID := c.GetString("user_id")
	a, err := h.agentRepo.FindByID(c.Request.Context(), c.Param("id"), userID)
	if err != nil {
		view.JSONError(c, 500, "query agent failed")
		return
	}
	if a == nil {
		view.JSONError(c, 404, "agent not found")
		return
	}
	view.JSONOK(c, a)
}

// UpdateAgent PATCH /api/agents/:id
func (h *AgentHandler) UpdateAgent(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	existing, err := h.agentRepo.FindByID(c.Request.Context(), id, userID)
	if err != nil || existing == nil {
		view.JSONError(c, 404, "agent not found")
		return
	}

	var req struct {
		Name         *string `json:"name"`
		SystemPrompt *string `json:"system_prompt"`
		Model        *string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid request")
		return
	}

	if req.Name != nil {
		if *req.Name == "" || len([]rune(*req.Name)) > 50 {
			view.JSONError(c, 400, "name max 50 chars")
			return
		}
		existing.Name = *req.Name
	}
	if req.SystemPrompt != nil {
		if *req.SystemPrompt == "" || len([]rune(*req.SystemPrompt)) > 2000 {
			view.JSONError(c, 400, "system_prompt max 2000 chars")
			return
		}
		if nsfw, _ := h.checkNSFW(c.Request.Context(), *req.SystemPrompt); nsfw {
			view.JSONError(c, 400, "content policy violation")
			return
		}
		existing.SystemPrompt = *req.SystemPrompt
	}
	if req.Model != nil {
		existing.Model = *req.Model
	}

	if err := h.agentRepo.Update(c.Request.Context(), existing); err != nil {
		view.JSONError(c, 500, "update agent failed")
		return
	}
	view.JSONOK(c, existing)
}

// DeleteAgent DELETE /api/agents/:id
func (h *AgentHandler) DeleteAgent(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.agentRepo.Delete(c.Request.Context(), c.Param("id"), userID); err != nil {
		view.JSONError(c, 500, "delete agent failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}

// checkNSFW 用 LLM 检测内容是否违规
func (h *AgentHandler) checkNSFW(ctx context.Context, text string) (bool, error) {
	if h.llmClient == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	msg, err := h.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是一个内容审核助手。判断文本是否包含色情、暴力、违法内容。只回复'是'或'否'，不要回复其他内容。",
		Messages:     []llm.Message{{Role: "user", Content: text}},
	})
	if err != nil {
		return false, err
	}
	return strings.Contains(msg.Content, "是"), nil
}
