package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

type taskRow struct {
	EventID  string  `gorm:"column:event_id"`
	Type     string  `gorm:"column:type"`
	Actor    string  `gorm:"column:actor"`
	Trigger  string  `gorm:"column:trigger"`
	Outcome  *string `gorm:"column:outcome"`
	ErrType  *string `gorm:"column:error_type"`
	ParentID *string `gorm:"column:parent_event_id"`
	PairID   *string `gorm:"column:pair_id"`
	ConvID   *string `gorm:"column:conversation_id"`
	TurnID   *string `gorm:"column:turn_id"`
	BizKey   *string `gorm:"column:biz_key"`
	Attrs    *string `gorm:"column:attrs"`
}

func readTask(t *testing.T, s *activity.Store, typ string) []taskRow {
	t.Helper()
	var rows []taskRow
	if err := s.DB().Raw(`SELECT * FROM events WHERE type=? ORDER BY rowid`, typ).Scan(&rows).Error; err != nil {
		t.Fatalf("scan %s: %v", typ, err)
	}
	return rows
}

func setupTaskActivity(t *testing.T) (*activity.Store, context.Context) {
	t.Helper()
	s, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := activity.WithTurn(context.Background(), activity.TurnCtx{TurnID: "t1", StartEventID: "s1", ConvID: "main-1"})
	ctx = activity.WithToolCall(ctx, "tc1")
	return s, ctx
}

// task.spawned/ended：父 = tool.call.started，biz_key = task_id，pair_id 互指。
func TestRunSubAgent_TaskActivity(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	if err := convRepo.Create(context.Background(), &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	s, ctx := setupTaskActivity(t)

	orch := newSubAgentOrchestrator(&syncMockLLM{responses: []llm.Message{{Content: "结论"}}}, convRepo, msgRepo)
	orch.activity = s

	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", TurnID: "t1", ClientTools: testClientToolOpts(),
	})
	if !res.Success {
		t.Fatalf("RunSubAgent: %+v", res)
	}
	s.Flush()

	spawn, end := readTask(t, s, activity.TypeTaskSpawned), readTask(t, s, activity.TypeTaskEnded)
	if len(spawn) != 1 || len(end) != 1 {
		t.Fatalf("counts spawn=%d end=%d, want 1/1", len(spawn), len(end))
	}
	sp, en := spawn[0], end[0]
	if sp.BizKey == nil || *sp.BizKey != res.ConvID || en.BizKey == nil || *en.BizKey != res.ConvID {
		t.Errorf("biz_key spawn=%v end=%v, want %s", sp.BizKey, en.BizKey, res.ConvID)
	}
	if sp.ParentID == nil || *sp.ParentID != "tc1" {
		t.Errorf("spawn parent=%v, want tc1（tool.call.started）", sp.ParentID)
	}
	if sp.TurnID == nil || *sp.TurnID != "t1" || sp.ConvID == nil || *sp.ConvID != "main-1" {
		t.Errorf("spawn turn/conv = %v/%v", sp.TurnID, sp.ConvID)
	}
	if sp.Actor != "assistant" || sp.Trigger != "manual" || sp.Outcome != nil {
		t.Errorf("spawn row = %+v", sp)
	}
	if sp.PairID == nil || en.PairID == nil || *sp.PairID != en.EventID || *en.PairID != sp.EventID {
		t.Errorf("pair_id 应互指: spawn=%v end=%v", sp.PairID, en.PairID)
	}
	if en.Outcome == nil || *en.Outcome != activity.OutcomeCompleted {
		t.Errorf("ended outcome = %v, want completed", en.Outcome)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(*sp.Attrs), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["task_id"] != res.ConvID || attrs["description_len"].(float64) != float64(len("调研")) {
		t.Errorf("spawn attrs = %v", attrs)
	}
}

// 超时终态：outcome=failed、error_type=timeout；spawn/end 仍成对。
func TestRunSubAgent_TaskActivityTimeout(t *testing.T) {
	convRepo, msgRepo, _, cleanup := setupSubAgentRepos(t)
	defer cleanup()
	if err := convRepo.Create(context.Background(), &model.Conversation{ID: "main-1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	s, ctx := setupTaskActivity(t)

	orch := newSubAgentOrchestrator(&blockingLLM{}, convRepo, msgRepo)
	orch.activity = s
	orch.subAgentTimeout = 80 * time.Millisecond

	res := orch.RunSubAgent(ctx, &tool.SubAgentRunOptions{
		SubAgentType: "web_explore", Description: "调研", Prompt: "p", ParentConvID: "main-1", TurnID: "t1", ClientTools: testClientToolOpts(),
	})
	if res.Success {
		t.Fatalf("应超时失败: %+v", res)
	}
	s.Flush()

	spawn, end := readTask(t, s, activity.TypeTaskSpawned), readTask(t, s, activity.TypeTaskEnded)
	if len(spawn) != 1 || len(end) != 1 {
		t.Fatalf("counts spawn=%d end=%d, want 1/1", len(spawn), len(end))
	}
	if end[0].Outcome == nil || *end[0].Outcome != activity.OutcomeFailed {
		t.Errorf("ended outcome = %v, want failed", end[0].Outcome)
	}
	if end[0].ErrType == nil || *end[0].ErrType != "timeout" {
		t.Errorf("ended error_type = %v, want timeout", end[0].ErrType)
	}
}
