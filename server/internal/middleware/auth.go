package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// Auth 设备 Token 认证中间件
func Auth(devRepo *service.DeviceRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" {
			view.JSONError(c, 401, "missing token")
			return
		}
		device, err := devRepo.FindByToken(c.Request.Context(), token)
		if err != nil || device == nil {
			view.JSONError(c, 401, "invalid token")
			return
		}
		c.Set("device", device)
		c.Next()
	}
}
