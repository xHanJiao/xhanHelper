package service

import (
	"context"
	"errors"
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
	if err := db.AutoMigrate(&model.User{}, &model.UserToken{}, &model.Message{}); err != nil {
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

func TestUserRepo_TokenHashedAtRest(t *testing.T) {
	db := newUserRepoTestDB(t)
	repo := NewUserRepo(db, 5, 4)
	ctx := context.Background()

	if _, raw, err := repo.Create(ctx, "alice", "password-123"); err != nil {
		t.Fatalf("create: %v", err)
	} else {
		var row model.UserToken
		if err := db.First(&row).Error; err != nil {
			t.Fatalf("load token row: %v", err)
		}
		if row.TokenHash == raw || len(row.TokenHash) != 64 {
			t.Fatalf("库里应是 64 位哈希，got %q", row.TokenHash)
		}
		if _, err := repo.FindByToken(ctx, raw); err != nil {
			t.Fatalf("明文 token 应能认证: %v", err)
		}
		if err := repo.Logout(ctx, raw); err != nil {
			t.Fatalf("logout: %v", err)
		}
		if _, err := repo.FindByToken(ctx, raw); err == nil {
			t.Fatal("logout 后 token 应失效")
		}
	}
}

func TestUserRepo_ChangePasswordRevokesOtherTokens(t *testing.T) {
	db := newUserRepoTestDB(t)
	repo := NewUserRepo(db, 5, 4)
	ctx := context.Background()

	user, current, err := repo.Create(ctx, "alice", "old-password-1")
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := repo.Login(ctx, "alice", "old-password-1")
	if err != nil {
		t.Fatal(err)
	}

	revoked, err := repo.ChangePassword(ctx, user.ID, "old-password-1", "new-password-1", current)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Fatalf("应只吊销其它设备 1 个 token, got %d", revoked)
	}
	if _, err := repo.FindByToken(ctx, current); err != nil {
		t.Fatalf("当前设备 token 应保留: %v", err)
	}
	if _, err := repo.FindByToken(ctx, other); err == nil {
		t.Fatal("其它设备 token 应被吊销")
	}
	if _, _, err := repo.Login(ctx, "alice", "old-password-1"); err == nil {
		t.Fatal("旧密码应失效")
	}
	if _, _, err := repo.Login(ctx, "alice", "new-password-1"); err != nil {
		t.Fatalf("新密码应可登录: %v", err)
	}

	// 旧密码错误必须返回 ErrInvalidPassword，而不是 DB 错误
	if _, err := repo.ChangePassword(ctx, user.ID, "wrong-password", "another-password", current); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("want ErrInvalidPassword, got %v", err)
	}
}

func TestUserRepo_ResetPasswordRevokesAllTokens(t *testing.T) {
	db := newUserRepoTestDB(t)
	repo := NewUserRepo(db, 5, 4)
	ctx := context.Background()

	user, tok1, err := repo.Create(ctx, "alice", "old-password-1")
	if err != nil {
		t.Fatal(err)
	}
	_, tok2, err := repo.Login(ctx, "alice", "old-password-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.ResetPassword(ctx, user.ID, "reset-password-1"); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{tok1, tok2} {
		if _, err := repo.FindByToken(ctx, tok); err == nil {
			t.Fatal("重置密码后全部 token 应失效")
		}
	}
	if _, _, err := repo.Login(ctx, "alice", "reset-password-1"); err != nil {
		t.Fatalf("重置后的密码应可登录: %v", err)
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
