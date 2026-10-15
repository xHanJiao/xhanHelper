package service

import (
	"context"
	"testing"
	"time"

	"xhan-helper/internal/model"
)

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
