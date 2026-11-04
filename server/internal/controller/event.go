package controller

import (
	"encoding/json"
	"log"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// EventHandler — 事件只读 API（/api/event-threads）。
//
// 事件的写操作全部走聊天里的 event 工具；这里只给前端/客户端看的列表和详情。
type EventHandler struct {
	eventRepo *service.EventRepo
	memRepo   *service.UserMemoryRepo
}

func NewEventHandler(eventRepo *service.EventRepo, memRepo *service.UserMemoryRepo) *EventHandler {
	return &EventHandler{eventRepo: eventRepo, memRepo: memRepo}
}

type eventListItem struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Summary      string     `json:"summary"`
	MemoryCount  int        `json:"memory_count"`
	LastMemoryAt *time.Time `json:"last_memory_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type eventMemoryItem struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Topic     string    `json:"topic"`
	Facts     []string  `json:"facts,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListEvents GET /api/event-threads?limit=50&offset=0
func (h *EventHandler) ListEvents(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	ctx := c.Request.Context()

	events, err := h.eventRepo.List(ctx, userID, false, limit, offset)
	if err != nil {
		log.Printf("[event] List error: %v", err)
		view.JSONError(c, 500, "查询事件失败")
		return
	}
	total, err := h.eventRepo.Count(ctx, userID, false)
	if err != nil {
		log.Printf("[event] Count error: %v", err)
		view.JSONError(c, 500, "查询事件失败")
		return
	}

	ids := make([]string, 0, len(events))
	for i := range events {
		ids = append(ids, events[i].ID)
	}
	counts, err := h.memRepo.CountByEventIDs(ctx, userID, ids)
	if err != nil {
		log.Printf("[event] CountByEventIDs error: %v", err)
		view.JSONError(c, 500, "查询事件失败")
		return
	}

	items := make([]eventListItem, 0, len(events))
	for i := range events {
		e := &events[i]
		items = append(items, eventListItem{
			ID: e.ID, Name: e.Name, Summary: e.Summary,
			MemoryCount: counts[e.ID], LastMemoryAt: e.LastMemoryAt,
			CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		})
	}
	view.JSONOK(c, gin.H{"items": items, "total": total})
}

// GetEvent GET /api/event-threads/:id
func (h *EventHandler) GetEvent(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := c.Request.Context()

	e, err := h.eventRepo.GetByID(ctx, userID, id)
	if err != nil {
		log.Printf("[event] GetByID error: %v", err)
		view.JSONError(c, 500, "查询事件失败")
		return
	}
	if e == nil {
		view.JSONError(c, 404, "事件不存在")
		return
	}

	memories, err := h.memRepo.ListByEventIDs(ctx, userID, []string{e.ID})
	if err != nil {
		log.Printf("[event] ListByEventIDs error: %v", err)
		view.JSONError(c, 500, "查询事件失败")
		return
	}
	sort.Slice(memories, func(i, j int) bool {
		if !memories[i].UpdatedAt.Equal(memories[j].UpdatedAt) {
			return memories[i].UpdatedAt.After(memories[j].UpdatedAt)
		}
		return memories[i].ID < memories[j].ID
	})
	items := make([]eventMemoryItem, 0, len(memories))
	for _, m := range memories {
		items = append(items, eventMemoryItem{
			ID: m.ID, Content: m.MemoryValue, Topic: m.Topic,
			Facts: parseMemoryFacts(m.Facts), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		})
	}
	view.JSONOK(c, gin.H{
		"id": e.ID, "name": e.Name, "summary": e.Summary,
		"memory_count": len(items), "memories": items,
		"is_deleted": e.IsDeleted, "last_memory_at": e.LastMemoryAt,
		"created_at": e.CreatedAt, "updated_at": e.UpdatedAt,
	})
}

// parseMemoryFacts 解析记忆的 facts JSON 数组；脏数据只记日志，不影响详情返回。
func parseMemoryFacts(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}
	var facts []string
	if err := json.Unmarshal([]byte(*raw), &facts); err != nil {
		log.Printf("[event] parse memory facts error: %v", err)
		return nil
	}
	return facts
}
