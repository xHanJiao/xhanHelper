package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// MemoryHandler — 用户记忆 REST API
type MemoryHandler struct {
	memRepo *service.UserMemoryRepo
	purger  *service.EntityPurger
}

// NewMemoryHandler 创建 MemoryHandler
func NewMemoryHandler(memRepo *service.UserMemoryRepo, purger *service.EntityPurger) *MemoryHandler {
	return &MemoryHandler{memRepo: memRepo, purger: purger}
}

// ListMemories GET /api/memories?limit=50&offset=0&keyword=
func (h *MemoryHandler) ListMemories(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	memories, total, err := h.memRepo.ListByUser(c.Request.Context(), userID,
		c.Query("keyword"), c.Query("source"), c.Query("topic"), limit, offset)
	if err != nil {
		log.Printf("[memory] List error: %v", err)
		view.JSONError(c, 500, "query memories failed")
		return
	}
	view.JSONOK(c, gin.H{"memories": memories, "total": total})
}

// ListMemoryTopics GET /api/memories/topics — 记忆词云用的 topic 及出现次数
func (h *MemoryHandler) ListMemoryTopics(c *gin.Context) {
	userID := c.GetString("user_id")

	topics, err := h.memRepo.DistinctTopics(c.Request.Context(), userID)
	if err != nil {
		log.Printf("[memory] ListTopics error: %v", err)
		view.JSONError(c, 500, "query memory topics failed")
		return
	}
	if topics == nil {
		topics = []service.TopicCount{}
	}
	// 聚合接口不做分页，加个上限兜底，避免异常数据下无界响应
	if len(topics) > 500 {
		topics = topics[:500]
	}
	view.JSONOK(c, gin.H{"topics": topics})
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

	// 清理派生数据（向量行/chunk/内存向量）。记忆已删，清理失败只记日志、不翻转结果。
	if err := h.purger.Purge(c.Request.Context(), model.EntityTypeMemory, id); err != nil {
		log.Printf("[memory] purge derived data for %s error: %v", id, err)
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

	// 清理派生数据（逐条，幂等）。记忆已删，清理失败只记日志、不翻转结果。
	for _, id := range req.IDs {
		if err := h.purger.Purge(c.Request.Context(), model.EntityTypeMemory, id); err != nil {
			log.Printf("[memory] purge derived data for %s error: %v", id, err)
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

// SetMemoryRating PATCH /api/memories/:id/rating — 用户反馈：0 未评 / 1 有用 / -1 没用
func (h *MemoryHandler) SetMemoryRating(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	var req struct {
		Rating int `json:"rating"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid body")
		return
	}
	if req.Rating < -1 || req.Rating > 1 {
		view.JSONError(c, 400, "rating must be -1, 0 or 1")
		return
	}

	// ponytail: 先查后写非原子——两步之间记忆被删则 Update 影响 0 行、此处仍返 200（幻影成功）。
	// 无害：DB 是事实，下次列表 load 自纠。不用 RowsAffected==0 判 404——本库 DSN 无 clientFoundRows，
	// MySQL 对 no-op 更新（值未变）返回 0 行，会把「取消赞」误判成 404。
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

	if err := h.memRepo.SetRating(c.Request.Context(), userID, id, req.Rating); err != nil {
		log.Printf("[memory] SetRating error: %v", err)
		view.JSONError(c, 500, "set rating failed")
		return
	}

	view.JSONOK(c, gin.H{"rating": req.Rating})
}
