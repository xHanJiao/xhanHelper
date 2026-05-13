package controller

import (
	"context"
	"log"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// AgentHandler — 智能体 REST API
type AgentHandler struct {
	agentRepo   *service.AgentRepo
	flashClient llm.LLMClient // 生成 Summary 用
}

// NewAgentHandler 创建 AgentHandler
func NewAgentHandler(agentRepo *service.AgentRepo, flashClient llm.LLMClient) *AgentHandler {
	return &AgentHandler{agentRepo: agentRepo, flashClient: flashClient}
}

func (h *AgentHandler) generateSummary(ctx context.Context, sp string) string {
	if h.flashClient == nil || sp == "" {
		return ""
	}
	msg, err := h.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是一个摘要生成器。用一句话（50字内）概括AI助手的职责和特点。只输出摘要本身。",
		Messages:     []llm.Message{{Role: "user", Content: "请概括: " + sp}},
	})
	if err != nil {
		log.Printf("[agent] generateSummary error: %v", err)
		return ""
	}
	s := msg.Content
	if len([]rune(s)) > 100 {
		s = string([]rune(s)[:100])
	}
	return s
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
	if req.SystemPrompt == "" || len([]rune(req.SystemPrompt)) > 3000 {
		view.JSONError(c, 400, "system_prompt required, max 3000 chars")
		return
	}

	agent := &model.Agent{
		ID:           uuid.New().String(),
		UserID:       userID,
		Name:         req.Name,
		SystemPrompt: req.SystemPrompt,
		Summary:      h.generateSummary(c.Request.Context(), req.SystemPrompt),
		Model:        req.Model,
	}
	if err := h.agentRepo.Create(c.Request.Context(), agent); err != nil {
		log.Printf("[agent] Create error: %v", err)
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

	agents, total, err := h.agentRepo.ListByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Printf("[agent] List error: %v", err)
		view.JSONError(c, 500, "query agents failed")
		return
	}
	view.JSONOK(c, gin.H{"agents": agents, "total": total})
}

// GetAgent GET /api/agents/:id
func (h *AgentHandler) GetAgent(c *gin.Context) {
	userID := c.GetString("user_id")
	a, err := h.agentRepo.FindByID(c.Request.Context(), c.Param("id"), userID)
	if err != nil {
		log.Printf("[agent] Get error: %v", err)
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
	if err != nil {
		log.Printf("[agent] Update FindByID error: %v", err)
		view.JSONError(c, 500, "query agent failed")
		return
	}
	if existing == nil {
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
		if *req.SystemPrompt == "" || len([]rune(*req.SystemPrompt)) > 3000 {
			view.JSONError(c, 400, "system_prompt max 3000 chars")
			return
		}
		existing.SystemPrompt = *req.SystemPrompt
		existing.Summary = h.generateSummary(c.Request.Context(), *req.SystemPrompt)
	}
	if req.Model != nil {
		existing.Model = *req.Model
	}

	if err := h.agentRepo.Update(c.Request.Context(), existing); err != nil {
		log.Printf("[agent] Update error: %v", err)
		view.JSONError(c, 500, "update agent failed")
		return
	}
	view.JSONOK(c, existing)
}

// DeleteAgent DELETE /api/agents/:id
func (h *AgentHandler) DeleteAgent(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.agentRepo.Delete(c.Request.Context(), c.Param("id"), userID); err != nil {
		log.Printf("[agent] Delete error: %v", err)
		view.JSONError(c, 500, "delete agent failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}
