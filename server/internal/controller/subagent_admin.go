package controller

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// SubAgentAdminRoutesConfig — 子任务管理 API 路由依赖。
type SubAgentAdminRoutesConfig struct {
	R               gin.IRouter
	AuthMiddleware  gin.HandlerFunc
	AdminMiddleware gin.HandlerFunc
	ConvRepo        *service.ConversationRepo
	MsgRepo         *service.MessageRepo
	CostRepo        *service.LlmCostRepo
}

// RegisterSubAgentAdminRoutes 注册子任务管理 API 路由（Admin）。
func RegisterSubAgentAdminRoutes(rc SubAgentAdminRoutesConfig) {
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	h := NewSubAgentAdminHandler(rc.ConvRepo, rc.MsgRepo, rc.CostRepo)
	admin.GET("/subagents", h.List)
	admin.GET("/subagents/:id/messages", h.Messages)
}

// SubAgentAdminHandler — 子任务（subAgent）管理（Admin）。
type SubAgentAdminHandler struct {
	convRepo *service.ConversationRepo
	msgRepo  *service.MessageRepo
	costRepo *service.LlmCostRepo
}

func NewSubAgentAdminHandler(convRepo *service.ConversationRepo, msgRepo *service.MessageRepo, costRepo *service.LlmCostRepo) *SubAgentAdminHandler {
	return &SubAgentAdminHandler{convRepo: convRepo, msgRepo: msgRepo, costRepo: costRepo}
}

// List GET /api/admin/subagents — 子任务列表（倒序，loadMore 分页）。
func (h *SubAgentAdminHandler) List(c *gin.Context) {
	limit := parseListLimit(c, 20)
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	rows, hasMore, err := h.convRepo.ListSubConversations(c.Request.Context(), limit, offset)
	if err != nil {
		log.Printf("[subagent_admin] list error: %v", err)
		view.JSONError(c, http.StatusInternalServerError, "list sub agents failed")
		return
	}

	convIDs := make([]string, 0, len(rows))
	for i := range rows {
		convIDs = append(convIDs, rows[i].ID)
	}
	costs, err := h.costRepo.TotalsByConversations(c.Request.Context(), convIDs)
	if err != nil {
		log.Printf("[subagent_admin] cost totals error: %v", err)
	}

	items := make([]gin.H, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		var extra model.ConversationExtra
		if row.Extra != "" {
			if err := json.Unmarshal([]byte(row.Extra), &extra); err != nil {
				log.Printf("[subagent_admin] unmarshal extra: conv=%s err=%v", row.ID, err)
			}
		}
		item := gin.H{
			"conv_id":        row.ID,
			"title":          row.Title,
			"user_id":        row.UserID,
			"parent_conv_id": row.ParentID,
			"subagent_type":  extra.SubAgentType,
			"msg_count":      row.MsgCount,
			"created_at":     row.CreatedAt,
			"updated_at":     row.UpdatedAt,
		}
		if extra.SubAgentResult != nil {
			item["result"] = extra.SubAgentResult
		}
		if cost, ok := costs[row.ID]; ok {
			item["cost"] = cost
		}
		items = append(items, item)
	}
	view.JSONOK(c, gin.H{"items": items, "has_more": hasMore})
}

// Messages GET /api/admin/subagents/:id/messages — 子会话全部消息（时间正序，含 tool）。
// 仅允许查询子会话（parent_id IS NOT NULL）。
func (h *SubAgentAdminHandler) Messages(c *gin.Context) {
	convID := c.Param("id")
	conv, err := h.convRepo.FindByID(c.Request.Context(), convID)
	if err != nil || conv == nil || conv.ParentID == nil {
		log.Printf("[subagent_admin] messages: conv %s not found or not a sub-conversation (err=%v)", convID, err)
		view.JSONError(c, http.StatusNotFound, "子会话不存在")
		return
	}
	limit := 500
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "500")); err == nil && l > 0 && l <= 1000 {
		limit = l
	}
	msgs, err := h.msgRepo.ListByConversation(c.Request.Context(), convID, limit)
	if err != nil {
		log.Printf("[subagent_admin] list messages error (conv=%s): %v", convID, err)
		view.JSONError(c, http.StatusInternalServerError, "list messages failed")
		return
	}
	var firstUserMsgID string
	for i := range msgs {
		if msgs[i].Role == "user" {
			firstUserMsgID = msgs[i].ID
			break
		}
	}
	view.JSONOK(c, gin.H{"conv_id": convID, "first_user_msg_id": firstUserMsgID, "messages": msgs})
}
