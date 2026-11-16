package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

func setupAnnotationRepo(t *testing.T) (*DocumentAnnotationRepo, *DocumentRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Document{}, &model.DocumentAnnotation{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}
	return NewDocumentAnnotationRepo(db), NewDocumentRepo(db), cleanup
}

// seedAnnotatedDoc 建一篇带一条划线的文档，返回 (annoRepo, docRepo, docID, annoID)
func seedAnnotatedDoc(t *testing.T, content, quote string) (*DocumentAnnotationRepo, *DocumentRepo, string, string) {
	t.Helper()
	annoRepo, docRepo, cleanup := setupAnnotationRepo(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	doc := &model.Document{ID: "doc-1", UserID: "u1", Title: "t", Content: content}
	if err := docRepo.Create(ctx, doc); err != nil {
		t.Fatalf("seed doc: %v", err)
	}
	anchor, ok := NewAnchorer(content).Locate(quote, "", "")
	if !ok {
		t.Fatalf("seed 定位失败: %q", quote)
	}
	pfx, sfx := NewAnchorer(content).Context(anchor)
	a := &model.DocumentAnnotation{
		ID: "anno-1", UserID: "u1", DocumentID: "doc-1",
		Quote: anchor.Quote, Prefix: pfx, Suffix: sfx,
		StartOffset: anchor.Start, EndOffset: anchor.End,
	}
	if err := annoRepo.Create(ctx, a); err != nil {
		t.Fatalf("seed anno: %v", err)
	}
	return annoRepo, docRepo, doc.ID, a.ID
}

func TestRelocateAll_ShiftsWithoutOrphaning(t *testing.T) {
	ctx := context.Background()
	annoRepo, _, docID, annoID := seedAnnotatedDoc(t, "第一段。\n\n重点句在这里。\n\n第三段。", "重点句在这里")

	// 在划线前面插一段（正文改了，但划线那句话还在）
	affected, orphaned, err := annoRepo.RelocateAll(ctx, docID, "u1", "新增前言。\n\n第一段。\n\n重点句在这里。\n\n第三段。")
	if err != nil {
		t.Fatalf("RelocateAll: %v", err)
	}
	if orphaned != 0 {
		t.Fatalf("不该失活，orphaned=%d", orphaned)
	}
	_ = affected

	a, err := annoRepo.GetByID(ctx, annoID, "u1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if a.Orphaned {
		t.Fatal("平移后不该标 orphaned")
	}
	if a.StartOffset == 0 {
		t.Fatalf("偏移没更新：%d", a.StartOffset)
	}
}

func TestRelocateAll_OrphansAndRecovers(t *testing.T) {
	ctx := context.Background()
	annoRepo, _, docID, annoID := seedAnnotatedDoc(t, "第一段。\n\n重点句在这里。\n\n第三段。", "重点句在这里")

	// 改写成完全不同的句子 → 失活
	_, orphaned, err := annoRepo.RelocateAll(ctx, docID, "u1", "第一段。\n\n句子被彻底重写了。\n\n第三段。")
	if err != nil {
		t.Fatalf("RelocateAll: %v", err)
	}
	if orphaned != 1 {
		t.Fatalf("应报 1 条新失活，got %d", orphaned)
	}
	a, _ := annoRepo.GetByID(ctx, annoID, "u1")
	if !a.Orphaned {
		t.Fatal("应标 orphaned")
	}

	// 改回来 → 自动恢复
	affected, orphaned, err := annoRepo.RelocateAll(ctx, docID, "u1", "第一段。\n\n重点句在这里。\n\n第三段。")
	if err != nil {
		t.Fatalf("RelocateAll: %v", err)
	}
	if affected != 1 || orphaned != 0 {
		t.Fatalf("应报 1 条状态变化且 0 条失活，got affected=%d orphaned=%d", affected, orphaned)
	}
	a, _ = annoRepo.GetByID(ctx, annoID, "u1")
	if a.Orphaned {
		t.Fatal("改回原文后应恢复")
	}
}

func TestUpdateNote_EmptyMeansPlainHighlight(t *testing.T) {
	ctx := context.Background()
	annoRepo, _, _, annoID := seedAnnotatedDoc(t, "第一段。\n\n重点句在这里。", "重点句在这里")

	if rows, err := annoRepo.UpdateNote(ctx, annoID, "u1", "我的批注"); err != nil || rows != 1 {
		t.Fatalf("UpdateNote: rows=%d err=%v", rows, err)
	}
	if rows, err := annoRepo.UpdateNote(ctx, annoID, "u1", ""); err != nil || rows != 1 {
		t.Fatalf("清空 note: rows=%d err=%v", rows, err)
	}
	a, _ := annoRepo.GetByID(ctx, annoID, "u1")
	if a.Note != "" {
		t.Fatalf("note 应为空，got %q", a.Note)
	}
}

// 别人的批注不能被改 / 删
func TestAnnotationOwnershipIsolation(t *testing.T) {
	ctx := context.Background()
	annoRepo, _, _, annoID := seedAnnotatedDoc(t, "第一段。\n\n重点句在这里。", "重点句在这里")

	if rows, _ := annoRepo.UpdateNote(ctx, annoID, "u2", "越权"); rows != 0 {
		t.Fatal("他人 user_id 不该能改")
	}
	if rows, _ := annoRepo.Delete(ctx, annoID, "u2"); rows != 0 {
		t.Fatal("他人 user_id 不该能删")
	}
	if _, err := annoRepo.GetByID(ctx, annoID, "u2"); err == nil {
		t.Fatal("他人 user_id 不该能查")
	}
}

// 定位成功后必须刷新 quote/prefix/suffix 快照（让锚点始终描述"当前正文"）
func TestRelocateAll_RefreshesSnapshot(t *testing.T) {
	ctx := context.Background()
	content := "第一章 引言\n\n我们的结论是：渲染期副产物。\n\n第二章 对照\n\n后面的内容在这里。\n"
	annoRepo, _, docID, annoID := seedAnnotatedDoc(t, content, "渲染期副产物")

	// 在锚点前面插一段：偏移平移，快照也应更新
	shifted := "新前言。\n\n" + content
	if _, _, err := annoRepo.RelocateAll(ctx, docID, "u1", shifted); err != nil {
		t.Fatalf("RelocateAll: %v", err)
	}
	a, _ := annoRepo.GetByID(ctx, annoID, "u1")
	if a.Prefix != "新前言。\n\n第一章 引言\n\n我们的结论是：" {
		t.Fatalf("prefix 未刷新：%q", a.Prefix)
	}
	if a.QuoteRewritten {
		t.Fatal("平移不该标记 QuoteRewritten")
	}

	// 同内容再跑一次必须无变化（幂等，不产生无谓写）
	affected, orphaned, err := annoRepo.RelocateAll(ctx, docID, "u1", shifted)
	if err != nil {
		t.Fatalf("RelocateAll: %v", err)
	}
	if affected != 0 || orphaned != 0 {
		t.Fatalf("幂等调用不该报变化：affected=%d orphaned=%d", affected, orphaned)
	}
}

// 夹逼改写：quote 被服务端改写成新文本，且 QuoteRewritten 置位（让"动了用户数据"可见）
func TestRelocateAll_FuzzyRewriteFlagsAndRecovers(t *testing.T) {
	ctx := context.Background()
	content := "第一章 引言\n\n我们的结论是：渲染期副产物。\n\n第二章 对照\n\n后面的内容在这里。\n"
	annoRepo, _, docID, annoID := seedAnnotatedDoc(t, content, "渲染期副产物")

	// span 内部插字 → 夹逼认下来
	edited := strings.Replace(content, "渲染期副产物", "渲染期（重要）副产物", 1)
	affected, orphaned, err := annoRepo.RelocateAll(ctx, docID, "u1", edited)
	if err != nil {
		t.Fatalf("RelocateAll: %v", err)
	}
	if affected != 0 || orphaned != 0 {
		t.Fatalf("夹逼改写不应算失活：affected=%d orphaned=%d", affected, orphaned)
	}
	a, _ := annoRepo.GetByID(ctx, annoID, "u1")
	if a.Orphaned {
		t.Fatal("内部插字不该失活")
	}
	if !a.QuoteRewritten {
		t.Fatal("夹逼改写应标记 QuoteRewritten")
	}
	if a.Quote != "渲染期（重要）副产物" {
		t.Fatalf("quote 未更新：%q", a.Quote)
	}
	if got := string([]rune(edited)[a.StartOffset:a.EndOffset]); got != "渲染期（重要）副产物" {
		t.Fatalf("偏移与 quote 不一致，源文切片是 %q", got)
	}
}
