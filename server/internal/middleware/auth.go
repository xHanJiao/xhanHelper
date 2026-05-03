package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// Auth Bearer Token 认证中间件
func Auth(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" {
			view.JSONError(c, 401, "missing token")
			return
		}
		user, err := userRepo.FindByToken(c.Request.Context(), token)
		if err != nil || user == nil {
			view.JSONError(c, 401, "invalid token")
			return
		}
		c.Set("user_id", user.ID)
		c.Set("bearer_token", token)
		c.Next()
	}
}
