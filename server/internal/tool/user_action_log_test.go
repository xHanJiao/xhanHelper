package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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
	return NewUserActionLogTool(s, nil), s
}

// TestUserActionLogReadOnly 只读声明：除 event_detail（会写 read.query 留痕）外都应只读。
func TestUserActionLogReadOnly(t *testing.T) {
	tl := NewUserActionLogTool(nil, nil)
	ro := tl.ReadOnlyFuncs()
	for _, f := range tl.Functions() {
		want := f.Name != "event_detail"
		if ro[f.Name] != want {
			t.Errorf("函数 %s 只读=%v, want %v", f.Name, ro[f.Name], want)
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

// TestUserActionLogRangeArgs 范围级函数的参数校验与 doc 标题注入。
func TestUserActionLogRangeArgs(t *testing.T) {
	tl, s := newActionLogTool(t)
	defer s.Close()

	bad := [][2]string{
		{"time_ranges_by_resource", `{"resource_kind":"url","from":"2026-09-25T10:00:00","to":"2026-09-25T11:00:00"}`},
		{"time_ranges_by_resource", `{"resource_kind":"url","resource_id":"x","from":"bad","to":"2026-09-25T11:00:00"}`},
		{"facts_by_memory", `{}`},
		{"list_events", `{"from":"2026-09-25T10:00:00","to":"2026-09-25T11:00:00","resource_kind":"url"}`},
	}
	for _, b := range bad {
		if res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, b[0], json.RawMessage(b[1])); res.Success {
			t.Errorf("%s %s 应失败", b[0], b[1])
		}
	}

	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	s.Emit(activity.Event{EventID: "d1", Type: activity.TypeDocCreated, OccurredAt: base,
		Attrs: map[string]any{"doc_id": "doc-1"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}

	titlesCalled := false
	tlWithTitles := NewUserActionLogTool(s, func(_ context.Context, _ string, ids []string) (map[string]string, error) {
		titlesCalled = true
		return map[string]string{"doc-1": "设计文档"}, nil
	})
	from := base.Add(-time.Hour).Format("2006-01-02T15:04:05")
	to := base.Add(time.Hour).Format("2006-01-02T15:04:05")
	res := tlWithTitles.Execute(&ExecuteContext{Ctx: context.Background()}, "list_events",
		json.RawMessage(`{"from":"`+from+`","to":"`+to+`"}`))
	if !res.Success {
		t.Fatalf("list_events 期望成功，得到 %s", res.Error)
	}
	if !titlesCalled {
		t.Error("含 doc 资源时应调用标题解析")
	}
	var got struct {
		Events []struct {
			Resources []struct {
				Kind      string `json:"kind"`
				Canonical string `json:"canonical"`
			} `json:"resources"`
		} `json:"events"`
	}
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, e := range got.Events {
		for _, r := range e.Resources {
			if r.Kind == "doc" && r.Canonical == "设计文档" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("doc 资源应被换成标题，得到 %s", res.Data)
	}
}

// TestUserActionLogEventDetailLogsRead event_detail 取明细并落一条 read.query 留痕。
func TestUserActionLogEventDetailLogsRead(t *testing.T) {
	tl, s := newActionLogTool(t)
	defer s.Close()

	s.Emit(activity.Event{EventID: "b1", Type: activity.TypeBrowserNavStarted, OccurredAt: time.Now(),
		Actor: activity.ActorUser, Attrs: map[string]any{"page_title": "某页面"}})
	s.Flush()

	res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "event_detail", json.RawMessage(`{"event_ids":["b1"]}`))
	if !res.Success {
		t.Fatalf("event_detail 失败: %s", res.Error)
	}
	if !strings.Contains(string(res.Data), "某页面") {
		t.Errorf("明细应含页面标题，得到 %s", res.Data)
	}
	s.Flush()
	// 留痕：应能经 read_log 读回一条 read.query。
	rl := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "read_log",
		json.RawMessage(`{"from":"2000-01-01T00:00:00","to":"2100-01-01T00:00:00"}`))
	if !rl.Success {
		t.Fatalf("read_log 失败: %s", rl.Error)
	}
	if !strings.Contains(string(rl.Data), activity.TypeReadQuery) {
		t.Errorf("read_log 应含 read.query，得到 %s", rl.Data)
	}
}

// TestUserActionLogEventDetailTooMany 超过上限显式拒绝，不静默截断。
func TestUserActionLogEventDetailTooMany(t *testing.T) {
	tl, s := newActionLogTool(t)
	defer s.Close()

	ids := make([]string, activity.MaxDetailIDs+1)
	for i := range ids {
		ids[i] = "e"
	}
	b, err := json.Marshal(map[string]any{"event_ids": ids})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if res := tl.Execute(&ExecuteContext{Ctx: context.Background()}, "event_detail", b); res.Success {
		t.Error("超过 MaxDetailIDs 应失败")
	}
}

// TestDaySkeletonViewCapsSize 骨架必须压到可进上下文的体积：资源留前 30、区间只留 L0/L1，
// 并回带原始条数（回归：2026-10-01 整天全量返回 302KB，一次调用就把上下文塞满）。
func TestDaySkeletonViewCapsSize(t *testing.T) {
	sk := &activity.DaySkeleton{Day: "2026-10-01"}
	for i := 0; i < 493; i++ {
		sk.Resources = append(sk.Resources, activity.ResourceSummary{
			Kind: "url", ResourceID: "r", Canonical: strings.Repeat("x", 300), EventCnt: int64(493 - i),
		})
	}
	for i := 0; i < 293; i++ {
		lvl := 0
		switch {
		case i >= 9 && i < 150:
			lvl = 2
		case i >= 150:
			lvl = 3
		}
		sk.Sessions = append(sk.Sessions, activity.SessionSummary{SessionID: "s", Level: lvl})
	}

	out := daySkeletonView(sk)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) > 30*1024 {
		t.Errorf("骨架输出 %d 字节，超过 30KB 预算", len(b))
	}
	if got := out["resources_total"]; got != 493 {
		t.Errorf("resources_total=%v, want 493", got)
	}
	if got := out["sessions_total"]; got != 293 {
		t.Errorf("sessions_total=%v, want 293", got)
	}
	if out["truncated"] != true {
		t.Error("裁剪后 truncated 应为 true")
	}
	if _, ok := out["hint"]; !ok {
		t.Error("truncated=true 时必须给下钻提示")
	}

	// 只留 L0/L1：9 条（前 9 个 i 的 lvl=0），L2/L3 一律不进骨架。
	if n := len(out["sessions"].([]activity.SessionSummary)); n != 9 {
		t.Errorf("骨架 sessions=%d 条，want 9（仅 L0/L1）", n)
	}
	// 资源按 event_cnt 降序截前 30（store 已排序，裁剪不能打乱含义）。
	res := out["resources"].([]activity.ResourceSummary)
	if len(res) != maxDayResources {
		t.Fatalf("resources=%d 条，want %d", len(res), maxDayResources)
	}
	if res[0].EventCnt != 493 {
		t.Errorf("首个资源 event_cnt=%d，want 493（保持降序）", res[0].EventCnt)
	}
}

// TestDaySkeletonViewNoTrim 数据小时不标 truncated、也不带 hint。
func TestDaySkeletonViewNoTrim(t *testing.T) {
	out := daySkeletonView(&activity.DaySkeleton{Day: "2026-11-03"})
	if out["truncated"] != false {
		t.Error("小数据不应标 truncated")
	}
	if _, ok := out["hint"]; ok {
		t.Error("未截断时不该给 hint")
	}
}

// TestNormalizeLimit limit 收敛：缺省/非正走默认值，超过硬上限钳到 500。
func TestNormalizeLimit(t *testing.T) {
	n := func(v int) *int { return &v }
	cases := []struct {
		in   *int
		want int
	}{
		{nil, defaultListLimit},
		{n(0), defaultListLimit},
		{n(-5), defaultListLimit},
		{n(50), 50},
		{n(1000), 500},
	}
	for _, c := range cases {
		if got := normalizeLimit(c.in); got != c.want {
			t.Errorf("normalizeLimit(%v)=%d, want %d", c.in, got, c.want)
		}
	}
}

// TestCappedListTruncated 满 limit 时标 truncated 并给提示，未满则否。
func TestCappedListTruncated(t *testing.T) {
	full := cappedList(map[string]any{"events": make([]int, 100)}, 100, true)
	if !full.Success {
		t.Fatalf("cappedList 失败: %s", full.Error)
	}
	var hi map[string]any
	if err := json.Unmarshal(full.Data, &hi); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if hi["truncated"] != true || hi["hint"] == nil {
		t.Errorf("满额应 truncated+hint，got %+v", hi)
	}

	res := cappedList(map[string]any{"events": make([]int, 3)}, 3, false)
	var out map[string]any
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["truncated"] != false || out["hint"] != nil {
		t.Errorf("未满额不该 truncated，got %+v", out)
	}
}
