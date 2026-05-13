package monitor

import (
	"github.com/gin-gonic/gin"
)

// HealthHandler 返回 GET /api/admin/health 的处理函数
func HealthHandler(mon *Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(200, mon.Snapshot())
	}
}

// HealthHandlerGlobal 使用全局 Monitor
func HealthHandlerGlobal() gin.HandlerFunc {
	return HealthHandler(global)
}
