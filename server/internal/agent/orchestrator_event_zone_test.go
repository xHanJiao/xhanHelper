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

type stubEventLister struct {
	pkts  []retrieval.ContextPacket
	err   error
	calls int
}

func (s *stubEventLister) ListForInjection(ctx context.Context, userID, query string, pinnedIDs []string) ([]retrieval.ContextPacket, error) {
	s.calls++
	return s.pkts, s.err
}

func listedEventPacket() retrieval.ContextPacket {
	return retrieval.ContextPacket{
		ID: "e1", Type: ctxflow.TypeEvent,
		Meta: retrieval.EventMeta{
			EventID: "e1", Title: "回西安", Summary: "11-25 退租",
			Mode:     retrieval.EventModeSummary,
			Memories: []model.UserMemory{{ID: "m1", MemoryValue: "内容"}},
		},
	}
}

func TestEventZonePackets(t *testing.T) {
	lister := &stubEventLister{pkts: []retrieval.ContextPacket{listedEventPacket()}}
	o := &Orchestrator{eventLister: lister}
	req := &RunRequest{UserID: "u1", RawUserMessage: "继续"}

	// 跳过检索（短输入）：直接向 lister 要全量列表
	got := o.eventZonePackets(context.Background(), req, nil)
	if len(got) != 1 || lister.calls != 1 {
		t.Fatalf("skip retrieval should list all events, got %+v (calls=%d)", got, lister.calls)
	}

	// EventSource 报错：同样走 lister，不能整块消失
	failed := &ctxflow.RetrieveResult{Trace: &ctxflow.RetrieveTrace{Providers: []ctxflow.ProviderTrace{
		{Name: retrieval.EventSourceName, Error: "db down"},
	}}}
	if got := o.eventZonePackets(context.Background(), req, failed); len(got) != 1 || lister.calls != 2 {
		t.Fatalf("event source failure should fall back to lister, got %+v", got)
	}

	// 检索正常：用本轮结果，不调 lister
	fresh := []retrieval.ContextPacket{{
		ID: "e2", Type: ctxflow.TypeEvent,
		Meta: retrieval.EventMeta{EventID: "e2", Mode: retrieval.EventModeHit},
	}}
	ok := &ctxflow.RetrieveResult{
		Packets: map[string][]retrieval.ContextPacket{ctxflow.TypeEvent: fresh},
		Trace:   &ctxflow.RetrieveTrace{Providers: []ctxflow.ProviderTrace{{Name: retrieval.EventSourceName}}},
	}
	if got := o.eventZonePackets(context.Background(), req, ok); len(got) != 1 || got[0].ID != "e2" || lister.calls != 2 {
		t.Fatalf("normal retrieval should use fresh events, got %+v", got)
	}
}

func TestMkSearchOptions_PinnedEventIDs(t *testing.T) {
	opts := mkSearchOptions(&RunRequest{PinnedEventIDs: []string{"e1", "e2"}}, nil)
	if len(opts.PinnedEventIDs) != 2 || opts.PinnedEventIDs[0] != "e1" {
		t.Fatalf("pinned event ids not wired into SearchOptions: %+v", opts.PinnedEventIDs)
	}
	if !opts.EventsAll {
		t.Fatal("main chat must request the full event list")
	}
}

// newEventZoneOrchestrator — buildUserMessage 所需的最小装配（只关心资源区/事件接线）。
func newEventZoneOrchestrator() *Orchestrator {
	return &Orchestrator{
		contextFormatter:    ctxflow.NewXMLFormatter(),
		resourceZoneEnabled: true,
		resourceZoneQuotas:  ctxflow.DefaultZoneQuotas(),
		resourceZoneBudgets: ctxflow.DefaultZoneBudgets(),
		resourceZoneBudget:  24000,
	}
}

// 端到端：本轮 EventSource 结果 → 事件连同记忆进资源区 events 块，且不被常规格式化重复注入。
func TestBuildUserMessage_InjectsEventZone(t *testing.T) {
	o := newEventZoneOrchestrator()
	rr := &ctxflow.RetrieveResult{
		Packets: map[string][]retrieval.ContextPacket{
			ctxflow.TypeEvent: {{
				ID: "e1", Type: ctxflow.TypeEvent,
				Meta: retrieval.EventMeta{
					EventID: "e1", Title: "驾照考取", Summary: "科三待补考",
					Mode: retrieval.EventModeHit,
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

// 端到端：跳过检索（短输入）→ 事件区仍然出全量列表（不再携带上一轮 XML）。
func TestBuildUserMessage_ListsEventsWithoutRetrieval(t *testing.T) {
	o := newEventZoneOrchestrator()
	o.eventLister = &stubEventLister{pkts: []retrieval.ContextPacket{listedEventPacket()}}
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		UserMessageContent: "继续",
		Window:             WindowResult{IsNewSession: true},
	}

	msg := o.buildUserMessage(context.Background(), req, nil)
	if msg.XMLContext == nil {
		t.Fatal("XMLContext 未生成")
	}
	xml := *msg.XMLContext
	for _, want := range []string{`title="回西安"`, `mode="summary"`, "11-25 退租"} {
		if !strings.Contains(xml, want) {
			t.Errorf("短输入事件列表缺少 %q:\n%s", want, xml)
		}
	}
}
