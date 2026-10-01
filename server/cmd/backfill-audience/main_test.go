package main

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// 回归：批量回标不得刷新 updated_at（否则列表最近排序被毁）。
func TestApplyAudienceKeepsUpdatedAt(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Document{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	docs := []model.Document{
		{ID: "f1", UserID: "u1", Title: "a", Content: "c", Audience: model.DocumentAudienceInternal},
		{ID: "i1", UserID: "u1", Title: "b", Content: "c", Audience: model.DocumentAudienceFinished},
	}
	if err := db.Create(&docs).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var before model.Document
	db.Where("id = ?", "f1").First(&before)

	time.Sleep(10 * time.Millisecond) // 让 now() 前进，Update 若刷新必被检出
	if err := applyAudience(db, []string{"i1"}, []string{"f1"}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var f, i model.Document
	db.Where("id = ?", "f1").First(&f)
	db.Where("id = ?", "i1").First(&i)
	if f.Audience != model.DocumentAudienceFinished || i.Audience != model.DocumentAudienceInternal {
		t.Errorf("audience 未改写: f1=%q i1=%q", f.Audience, i.Audience)
	}
	if !f.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at 被刷新: got %v, want %v", f.UpdatedAt, before.UpdatedAt)
	}
}
