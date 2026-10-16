package agent

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func TestVerifyVerbatimFacts(t *testing.T) {
	userMsgs := []string{
		"我本来的节奏是11.25离开北京，现在还没怎么做准备",
		"然后如果回的话，极限压缩一下，我这边帮忙打包好东西",
	}
	cases := []struct {
		name    string
		facts   []string
		kept    []string
		dropped int
	}{
		{
			name:  "完整原话命中",
			facts: []string{"我本来的节奏是11.25离开北京，现在还没怎么做准备"},
			kept:  []string{"我本来的节奏是11.25离开北京，现在还没怎么做准备"},
		},
		{
			name:  "半句截断也算命中",
			facts: []string{"我本来的节奏是11.25离开北京"},
			kept:  []string{"我本来的节奏是11.25离开北京"},
		},
		{
			name:    "改写（换词）丢掉",
			facts:   []string{"用户计划 11 月 25 日离开北京"},
			dropped: 1,
		},
		{
			name:    "助手的话不算来源",
			facts:   []string{"建议先做工资测算再决定"},
			dropped: 1,
		},
		{
			name:    "跨两条消息拼接必须丢掉",
			facts:   []string{"现在还没怎么做准备然后如果回的话极限压缩一下"},
			dropped: 1,
		},
		{
			name:    "空片段丢掉",
			facts:   []string{"", "   "},
			dropped: 2,
		},
		{
			name:  "换行/空格差异不算改写",
			facts: []string{"我本来的节奏是\n11.25离开北京，   现在还没怎么做准备"},
			kept:  []string{"我本来的节奏是\n11.25离开北京，   现在还没怎么做准备"},
		},
		{
			name:    "命中与未命中混合，保序保留命中的",
			facts:   []string{"这是编的", "我这边帮忙打包好东西", "换个说法"},
			kept:    []string{"我这边帮忙打包好东西"},
			dropped: 2,
		},
		{
			name:  "命中片段两端空白被 trim",
			facts: []string{"  我这边帮忙打包好东西  "},
			kept:  []string{"我这边帮忙打包好东西"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := verifyVerbatimFacts(tc.facts, userMsgs)
			if dropped != tc.dropped {
				t.Errorf("dropped: want %d, got %d", tc.dropped, dropped)
			}
			if len(kept) != len(tc.kept) {
				t.Fatalf("kept: want %v, got %v", tc.kept, kept)
			}
			for i := range tc.kept {
				if kept[i] != tc.kept[i] {
					t.Errorf("kept[%d]: want %q, got %q", i, tc.kept[i], kept[i])
				}
			}
		})
	}
}

func TestVerifyVerbatimFacts_NoFacts(t *testing.T) {
	if kept, dropped := verifyVerbatimFacts(nil, []string{"任意"}); kept != nil || dropped != 0 {
		t.Errorf("no facts should pass through untouched, got kept=%v dropped=%d", kept, dropped)
	}
}

// 没有用户消息可作基准（例如全是助手发言）时，任何 fact 都不该留下。
func TestVerifyVerbatimFacts_NoSourcesDropsAll(t *testing.T) {
	if kept, dropped := verifyVerbatimFacts([]string{"随便一句"}, nil); len(kept) != 0 || dropped != 1 {
		t.Errorf("want all dropped, got kept=%v dropped=%d", kept, dropped)
	}
}

// 端到端：抽取段返回的 facts 里，非用户原话的片段被丢掉，原话留下。
func TestExtractCandidates_DropsNonVerbatimFacts(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"extracted":true,"memories":[
		{"summary":"用户计划提前离京","facts":["我本来的节奏是11.25离开北京","用户将于 11 月中旬回西安","建议先考完科目三"],"reason":"影响入职时间"}
	]}`}}}
	e := NewMemoryExtractor(&MemoryExtractorConfig{LLMClient: mock})

	got, err := e.ExtractCandidates(context.Background(), "u1", []model.Message{
		{ID: "m1", Role: "user", Content: "我本来的节奏是11.25离开北京，现在还没怎么做准备"},
		{ID: "m2", Role: "assistant", Content: "建议先考完科目三，再回西安"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %+v", got)
	}
	want := []string{"我本来的节奏是11.25离开北京"}
	if len(got[0].Facts) != len(want) || got[0].Facts[0] != want[0] {
		t.Errorf("only verbatim user quotes may survive: want %v, got %v", want, got[0].Facts)
	}
}

// 写入侧收口：决策循环是第二次独立采样，落库的 facts 也必须只能是候选原话的一段。
func TestFilterWrittenFacts(t *testing.T) {
	s := &recordLoopState{source: "post_chat", candFacts: []string{
		"我本来的节奏是11.25离开北京，现在还没怎么做准备",
	}}
	kept := s.filterWrittenFacts([]string{
		"我本来的节奏是11.25离开北京",                // 截断 → 留
		"用户计划 11 月中旬离京",                   // 改写 → 丢
		"我本来的节奏是\n11.25离开北京，   现在还没怎么做准备", // 只差空白 → 留
		"", // 空 → 丢
	})
	want := []string{"我本来的节奏是11.25离开北京", "我本来的节奏是\n11.25离开北京，   现在还没怎么做准备"}
	if len(kept) != len(want) {
		t.Fatalf("want %d kept, got %v", len(want), kept)
	}
	for i := range want {
		if kept[i] != want[i] {
			t.Errorf("kept[%d]: want %q, got %q", i, want[i], kept[i])
		}
	}

	// 候选没有原话（facts 被入口全部丢弃）→ 循环写什么都进不来。
	empty := &recordLoopState{source: "inline"}
	if got := empty.filterWrittenFacts([]string{"凭 summary 编一句"}); len(got) != 0 {
		t.Errorf("no candidate quotes means nothing may be written, got %v", got)
	}
	// 没传 facts 不报错、不写。
	if got := s.filterWrittenFacts(nil); got != nil {
		t.Errorf("nil facts should stay nil, got %v", got)
	}
}

// verifyInlineFacts 的三个分支：命中/丢弃、convID 空、查库失败。
func TestVerifyInlineFacts(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewMessageRepo(db)
	for _, m := range []model.Message{
		{ID: "m1", ConversationID: "c1", UserID: "u1", Role: "user", Content: "我本来的节奏是11.25离开北京", Status: model.MsgStatusNormal},
		{ID: "m2", ConversationID: "c1", UserID: "u1", Role: "assistant", Content: "建议先考完科目三", Status: model.MsgStatusNormal},
		// 非正常态的消息都不该当原话基准：已删除（软删）与隐藏（接力等控制轮，落库留档但不进对话记录）。
		{ID: "m3", ConversationID: "c1", UserID: "u1", Role: "user", Content: "已删除的原话", Status: model.MsgStatusDeleted},
		{ID: "m4", ConversationID: "c1", UserID: "u1", Role: "user", Content: "隐藏控制轮的原话", Status: model.MsgStatusHidden},
	} {
		if err := repo.Create(ctx, &m); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	d := NewRecordDecisionLoop(&RecordDeps{MsgRepo: repo}, RecordLoopConfig{}, "inline")

	kept, dropped := d.verifyInlineFacts(ctx, "u1", "c1", []string{
		"我本来的节奏是11.25离开北京", "助手说先考科目三", "已删除的原话", "隐藏控制轮的原话",
	})
	if len(kept) != 1 || kept[0] != "我本来的节奏是11.25离开北京" || dropped != 3 {
		t.Errorf("only live user messages count as source, got kept=%v dropped=%d", kept, dropped)
	}

	// convID 为空 → 全丢（不能因为查不了就放行）
	if kept, dropped := d.verifyInlineFacts(ctx, "u1", "", []string{"随便"}); len(kept) != 0 || dropped != 1 {
		t.Errorf("empty convID must drop all, got kept=%v dropped=%d", kept, dropped)
	}

	// 查库失败 → 全丢
	fileDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "x.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open file db: %v", err)
	}
	if err := fileDB.AutoMigrate(&model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	broken := NewRecordDecisionLoop(&RecordDeps{MsgRepo: service.NewMessageRepo(fileDB)}, RecordLoopConfig{}, "inline")
	if sqlDB, err := fileDB.DB(); err == nil {
		sqlDB.Close()
	}
	if kept, dropped := broken.verifyInlineFacts(ctx, "u1", "c1", []string{"随便"}); len(kept) != 0 || dropped != 1 {
		t.Errorf("repo error must drop all facts, got kept=%v dropped=%d", kept, dropped)
	}

	// 没传 facts 直接短路，不查库。
	if kept, dropped := broken.verifyInlineFacts(ctx, "u1", "c1", nil); kept != nil || dropped != 0 {
		t.Errorf("no facts should short-circuit, got kept=%v dropped=%d", kept, dropped)
	}
}
