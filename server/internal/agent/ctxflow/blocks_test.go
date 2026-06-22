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
	if !strings.Contains(got, `<memory topic="睡眠管理" date="2026年06月15日">`) {
		t.Errorf("missing topic+date: %s", got)
	}
	if !strings.Contains(got, `<memory topic="饮食">戒糖两周了</memory>`) {
		t.Errorf("missing topic-only: %s", got)
	}
}

func TestBuildMemoryBlockNoTopic(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "memory", Meta: retrieval.EntityMeta{Content: "泛内容"}},
	}
	got := marshalBlock(t, buildBlock("memory", items))
	if !strings.Contains(got, `<memory>泛内容</memory>`) {
		t.Errorf("unexpected: %s", got)
	}
}

func TestBuildMemoryBlockEmpty(t *testing.T) {
	got := marshalBlock(t, buildBlock("memory", nil))
	if !strings.Contains(got, "<memories>") {
		t.Errorf("should still produce root tag: %s", got)
	}
}

func TestBuildNoteBlock(t *testing.T) {
	items := []retrieval.ContextPacket{
		{Type: "note", Meta: retrieval.EntityMeta{Content: "项目方案", Topic: "工作", CreatedAt: makeTime(2026, 6, 1)}},
	}
	got := marshalBlock(t, buildBlock("note", items))
	if !strings.Contains(got, `<note topic="工作" date="2026年06月01日">项目方案</note>`) {
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
	if !strings.Contains(got, `<round date="2026年06月10日">`) {
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
		{Type: "theme", Meta: retrieval.ThemeMeta{Description: "健身计划", StateText: "活跃中"}},
	}
	got := marshalBlock(t, buildBlock("theme", items))
	if !strings.Contains(got, `<topic desc="活跃中">健身计划</topic>`) {
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
		{Type: "theme", Meta: retrieval.ThemeMeta{Description: "阅读计划"}},
	}
	got := marshalBlock(t, buildBlock("theme", items))
	if !strings.Contains(got, `<topic>阅读计划</topic>`) {
		t.Errorf("unexpected: %s", got)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
