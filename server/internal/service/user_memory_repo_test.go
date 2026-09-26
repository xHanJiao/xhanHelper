package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// TestMemoryTopics 覆盖记忆词云依赖的两件事：按 topic 过滤、topic 聚合。
func TestMemoryTopics(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewUserMemoryRepo(db)
	ctx := context.Background()

	seed := []struct{ topic, value string }{
		{"Go", "go 1"}, {"Go", "go 2"}, {"Go", "go 3"},
		{"Python", "py 1"}, {"Python", "py 2"},
		{"", "no topic"},
	}
	for _, s := range seed {
		m := &model.UserMemory{
			ID:          uuid.New().String(),
			UserID:      "u1",
			MemoryKey:   s.topic,
			ContentHash: uuid.New().String(),
			MemoryValue: s.value,
			Topic:       s.topic,
		}
		if err := repo.Create(ctx, m); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// 1) 按 topic 过滤：只返回该 topic 的记忆
	list, total, err := repo.ListByUser(ctx, "u1", "", "", "Go", 50, 0)
	if err != nil {
		t.Fatalf("list by topic: %v", err)
	}
	if total != 3 || len(list) != 3 {
		t.Fatalf("topic filter got total=%d len=%d, want 3/3", total, len(list))
	}
	for _, m := range list {
		if m.Topic != "Go" {
			t.Fatalf("unexpected topic %q", m.Topic)
		}
	}

	// 2) topic 聚合：按 count 倒序、排除空 topic
	topics, err := repo.DistinctTopics(ctx, "u1")
	if err != nil {
		t.Fatalf("distinct topics: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("distinct topics len=%d, want 2 (%+v)", len(topics), topics)
	}
	if topics[0].Topic != "Go" || topics[0].Count != 3 {
		t.Fatalf("topics[0]=%+v, want {Go 3}", topics[0])
	}
	if topics[1].Topic != "Python" || topics[1].Count != 2 {
		t.Fatalf("topics[1]=%+v, want {Python 2}", topics[1])
	}
}
