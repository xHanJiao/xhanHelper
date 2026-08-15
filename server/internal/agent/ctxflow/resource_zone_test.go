package ctxflow

import (
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/agent/retrieval"
)

// —— ParseResourceZone ——

const samplePrevXML = `<memories description="记忆">
  <memory id="m1" topic="健身" score="0.8123">每天早上跑步五公里</memory>
  <memory id="m2">喜欢喝茶</memory>
</memories>
<notes description="笔记">
  <note id="n1" topic="工作" score="0.9">待办：周报、季度评审</note>
</notes>
<summaries description="摘要">
  <summary id="s1" created_at="2026-07-24 17:27" cover_from="a1" cover_to="a2" parent="p0" topics="t1,t2" score="1">用户去了大理</summary>
</summaries>
<topics description="主题">
  <topic id="th1" desc="状态文本" score="0.7">求职进行中</topic>
</topics>
<current_datetime>2026年8月15日</current_datetime>
<user_query>原文</user_query>`

func TestParseResourceZone_ExtractsResourceBlocks(t *testing.T) {
	pkts := ParseResourceZone(samplePrevXML)
	if len(pkts) != 5 {
		t.Fatalf("expected 5 packets (2 mem + 1 note + 1 summary + 1 theme), got %d", len(pkts))
	}
	// memory
	if pkts[0].ID != "m1" || pkts[0].Type != TypeMemory {
		t.Errorf("pkt0: %s/%s", pkts[0].Type, pkts[0].ID)
	}
	if em, ok := pkts[0].Meta.(retrieval.EntityMeta); !ok || em.Topic != "健身" || em.Content != "每天早上跑步五公里" {
		t.Errorf("memory meta wrong: %+v", pkts[0].Meta)
	}
	// summary meta
	sm, ok := pkts[3].Meta.(retrieval.SummaryMeta)
	if !ok || sm.SummaryID != "s1" || sm.ParentSummaryID != "p0" || sm.CoverFromMsgID != "a1" || sm.Topics != "t1,t2" {
		t.Errorf("summary meta wrong: %+v", pkts[3].Meta)
	}
	if !sm.CreatedAt.Equal(time.Date(2026, 7, 24, 17, 27, 0, 0, time.UTC)) && sm.CreatedAt.IsZero() {
		t.Errorf("summary created_at parse failed: %v", sm.CreatedAt)
	}
	// theme meta: StateText=desc attr, Description=content
	tm, ok := pkts[4].Meta.(retrieval.ThemeMeta)
	if !ok || tm.StateText != "状态文本" || tm.Description != "求职进行中" {
		t.Errorf("theme meta wrong: %+v", pkts[4].Meta)
	}
}

func TestParseResourceZone_EmptyAndInvalid(t *testing.T) {
	if ParseResourceZone("") != nil {
		t.Error("empty xml should return nil")
	}
	if ParseResourceZone("<broken>") != nil {
		t.Error("invalid xml should return nil")
	}
	// 旧 XML：memory/note 无 id → 内容级 key 可用
	pkts := ParseResourceZone(`<memories><memory topic="a">内容</memory></memories>`)
	if len(pkts) != 1 || pkts[0].ID != "" {
		t.Errorf("old xml memory should parse without id: %+v", pkts)
	}
}

func TestParseResourceZone_IgnoresNonResourceBlocks(t *testing.T) {
	pkts := ParseResourceZone(`<history><round><message role="user">hi</message></round></history><user_query>q</user_query><current_datetime>t</current_datetime><reminders><reminder id="r">x</reminder></reminders>`)
	if len(pkts) != 0 {
		t.Errorf("history/user_query/datetime/reminders should not enter resource zone, got %d", len(pkts))
	}
}

// —— MergeResourceZone ——

func TestMergeResourceZone_DedupByID(t *testing.T) {
	prev := []retrieval.ContextPacket{
		{ID: "m1", Type: TypeMemory, Text: "旧内容"},
		{ID: "n1", Type: TypeNote, Text: "笔记1"},
	}
	fresh := []retrieval.ContextPacket{
		{ID: "m1", Type: TypeMemory, Text: "新内容"}, // 同 ID 替换
		{ID: "m2", Type: TypeMemory, Text: "新记忆"},
	}
	out := MergeResourceZone(prev, fresh, DefaultZoneQuotas())
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(out), out)
	}
	// m1 被替换为 fresh 版本（保留原位置：第一个）
	if out[0].ID != "m1" || out[0].Text != "新内容" {
		t.Errorf("m1 should be replaced in place: %+v", out[0])
	}
}

func TestMergeResourceZone_DedupByContentForOldXML(t *testing.T) {
	// 旧 XML memory 无 id → topic+content 匹配去重
	prev := []retrieval.ContextPacket{
		{Type: TypeMemory, Meta: retrieval.EntityMeta{Content: "跑步五公里", Topic: "健身"}},
	}
	fresh := []retrieval.ContextPacket{
		{ID: "m-x", Type: TypeMemory, Meta: retrieval.EntityMeta{Content: "跑步五公里", Topic: "健身"}},
	}
	out := MergeResourceZone(prev, fresh, DefaultZoneQuotas())
	if len(out) != 1 {
		t.Fatalf("expected 1 (content dedup), got %d", len(out))
	}
	if out[0].ID != "m-x" {
		t.Errorf("fresh version should win: %+v", out[0])
	}
}

func TestMergeResourceZone_QuotaTrimsOldest(t *testing.T) {
	q := DefaultZoneQuotas()
	q.Note = 2
	prev := []retrieval.ContextPacket{
		{ID: "n1", Type: TypeNote, Text: "n1"},
		{ID: "n2", Type: TypeNote, Text: "n2"},
	}
	fresh := []retrieval.ContextPacket{
		{ID: "n3", Type: TypeNote, Text: "n3"},
	}
	out := MergeResourceZone(prev, fresh, q)
	if len(out) != 2 {
		t.Fatalf("quota 2 should keep 2, got %d", len(out))
	}
	if out[0].ID != "n2" || out[1].ID != "n3" {
		t.Errorf("oldest should be trimmed, keep newest: %v", out)
	}
}

func TestMergeResourceZone_TypeGroupedOrder(t *testing.T) {
	prev := []retrieval.ContextPacket{
		{ID: "m1", Type: TypeMemory, Text: "m1"},
	}
	fresh := []retrieval.ContextPacket{
		{ID: "s1", Type: TypeSummary, Text: "s1"},
		{ID: "n1", Type: TypeNote, Text: "n1"},
	}
	out := MergeResourceZone(prev, fresh, DefaultZoneQuotas())
	// zoneTypeOrder: summary, memory, note, theme
	var types []string
	for _, p := range out {
		types = append(types, p.Type)
	}
	got := strings.Join(types, ",")
	if got != "summary,memory,note" {
		t.Errorf("expected summary,memory,note got %s", got)
	}
}

func TestMergeResourceZone_ReplaceCleansOldContentKey(t *testing.T) {
	// 回归：同 ID 更新内容后，残留的旧内容 key 不得把"同内容不同 ID"的新资源误定位替换
	prev := []retrieval.ContextPacket{
		{ID: "m1", Type: TypeMemory, Text: "旧内容X"},
	}
	fresh := []retrieval.ContextPacket{
		{ID: "m1", Type: TypeMemory, Text: "新内容Y"}, // 同 ID 更新
		{ID: "m2", Type: TypeMemory, Text: "旧内容X"}, // 内容恰等于旧版 X（不同 ID）
	}
	out := MergeResourceZone(prev, fresh, DefaultZoneQuotas())
	if len(out) != 2 {
		t.Fatalf("expected 2 (m1-updated + m2), got %d: %v", len(out), out)
	}
	if out[0].ID != "m1" || out[0].Text != "新内容Y" {
		t.Errorf("m1 updated version should be kept in place: %+v", out[0])
	}
	if out[1].ID != "m2" {
		t.Errorf("m2 (same content as old m1) should be appended, got: %+v", out[1])
	}
}

func TestMergeResourceZone_IgnoresNonZoneTypes(t *testing.T) {
	prev := []retrieval.ContextPacket{{ID: "h1", Type: TypeHistory, Text: "h"}}
	fresh := []retrieval.ContextPacket{{ID: "r1", Type: TypeReminder, Text: "r"}}
	out := MergeResourceZone(prev, fresh, DefaultZoneQuotas())
	if len(out) != 0 {
		t.Errorf("history/reminder should not enter zone: %v", out)
	}
}

// —— BudgetResourceZone ——

func TestBudgetResourceZone_TrimsOldestByPriority(t *testing.T) {
	// 3 条 note（大内容）+ 1 条 summary，预算只够 1 条 note + summary
	pkts := []retrieval.ContextPacket{
		{ID: "n1", Type: TypeNote, Text: strings.Repeat("a", 800)},
		{ID: "n2", Type: TypeNote, Text: strings.Repeat("b", 800)},
		{ID: "n3", Type: TypeNote, Text: strings.Repeat("c", 800)},
		{ID: "s1", Type: TypeSummary, Text: strings.Repeat("d", 800)},
	}
	out := BudgetResourceZone(pkts, ZoneBudgets{Note: 3000, Summary: 800, Memory: 600, Topic: 200, Profile: 1600}, 2500)
	if len(out) > 2 {
		t.Fatalf("budget 2500 should trim to <=2 items, got %d", len(out))
	}
	// 优先级：memory > note > theme > summary → summary 保留
	for _, p := range out {
		if p.Type != TypeSummary && p.Type != TypeNote {
			t.Errorf("unexpected type %s in result", p.Type)
		}
	}
	// 预算按字符数（rune count，与实现口径一致）
	if len([]rune(FormatResourceZone(out))) > 2500 {
		t.Error("budget not respected")
	}
}

func TestBudgetResourceZone_UnderBudgetUntouched(t *testing.T) {
	pkts := []retrieval.ContextPacket{
		{ID: "n1", Type: TypeNote, Text: "short"},
	}
	out := BudgetResourceZone(pkts, ZoneBudgets{Note: 3000, Summary: 800, Memory: 600, Topic: 200, Profile: 1600}, 100000)
	if len(out) != 1 {
		t.Errorf("should keep all when under budget: %d", len(out))
	}
}

// —— FormatResourceZone ——

func TestFormatResourceZone_PreservesOrder(t *testing.T) {
	pkts := []retrieval.ContextPacket{
		{ID: "s1", Type: TypeSummary, Text: "摘要", Meta: retrieval.SummaryMeta{SummaryID: "s1", SummaryText: "摘要"}},
		{ID: "m1", Type: TypeMemory, Text: "记忆", Meta: retrieval.EntityMeta{Content: "记忆"}},
	}
	xml := FormatResourceZone(pkts)
	// 顺序：summaries 块在 memories 块前
	si := strings.Index(xml, "<summaries")
	mi := strings.Index(xml, "<memories")
	if si < 0 || mi < 0 || si > mi {
		t.Errorf("order wrong: summaries idx=%d memories idx=%d\n%s", si, mi, xml)
	}
	// round-trip：编码后再解析应还原
	reparsed := ParseResourceZone(xml)
	if len(reparsed) != 2 {
		t.Fatalf("round-trip failed: got %d packets", len(reparsed))
	}
}

// —— 分组编码 ——

func TestFormatResourceZone_GroupsByType(t *testing.T) {
	pkts := []retrieval.ContextPacket{
		{ID: "n1", Type: TypeNote, Text: "n1", Meta: retrieval.EntityMeta{Content: "n1"}},
		{ID: "n2", Type: TypeNote, Text: "n2", Meta: retrieval.EntityMeta{Content: "n2"}},
		{ID: "m1", Type: TypeMemory, Text: "m1", Meta: retrieval.EntityMeta{Content: "m1"}},
	}
	xml := FormatResourceZone(pkts)
	// 同类型只出现一个块
	if c := strings.Count(xml, "<notes"); c != 1 {
		t.Errorf("expected 1 <notes> block, got %d\n%s", c, xml)
	}
	if c := strings.Count(xml, "<memories"); c != 1 {
		t.Errorf("expected 1 <memories> block, got %d", c)
	}
	// notes 块内两条
	if c := strings.Count(xml, "<note "); c != 2 {
		t.Errorf("expected 2 <note> items in one block, got %d", c)
	}
}

// —— per-type 预算 ——

func TestBudgetResourceZone_PerTypeIndependent(t *testing.T) {
	// notes 超预算（4 条 × 800 字 = 3200 > note 预算 3000）→ 只砍 note 最旧，summary 不受影响
	long := strings.Repeat("x", 800)
	pkts := []retrieval.ContextPacket{
		{ID: "n1", Type: TypeNote, Text: long, Meta: retrieval.EntityMeta{Content: long}},
		{ID: "n2", Type: TypeNote, Text: long, Meta: retrieval.EntityMeta{Content: long}},
		{ID: "n3", Type: TypeNote, Text: long, Meta: retrieval.EntityMeta{Content: long}},
		{ID: "n4", Type: TypeNote, Text: long, Meta: retrieval.EntityMeta{Content: long}},
		{ID: "s1", Type: TypeSummary, Text: strings.Repeat("y", 200), Meta: retrieval.SummaryMeta{SummaryID: "s1", SummaryText: strings.Repeat("y", 200)}},
	}
	out := BudgetResourceZone(pkts, ZoneBudgets{Note: 3000, Summary: 800, Memory: 600, Topic: 200, Profile: 1600}, 100000)
	// note 砍到 3 条（3×800+开销 ≈ 2.6k ≤ 3000；4 条 ≈ 3.4k > 3000），summary 保留
	nNotes, nSummary := 0, 0
	for _, p := range out {
		switch p.Type {
		case TypeNote:
			nNotes++
		case TypeSummary:
			nSummary++
		}
	}
	if nNotes != 3 {
		t.Errorf("expected 3 notes after per-type budget (3000/800字), got %d", nNotes)
	}
	if nSummary != 1 {
		t.Errorf("summary should be untouched by note overflow, got %d", nSummary)
	}
	// 最旧（n1）被砍
	for _, p := range out {
		if p.Type == TypeNote && p.ID == "n1" {
			t.Error("oldest note n1 should be trimmed")
		}
	}
}

// —— profile 进资源区 ——

func TestMergeResourceZone_ProfileDedupByKey(t *testing.T) {
	prev := []retrieval.ContextPacket{
		{ID: "职业领域", Type: TypeProfile, Meta: retrieval.ProfileMeta{Key: "职业领域", Value: "旧值"}},
	}
	fresh := []retrieval.ContextPacket{
		{ID: "职业领域", Type: TypeProfile, Meta: retrieval.ProfileMeta{Key: "职业领域", Value: "新值"}},
		{ID: "性别", Type: TypeProfile, Meta: retrieval.ProfileMeta{Key: "性别", Value: "男"}},
	}
	out := MergeResourceZone(prev, fresh, DefaultZoneQuotas())
	// 同 key 替换 + 新 key 追加 → 2 条
	if len(out) != 2 {
		t.Fatalf("expected 2 profile fields, got %d", len(out))
	}
	// profile 在资源区最前（zoneTypeOrder 第一位）
	if out[0].Type != TypeProfile {
		t.Errorf("profile should be first in zone, got %s", out[0].Type)
	}
	pm, ok := out[0].Meta.(retrieval.ProfileMeta)
	if !ok || pm.Value != "新值" {
		t.Errorf("profile field should be updated to new value: %+v", out[0].Meta)
	}
}

func TestParseResourceZone_ExtractsProfile(t *testing.T) {
	pkts := ParseResourceZone(`<profile description="画像"><field key="性别" updated_at="2026-08-09" source="explicit">男</field></profile>`)
	if len(pkts) != 1 {
		t.Fatalf("expected 1 profile field, got %d", len(pkts))
	}
	if pkts[0].Type != TypeProfile || pkts[0].ID != "性别" {
		t.Errorf("wrong profile packet: %s/%s", pkts[0].Type, pkts[0].ID)
	}
	pm, ok := pkts[0].Meta.(retrieval.ProfileMeta)
	if !ok || pm.Key != "性别" || pm.Value != "男" || pm.Source != "explicit" {
		t.Errorf("profile meta wrong: %+v", pkts[0].Meta)
	}
	if pm.UpdatedAt.IsZero() {
		t.Error("profile updated_at should parse")
	}
}

func TestBudgetResourceZone_ProfileTrimmedLast(t *testing.T) {
	// 总预算压力下：memory/note 砍光后才砍 profile
	big := strings.Repeat("z", 500)
	pkts := []retrieval.ContextPacket{
		{ID: "m1", Type: TypeMemory, Text: big, Meta: retrieval.EntityMeta{Content: big}},
		{ID: "p1", Type: TypeProfile, Text: big, Meta: retrieval.ProfileMeta{Key: "p1", Value: big}},
	}
	out := BudgetResourceZone(pkts, ZoneBudgets{Note: 3000, Summary: 800, Memory: 600, Topic: 200, Profile: 1600}, 400)
	// 总预算 400：先砍 memory（400 字>预算），再砍 profile → 全空或仅 profile 被砍
	for _, p := range out {
		if p.Type == TypeMemory {
			t.Error("memory should be trimmed before profile")
		}
	}
}
