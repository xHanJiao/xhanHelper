package service

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"xhan-helper/internal/config"
)

// NewDB 根据配置创建 GORM 连接池
func NewDB(cfg config.DBConfig) *gorm.DB {
	var dialector gorm.Dialector

	switch cfg.Driver {
	case "sqlite":
		dialector = sqlite.Open(cfg.DSN())
	default:
		dialector = mysql.Open(cfg.DSN())
	}

	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		log.Fatal("failed to connect database:", err)
	}
	return db
}
