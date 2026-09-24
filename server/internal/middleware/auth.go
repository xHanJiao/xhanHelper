package middleware

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// truncateToken 日志里只留前缀片段，别把整串 token 写进日志。
func truncateToken(t string) string {
	if len(t) <= 12 {
		return t
	}
	return t[:12] + "…"
}

// LocalAuth — 本地单用户模式的免密直通配置（Enabled 为 false 时完全不生效）
type LocalAuth struct {
	Enabled bool
	UserID  string
	Token   string
	// Env — 本环境标签（dev | prod，第 4 期）。非空时要求请求携带的 token 以 "<env>:" 开头，
	// 否则 403：客户端 token 由 app 按构建生成（Debug=dev / Release=prod），
	// 这样「dev 经逃生口连 prod」这类越界请求连门都进不来。
	Env string
}

// Auth Bearer Token 认证中间件
func Auth(userRepo *service.UserRepo, local LocalAuth) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" {
			view.JSONError(c, 401, "missing token")
			return
		}
		if local.Enabled && local.Env != "" && !strings.HasPrefix(token, local.Env+":") {
			// 环境不匹配 = 拿着别的环境的 token（或云端 user token）来连本地后端。
			log.Printf("[local] env mismatch: want prefix %q, got %q (path=%s)", local.Env+":", truncateToken(token), c.Request.URL.Path)
			view.JSONError(c, 403, fmt.Sprintf("环境不匹配：本地服务为 %s", local.Env))
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
		// 本地模式下不再回落 user_tokens 表：本地只有一把 token，回落只会给越界留口子。
		if local.Enabled {
			view.JSONError(c, 401, "invalid token")
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
