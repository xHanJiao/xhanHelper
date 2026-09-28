package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"xhan-helper/internal/activity"
)

func newActionLogTool(t *testing.T) (*UserActionLogTool, *activity.Store) {
	t.Helper()
	s, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	return NewUserActionLogTool(s), s
}

// TestUserActionLogReadOnly 所有函数都声明只读（plan 模式只读拦截依赖）。
func TestUserActionLogReadOnly(t *testing.T) {
	tl := NewUserActionLogTool(nil)
	ro := tl.ReadOnlyFuncs()
	for _, f := range tl.Functions() {
		if !ro[f.Name] {
			t.Errorf("函数 %s 未声明只读", f.Name)
		}
	}
}

// TestUserActionLogDaySkeleton 正常查询路径：有数据 → 返回 JSON。
func TestUserActionLogDaySkeleton(t *testing.T) {
	tl, s := newActionLogTool(t)
	defer s.Close()

	res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "day_skeleton", json.RawMessage(`{"date":"2026-09-25"}`))
	if !res.Success {
		t.Fatalf("期望成功，得到 %s", res.Error)
	}
	var sk activity.DaySkeleton
	if err := json.Unmarshal(res.Data, &sk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sk.Day != "2026-09-25" {
		t.Errorf("day=%q, want 2026-09-25", sk.Day)
	}
}

// TestUserActionLogBadArgs 参数错误必须报错，不静默接受。
func TestUserActionLogBadArgs(t *testing.T) {
	tl, s := newActionLogTool(t)
	defer s.Close()

	if res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "day_skeleton", json.RawMessage(`{}`)); res.Success {
		t.Error("空 date 应失败")
	}
	if res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "day_skeleton", json.RawMessage(`{"date":"bad"}`)); res.Success {
		t.Error("非法 date 应失败")
	}
	if res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "list_sessions",
		json.RawMessage(`{"from":"2026-09-25T10:00:00","to":"2026-09-25T09:00:00"}`)); res.Success {
		t.Error("to 早于 from 应失败")
	}
	if res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "unknown_fn", json.RawMessage(`{}`)); res.Success {
		t.Error("未知函数应失败")
	}
}

// TestUserActionLogListSessions 灌一段 L1 在场区间后断言命中数与字段。
func TestUserActionLogListSessions(t *testing.T) {
	tl, s := newActionLogTool(t)
	defer s.Close()

	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	s.Emit(activity.Event{EventID: "l1s", Type: activity.TypeClientAppLaunched, OccurredAt: base, LaunchID: "launch-1"})
	s.Emit(activity.Event{EventID: "l1e", Type: activity.TypeClientAppQuit, OccurredAt: base.Add(10 * time.Minute), LaunchID: "launch-1"})
	s.Flush()
	if err := s.RebuildSessions(); err != nil {
		t.Fatalf("rebuild sessions: %v", err)
	}

	from := base.Add(-time.Hour).Format("2006-01-02T15:04:05")
	to := base.Add(time.Hour).Format("2006-01-02T15:04:05")
	res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "list_sessions",
		json.RawMessage(`{"from":"`+from+`","to":"`+to+`","level":1}`))
	if !res.Success {
		t.Fatalf("期望成功，得到 %s", res.Error)
	}
	var got struct {
		Count    int `json:"count"`
		Sessions []struct {
			Level      int   `json:"level"`
			DurationMs int64 `json:"duration_ms"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Count != 1 || len(got.Sessions) != 1 {
		t.Fatalf("期望命中 1 段 L1 session，得到 count=%d len=%d", got.Count, len(got.Sessions))
	}
	if got.Sessions[0].Level != 1 {
		t.Errorf("level=%d, want 1", got.Sessions[0].Level)
	}
	if got.Sessions[0].DurationMs != (10 * time.Minute).Milliseconds() {
		t.Errorf("duration_ms=%d, want %d", got.Sessions[0].DurationMs, (10 * time.Minute).Milliseconds())
	}
}
