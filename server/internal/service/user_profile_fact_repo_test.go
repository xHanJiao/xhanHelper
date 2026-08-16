package service

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
)

func setupFactRepo(t *testing.T) *UserProfileFactRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfileFact{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return NewUserProfileFactRepo(db)
}

func TestFactRepo_ReplaceFieldFacts(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	if err := repo.ReplaceFieldFacts(ctx, "u1", "重要关系", []string{"CJ: 伴侣", "又又: 好友"}, "b1"); err != nil {
		t.Fatalf("replace round 1: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "重要关系")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active facts, got %d", len(active))
	}

	// 第二轮替换：又又保留、CJ 淘汰、新增惠茜
	if err := repo.ReplaceFieldFacts(ctx, "u1", "重要关系", []string{"又又: 好友", "惠茜: 表妹"}, "b2"); err != nil {
		t.Fatalf("replace round 2: %v", err)
	}
	active, err = repo.GetActiveByField(ctx, "u1", "重要关系")
	if err != nil {
		t.Fatalf("get active round 2: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active facts after round 2, got %d", len(active))
	}
	got := map[string]bool{}
	for _, f := range active {
		got[f.Value] = true
		if f.Status != model.FactStatusActive {
			t.Errorf("fact %q should be active, got %s", f.Value, f.Status)
		}
		if f.BatchID != "b2" {
			t.Errorf("fact %q batch = %s, expected b2", f.Value, f.BatchID)
		}
	}
	if !got["又又: 好友"] || !got["惠茜: 表妹"] {
		t.Errorf("expected 又又 and 惠茜 active, got %v", got)
	}
	if got["CJ: 伴侣"] {
		t.Error("CJ should be outdated")
	}

	// outdated 审计可查
	all, err := repo.GetActiveByField(ctx, "u1", "重要关系")
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 total facts, got %d", len(all))
	}
}

func TestFactRepo_ReplaceFieldFacts_IsolatedByUserAndField(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	if err := repo.ReplaceFieldFacts(ctx, "u1", "职业领域", []string{"Go后端"}, "b1"); err != nil {
		t.Fatalf("seed u1: %v", err)
	}
	if err := repo.ReplaceFieldFacts(ctx, "u2", "职业领域", []string{"医生"}, "b1"); err != nil {
		t.Fatalf("seed u2: %v", err)
	}
	if err := repo.ReplaceFieldFacts(ctx, "u1", "饮食习惯", []string{"不吃辣"}, "b1"); err != nil {
		t.Fatalf("seed u1 field2: %v", err)
	}

	// u2 的替换不影响 u1
	if err := repo.ReplaceFieldFacts(ctx, "u2", "职业领域", []string{"牙医"}, "b2"); err != nil {
		t.Fatalf("replace u2: %v", err)
	}
	u1Career, err := repo.GetActiveByField(ctx, "u1", "职业领域")
	if err != nil {
		t.Fatalf("get u1 career: %v", err)
	}
	if len(u1Career) != 1 || u1Career[0].Value != "Go后端" {
		t.Errorf("u1 career should be unchanged, got %+v", u1Career)
	}
	u1Diet, err := repo.GetActiveByField(ctx, "u1", "饮食习惯")
	if err != nil {
		t.Fatalf("get u1 diet: %v", err)
	}
	if len(u1Diet) != 1 || u1Diet[0].Value != "不吃辣" {
		t.Errorf("u1 diet should be unchanged, got %+v", u1Diet)
	}
}

func TestFactRepo_HasActive(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	ok, err := repo.HasActive(ctx, "u1")
	if err != nil {
		t.Fatalf("has active: %v", err)
	}
	if ok {
		t.Error("empty user should have no active facts")
	}

	if err := repo.ReplaceFieldFacts(ctx, "u1", "称呼", []string{"哥"}, "b1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ok, err = repo.HasActive(ctx, "u1")
	if err != nil {
		t.Fatalf("has active 2: %v", err)
	}
	if !ok {
		t.Error("seeded user should have active facts")
	}

	// 全量替换后（仍有 active）
	if err := repo.ReplaceFieldFacts(ctx, "u1", "称呼", []string{"老哥"}, "b2"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	ok, err = repo.HasActive(ctx, "u1")
	if err != nil {
		t.Fatalf("has active 3: %v", err)
	}
	if !ok {
		t.Error("user should still have active facts after replace")
	}
}

func TestFactRepo_ReplaceFieldFacts_DedupeAndEmpty(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	err := repo.ReplaceFieldFacts(ctx, "u1", "补充说明",
		[]string{"睡眠差", "睡眠差", "", "  房价观察  ", "房价观察"}, "b1")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "补充说明")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 unique facts, got %d: %+v", len(active), active)
	}
	if active[0].Value != "睡眠差" {
		t.Errorf("first fact should be 睡眠差, got %q", active[0].Value)
	}
	if active[1].Value != "房价观察" {
		t.Errorf("second fact should be trimmed 房价观察, got %q", active[1].Value)
	}
}

func TestFactRepo_CleanupOutdated(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	// 4 轮替换 → 4 批 outdated（b0~b2 是旧批，b3 是当前 active 的批）
	for i, batch := range []string{"b0", "b1", "b2", "b3"} {
		_ = i
		if err := repo.ReplaceFieldFacts(ctx, "u1", "职业领域", []string{"fact-" + batch}, batch); err != nil {
			t.Fatalf("replace %s: %v", batch, err)
		}
	}
	// 此时 outdated 批次 = b0, b1, b2（3 批），active = b3
	// keepBatches=2 → 删最旧的 b0
	n, err := repo.CleanupOutdated(ctx, "u1", 2)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 deleted fact, got %d", n)
	}

	// b0 的 fact 没了，b1/b2 还在
	var outdated []model.UserProfileFact
	if err := repo.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", "u1", model.FactStatusOutdated).
		Find(&outdated).Error; err != nil {
		t.Fatalf("query outdated: %v", err)
	}
	if len(outdated) != 2 {
		t.Fatalf("expected 2 outdated facts left, got %d", len(outdated))
	}
	for _, f := range outdated {
		if f.BatchID == "b0" {
			t.Errorf("b0 batch should be cleaned, got %+v", f)
		}
	}

	// active 不受影响
	active, err := repo.GetActiveByField(ctx, "u1", "职业领域")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 1 || active[0].Value != "fact-b3" {
		t.Errorf("active should be fact-b3, got %+v", active)
	}
}

func init() {
	idgen.Init(1)
}
