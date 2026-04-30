package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func registerDevice(devRepo *service.DeviceRepo, registrationSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			DeviceName string `json:"device_name"`
			Platform   string `json:"platform"`
			Secret     string `json:"secret"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			view.JSONError(c, 400, "invalid request")
			return
		}

		if registrationSecret != "" && req.Secret != registrationSecret {
			view.JSONError(c, 403, "invalid secret")
			return
		}

		device := &model.Device{
			ID:       uuid.New().String(),
			Token:    uuid.New().String(),
			Name:     req.DeviceName,
			Platform: req.Platform,
		}
		if err := devRepo.Create(c.Request.Context(), device); err != nil {
			view.JSONError(c, 500, "create device failed")
			return
		}

		view.JSONOK(c, gin.H{
			"device_token": device.Token,
			"device_id":    device.ID,
		})
	}
}
