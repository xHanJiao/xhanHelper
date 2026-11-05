package controller

import (
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xhan-helper/internal/config"
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
		if err := validatePassword(req.Password); err != nil {
			view.JSONError(c, 400, err.Error())
			return
		}
		user, token, err := userRepo.Create(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, service.ErrUsernameTaken) {
				view.JSONError(c, 409, "用户名已存在")
				return
			}
			log.Printf("[admin] create user error: user=%s err=%v", req.Username, err)
			view.JSONError(c, 500, "创建用户失败")
			return
		}
		view.JSONOK(c, gin.H{"user_id": user.ID, "username": user.Username, "token": token})
	}
}

// resetUserPassword POST /api/admin/users/:id/reset-password — 管理员重置为随机密码，并吊销该用户全部 token
func resetUserPassword(userRepo *service.UserRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.Param("id")
		newPassword, err := randomPassword()
		if err != nil {
			log.Printf("[admin] generate reset password error: %v", err)
			view.JSONError(c, 500, "reset password failed")
			return
		}
		if err := userRepo.ResetPassword(c.Request.Context(), userID, newPassword); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				view.JSONError(c, 404, "重置失败：用户不存在")
				return
			}
			log.Printf("[admin] reset password error user=%s: %v", userID, err)
			view.JSONError(c, 500, "重置失败")
			return
		}
		view.JSONOK(c, gin.H{"user_id": userID, "new_password": newPassword})
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
		days, _ := strconv.Atoi(c.DefaultQuery("days", "3"))
		if days <= 0 {
			days = 3
		}
		result, err := userRepo.List(c.Request.Context(), offset, limit)
		if err != nil {
			log.Printf("[admin] list users error: %v", err)
			view.JSONError(c, 500, "获取用户列表失败")
			return
		}

		userIDs := make([]string, len(result.Users))
		for i, u := range result.Users {
			userIDs[i] = u.ID
		}

		since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		statsMap, err := userRepo.GetUserStats(c.Request.Context(), userIDs, since)
		if err != nil {
			log.Printf("[admin] get user stats error: %v", err)
		}

		type userDTO struct {
			ID                 string `json:"id"`
			Username           string `json:"username"`
			CreatedAt          string `json:"created_at"`
			LastMessageAt      string `json:"last_message_at,omitempty"`
			RecentMessageCount int64  `json:"recent_message_count"`
			RecentNoteCount    int64  `json:"recent_note_count"`
			RecentMemoryCount  int64  `json:"recent_memory_count"`
			RecentDocCount     int64  `json:"recent_doc_count"`
		}
		users := make([]userDTO, 0, len(result.Users))
		for _, u := range result.Users {
			dto := userDTO{
				ID:        u.ID,
				Username:  u.Username,
				CreatedAt: u.CreatedAt.In(config.BJ).Format("2006-01-02 15:04:05"),
			}
			if statsMap != nil {
				if s, ok := statsMap[u.ID]; ok {
					if s.LastMessageAt != nil {
						dto.LastMessageAt = s.LastMessageAt.In(config.BJ).Format("2006-01-02 15:04:05")
					}
					dto.RecentMessageCount = s.RecentMessageCount
					dto.RecentNoteCount = s.RecentNoteCount
					dto.RecentMemoryCount = s.RecentMemoryCount
					dto.RecentDocCount = s.RecentDocCount
				}
			}
			users = append(users, dto)
		}
		view.JSONOK(c, gin.H{"users": users, "total_count": result.TotalCount})
	}
}
