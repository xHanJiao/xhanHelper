package controller

import (
	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func registerUser(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			view.JSONError(c, 400, "username and password required")
			return
		}
		user, token, err := userRepo.Create(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			view.JSONError(c, 409, "registration failed (username may already exist)")
			return
		}
		view.JSONOK(c, gin.H{"user_id": user.ID, "token": token})
	}
}

func loginUser(userRepo *service.UserRepo) gin.HandlerFunc {
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
		view.JSONOK(c, gin.H{"user_id": user.ID, "token": token})
	}
}

func logoutUser(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetString("bearer_token")
		userRepo.Logout(c.Request.Context(), token)
		view.JSONOK(c, gin.H{"ok": true})
	}
}
