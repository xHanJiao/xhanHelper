package agent

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 回归 P1：追问是 [user, assistant] 同批落库，GORM 的 autoCreateTime 对同一批只取一个 now，
// 两行 created_at 完全相同 → 排序只能靠 id 破平。回复 ID 必须是时间有序的雪花（不是 uuid），
// 否则顺序随机反转：线程把 AI 答复排在用户追问之前、折叠行 last_reply 取到用户自己。
func TestAnnotationReplier_FollowupOrderStable(t *testing.T) {
	idgen.Init(1)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Document{}, &model.DocumentAnnotation{}, &model.DocumentAnnotationReply{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	if err := db.Create(&model.Document{ID: "doc-1", UserID: "u1", Title: "T", Content: "正文"}).Error; err != nil {
		t.Fatalf("seed doc: %v", err)
	}
	if err := db.Create(&model.DocumentAnnotation{
		ID: "anno-1", UserID: "u1", DocumentID: "doc-1", Quote: "一段原文", Note: "为什么？",
	}).Error; err != nil {
		t.Fatalf("seed anno: %v", err)
	}

	replyRepo := service.NewDocumentAnnotationReplyRepo(db)
	replier := NewAnnotationReplier(&AnnotationReplierConfig{
		AnnoRepo:  service.NewDocumentAnnotationRepo(db),
		DocRepo:   service.NewDocumentRepo(db),
		ReplyRepo: replyRepo,
		Client:    &syncMockLLM{responses: []llm.Message{{Content: "因为如此。"}}},
		Registry:  noopRegistry(),
	})

	out, err := replier.Generate(ctx, "u1", "anno-1", "为什么？")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Added) != 2 || out.Added[0].Role != "user" || out.Added[1].Role != "assistant" {
		t.Fatalf("Added 应为 [user assistant]，得到 %+v", out.Added)
	}
	if out.Added[0].ID >= out.Added[1].ID {
		t.Fatalf("回复 ID 应时间有序（user < assistant），得到 %s / %s", out.Added[0].ID, out.Added[1].ID)
	}

	list, total, err := replyRepo.ListByAnnotationID(ctx, "anno-1", "u1", -1, 0)
	if err != nil || total != 2 {
		t.Fatalf("list: total=%d err=%v", total, err)
	}
	if list[0].Role != "user" || list[1].Role != "assistant" {
		t.Fatalf("读回顺序应为 [user assistant]，得到 [%s %s]", list[0].Role, list[1].Role)
	}

	latest, err := replyRepo.LatestByAnnotationIDs(ctx, []string{"anno-1"}, "u1")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest["anno-1"].Role != "assistant" {
		t.Fatalf("last_reply 应为 assistant（AI 回复），得到 %s", latest["anno-1"].Role)
	}
}
