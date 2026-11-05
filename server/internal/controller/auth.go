package controller

import (
	"errors"
	"log"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func registerUser(userRepo *service.UserRepo, enabled *bool, allowed []string, adminUsers []string, limiter *authFailureLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enabled == nil || !*enabled {
			view.JSONError(c, 403, "registration is disabled")
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			view.JSONError(c, 400, "username and password required")
			return
		}
		if err := validatePassword(req.Password); err != nil {
			view.JSONError(c, 400, err.Error())
			return
		}
		key := authKey(c, req.Username)
		ipKey := authIPKey(c)
		if !limiter.Allow(key) || !limiter.Allow(ipKey) {
			view.JSONError(c, 429, "too many attempts, try again later")
			return
		}
		if len(allowed) > 0 {
			ok := false
			for _, u := range allowed {
				if req.Username == u {
					ok = true
					break
				}
			}
			if !ok {
				view.JSONError(c, 403, "username not in allowlist")
				return
			}
		}
		user, token, err := userRepo.Create(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, service.ErrUsernameTaken) {
				limiter.Fail(key)
				limiter.Fail(ipKey)
				view.JSONError(c, 409, "username already exists")
				return
			}
			log.Printf("[auth] register error: user=%s err=%v", req.Username, err)
			view.JSONError(c, 500, "registration failed")
			return
		}
		limiter.Reset(key)
		limiter.Reset(ipKey)
		view.JSONOK(c, gin.H{"user_id": user.ID, "token": token, "is_admin": isAdmin(user.ID, adminUsers)})
	}
}

func loginUser(userRepo *service.UserRepo, adminUsers []string, limiter *authFailureLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			view.JSONError(c, 400, "username and password required")
			return
		}
		key := authKey(c, req.Username)
		ipKey := authIPKey(c)
		if !limiter.Allow(key) || !limiter.Allow(ipKey) {
			view.JSONError(c, 429, "too many attempts, try again later")
			return
		}
		user, token, err := userRepo.Login(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, service.ErrInvalidCredentials) {
				limiter.Fail(key)
				limiter.Fail(ipKey)
				view.JSONError(c, 401, "invalid credentials")
				return
			}
			log.Printf("[auth] login error: user=%s err=%v", req.Username, err)
			view.JSONError(c, 500, "login failed")
			return
		}
		limiter.Reset(key)
		limiter.Reset(ipKey)
		view.JSONOK(c, gin.H{"user_id": user.ID, "token": token, "is_admin": isAdmin(user.ID, adminUsers)})
	}
}

func changePassword(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		var req struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
			view.JSONError(c, 400, "old_password and new_password required")
			return
		}
		if err := validatePassword(req.NewPassword); err != nil {
			view.JSONError(c, 400, err.Error())
			return
		}
		revoked, err := userRepo.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword, c.GetString("bearer_token"))
		if err != nil {
			if errors.Is(err, service.ErrInvalidPassword) {
				view.JSONError(c, 401, "invalid old password")
				return
			}
			log.Printf("[auth] change password error: user=%s err=%v", userID, err)
			view.JSONError(c, 500, "change password failed")
			return
		}
		log.Printf("[auth] password changed: user=%s revoked_tokens=%d", userID, revoked)
		view.JSONOK(c, gin.H{"ok": true, "revoked": revoked})
	}
}

func isAdmin(userID string, adminUsers []string) bool {
	for _, id := range adminUsers {
		if id == userID {
			return true
		}
	}
	return false
}

func refreshToken(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 本地单用户 token 无过期概念、也不在 user_tokens 表里 → 直接续期成功，
		// 否则前端 30 分钟心跳拿到 401 会主动登出。
		if c.GetBool("local_auth") {
			view.JSONOK(c, gin.H{"ok": true})
			return
		}
		token := c.GetString("bearer_token")
		if err := userRepo.RefreshToken(c.Request.Context(), token); err != nil {
			if errors.Is(err, service.ErrTokenExpired) || errors.Is(err, service.ErrInvalidToken) {
				view.JSONError(c, 401, "token expired or invalid")
				return
			}
			// 心跳遇到 DB 故障也不能回 401，否则所有客户端会同时登出。
			log.Printf("[auth] refresh token error: %v", err)
			view.JSONError(c, 500, "refresh failed")
			return
		}
		view.JSONOK(c, gin.H{"ok": true})
	}
}

func logoutUser(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetString("bearer_token")
		if err := userRepo.Logout(c.Request.Context(), token); err != nil {
			log.Printf("[auth] logout error: %v", err)
		}
		view.JSONOK(c, gin.H{"ok": true})
	}
}
