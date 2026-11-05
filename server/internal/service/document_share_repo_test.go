package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// 建新分享失败时必须保留旧链接：删旧+建新同事务，失败整体回滚。
func TestDocumentShareRepo_CreateRollsBackOnFailure(t *testing.T) {
	docRepo, db, cleanup := setupDocumentRepo(t)
	defer cleanup()
	repo := NewDocumentShareRepo(db, docRepo)
	ctx := context.Background()

	seedDoc(t, docRepo, "d1", "u1", "")

	old, err := repo.Create(ctx, "d1", "u1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := db.Callback().Create().Before("gorm:create").Register("fail_share_create", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "document_shares" {
			_ = tx.AddError(errors.New("boom"))
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	if _, err := repo.Create(ctx, "d1", "u1"); err == nil {
		t.Fatal("建新分享失败时应返回错误")
	}

	var left []model.DocumentShare
	if err := db.Where("document_id = ?", "d1").Find(&left).Error; err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 1 || left[0].Token != old.Token {
		t.Fatalf("旧分享应原样保留, got %+v", left)
	}
}

// 分享链接永不过期：历史记录里 expires_at 已是过去时间，仍应可访问。
func TestDocumentShareRepo_NoExpiry(t *testing.T) {
	docRepo, db, cleanup := setupDocumentRepo(t)
	defer cleanup()
	repo := NewDocumentShareRepo(db, docRepo)
	ctx := context.Background()

	seedDoc(t, docRepo, "d1", "u1", "")

	share, err := repo.Create(ctx, "d1", "u1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if share.ExpiresAt != nil {
		t.Fatalf("new share should have no expiry, got %v", share.ExpiresAt)
	}

	if err := db.Model(&model.DocumentShare{}).Where("id = ?", share.ID).
		Update("expires_at", time.Now().Add(-24*time.Hour)).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}

	_, doc, err := repo.GetByToken(ctx, share.Token)
	if err != nil {
		t.Fatalf("get by token: %v", err)
	}
	if doc.ID != "d1" {
		t.Fatalf("got doc %q, want d1", doc.ID)
	}
}
