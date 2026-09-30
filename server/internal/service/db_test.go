package service

import (
	"fmt"
	"path/filepath"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupVacuumDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "vac.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}
	return db, cleanup
}

// seedAndDeleteBigRows 灌入 n 条 4KB 大行再全删，制造可回收空闲页。
func seedAndDeleteBigRows(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	s := string(big)
	rows := make([]model.Embedding, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, model.Embedding{
			ID: fmt.Sprintf("row-%d", i), EntityType: "message", EntityID: fmt.Sprintf("e-%d", i),
			UserID: "u", Model: "m", Embedding: &s,
		})
	}
	if err := db.CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	if err := db.Where("1 = 1").Delete(&model.Embedding{}).Error; err != nil {
		t.Fatalf("delete rows: %v", err)
	}
}

// TestFreePageBytes_SQLite — 删大量行后 freelist 增长，FreePageBytes 可观测且说明是 SQLite。
func TestFreePageBytes_SQLite(t *testing.T) {
	db, cleanup := setupVacuumDB(t)
	defer cleanup()

	free, ok := FreePageBytes(db)
	if !ok {
		t.Fatal("SQLite 应返回 ok=true")
	}
	if free != 0 {
		t.Errorf("空库空闲页应为 0, got %d", free)
	}

	seedAndDeleteBigRows(t, db, 500)

	freeAfter, ok := FreePageBytes(db)
	if !ok {
		t.Fatal("SQLite 应返回 ok=true")
	}
	if freeAfter <= 0 {
		t.Errorf("删行后空闲页应增长, got %d", freeAfter)
	}
}

// TestFreePageBytes_NonSQLite — 非 SQLite 方言返回 (0, false)，不执行任何 SQL。
// 不走 gorm.Open（会拨号），直接构造只带 Dialector 的 Session 验证类型断言分支。
func TestFreePageBytes_NonSQLite(t *testing.T) {
	db := &gorm.DB{Config: &gorm.Config{Dialector: mysql.New(mysql.Config{DriverName: "stub"})}}
	if free, ok := FreePageBytes(db); ok || free != 0 {
		t.Errorf("非 SQLite 应返回 (0,false), got (%d,%v)", free, ok)
	}
}

// TestVacuumIfFreeBytesAbove_BelowThreshold — 未超阈值不执行 VACUUM。
func TestVacuumIfFreeBytesAbove_BelowThreshold(t *testing.T) {
	db, cleanup := setupVacuumDB(t)
	defer cleanup()
	done, err := VacuumIfFreeBytesAbove(db, 1<<40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Error("阈值极大时不应执行 VACUUM")
	}
}

// TestVacuumIfFreeBytesAbove_AboveThreshold — 超阈值执行 VACUUM 并回收空闲页。
func TestVacuumIfFreeBytesAbove_AboveThreshold(t *testing.T) {
	db, cleanup := setupVacuumDB(t)
	defer cleanup()
	seedAndDeleteBigRows(t, db, 300)

	before, _ := FreePageBytes(db)
	if before == 0 {
		t.Skip("未制造出空闲页，跳过")
	}
	done, err := VacuumIfFreeBytesAbove(db, 0)
	if err != nil {
		t.Fatalf("VACUUM error: %v", err)
	}
	if !done {
		t.Fatal("超阈值应执行 VACUUM")
	}
	if after, _ := FreePageBytes(db); after != 0 {
		t.Errorf("VACUUM 后空闲页应归零, got %d", after)
	}
}
