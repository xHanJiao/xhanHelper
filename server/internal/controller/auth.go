package controller

import (
	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func registerUser(userRepo *service.UserRepo, enabled *bool, allowed []string, adminUsers []string) gin.HandlerFunc {
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
			view.JSONError(c, 409, "registration failed (username may already exist)")
			return
		}
		view.JSONOK(c, gin.H{"user_id": user.ID, "token": token, "is_admin": isAdmin(user.ID, adminUsers)})
	}
}

func loginUser(userRepo *service.UserRepo, adminUsers []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			view.JSONError(c, 400, "username and password required")
			return
		}
		user, token, err := userRepo.Login(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			view.JSONError(c, 401, "invalid credentials")
			return
		}
		view.JSONOK(c, gin.H{"user_id": user.ID, "token": token, "is_admin": isAdmin(user.ID, adminUsers)})
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

func logoutUser(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetString("bearer_token")
		userRepo.Logout(c.Request.Context(), token)
		view.JSONOK(c, gin.H{"ok": true})
	}
}
