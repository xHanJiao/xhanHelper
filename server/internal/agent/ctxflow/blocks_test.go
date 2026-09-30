package ctxflow

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/model"
)

func makeTime(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func marshalBlock(t *testing.T, block any) string {
	b, err := xml.MarshalIndent(block, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestBuildMemoryBlock(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "memory", Meta: retrieval.EntityMeta{Content: "昨晚11点睡了", Topic: "睡眠管理", CreatedAt: makeTime(2026, 6, 15)}},
		{Type: "memory", Meta: retrieval.EntityMeta{Content: "戒糖两周了", Topic: "饮食"}},
	}

	got := marshalBlock(t, buildBlock("memory", items))
	if !strings.Contains(got, `memory topic="睡眠管理" date="2026年06月15日"`) {
		t.Errorf("missing topic+date: %s", got)
	}
	if !strings.Contains(got, `memory topic="饮食"`) {
		t.Errorf("missing topic-only: %s", got)
	}
}

func TestBuildMemoryBlockNoTopic(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "memory", Meta: retrieval.EntityMeta{Content: "泛内容"}},
	}
	got := marshalBlock(t, buildBlock("memory", items))
	if !strings.Contains(got, `memory>泛内容</memory>`) {
		t.Errorf("unexpected: %s", got)
	}
}

func TestBuildMemoryBlockEmpty(t *testing.T) {
	got := marshalBlock(t, buildBlock("memory", nil))
	if !strings.Contains(got, "<memories") {
		t.Errorf("should still produce root tag: %s", got)
	}
}

func TestBuildNoteBlock(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "note", Meta: retrieval.EntityMeta{Content: "项目方案", Topic: "工作", CreatedAt: makeTime(2026, 6, 1)}},
	}
	got := marshalBlock(t, buildBlock("note", items))
	if !strings.Contains(got, `note topic="工作" date="2026年06月01日"`) {
		t.Errorf("unexpected: %s", got)
	}
}

func TestBuildHistoryBlock(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "history", Meta: retrieval.HistoryMeta{
			HitDate: ptrTime(makeTime(2026, 6, 10)),
			Messages: []*model.Message{
				{Role: "user", Content: "帮我记账"},
				{Role: "assistant", Content: "好的"},
			},
		}},
	}
	got := marshalBlock(t, buildBlock("history", items))
	if !strings.Contains(got, `round date="2026年06月10日"`) {
		t.Errorf("missing date: %s", got)
	}
	if !strings.Contains(got, `role="user"`) {
		t.Errorf("missing role attr: %s", got)
	}
}

func TestBuildHistoryBlockEmptyMessages(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "history", Meta: retrieval.HistoryMeta{Messages: nil}},
	}
	block := buildBlock("history", items)
	if block != nil {
		t.Errorf("expected nil for empty messages, got %v", block)
	}
}

func TestBuildThemeBlock(t *testing.T) {
	items := []retrieval.ContextPacket{
		{ID: "theme-1", Type: "theme", Meta: retrieval.ThemeMeta{Description: "健身计划", StateText: "活跃中"}},
	}
	got := marshalBlock(t, buildBlock("theme", items))
	if !strings.Contains(got, `topic id="theme-1" desc="活跃中"`) {
		t.Errorf("unexpected: %s", got)
	}
}

func TestBuildHistoryBlockFallbackDate(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "history", Meta: retrieval.HistoryMeta{
			Messages: []*model.Message{
				{Role: "user", Content: "今天天气?", CreatedAt: makeTime(2026, 5, 1)},
			},
		}},
	}
	got := marshalBlock(t, buildBlock("history", items))
	if !strings.Contains(got, `2026年05月01日`) {
		t.Errorf("should fallback to Messages[0].CreatedAt: %s", got)
	}
}

func TestBuildHistoryBlockHitDatePrecedence(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "history", Meta: retrieval.HistoryMeta{
			HitDate: ptrTime(makeTime(2026, 3, 15)),
			Messages: []*model.Message{
				{Role: "user", Content: "x", CreatedAt: makeTime(2026, 1, 1)},
			},
		}},
	}
	got := marshalBlock(t, buildBlock("history", items))
	if strings.Contains(got, "2026年01月01日") {
		t.Errorf("should prefer HitDate: %s", got)
	}
	if !strings.Contains(got, "2026年03月15日") {
		t.Errorf("missing HitDate: %s", got)
	}
}

func TestBuildThemeBlockNoState(t *testing.T) {
	items := []retrieval.ContextPacket{
		{ID: "theme-2", Type: "theme", Meta: retrieval.ThemeMeta{Description: "阅读计划"}},
	}
	got := marshalBlock(t, buildBlock("theme", items))
	if !strings.Contains(got, `topic id="theme-2">阅读计划</topic>`) {
		t.Errorf("unexpected: %s", got)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestMergePackets_Empty(t *testing.T) {
	got := MergePackets(nil)
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

func TestMergePackets_ExtraOnly(t *testing.T) {
	got := MergePackets(nil,
		retrieval.ContextPacket{Type: TypeCurrentDatetime, Text: "now"},
		retrieval.ContextPacket{Type: TypeMemory, Text: "memory text"},
	)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if len(got[TypeCurrentDatetime]) != 1 || got[TypeCurrentDatetime][0].Text != "now" {
		t.Errorf("missing datetime: %v", got)
	}
	if len(got[TypeMemory]) != 1 || got[TypeMemory][0].Text != "memory text" {
		t.Errorf("missing memory: %v", got)
	}
}

func TestMergePackets_RetrieveOnly(t *testing.T) {
	rr := &RetrieveResult{
		Packets: map[string][]retrieval.ContextPacket{
			TypeMemory: {{Type: TypeMemory, Text: "m1"}, {Type: TypeMemory, Text: "m2"}},
			TypeNote:   {{Type: TypeNote, Text: "n1"}},
		},
	}
	got := MergePackets(rr)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if len(got[TypeMemory]) != 2 {
		t.Errorf("expected 2 memory packets, got %d", len(got[TypeMemory]))
	}
}

func TestMergePackets_MergeSameType(t *testing.T) {
	rr := &RetrieveResult{
		Packets: map[string][]retrieval.ContextPacket{
			TypeMemory: {{Type: TypeMemory, Text: "retrieved memory"}},
		},
	}
	got := MergePackets(rr,
		retrieval.ContextPacket{Type: TypeMemory, Text: "extra memory"},
		retrieval.ContextPacket{Type: TypeCurrentDatetime, Text: "now"},
	)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if len(got[TypeMemory]) != 2 {
		t.Errorf("expected 2 memory packets (1 extra + 1 retrieved), got %d", len(got[TypeMemory]))
	}
	if got[TypeMemory][0].Text != "extra memory" {
		t.Errorf("extra should come first, got %q", got[TypeMemory][0].Text)
	}
	if got[TypeMemory][1].Text != "retrieved memory" {
		t.Errorf("retrieved should come second, got %q", got[TypeMemory][1].Text)
	}
	if len(got[TypeCurrentDatetime]) != 1 {
		t.Errorf("expected 1 datetime packet, got %d", len(got[TypeCurrentDatetime]))
	}
}

func TestMergePackets_RetrieveNilPackets(t *testing.T) {
	rr := &RetrieveResult{Packets: nil}
	got := MergePackets(rr,
		retrieval.ContextPacket{Type: TypeCurrentDatetime, Text: "now"},
	)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[TypeCurrentDatetime][0].Text != "now" {
		t.Errorf("missing datetime: %v", got)
	}
}

func TestNoticeBlock_RendersInUserMessage(t *testing.T) {
	f := NewXMLFormatter()
	packets := map[string][]retrieval.ContextPacket{
		TypeNotice: {NoticePacket("[计划模式] 只读讨论")},
	}
	out := f.FormatUserMessage(packets, "帮我调研")
	if !strings.Contains(out, "<notice>") || !strings.Contains(out, "[计划模式] 只读讨论") {
		t.Errorf("notice 块应渲染进 user 消息: %s", out)
	}
	if !strings.Contains(out, "<user_query>") {
		t.Errorf("user_query 应保留: %s", out)
	}
}

func TestBuildProfileBlock(t *testing.T) {
	items := []retrieval.ContextPacket{
		ProfilePacket("补充说明", "睡眠差\n房价观察", makeTime(2026, 8, 7)),
	}
	got := marshalBlock(t, buildBlock(TypeProfile, items))
	if !strings.Contains(got, `<profile`) {
		t.Errorf("should produce profile root: %s", got)
	}
	if !strings.Contains(got, `key="补充说明"`) {
		t.Errorf("missing field key: %s", got)
	}
	if !strings.Contains(got, `source="llm_additional"`) {
		t.Errorf("补充说明 field should derive source=llm_additional: %s", got)
	}
	if !strings.Contains(got, `updated_at="2026-08-07"`) {
		t.Errorf("missing updated_at attr: %s", got)
	}
	if !strings.Contains(got, ">睡眠差&#xA;房价观察</field>") {
		t.Errorf("missing field value: %s", got)
	}
}

func TestProfilePacket_NonAdditionalField_NoSource(t *testing.T) {
	pkt := ProfilePacket("籍贯", "河南焦作", makeTime(2026, 8, 7))
	meta := pkt.Meta.(retrieval.ProfileMeta)
	if meta.Source != "" {
		t.Errorf("non-补充说明 field should have empty source, got %q", meta.Source)
	}
}

func TestBuildProfileBlock_EmptyReturnsNil(t *testing.T) {
	if buildBlock(TypeProfile, nil) != nil {
		t.Error("empty profile should return nil (not injected)")
	}
}

func TestProfileBlock_RendersFirstInUserMessage(t *testing.T) {
	f := NewXMLFormatter()
	packets := map[string][]retrieval.ContextPacket{
		TypeProfile: {
			ProfilePacket("籍贯", "河南焦作", makeTime(2026, 8, 7)),
		},
		TypeCurrentDatetime: {DateTimePacket(makeTime(2026, 8, 8))},
		TypeMemory:          {retrieval.ContextPacket{Type: TypeMemory, Text: "某记忆"}},
	}
	out := f.FormatUserMessage(packets, "你好")
	if !strings.Contains(out, "<profile ") && !strings.Contains(out, "<profile>") {
		t.Errorf("profile block should render: %s", out)
	}
	profileIdx := strings.Index(out, "<profile")
	datetimeIdx := strings.Index(out, "<current_datetime>")
	if profileIdx < 0 || datetimeIdx < 0 || profileIdx > datetimeIdx {
		t.Errorf("profile should render before current_datetime: %s", out)
	}
}

func TestSortPacketsByType_ProfileAlphabetical(t *testing.T) {
	// 乱序输入，期望按字段名字母序输出（确定性，与更新时间无关）
	pkts := []retrieval.ContextPacket{
		ProfilePacket("伴侣", "CJ", makeTime(2026, 8, 7)),
		ProfilePacket("籍贯", "河南焦作", makeTime(2026, 8, 1)),
		ProfilePacket("出生日期", "1995", makeTime(2026, 8, 8)),
	}
	sortPacketsByType(TypeProfile, pkts)
	got := make([]string, 0, len(pkts))
	for _, p := range pkts {
		got = append(got, p.ID)
	}
	want := []string{"伴侣", "出生日期", "籍贯"} // 按 UTF-8 字节序："伴" < "出" < "籍"
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestSortPacketsByType_ScoreDescThenID(t *testing.T) {
	pkts := []retrieval.ContextPacket{
		{ID: "b", Score: 0.5, Text: "low"},
		{ID: "c", Score: 0.9, Text: "high-c"},
		{ID: "a", Score: 0.9, Text: "high-a"},
	}
	sortPacketsByType(TypeMemory, pkts)
	if pkts[0].ID != "a" || pkts[1].ID != "c" || pkts[2].ID != "b" {
		t.Fatalf("expected a,c,b got %s,%s,%s", pkts[0].ID, pkts[1].ID, pkts[2].ID)
	}
}

func TestSortPacketsByType_SummaryKeepsChronological(t *testing.T) {
	pkts := []retrieval.ContextPacket{
		{ID: "s3", Type: TypeSummary, Score: 1.0, Meta: retrieval.SummaryMeta{CreatedAt: makeTime(2026, 8, 8)}},
		{ID: "s1", Type: TypeSummary, Score: 1.0, Meta: retrieval.SummaryMeta{CreatedAt: makeTime(2026, 8, 6)}},
		{ID: "s2", Type: TypeSummary, Score: 1.0, Meta: retrieval.SummaryMeta{CreatedAt: makeTime(2026, 8, 7)}},
	}
	sortPacketsByType(TypeSummary, pkts)
	got := []string{pkts[0].ID, pkts[1].ID, pkts[2].ID}
	want := []string{"s1", "s2", "s3"} // 时间旧→新
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestSortPacketsByType_ReminderKeepsProducerOrder(t *testing.T) {
	pkts := []retrieval.ContextPacket{
		{ID: "upcoming-2", Score: 0, Text: "到期提醒"},
		{ID: "expired-1", Score: 0, Text: "过期提醒"},
	}
	sortPacketsByType(TypeReminder, pkts)
	if pkts[0].ID != "upcoming-2" || pkts[1].ID != "expired-1" {
		t.Fatalf("reminder order should be preserved: %s,%s", pkts[0].ID, pkts[1].ID)
	}
}

func TestFormatUserMessage_ProfileItemsSorted(t *testing.T) {
	f := NewXMLFormatter()
	packets := map[string][]retrieval.ContextPacket{
		TypeProfile: {
			ProfilePacket("籍贯", "河南焦作", makeTime(2026, 8, 1)),
			ProfilePacket("伴侣", "CJ", makeTime(2026, 8, 7)),
		},
	}
	out := f.FormatUserMessage(packets, "你好")
	companionIdx := strings.Index(out, `key="伴侣"`)
	hometownIdx := strings.Index(out, `key="籍贯"`)
	if companionIdx < 0 || hometownIdx < 0 {
		t.Fatalf("expected both fields in output: %s", out)
	}
	if companionIdx > hometownIdx {
		t.Errorf("profile items should be sorted alphabetically (伴侣 before 籍贯): %s", out)
	}
}
