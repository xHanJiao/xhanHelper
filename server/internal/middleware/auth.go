package middleware

import (
	"crypto/subtle"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// LocalAuth — 本地单用户模式的免密直通配置（Enabled 为 false 时完全不生效）
type LocalAuth struct {
	Enabled bool
	UserID  string
	Token   string
}

// Auth Bearer Token 认证中间件
func Auth(userRepo *service.UserRepo, local LocalAuth) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" {
			view.JSONError(c, 401, "missing token")
			return
		}
		// 本地单用户：Bearer 等于本地 token 就直接认作本地用户（不查 UserToken 表）。
		// 用常量时间比较避免 token 前缀时序泄露。
		if local.Enabled && local.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(local.Token)) == 1 {
			c.Set("user_id", local.UserID)
			c.Set("bearer_token", token)
			c.Set("local_auth", true)
			c.Next()
			return
		}
		user, err := userRepo.FindByToken(c.Request.Context(), token)
		if err != nil {
			if errors.Is(err, service.ErrTokenExpired) {
				view.JSONError(c, 401, "token expired")
				return
			}
			view.JSONError(c, 401, "invalid token")
			return
		}
		c.Set("user_id", user.ID)
		c.Set("bearer_token", token)
		c.Next()
	}
}
