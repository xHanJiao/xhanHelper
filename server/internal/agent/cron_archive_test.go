package agent

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupTestConversationRepo(t *testing.T) (*service.ConversationRepo, *gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return service.NewConversationRepo(db), db, cleanup
}

func TestArchiveStale_DisabledIsNoop(t *testing.T) {
	repo, db, cleanup := setupTestConversationRepo(t)
	defer cleanup()
	ctx := context.Background()
	db.Create(&model.Conversation{ID: "c1", UserID: "u1"})
	db.Model(&model.Conversation{}).Where("id = ?", "c1").
		UpdateColumn("updated_at", time.Now().AddDate(0, 0, -30))

	cr := &CronRunner{convRepo: repo, archiveAfterDays: 0}
	cr.archiveStale(ctx)

	var c model.Conversation
	db.First(&c, "id = ?", "c1")
	if c.IsDeleted {
		t.Fatal("after_days=0 should disable archiving")
	}
}

func TestArchiveStale_ArchivesOldConversation(t *testing.T) {
	repo, db, cleanup := setupTestConversationRepo(t)
	defer cleanup()
	ctx := context.Background()
	db.Create(&model.Conversation{ID: "old", UserID: "u1"})
	db.Create(&model.Conversation{ID: "new", UserID: "u1"})
	db.Model(&model.Conversation{}).Where("id = ?", "old").
		UpdateColumn("updated_at", time.Now().AddDate(0, 0, -30))

	cr := &CronRunner{convRepo: repo, archiveAfterDays: 14}
	cr.archiveStale(ctx)

	var oldC, newC model.Conversation
	db.First(&oldC, "id = ?", "old")
	db.First(&newC, "id = ?", "new")
	if !oldC.IsDeleted {
		t.Error("stale conversation should be archived")
	}
	if newC.IsDeleted {
		t.Error("fresh conversation should NOT be archived")
	}
}
