package service

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func newUserRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.User{}, &model.Message{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedActiveGateRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	users := []model.User{
		{ID: "u_active", Username: "active", PasswordHash: "x"},
		{ID: "u_stale", Username: "stale", PasswordHash: "x"},
		{ID: "u_empty", Username: "empty", PasswordHash: "x"},
	}
	for _, u := range users {
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	msgs := []model.Message{
		{ID: "m1", UserID: "u_active", ConversationID: "c1", Role: "user", Status: model.MsgStatusNormal, CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "m2", UserID: "u_active", ConversationID: "c1", Role: "assistant", Status: model.MsgStatusNormal, CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "m3", UserID: "u_stale", ConversationID: "c2", Role: "user", Status: model.MsgStatusNormal, CreatedAt: now.Add(-10 * 24 * time.Hour)},
		{ID: "m4", UserID: "u_active", ConversationID: "c3", Role: "user", Status: model.MsgStatusDeleted, CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "m5", UserID: "u_active", ConversationID: "c4", Role: "user", Status: model.MsgStatusNormal, CreatedAt: now.Add(-1 * time.Hour), IsSubAgent: true},
	}
	for _, m := range msgs {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestUserRepo_ListActiveSince(t *testing.T) {
	db := newUserRepoTestDB(t)
	seedActiveGateRows(t, db)
	repo := NewUserRepo(db, 5, 4)

	users, err := repo.ListActiveSince(context.Background(), time.Now().Add(-3*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 active user, got %d: %+v", len(users), users)
	}
	if users[0].ID != "u_active" {
		t.Fatalf("expected u_active, got %s", users[0].ID)
	}

	users, err = repo.ListActiveSince(context.Background(), time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 active users with 30d window, got %d", len(users))
	}
}

func TestUserRepo_IsActiveSince(t *testing.T) {
	db := newUserRepoTestDB(t)
	seedActiveGateRows(t, db)
	repo := NewUserRepo(db, 5, 4)
	ctx := context.Background()
	since := time.Now().Add(-3 * 24 * time.Hour)

	active, err := repo.IsActiveSince(ctx, "u_active", since)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("u_active should be active")
	}

	active, err = repo.IsActiveSince(ctx, "u_stale", since)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("u_stale should be inactive")
	}

	active, err = repo.IsActiveSince(ctx, "u_empty", since)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("u_empty should be inactive")
	}
}
