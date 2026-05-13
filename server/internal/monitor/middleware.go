package monitor

import (
	"github.com/gin-gonic/gin"
)

// AdminOnly 返回管理员鉴权中间件
func AdminOnly(adminUsers []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(adminUsers) == 0 {
			c.AbortWithStatusJSON(403, gin.H{"error": "no admin configured"})
			return
		}
		userID := c.GetString("user_id")
		for _, id := range adminUsers {
			if id == userID {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(403, gin.H{"error": "admin access required"})
	}
}
