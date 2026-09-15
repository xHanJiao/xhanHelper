package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// newPlanTestTool 内存 SQLite plan repo + PlanTool
func newPlanTestTool(t *testing.T) (*PlanTool, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(tmpDir, "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ConversationPlan{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewConversationPlanRepo(db)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewPlanTool(repo), cleanup
}

func TestPlanTool_SaveAndGet(t *testing.T) {
	tool, cleanup := newPlanTestTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1"}

	// 初始 get：无计划
	res := tool.Execute(ec, "get", json.RawMessage(`{}`))
	if !res.Success {
		t.Fatalf("get empty: %s", res.Error)
	}
	var got struct {
		Goal  string           `json:"goal"`
		Steps []model.PlanStep `json:"steps"`
	}
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Steps) != 0 {
		t.Errorf("初始应无步骤, got %d", len(got.Steps))
	}

	// save 全量
	res = tool.Execute(ec, "save", json.RawMessage(`{"goal":"调研方案","steps":[{"content":"打开预售列表"},{"content":"筛楼盘","status":"in_progress"}]}`))
	if !res.Success {
		t.Fatalf("save: %s", res.Error)
	}

	// get 回读
	res = tool.Execute(ec, "get", json.RawMessage(`{}`))
	got = struct {
		Goal  string           `json:"goal"`
		Steps []model.PlanStep `json:"steps"`
	}{}
	json.Unmarshal(res.Data, &got)
	if got.Goal != "调研方案" || len(got.Steps) != 2 {
		t.Fatalf("get after save: goal=%q steps=%d", got.Goal, len(got.Steps))
	}
	if got.Steps[1].Status != "in_progress" {
		t.Errorf("status 应保留, got %q", got.Steps[1].Status)
	}

	// 全量替换：第二次 save 只传 1 步 → 覆盖为 1 步
	res = tool.Execute(ec, "save", json.RawMessage(`{"steps":[{"content":"只看备案价"}]}`))
	if !res.Success {
		t.Fatalf("save2: %s", res.Error)
	}
	res = tool.Execute(ec, "get", json.RawMessage(`{}`))
	json.Unmarshal(res.Data, &got)
	if len(got.Steps) != 1 || got.Steps[0].Content != "只看备案价" {
		t.Errorf("全量替换应覆盖为 1 步, got %d 步: %+v", len(got.Steps), got.Steps)
	}
	// 默认 status
	if got.Steps[0].Status != "pending" {
		t.Errorf("默认 status 应为 pending, got %q", got.Steps[0].Status)
	}
}

func TestPlanTool_Validation(t *testing.T) {
	tool, cleanup := newPlanTestTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1"}

	if res := tool.Execute(ec, "save", json.RawMessage(`{"steps":[]}`)); res.Success {
		t.Error("空 steps 应报错")
	}
	if res := tool.Execute(ec, "save", json.RawMessage(`{"steps":[{"content":"","status":"pending"}]}`)); res.Success {
		t.Error("空 content 应报错")
	}
	if res := tool.Execute(ec, "save", json.RawMessage(`{"steps":[{"content":"x","status":"bogus"}]}`)); res.Success {
		t.Error("非法 status 应报错")
	}
	if res := tool.Execute(ec, "save", json.RawMessage(`{"steps":[{"content":"x"}]}`)); !res.Success {
		t.Errorf("合法 save 应成功: %s", res.Error)
	}
}

func TestPlanTool_MissingContext(t *testing.T) {
	tool, cleanup := newPlanTestTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background()} // 无 ConvID/UserID
	if res := tool.Execute(ec, "save", json.RawMessage(`{"steps":[{"content":"x"}]}`)); res.Success {
		t.Error("缺会话上下文应报错")
	}
}

func TestPlanTool_ReadOnlyFuncs(t *testing.T) {
	tool, cleanup := newPlanTestTool(t)
	defer cleanup()
	ro := tool.ReadOnlyFuncs()
	if !ro["get"] {
		t.Error("get 应为只读")
	}
	if ro["save"] {
		t.Error("save 不应为只读（plan 模式应拦截）")
	}
}

func TestPlanTool_PlanModeBlocksWrite(t *testing.T) {
	// 2026-09-15 只读化：plan 模式下写函数 save 拦截、只读函数 get 放行
	tool, cleanup := newPlanTestTool(t)
	defer cleanup()
	r := NewRegistry()
	r.Register(tool)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", Mode: model.ConvModePlan}
	res := r.ExecuteByName(ec, "plan", "save", json.RawMessage(`{"steps":[{"content":"x"}]}`))
	if res.Success {
		t.Fatal("plan 模式下 plan.save 应被拦截")
	}
	if !strings.Contains(res.Error, "计划模式") {
		t.Errorf("拦截错误应说明计划模式: %s", res.Error)
	}
	// get 只读：放行
	res = r.ExecuteByName(ec, "plan", "get", json.RawMessage(`{}`))
	if !res.Success {
		t.Fatalf("plan 模式下 plan.get 应放行, got: %s", res.Error)
	}
}
