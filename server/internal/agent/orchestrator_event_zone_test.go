package agent

import (
	"testing"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/model"
)

func prevEventPacket() retrieval.ContextPacket {
	return retrieval.ContextPacket{
		ID: "e1", Type: ctxflow.TypeEvent,
		Meta: retrieval.EventMeta{
			EventID: "e1", Title: "旧事件", Summary: "旧现状",
			Mode:     retrieval.EventModeFull,
			Memories: []model.UserMemory{{ID: "m1", MemoryValue: "内容"}},
		},
	}
}

func TestEventZonePackets(t *testing.T) {
	prev := []retrieval.ContextPacket{prevEventPacket()}

	// 跳过检索（短输入）：同窗口原样携带
	got := eventZonePackets(nil, prev, false, nil)
	if len(got) != 1 {
		t.Fatalf("skip retrieval should carry prev events, got %+v", got)
	}
	if meta := got[0].Meta.(retrieval.EventMeta); meta.Mode != retrieval.EventModeFull {
		t.Fatalf("same-session carry should keep full, got %+v", meta)
	}

	// 新 session：降为 summary carried，不带记忆
	got = eventZonePackets(nil, prev, true, nil)
	meta := got[0].Meta.(retrieval.EventMeta)
	if meta.Mode != retrieval.EventModeSummary || !meta.Carried || len(meta.Memories) != 0 {
		t.Fatalf("new-session carry should be summary carried, got %+v", meta)
	}

	// 新 session + pinned：保持 full 与记忆
	got = eventZonePackets(nil, prev, true, []string{"e1"})
	pinnedMeta := got[0].Meta.(retrieval.EventMeta)
	if pinnedMeta.Mode != retrieval.EventModeFull || !pinnedMeta.Pinned || len(pinnedMeta.Memories) != 1 {
		t.Fatalf("pinned event should stay full across windows, got %+v", pinnedMeta)
	}

	// EventSource 报错：携带上一轮（不能整块消失）
	failed := &ctxflow.RetrieveResult{Trace: &ctxflow.RetrieveTrace{Providers: []ctxflow.ProviderTrace{
		{Name: retrieval.EventSourceName, Error: "db down"},
	}}}
	if got := eventZonePackets(failed, prev, false, nil); len(got) != 1 {
		t.Fatalf("event source failure should carry prev events, got %+v", got)
	}

	// 检索正常但没有命中：不携带（真实 miss，由 EventSource 的粘滞逻辑降级）
	empty := &ctxflow.RetrieveResult{
		Packets: map[string][]retrieval.ContextPacket{},
		Trace:   &ctxflow.RetrieveTrace{Providers: []ctxflow.ProviderTrace{{Name: retrieval.EventSourceName}}},
	}
	if got := eventZonePackets(empty, prev, false, nil); len(got) != 0 {
		t.Fatalf("normal miss must not carry prev events, got %+v", got)
	}

	// 检索正常且有命中：用本轮结果
	fresh := []retrieval.ContextPacket{{
		ID: "e2", Type: ctxflow.TypeEvent,
		Meta: retrieval.EventMeta{EventID: "e2", Mode: retrieval.EventModeFull},
	}}
	empty.Packets[ctxflow.TypeEvent] = fresh
	got = eventZonePackets(empty, prev, false, nil)
	if len(got) != 1 || got[0].ID != "e2" {
		t.Fatalf("normal retrieval should use fresh events, got %+v", got)
	}
}

func TestMkSearchOptions_PinnedEventIDs(t *testing.T) {
	opts := mkSearchOptions(&RunRequest{PinnedEventIDs: []string{"e1", "e2"}}, nil, nil)
	if len(opts.PinnedEventIDs) != 2 || opts.PinnedEventIDs[0] != "e1" {
		t.Fatalf("pinned event ids not wired into SearchOptions: %+v", opts.PinnedEventIDs)
	}
}
