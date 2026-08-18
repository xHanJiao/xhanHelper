package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func TestStableContext_NilReposEmpty(t *testing.T) {
	// replay/测试场景：无仓库 → 空串，不 panic
	o := &Orchestrator{}
	if xml := o.buildStableContextXML(context.Background(), &RunRequest{UserID: "u1"}); xml != "" {
		t.Errorf("expected empty stable context without repos, got %q", xml)
	}
}

func TestStableContext_ProfileAndSummaryInXML_ByteStable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ConversationSummary{}, &model.UserProfileFact{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	db.Create(&model.ConversationSummary{
		ID: "sum-1", UserID: "u1", ConversationID: "c1",
		SummaryText: "近期摘要：讨论了函数级暴露", CreatedAt: now,
	})
	db.Create(&model.UserProfileFact{
		ID: "f-1", UserID: "u1", Field: "工作", Value: "后端工程师",
		Status: model.FactStatusActive, CreatedAt: now,
	})

	o := &Orchestrator{
		summaryRepo:          service.NewConversationSummaryRepo(db),
		profileFactRepo:      service.NewUserProfileFactRepo(db),
		vectorStore:          NewVectorStore(),
		summaryAttachCount:   10,
		summaryAttachMaxDays: 3,
		resourceZoneBudgets:  ctxflow.DefaultZoneBudgets(),
	}
	req := &RunRequest{UserID: "u1", ConversationID: "c1"}
	xml1 := o.buildStableContextXML(context.Background(), req)
	xml2 := o.buildStableContextXML(context.Background(), req)

	if xml1 == "" {
		t.Fatal("expected stable context with profile+summary")
	}
	for _, want := range []string{"<profile", "<summaries"} {
		if !strings.Contains(xml1, want) {
			t.Errorf("expected %s block in stable context, got:\n%s", want, xml1)
		}
	}
	if xml1 != xml2 {
		t.Error("stable context must be byte-stable across calls (cache-friendly)")
	}
}
