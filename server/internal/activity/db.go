package activity

import (
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// schemaVersion 与附表 B 的 meta.schema_version 对齐；字段演进时 +1。
const schemaVersion = 1

// Store 是 events.db 的连接与元数据入口，同时持有唯一 emit 入口。
type Store struct {
	db            *gorm.DB
	em            *Emitter
	watermarkMode string // 上传接口水位口径：WatermarkTime（默认）/ WatermarkSegment；仅启动期设置一次，运行期只读

	// writeMu 是 events.db 的写闸：所有写路径（emit 刷盘、客户端上报 IngestBatch）持读锁；
	// Recompute 持写锁独占，保证重建期间没有其它写事务与之撞锁（busy_timeout 后报 database is locked）。
	writeMu sync.RWMutex
}

// DBPath 返回与主库同目录的 events.db 路径。
func DBPath(mainDBPath string) string {
	return filepath.Join(filepath.Dir(mainDBPath), "events.db")
}

// Open 打开（或创建）events.db：建表、设 pragma、预置 meta。
// 只在采集开关打开时调用；enabled 记进 meta，让"这台采没采"可查。
func Open(path string, enabled bool) (*Store, error) {
	// pragma 写进 DSN，保证连接池里每条连接都是 WAL + NORMAL
	dsn := path + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open events.db %s: %w", path, err)
	}

	for _, stmt := range splitStatements(schemaDDL) {
		if err := db.Exec(stmt).Error; err != nil {
			return nil, fmt.Errorf("events.db create schema: %w", err)
		}
	}

	s := &Store{db: db, watermarkMode: WatermarkTime}
	if err := s.seedMeta(enabled); err != nil {
		return nil, err
	}
	s.em = startEmitter(s)
	return s, nil
}

// SetWatermarkMode 设置上传接口水位口径（仅接受 WatermarkTime / WatermarkSegment）。
// 由启动装配按 activity.watermark_mode 注入一次；默认 time。未知非空值记日志后回落 time（不静默）。
func (s *Store) SetWatermarkMode(mode string) {
	if mode == WatermarkSegment {
		s.watermarkMode = WatermarkSegment
		return
	}
	if mode != "" && mode != WatermarkTime {
		log.Printf("[activity] 未知 watermark_mode=%q，回落 time", mode)
	}
	s.watermarkMode = WatermarkTime
}

// WatermarkMode 返回当前水位口径。
func (s *Store) WatermarkMode() string {
	if s == nil || s.watermarkMode == "" {
		return WatermarkTime
	}
	return s.watermarkMode
}

// seedMeta 预置版本键；INSERT OR IGNORE，重复 Open 不覆盖已有值。
func (s *Store) seedMeta(enabled bool) error {
	defaults := map[string]string{
		"schema_version":         strconv.Itoa(schemaVersion),
		"rule_version":           "1",
		"threshold_version":      "1",
		"normalize_rule_version": "1",
		"last_recompute_at":      "",
		"dirty_days":             "[]",
		"activity.enabled":       strconv.FormatBool(enabled),
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for k, v := range defaults {
		if err := s.db.Exec(
			`INSERT OR IGNORE INTO meta("key", value, updated_at) VALUES(?, ?, ?)`,
			k, v, now,
		).Error; err != nil {
			return fmt.Errorf("events.db seed meta %s: %w", k, err)
		}
	}
	return nil
}

// GetMeta 读取 meta 值；键不存在时返回空串、false。
func (s *Store) GetMeta(key string) (string, bool, error) {
	var row struct {
		Value string
	}
	err := s.db.Raw(`SELECT value FROM meta WHERE "key" = ?`, key).Scan(&row).Error
	if err != nil {
		return "", false, fmt.Errorf("events.db get meta %s: %w", key, err)
	}
	if row.Value == "" {
		return "", false, nil
	}
	return row.Value, true, nil
}

// SetMeta 写入（或更新）meta 值。
func (s *Store) SetMeta(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	err := s.db.Exec(
		`INSERT INTO meta("key", value, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT("key") DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now,
	).Error
	if err != nil {
		return fmt.Errorf("events.db set meta %s: %w", key, err)
	}
	return nil
}

// Close 停止 emit 入口（刷完队列）后关闭底层连接。
func (s *Store) Close() error {
	if s.em != nil {
		s.em.close()
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Emit 投递一条事件（服务端唯一入口，见 emit.go）。
func (s *Store) Emit(ev Event) {
	if s.em == nil {
		return
	}
	s.em.Emit(ev)
}

// Flush 立即刷队列，关键里程碑可调。
func (s *Store) Flush() {
	if s.em != nil {
		s.em.Flush()
	}
}

// DB 暴露底层连接，供同进程内的派生期与测试使用。
func (s *Store) DB() *gorm.DB { return s.db }
