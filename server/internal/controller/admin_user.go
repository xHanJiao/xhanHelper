package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// createUserByAdmin POST /api/admin/users — 管理员创建用户，绕过注册开关和白名单
func createUserByAdmin(userRepo *service.UserRepo) gin.HandlerFunc {
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
			log.Printf("[admin] create user error: %v", err)
			view.JSONError(c, 409, "创建用户失败（用户名可能已存在）")
			return
		}
		view.JSONOK(c, gin.H{"user_id": user.ID, "username": user.Username, "token": token})
	}
}

// listUsers GET /api/admin/users — 分页列出所有用户
func listUsers(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		if limit <= 0 || limit > 100 {
			limit = 20
		}
		result, err := userRepo.List(c.Request.Context(), offset, limit)
		if err != nil {
			log.Printf("[admin] list users error: %v", err)
			view.JSONError(c, 500, "获取用户列表失败")
			return
		}
		// 安全：不暴露 password_hash，只返回公开字段
		type userDTO struct {
			ID        string `json:"id"`
			Username  string `json:"username"`
			CreatedAt string `json:"created_at"`
		}
		users := make([]userDTO, 0, len(result.Users))
		for _, u := range result.Users {
			users = append(users, userDTO{
				ID:        u.ID,
				Username:  u.Username,
				CreatedAt: u.CreatedAt.Format("2006-01-02 15:04:05"),
			})
		}
		view.JSONOK(c, gin.H{"users": users, "total_count": result.TotalCount})
	}
}
