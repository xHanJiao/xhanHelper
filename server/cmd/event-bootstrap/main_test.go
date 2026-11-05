package main

import (
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/model"
)

func TestPickTruncatedEvents(t *testing.T) {
	mk := func(id, summary string, deleted bool) model.Event {
		return model.Event{ID: id, Name: id, Summary: summary, IsDeleted: deleted}
	}
	events := []model.Event{
		mk("short", strings.Repeat("字", 199), false),
		mk("truncated", strings.Repeat("字", legacySummaryRunes), false),
		mk("longer", strings.Repeat("字", 201), false),
		mk("deleted", strings.Repeat("字", legacySummaryRunes), true),
	}

	got := pickTruncatedEvents(events)
	if len(got) != 1 || got[0].ID != "truncated" {
		t.Fatalf("expected only the %d-rune active event, got %+v", legacySummaryRunes, got)
	}
}

func TestCleanResummarizedSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "  科三补考 11-14 待定  ", "科三补考 11-14 待定"},
		{"fenced", "```\n打包中，机票已买。\n```", "打包中，机票已买。"},
		{"quoted", `“已经拿到 offer，10 月入职。”`, "已经拿到 offer，10 月入职。"},
		{"newlines", "第一行。\n第二行。", "第一行。 第二行。"},
		{"empty_fence", "```", ""},
	}
	for _, c := range cases {
		if got := cleanResummarizedSummary(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBuildResummarizeInput(t *testing.T) {
	e := &model.Event{ID: "e1", Name: "回西安准备", Summary: strings.Repeat("旧", legacySummaryRunes)}
	older := model.UserMemory{Topic: "机票", MemoryValue: "已买 10/25 的票", CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	newer := model.UserMemory{Topic: "打包", MemoryValue: "箱子收拾了一半", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}

	out := buildResummarizeInput(e, []model.UserMemory{newer, older})
	if !strings.Contains(out, "回西安准备") || !strings.Contains(out, e.Summary) {
		t.Fatalf("input should carry name and old summary, got:\n%s", out)
	}
	if strings.Index(out, "箱子收拾了一半") > strings.Index(out, "已买 10/25 的票") {
		t.Fatalf("newer memory should be listed first, got:\n%s", out)
	}
}

func TestBuildResummarizeInputTruncatesByBudget(t *testing.T) {
	e := &model.Event{ID: "e1", Name: "大事件", Summary: "旧摘要"}
	huge := model.UserMemory{Topic: "长", MemoryValue: strings.Repeat("字", resummarizeMemoryBudget+100), CreatedAt: time.Now()}
	other := model.UserMemory{Topic: "短", MemoryValue: "这条不该出现", CreatedAt: time.Now()}

	out := buildResummarizeInput(e, []model.UserMemory{huge, other})
	if strings.Contains(out, "这条不该出现") {
		t.Fatalf("over-budget memory should be dropped, got:\n%s", out)
	}
	if !strings.Contains(out, "另有 2 条更早的记忆未列出") {
		t.Fatalf("should report omitted memories, got:\n%s", out)
	}
}
