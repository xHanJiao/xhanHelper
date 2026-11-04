package activity

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestDaySkeletonReadsDerived 端到端：灌事件 → Recompute → DaySkeleton 出计数/资源/体检。
func TestDaySkeletonReadsDerived(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	save := sessionDayLoc
	sessionDayLoc = time.UTC
	defer func() { sessionDayLoc = save }()

	// 一条工具区间（配对着闭合）+ 一条带 doc 资源的事件。
	s.Emit(Event{EventID: "s1", Type: TypeToolCallStarted, PairID: "e1", OccurredAt: at(0),
		Attrs: map[string]any{"tool_name": "bookkeeping__create"}})
	s.Emit(Event{EventID: "e1", Type: TypeToolCallEnded, PairID: "s1", OccurredAt: at(2 * time.Minute)})
	s.Emit(Event{EventID: "d1", Type: "doc.created", OccurredAt: at(3 * time.Minute),
		Attrs: map[string]any{"doc_id": "doc-123"}})
	s.Flush()
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	day := at(0).In(time.UTC).Format("2006-01-02")
	sk, err := s.DaySkeleton(day)
	if err != nil {
		t.Fatalf("DaySkeleton: %v", err)
	}
	if len(sk.Hours) == 0 {
		t.Fatal("Hours 为空，期望有 tool/doc 计数")
	}
	var toolCnt int64
	for _, h := range sk.Hours {
		if h.Source == "tool" {
			toolCnt += h.Cnt
		}
	}
	if toolCnt == 0 {
		t.Errorf("tool 小时计数为 0，Hours=%+v", sk.Hours)
	}
	foundDoc := false
	for _, r := range sk.Resources {
		if r.Kind == ResourceKindDoc && r.ResourceID == "doc-123" {
			foundDoc = true
		}
	}
	if !foundDoc {
		t.Errorf("资源清单缺 doc-123，Resources=%+v", sk.Resources)
	}
	if sk.Health == nil {
		t.Error("Health 为 nil，Recompute 后应有体检行")
	}
}

// TestDaySkeletonNoData 无数据的日期：空集合、不报错、Health 为 nil。
func TestDaySkeletonNoData(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	sk, err := s.DaySkeleton("2026-01-01")
	if err != nil {
		t.Fatalf("DaySkeleton: %v", err)
	}
	if len(sk.Hours) != 0 || len(sk.Sessions) != 0 || len(sk.Resources) != 0 {
		t.Errorf("期望空集合，得到 %+v", sk)
	}
	if sk.Health != nil {
		t.Errorf("无体检行时 Health 应为 nil，得到 %+v", sk.Health)
	}
}

// TestDaySkeletonRejectsBadDate 非法日期必须报错（不静默用零值继续）。
func TestDaySkeletonRejectsBadDate(t *testing.T) {
	s := openTest(t)
	defer s.Close()
	if _, err := s.DaySkeleton("2026/01/01"); err == nil {
		t.Error("非法日期应报错")
	}
}

// TestListSessionsFiltersLevel 按层级过滤，且只取范围内的 session。
func TestListSessionsFiltersLevel(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	save := sessionDayLoc
	sessionDayLoc = time.UTC
	defer func() { sessionDayLoc = save }()

	// L1（应用）在场：launch_id 锚。
	s.Emit(Event{EventID: "l1s", Type: TypeClientAppLaunched, OccurredAt: at(0),
		LaunchID: "launch-1"})
	s.Emit(Event{EventID: "l1e", Type: TypeClientAppQuit, OccurredAt: at(10 * time.Minute),
		LaunchID: "launch-1"})
	s.Flush()
	if err := s.RebuildSessions(); err != nil {
		t.Fatalf("rebuild sessions: %v", err)
	}

	from, to := at(-time.Hour), at(2*time.Hour)
	all, err := s.ListSessions(from, to, -1, 0)
	if err != nil {
		t.Fatalf("ListSessions all: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("期望至少一段 L1 session")
	}
	if all[0].DurationMs <= 0 {
		t.Errorf("已闭合 session 时长应 > 0，得到 %d", all[0].DurationMs)
	}

	l1, err := s.ListSessions(from, to, 1, 0)
	if err != nil {
		t.Fatalf("ListSessions level=1: %v", err)
	}
	for _, ss := range l1 {
		if ss.Level != 1 {
			t.Errorf("level=1 过滤失效，得到 L%d", ss.Level)
		}
	}

	l0, err := s.ListSessions(from, to, 0, 0)
	if err != nil {
		t.Fatalf("ListSessions level=0: %v", err)
	}
	if len(l0) != 0 {
		t.Errorf("level=0 应为空（无电源事件），得到 %d 段", len(l0))
	}
}

// TestListEventsFiltersAndResources 端到端：过滤维度生效、资源与 tool_name 带出。
func TestListEventsFiltersAndResources(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "b1", Type: TypeBrowserNavStarted, OccurredAt: at(0),
		Attrs: map[string]any{"url_raw": "https://example.com/a?utm_source=x", "tool_name": "webfetch"}})
	s.Emit(Event{EventID: "t1", Type: TypeToolCallStarted, PairID: "t1e", OccurredAt: at(time.Minute),
		Attrs: map[string]any{"tool_name": "bookkeeping__create"}})
	s.Emit(Event{EventID: "t1e", Type: TypeToolCallEnded, PairID: "t1", OccurredAt: at(2 * time.Minute)})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}

	from, to := at(-time.Hour), at(time.Hour)

	all, err := s.ListEvents(from, to, EventFilter{}, 0)
	if err != nil {
		t.Fatalf("ListEvents all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("期望 3 条事件，得到 %d", len(all))
	}
	// 浏览器事件应带 url 资源（canonical 已剥 utm）。
	var b EventSummary
	for _, e := range all {
		if e.EventID == "b1" {
			b = e
		}
	}
	if b.ToolName != "webfetch" {
		t.Errorf("tool_name=%q, want webfetch", b.ToolName)
	}
	if len(b.Resources) != 1 || b.Resources[0].Kind != ResourceKindURL {
		t.Fatalf("b1 资源=%+v, want 一条 url", b.Resources)
	}
	if b.Resources[0].Canonical != "https://example.com/a" {
		t.Errorf("canonical=%q, want 去 utm 后的 https://example.com/a", b.Resources[0].Canonical)
	}

	// source 过滤：browser 只有 1 条。
	bs, err := s.ListEvents(from, to, EventFilter{Source: "browser"}, 0)
	if err != nil {
		t.Fatalf("ListEvents source=browser: %v", err)
	}
	if len(bs) != 1 || bs[0].EventID != "b1" {
		t.Errorf("source=browser 期望仅 b1，得到 %+v", bs)
	}

	// type 过滤。
	ts, err := s.ListEvents(from, to, EventFilter{Type: TypeToolCallEnded}, 0)
	if err != nil {
		t.Fatalf("ListEvents type: %v", err)
	}
	if len(ts) != 1 || ts[0].EventID != "t1e" {
		t.Errorf("type=tool.call.ended 期望仅 t1e，得到 %+v", ts)
	}

	// 资源过滤：url 资源命中 1 条。
	rs, err := s.ListEvents(from, to, EventFilter{ResourceKind: ResourceKindURL, ResourceID: "https://example.com/a"}, 0)
	if err != nil {
		t.Fatalf("ListEvents resource: %v", err)
	}
	if len(rs) != 1 || rs[0].EventID != "b1" {
		t.Errorf("按 url 资源过滤期望仅 b1，得到 %+v", rs)
	}
}

// TestListEventsEmpty 空库返回空集合、不报错。
func TestListEventsEmpty(t *testing.T) {
	s := openTest(t)
	defer s.Close()
	events, err := s.ListEvents(at(-time.Hour), at(time.Hour), EventFilter{}, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("空库应为空，得到 %d", len(events))
	}
}

// TestTimeRangesByResourceMerges 相邻事件并段、超间隔断开。
func TestTimeRangesByResourceMerges(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	// 同一 url：0、2、4 分钟（≤5min 并一段），20 分钟后另起一段。
	url := "https://example.com/a"
	for i, off := range []time.Duration{0, 2 * time.Minute, 4 * time.Minute, 20 * time.Minute} {
		s.Emit(Event{EventID: "u" + strconv.Itoa(i), Type: TypeBrowserNavStarted, OccurredAt: at(off),
			Attrs: map[string]any{"url_raw": url}})
	}
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}

	ranges, readCapped, err := s.TimeRangesByResource(ResourceKindURL, url, at(-time.Hour), at(time.Hour), 0)
	if err != nil {
		t.Fatalf("TimeRangesByResource: %v", err)
	}
	if readCapped {
		t.Error("数据量远小于读取上限，不该标 readCapped")
	}
	if len(ranges) != 2 {
		t.Fatalf("期望并成 2 段，得到 %d: %+v", len(ranges), ranges)
	}
	if ranges[0].EventCnt != 3 {
		t.Errorf("首段事件数=%d, want 3", ranges[0].EventCnt)
	}
	if ranges[0].DurationMs != (4 * time.Minute).Milliseconds() {
		t.Errorf("首段时长=%d, want 4min", ranges[0].DurationMs)
	}
	if ranges[1].EventCnt != 1 {
		t.Errorf("次段事件数=%d, want 1", ranges[1].EventCnt)
	}
}

// TestFactsByMemoryEmptyRefs event_refs 未填充时返回空集合、不报错。
func TestFactsByMemoryEmptyRefs(t *testing.T) {
	s := openTest(t)
	defer s.Close()
	facts, err := s.FactsByMemory("mem-1", 0)
	if err != nil {
		t.Fatalf("FactsByMemory: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("空 refs 应为空，得到 %d", len(facts))
	}
}

// TestTimeRangesLimitKeepsDuration 段数超过 limit 时提前返回，每段时长仍须算好（回归 P1）。
func TestTimeRangesLimitKeepsDuration(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	// 造 6 段：每段 2 条事件（相隔 1 分钟），段间隔 20 分钟（> 5min 断开）。
	url := "https://example.com/hot"
	evID := 0
	for seg := 0; seg < 6; seg++ {
		for k := 0; k < 2; k++ {
			off := time.Duration(seg*20) * time.Minute
			if k == 1 {
				off += time.Minute
			}
			s.Emit(Event{EventID: "h" + strconv.Itoa(evID), Type: TypeBrowserNavStarted,
				OccurredAt: at(off), Attrs: map[string]any{"url_raw": url}})
			evID++
		}
	}
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}

	ranges, _, err := s.TimeRangesByResource(ResourceKindURL, url, at(-time.Hour), at(3*time.Hour), 3)
	if err != nil {
		t.Fatalf("TimeRangesByResource: %v", err)
	}
	if len(ranges) != 3 {
		t.Fatalf("limit=3 应返回 3 段，得到 %d", len(ranges))
	}
	for i, r := range ranges {
		if r.DurationMs != time.Minute.Milliseconds() {
			t.Errorf("第 %d 段 duration_ms=%d, want 1min（提前 return 漏算）", i, r.DurationMs)
		}
	}
}

// TestListEventsRejectsBadSource source 含 GLOB 元字符必须报错（不放宽匹配）。
func TestListEventsRejectsBadSource(t *testing.T) {
	s := openTest(t)
	defer s.Close()
	for _, bad := range []string{"*", "ch*t", "a[b", "Chat", "chat.x"} {
		if _, err := s.ListEvents(at(-time.Hour), at(time.Hour), EventFilter{Source: bad}, 0); err == nil {
			t.Errorf("source=%q 应报错", bad)
		}
	}
	if _, err := s.ListEvents(at(-time.Hour), at(time.Hour), EventFilter{Source: "chat"}, 0); err != nil {
		t.Errorf("合法 source 不应报错: %v", err)
	}
}

// TestFactsByMemoryDedupesMultiSource 同一事件多条引用只出一次。
func TestFactsByMemoryDedupesMultiSource(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "f1", Type: TypeMemoryEntityChanged, OccurredAt: at(0),
		Attrs: map[string]any{"memory_id": "mem-1"}})
	s.Flush()
	// event_refs 主键含 source：同 (event, memory) 造两条不同 source 的引用。
	for _, src := range []string{"explicit", "inferred"} {
		if err := s.db.Exec(`INSERT INTO event_refs(event_id, ref_type, ref_id, source) VALUES (?,?,?,?)`,
			"f1", "memory", "mem-1", src).Error; err != nil {
			t.Fatalf("insert ref: %v", err)
		}
	}

	facts, err := s.FactsByMemory("mem-1", 0)
	if err != nil {
		t.Fatalf("FactsByMemory: %v", err)
	}
	if len(facts) != 1 {
		t.Errorf("同事件多 source 引用应去重为 1 条，得到 %d", len(facts))
	}
}

// TestEventDetailAndSanitize 明细级：取文本摘要、页标题脱敏、超上限截断。
func TestEventDetailAndSanitize(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "b1", Type: TypeBrowserNavStarted, OccurredAt: at(0), Actor: ActorUser,
		Attrs: map[string]any{"url": "https://x.com/a", "page_title": "标题\t带控制\x01符"}})
	s.Emit(Event{EventID: "t1", Type: TypeToolCallStarted, OccurredAt: at(time.Minute), Actor: ActorAssistant,
		Attrs: map[string]any{"tool_name": "note__create", "args_digest": "deadbeef", "args_len": 42}})
	s.Flush()

	items, err := s.EventDetail([]string{"b1", "t1"})
	if err != nil {
		t.Fatalf("EventDetail: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len=%d, want 2", len(items))
	}
	// 按 occurred_at 升序：b1 → t1
	if items[0].PageTitle != "标题 带控制符" {
		t.Errorf("page_title=%q, want 脱敏后的 %q", items[0].PageTitle, "标题 带控制符")
	}
	if items[1].ToolName != "note__create" || items[1].ArgsDigest != "deadbeef" || items[1].ArgsLen != 42 {
		t.Errorf("tool 明细 = %+v", items[1])
	}

	// 空 ids 返回空、不报错。
	if got, err := s.EventDetail(nil); err != nil || len(got) != 0 {
		t.Errorf("空 ids: got=%v err=%v", got, err)
	}
}

// TestSanitizeDetailTextTruncates 超长按字节上限截断（rune 边界安全，复用 truncateRunes）。
func TestSanitizeDetailTextTruncates(t *testing.T) {
	long := strings.Repeat("a", maxDetailTextLen+50)
	got := SanitizeDetailText(long)
	if len(got) != maxDetailTextLen {
		t.Errorf("len=%d, want %d", len(got), maxDetailTextLen)
	}
	// 多字节不应被从中间切断（结果必须仍是合法 UTF-8）。
	multi := SanitizeDetailText(strings.Repeat("啊", maxDetailTextLen))
	if !utf8.ValidString(multi) {
		t.Error("截断后不是合法 UTF-8")
	}
}

// TestReadQueryExcludedFromDayStats read.query 是元事件，不进每日分布（默认不参与统计）。
func TestReadQueryExcludedFromDayStats(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	save := sessionDayLoc
	sessionDayLoc = time.UTC
	defer func() { sessionDayLoc = save }()

	s.Emit(Event{EventID: "u1", Type: TypeClientAppLaunched, OccurredAt: at(0), Actor: ActorUser})
	s.Emit(Event{EventID: "r1", Type: TypeReadQuery, OccurredAt: at(time.Minute), Actor: ActorAssistant})
	s.Flush()
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	day := at(0).In(time.UTC).Format("2006-01-02")
	sk, err := s.DaySkeleton(day)
	if err != nil {
		t.Fatalf("DaySkeleton: %v", err)
	}
	for _, h := range sk.Hours {
		if h.Source == "read" {
			t.Errorf("read 来源不应进 day_stats，得到 %+v", h)
		}
	}
}
