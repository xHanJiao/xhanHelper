package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// 旧版 token 是 36 位明文 UUID；改为 64 位 SHA-256 hex 后，启动时应清掉旧行强制重登。
func TestPurgeLegacyPlaintextTokens(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tokens.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserToken{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rows := []model.UserToken{
		{ID: "old", UserID: "u1", TokenHash: strings.Repeat("a", 36), ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "new", UserID: "u1", TokenHash: strings.Repeat("b", 64), ExpiresAt: time.Now().Add(time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := purgeLegacyPlaintextTokens(db)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("应清掉 1 条旧 token, got %d", n)
	}
	var left []model.UserToken
	if err := db.Find(&left).Error; err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 1 || left[0].ID != "new" {
		t.Fatalf("只应保留新 token, got %+v", left)
	}
}
