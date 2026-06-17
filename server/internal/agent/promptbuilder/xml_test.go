package promptbuilder

import (
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/model"
)

func makeTime(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func TestBuildMemoryXML(t *testing.T) {
	items := []retrieval.ContextPacket{
		{
			Type: "memory",
			Meta: retrieval.EntityMeta{
				Content:   "昨晚11点睡了",
				Topic:     "睡眠管理",
				CreatedAt: makeTime(2026, 6, 15),
			},
		},
		{
			Type: "memory",
			Meta: retrieval.EntityMeta{
				Content: "戒糖两周了",
				Topic:   "饮食",
			},
		},
	}

	xml := buildMemoryXML(items)
	if !strings.Contains(xml, `<memory topic="睡眠管理" date="2026年06月15日">`) {
		t.Errorf("missing topic+date: %s", xml)
	}
	if !strings.Contains(xml, `<memory topic="饮食">戒糖两周了</memory>`) {
		t.Errorf("missing topic-only: %s", xml)
	}
	if !strings.Contains(xml, "<memories>") {
		t.Errorf("missing root tag: %s", xml)
	}
}

func TestBuildMemoryXMLNoTopic(t *testing.T) {
	items := []retrieval.ContextPacket{
		{
			Type: "memory",
			Meta: retrieval.EntityMeta{
				Content:   "纯文本记忆",
				CreatedAt: makeTime(2026, 1, 5),
			},
		},
	}

	xml := buildMemoryXML(items)
	if strings.Contains(xml, `topic=""`) {
		t.Errorf("empty topic should be omitted: %s", xml)
	}
	if !strings.Contains(xml, `date="2026年01月05日"`) {
		t.Errorf("missing date: %s", xml)
	}
	if !strings.Contains(xml, "纯文本记忆") {
		t.Errorf("missing content: %s", xml)
	}
}

func TestBuildMemoryXMLEmpty(t *testing.T) {
	xml := buildMemoryXML(nil)
	if !strings.Contains(xml, "<memories>") {
		t.Errorf("empty should still produce root tag: %s", xml)
	}
}

func TestBuildNoteXML(t *testing.T) {
	items := []retrieval.ContextPacket{
		{
			Type: "note",
			Meta: retrieval.EntityMeta{
				Content:   "五月总支出3200",
				Topic:     "财务",
				CreatedAt: makeTime(2026, 5, 20),
			},
		},
	}

	xml := buildNoteXML(items)
	if !strings.Contains(xml, `<note topic="财务" date="2026年05月20日">`) {
		t.Errorf("unexpected output: %s", xml)
	}
	if !strings.Contains(xml, "<notes>") {
		t.Errorf("missing root tag: %s", xml)
	}
}

func TestBuildHistoryXML(t *testing.T) {
	msg1 := model.Message{Role: "user", Content: "帮我记账"}
	msg1.CreatedAt = makeTime(2026, 5, 28)
	msg2 := model.Message{Role: "assistant", Content: "已记录"}
	msg2.CreatedAt = makeTime(2026, 5, 28)

	items := []retrieval.ContextPacket{
		{
			Type: "history",
			Meta: retrieval.HistoryMeta{
				Messages: []*model.Message{&msg1, &msg2},
			},
		},
	}

	xml := buildHistoryXML(items)
	if !strings.Contains(xml, `<round date="2026年05月28日">`) {
		t.Errorf("missing round date: %s", xml)
	}
	if !strings.Contains(xml, `<message role="user">帮我记账</message>`) {
		t.Errorf("missing user message: %s", xml)
	}
	if !strings.Contains(xml, `<message role="assistant">已记录</message>`) {
		t.Errorf("missing assistant message: %s", xml)
	}
	if !strings.Contains(xml, "<history>") {
		t.Errorf("missing root tag: %s", xml)
	}
}

func TestBuildHistoryXMLNilMessages(t *testing.T) {
	items := []retrieval.ContextPacket{
		{
			Type: "history",
			Meta: retrieval.HistoryMeta{
				Messages: []*model.Message{nil, nil},
			},
		},
	}

	xml := buildHistoryXML(items)
	if strings.Contains(xml, "<round") {
		t.Errorf("nil messages should produce empty output: %s", xml)
	}
}

func TestBuildThemeXML(t *testing.T) {
	items := []retrieval.ContextPacket{
		{
			Type: "theme",
			Meta: retrieval.ThemeMeta{
				Description: "睡眠管理",
				StateText:   "连续7天记录",
			},
		},
	}

	xml := buildThemeXML(items)
	if !strings.Contains(xml, `<topic desc="连续7天记录">睡眠管理</topic>`) {
		t.Errorf("unexpected output: %s", xml)
	}
	if !strings.Contains(xml, "<topics>") {
		t.Errorf("missing root tag: %s", xml)
	}
}

func TestBuildThemeXMLNoState(t *testing.T) {
	items := []retrieval.ContextPacket{
		{
			Type: "theme",
			Meta: retrieval.ThemeMeta{
				Description: "饮食",
			},
		},
	}

	xml := buildThemeXML(items)
	if strings.Contains(xml, `desc=""`) {
		t.Errorf("empty state should be omitted: %s", xml)
	}
	if strings.Contains(xml, `desc=`) {
		t.Errorf("no state should omit desc attr: %s", xml)
	}
}

func TestBuildProviderXMLDispatches(t *testing.T) {
	mem := buildProviderXML("memory", []retrieval.ContextPacket{
		{Type: "memory", Meta: retrieval.EntityMeta{Content: "x"}},
	})
	if !strings.HasPrefix(mem, "<memories>") {
		t.Errorf("memory dispatch failed: %s", mem)
	}

	note := buildProviderXML("note", []retrieval.ContextPacket{
		{Type: "note", Meta: retrieval.EntityMeta{Content: "x"}},
	})
	if !strings.HasPrefix(note, "<notes>") {
		t.Errorf("note dispatch failed: %s", note)
	}

	hist := buildProviderXML("history", []retrieval.ContextPacket{
		{Type: "history", Meta: retrieval.HistoryMeta{
			Messages: []*model.Message{{Role: "user", Content: "x"}},
		}},
	})
	if !strings.HasPrefix(hist, "<history>") {
		t.Errorf("history dispatch failed: %s", hist)
	}

	theme := buildProviderXML("theme", []retrieval.ContextPacket{
		{Type: "theme", Meta: retrieval.ThemeMeta{Description: "x"}},
	})
	if !strings.HasPrefix(theme, "<topics>") {
		t.Errorf("theme dispatch failed: %s", theme)
	}

	unknown := buildProviderXML("unknown", nil)
	if unknown != "" {
		t.Errorf("unknown should return empty: %s", unknown)
	}
}

func TestEntityMetaSatisfiesPacketMeta(t *testing.T) {
	var _ retrieval.PacketMeta = retrieval.EntityMeta{}
	var _ retrieval.PacketMeta = retrieval.HistoryMeta{}
	var _ retrieval.PacketMeta = retrieval.ThemeMeta{}
	var _ retrieval.PacketMeta = retrieval.MissionMeta{}
}
