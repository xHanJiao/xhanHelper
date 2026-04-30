package controller

import (
	"github.com/gin-gonic/gin"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func listConversations(convRepo *service.ConversationRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		dev, _ := c.Get("device")
		device := dev.(*model.Device)

		list, err := convRepo.ListByDevice(c.Request.Context(), device.ID, 50)
		if err != nil {
			view.JSONError(c, 500, "query conversations failed")
			return
		}
		view.JSONOK(c, list)
	}
}

func getMessages(convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")

		conv, err := convRepo.FindByID(c.Request.Context(), convID)
		if err != nil || conv == nil {
			view.JSONError(c, 404, "conversation not found")
			return
		}

		dev, _ := c.Get("device")
		if conv.DeviceID != dev.(*model.Device).ID {
			view.JSONError(c, 403, "access denied")
			return
		}

		msgs, err := msgRepo.ListByConversation(c.Request.Context(), convID, 100)
		if err != nil {
			view.JSONError(c, 500, "query messages failed")
			return
		}
		view.JSONOK(c, msgs)
	}
}
