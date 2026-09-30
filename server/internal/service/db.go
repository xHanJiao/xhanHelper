package service

import (
	"fmt"
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

// FreePageBytes 返回数据库文件中可被回收的空闲页字节数（freelist_count × page_size）。
// 仅对 SQLite 有意义（VACUUM 回收空页用）；非 SQLite 返回 (0, false)。
// 用类型断言识别方言，不依赖配置——测试也走同一路径。
func FreePageBytes(db *gorm.DB) (int64, bool) {
	if _, ok := db.Dialector.(*sqlite.Dialector); !ok {
		return 0, false
	}
	var freelist, pageSize int64
	if err := db.Raw("PRAGMA freelist_count").Scan(&freelist).Error; err != nil {
		log.Printf("[db] read freelist_count error: %v", err)
		return 0, true
	}
	if err := db.Raw("PRAGMA page_size").Scan(&pageSize).Error; err != nil {
		log.Printf("[db] read page_size error: %v", err)
		return 0, true
	}
	return freelist * pageSize, true
}

// VacuumIfFreeBytesAbove 空闲页字节超过 thresholdBytes 时执行全量 VACUUM，回收文件空间；
// 未超阈值或非 SQLite 时直接返回（false = 未执行）。成功返回 (true, nil)。
// VACUUM 需短暂全库写锁并重建整个文件（需等量临时空间），属运维级操作，仅由后台清理节拍触发。
func VacuumIfFreeBytesAbove(db *gorm.DB, thresholdBytes int64) (bool, error) {
	free, ok := FreePageBytes(db)
	if !ok {
		return false, nil
	}
	if free <= thresholdBytes {
		return false, nil
	}
	if err := db.Exec("VACUUM").Error; err != nil {
		return false, fmt.Errorf("vacuum: %w", err)
	}
	return true, nil
}
