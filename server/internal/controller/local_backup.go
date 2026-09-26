package controller

import (
	"log"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/backup"
	"xhan-helper/internal/config"
	"xhan-helper/internal/view"
)

// localBackupTrigger 本地模式「立即备份」：读 cfg.Backup，同步跑一次全量备份。
func localBackupTrigger(cfg *config.Config, svc *backup.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Backup.Enabled {
			view.JSONError(c, 400, "备份未启用，请先在设置页开启并选择备份位置")
			return
		}
		ar, err := svc.Create(c.Request.Context(), backup.OptionsFromConfig(cfg))
		if err != nil {
			log.Printf("[local_backup] 备份失败：%v", err)
			view.JSONError(c, 500, "备份失败："+err.Error())
			return
		}
		view.JSONOK(c, gin.H{"archive": ar})
	}
}
