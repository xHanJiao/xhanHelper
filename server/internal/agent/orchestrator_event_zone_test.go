package agent

import (
	"context"
	"strings"
	"testing"
	"time"

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

// newEventZoneOrchestrator — buildUserMessage 所需的最小装配（只关心资源区/事件接线）。
func newEventZoneOrchestrator() *Orchestrator {
	return &Orchestrator{
		contextFormatter:    ctxflow.NewXMLFormatter(),
		resourceZoneEnabled: true,
		resourceZoneQuotas:  ctxflow.DefaultZoneQuotas(),
		resourceZoneBudgets: ctxflow.DefaultZoneBudgets(),
		resourceZoneBudget:  15000,
	}
}

// 端到端：本轮 EventSource 命中 → 事件连同记忆进资源区 events 块，且不被常规格式化重复注入。
func TestBuildUserMessage_InjectsEventZone(t *testing.T) {
	o := newEventZoneOrchestrator()
	rr := &ctxflow.RetrieveResult{
		Packets: map[string][]retrieval.ContextPacket{
			ctxflow.TypeEvent: {{
				ID: "e1", Type: ctxflow.TypeEvent,
				Meta: retrieval.EventMeta{
					EventID: "e1", Title: "驾照考取", Summary: "科三待补考",
					Mode: retrieval.EventModeFull,
					Memories: []model.UserMemory{{
						ID: "m1", MemoryValue: "科三两把均败", CreatedAt: time.Now(),
					}},
				},
			}},
			ctxflow.TypeMemory: {{
				ID: "m2", Type: ctxflow.TypeMemory, Text: "普通记忆内容",
				Meta: retrieval.EntityMeta{Content: "普通记忆内容"},
			}},
		},
		Trace: &ctxflow.RetrieveTrace{Providers: []ctxflow.ProviderTrace{{Name: retrieval.EventSourceName}}},
	}
	req := &RunRequest{UserID: "u1", ConversationID: "c1", UserMessageContent: "科三怎么样"}

	msg := o.buildUserMessage(context.Background(), req, rr)
	if msg.XMLContext == nil {
		t.Fatal("XMLContext 未生成")
	}
	xml := *msg.XMLContext
	for _, want := range []string{"<events", `title="驾照考取"`, "科三待补考", "科三两把均败", "普通记忆内容", "<user_query>"} {
		if !strings.Contains(xml, want) {
			t.Errorf("XMLContext 缺少 %q:\n%s", want, xml)
		}
	}
	if n := strings.Count(xml, "<events"); n != 1 {
		t.Errorf("events 块应只出现一次（资源区），实际 %d:\n%s", n, xml)
	}
	if strings.Index(xml, "<events") > strings.Index(xml, "<user_query>") {
		t.Errorf("资源区应在 user_query 之前:\n%s", xml)
	}
}

// 端到端：新窗口 + 跳过检索 → 上一轮事件以 summary carried 形态延续，不带记忆。
func TestBuildUserMessage_CarriesEventAcrossWindow(t *testing.T) {
	o := newEventZoneOrchestrator()
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		UserMessageContent: "继续",
		Window:             WindowResult{IsNewSession: true},
		PrevZonePackets:    []retrieval.ContextPacket{prevEventPacket()},
	}

	msg := o.buildUserMessage(context.Background(), req, nil)
	if msg.XMLContext == nil {
		t.Fatal("XMLContext 未生成")
	}
	xml := *msg.XMLContext
	for _, want := range []string{`title="旧事件"`, `mode="summary"`, `carried="上一窗口"`, "旧现状"} {
		if !strings.Contains(xml, want) {
			t.Errorf("跨窗口 carry 缺少 %q:\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "<memory ") {
		t.Errorf("carried 不应带记忆正文:\n%s", xml)
	}
}
