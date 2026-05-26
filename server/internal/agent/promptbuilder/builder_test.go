package promptbuilder

import (
	"context"
	"strings"
	"testing"

	"xhan-helper/internal/agent/retrieval"
)

// ==================== StaticProvider Tests ====================

func TestCorePrinciplesProvider(t *testing.T) {
	p := &corePrinciplesProvider{}
	if p.Name() != "core_principles" {
		t.Errorf("name = %q", p.Name())
	}
	out := p.Render(context.Background())
	if !strings.Contains(out, "简洁回复") {
		t.Error("should contain core principles")
	}
}

func TestTimeProvider(t *testing.T) {
	p := &timeProvider{}
	out := p.Render(context.Background())
	if !strings.Contains(out, "当前时间") {
		t.Errorf("should contain '当前时间', got %q", out)
	}
}

// ==================== Layer 2 — Role Resolution ====================

type mockPromptResolver struct {
	active string
}

func (m *mockPromptResolver) ResolveActive(ctx context.Context, userID, promptType string) string {
	return m.active
}

func TestRoleResolutionPriority(t *testing.T) {
	basePrompt := "base-role-prompt"

	t.Run("agent prompt takes priority", func(t *testing.T) {
		b := &SystemPromptBuilder{
			basePrompt:     basePrompt,
			promptResolver: &mockPromptResolver{active: "user-custom-prompt"},
		}
		result := b.resolveRolePrompt(context.Background(), BuildOptions{
			AgentPrompt: "agent-prompt",
		})
		if result != "agent-prompt" {
			t.Errorf("expected agent-prompt, got %q", result)
		}
	})

	t.Run("user prompt over base when no agent prompt", func(t *testing.T) {
		b := &SystemPromptBuilder{
			basePrompt:     basePrompt,
			promptResolver: &mockPromptResolver{active: "user-custom-prompt"},
		}
		result := b.resolveRolePrompt(context.Background(), BuildOptions{})
		if result != "user-custom-prompt" {
			t.Errorf("expected user-custom-prompt, got %q", result)
		}
	})

	t.Run("base prompt when no agent or user prompt", func(t *testing.T) {
		b := &SystemPromptBuilder{
			basePrompt:     basePrompt,
			promptResolver: &mockPromptResolver{active: ""},
		}
		result := b.resolveRolePrompt(context.Background(), BuildOptions{})
		if result != basePrompt {
			t.Errorf("expected base prompt, got %q", result)
		}
	})

	t.Run("nil prompt resolver falls back to base", func(t *testing.T) {
		b := &SystemPromptBuilder{
			basePrompt:     basePrompt,
			promptResolver: nil,
		}
		result := b.resolveRolePrompt(context.Background(), BuildOptions{})
		if result != basePrompt {
			t.Errorf("expected base prompt, got %q", result)
		}
	})
}

// ==================== Layer 3 — Theme Provider ====================

func TestBuildResultMetadata(t *testing.T) {
	items := []retrieval.ContextPacket{
		{ID: "mem-1", Type: "memory", Text: "记忆1", FinalScore: 0.9},
		{ID: "mem-2", Type: "memory", Text: "记忆2", FinalScore: 0.7},
		{ID: "note-1", Type: "note", Text: "笔记1", FinalScore: 0.65},
		{ID: "theme-1", Type: "theme", Text: "主题1", FinalScore: 1.0},
	}

	result := &BuildResult{IDsByType: make(map[string][]string)}
	for _, item := range items {
		result.Prompt += item.Text
		result.IDsByType[item.Type] = append(result.IDsByType[item.Type], item.ID)
	}

	memIDs := result.IDsByType["memory"]
	noteIDs := result.IDsByType["note"]
	if len(memIDs) != 2 {
		t.Errorf("expected 2 memory IDs, got %d", len(memIDs))
	}
	if len(noteIDs) != 1 {
		t.Errorf("expected 1 note ID, got %d", len(noteIDs))
	}
	if memIDs[0] != "mem-1" || memIDs[1] != "mem-2" {
		t.Errorf("wrong memory IDs: %v", memIDs)
	}
	if noteIDs[0] != "note-1" {
		t.Errorf("wrong note ID: %v", noteIDs)
	}
}

// ==================== Integration Tests ====================

func TestBuildLayer1Order(t *testing.T) {
	p1 := &testStaticProvider{name: "first", content: "A"}
	p2 := &testStaticProvider{name: "second", content: "B"}
	p3 := &testStaticProvider{name: "third", content: "C"}

	b := &SystemPromptBuilder{
		staticProviders: []StaticProvider{p1, p2, p3},
	}

	result, err := b.Build(context.Background(), BuildOptions{
		UserMessage: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	aIdx := strings.Index(result.Prompt, "A")
	bIdx := strings.Index(result.Prompt, "B")
	cIdx := strings.Index(result.Prompt, "C")

	if aIdx < 0 || bIdx < 0 || cIdx < 0 {
		t.Fatal("not all providers rendered")
	}
	if aIdx >= bIdx || bIdx >= cIdx {
		t.Error("providers not in order")
	}
}

func TestBuildSortsByScore(t *testing.T) {
	prov := &testSearchProvider{
		name: "multi",
		items: []retrieval.ContextPacket{
			{Text: "LOW", FinalScore: 0.3, Type: "multi"},
			{Text: "MID", FinalScore: 0.5, Type: "multi"},
			{Text: "HIGH", FinalScore: 1.0, Type: "multi"},
		},
	}

	b := &SystemPromptBuilder{
		searchProviders: []SearchProvider{prov},
	}

	result, err := b.Build(context.Background(), BuildOptions{
		UserMessage: "test",
		Embedding:   []float64{0.1},
	})
	if err != nil {
		t.Fatal(err)
	}

	highIdx := strings.Index(result.Prompt, "HIGH")
	midIdx := strings.Index(result.Prompt, "MID")
	lowIdx := strings.Index(result.Prompt, "LOW")

	if highIdx < 0 || midIdx < 0 || lowIdx < 0 {
		t.Fatal("not all search results rendered")
	}
	if !(highIdx < midIdx && midIdx < lowIdx) {
		t.Errorf("items within group should be sorted by FinalScore: HIGH at %d, MID at %d, LOW at %d", highIdx, midIdx, lowIdx)
	}
}

func TestBuildSearchProviderError(t *testing.T) {
	errProvider := &errorSearchProvider{}
	okProvider := &testSearchProvider{
		name:  "ok",
		items: []retrieval.ContextPacket{{Text: "OK", FinalScore: 0.5, Type: "ok"}},
	}

	b := &SystemPromptBuilder{
		searchProviders: []SearchProvider{errProvider, okProvider},
	}

	result, err := b.Build(context.Background(), BuildOptions{
		UserMessage: "test",
		Embedding:   []float64{0.1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Prompt, "OK") {
		t.Error("OK provider should still render despite error provider")
	}
}

// ==================== Test Helpers ====================

type testStaticProvider struct {
	name    string
	content string
}

func (p *testStaticProvider) Name() string                      { return p.name }
func (p *testStaticProvider) Render(ctx context.Context) string { return p.content }

type testSearchProvider struct {
	name  string
	items []retrieval.ContextPacket
}

func (p *testSearchProvider) Name() string          { return p.name }
func (p *testSearchProvider) SectionHeader() string { return "" }
func (p *testSearchProvider) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	return &SearchResult{Items: p.items}, nil
}

type errorSearchProvider struct{}

func (p *errorSearchProvider) Name() string          { return "error" }
func (p *errorSearchProvider) SectionHeader() string { return "" }
func (p *errorSearchProvider) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	return nil, context.DeadlineExceeded
}

// ==================== Trace Helpers ====================

func TestBuildTraceItems(t *testing.T) {
	packets := []retrieval.ContextPacket{
		{ID: "mem-1", Text: "记忆1内容", Score: 0.9, KeywordScore: 0.7},
		{ID: "mem-2", Text: "记忆2内容", Score: 0.8, KeywordScore: 0.6},
	}
	nearMiss := []retrieval.NearMissItem{
		{ID: "mem-3", VectorScore: 0.4, Reason: "below_threshold"},
		{ID: "mem-4", VectorScore: 0.3, Reason: "wrong_prefix"},
	}

	items := buildTraceItems(packets, nearMiss)

	if len(items) != 4 {
		t.Fatalf("expected 4 trace items (2 selected + 2 near-miss), got %d", len(items))
	}

	if items[0].DiscardReason != "" {
		t.Errorf("selected items should have empty DiscardReason, got %q", items[0].DiscardReason)
	}
	if items[1].KeywordScore != 0.6 {
		t.Errorf("item 1 keyword score: expected 0.6, got %f", items[1].KeywordScore)
	}

	if items[2].DiscardReason != "below_threshold" {
		t.Errorf("near-miss should have DiscardReason, got %q", items[2].DiscardReason)
	}
	if items[2].VectorScore != 0.4 {
		t.Errorf("near-miss vector score: expected 0.4, got %f", items[2].VectorScore)
	}
	if items[3].DiscardReason != "wrong_prefix" {
		t.Errorf("near-miss should have DiscardReason, got %q", items[3].DiscardReason)
	}
}

func TestBuildTraceItemsEmpty(t *testing.T) {
	items := buildTraceItems(nil, nil)
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestCollectIDsByType(t *testing.T) {
	items := []retrieval.ContextPacket{
		{ID: "m1", Type: "memory"},
		{ID: "m2", Type: "memory"},
		{ID: "n1", Type: "note"},
		{ID: "m3", Type: "memory"},
	}

	var memIDs, noteIDs []string
	for _, item := range items {
		switch item.Type {
		case "memory":
			memIDs = append(memIDs, item.ID)
		case "note":
			noteIDs = append(noteIDs, item.ID)
		}
	}

	if len(memIDs) != 3 {
		t.Fatalf("expected 3 memory IDs, got %d", len(memIDs))
	}
	if memIDs[0] != "m1" || memIDs[1] != "m2" || memIDs[2] != "m3" {
		t.Errorf("unexpected IDs: %v", memIDs)
	}
	if len(noteIDs) != 1 || noteIDs[0] != "n1" {
		t.Errorf("expected [n1], got %v", noteIDs)
	}
}
