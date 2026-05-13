package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// MemoryHandler — 用户记忆 REST API
type MemoryHandler struct {
	memRepo     *service.UserMemoryRepo
	vectorStore *agent.VectorStore
}

// NewMemoryHandler 创建 MemoryHandler
func NewMemoryHandler(memRepo *service.UserMemoryRepo, vectorStore *agent.VectorStore) *MemoryHandler {
	return &MemoryHandler{memRepo: memRepo, vectorStore: vectorStore}
}

// ListMemories GET /api/memories?limit=50&offset=0&keyword=
func (h *MemoryHandler) ListMemories(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	memories, total, err := h.memRepo.ListByUser(c.Request.Context(), userID,
		c.Query("keyword"), c.Query("source"), limit, offset)
	if err != nil {
		log.Printf("[memory] List error: %v", err)
		view.JSONError(c, 500, "query memories failed")
		return
	}
	view.JSONOK(c, gin.H{"memories": memories, "total": total})
}

// DeleteMemory DELETE /api/memories/:id
func (h *MemoryHandler) DeleteMemory(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	m, err := h.memRepo.FindByID(c.Request.Context(), userID, id)
	if err != nil {
		log.Printf("[memory] Get error: %v", err)
		view.JSONError(c, 500, "query memory failed")
		return
	}
	if m == nil {
		view.JSONError(c, 404, "memory not found")
		return
	}

	if err := h.memRepo.Delete(c.Request.Context(), id); err != nil {
		log.Printf("[memory] Delete error: %v", err)
		view.JSONError(c, 500, "delete memory failed")
		return
	}

	// 同步从向量库中删除
	if h.vectorStore != nil {
		h.vectorStore.Delete("mem:" + id)
	}

	view.JSONOK(c, gin.H{"deleted": true})
}

// BatchDeleteMemories POST /api/memories/batch-delete
func (h *MemoryHandler) BatchDeleteMemories(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		view.JSONError(c, 400, "ids required")
		return
	}

	if err := h.memRepo.BatchDelete(c.Request.Context(), req.IDs, userID); err != nil {
		log.Printf("[memory] BatchDelete error: %v", err)
		view.JSONError(c, 500, "batch delete failed")
		return
	}

	// 同步从向量库中删除
	if h.vectorStore != nil {
		for _, id := range req.IDs {
			h.vectorStore.Delete("mem:" + id)
		}
	}

	view.JSONOK(c, gin.H{"deleted": len(req.IDs)})
}

// BatchGetMemories POST /api/memories/batch
func (h *MemoryHandler) BatchGetMemories(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		view.JSONError(c, 400, "ids required")
		return
	}

	memories, err := h.memRepo.GetByIDsAndUser(c.Request.Context(), req.IDs, userID)
	if err != nil {
		log.Printf("[memory] BatchGet error: %v", err)
		view.JSONError(c, 500, "query memories failed")
		return
	}
	view.JSONOK(c, memories)
}
