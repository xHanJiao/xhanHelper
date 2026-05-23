package agent

import (
	"context"
	"strings"
	"testing"
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
	// 验证 BuildResult 正确收集 memory/note IDs
	items := []ScoredItem{
		{
			Content: "theme content",
			Score:   1.0,
			Source:  "theme",
		},
		{
			Content: "retrieval content",
			Score:   0.8,
			Source:  "retrieval",
			Meta: map[string]any{
				"memory_ids": []string{"mem-1", "mem-2"},
				"note_ids":   []string{"note-1"},
			},
		},
	}

	result := &BuildResult{}
	for _, item := range items {
		if metaMap, ok := item.Meta.(map[string]any); ok {
			if memIDs, ok := metaMap["memory_ids"].([]string); ok {
				result.MemoryIDs = append(result.MemoryIDs, memIDs...)
			}
			if noteIDs, ok := metaMap["note_ids"].([]string); ok {
				result.NoteIDs = append(result.NoteIDs, noteIDs...)
			}
		}
	}

	if len(result.MemoryIDs) != 2 {
		t.Errorf("expected 2 memory IDs, got %d", len(result.MemoryIDs))
	}
	if len(result.NoteIDs) != 1 {
		t.Errorf("expected 1 note ID, got %d", len(result.NoteIDs))
	}
	if result.MemoryIDs[0] != "mem-1" || result.MemoryIDs[1] != "mem-2" {
		t.Errorf("wrong memory IDs: %v", result.MemoryIDs)
	}
	if result.NoteIDs[0] != "note-1" {
		t.Errorf("wrong note ID: %v", result.NoteIDs)
	}
}

// ==================== Integration Tests ====================

func TestBuildLayer1Order(t *testing.T) {
	// 验证 Layer 1 StaticProviders 按注册顺序渲染
	var order []string
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
	_ = order

	// A should appear before B, B before C
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
	// 验证 SearchProvider 的结果按 score 降序排列
	high := &testSearchProvider{
		name:  "high",
		items: []ScoredItem{{Content: "HIGH", Score: 1.0, Source: "high"}},
	}
	low := &testSearchProvider{
		name:  "low",
		items: []ScoredItem{{Content: "LOW", Score: 0.3, Source: "low"}},
	}
	mid := &testSearchProvider{
		name:  "mid",
		items: []ScoredItem{{Content: "MID", Score: 0.5, Source: "mid"}},
	}

	b := &SystemPromptBuilder{
		searchProviders: []SearchProvider{low, mid, high}, // register out of order
	}

	result, err := b.Build(context.Background(), BuildOptions{
		UserMessage: "test",
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
	if highIdx >= midIdx || midIdx >= lowIdx {
		t.Errorf("not sorted by score: HIGH at %d, MID at %d, LOW at %d", highIdx, midIdx, lowIdx)
	}
}

func TestBuildSearchProviderError(t *testing.T) {
	// 验证 SearchProvider 出错不阻塞整体构建
	errProvider := &errorSearchProvider{}
	okProvider := &testSearchProvider{
		name:  "ok",
		items: []ScoredItem{{Content: "OK", Score: 0.5, Source: "ok"}},
	}

	b := &SystemPromptBuilder{
		searchProviders: []SearchProvider{errProvider, okProvider},
	}

	result, err := b.Build(context.Background(), BuildOptions{
		UserMessage: "test",
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
	items []ScoredItem
}

func (p *testSearchProvider) Name() string { return p.name }
func (p *testSearchProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]ScoredItem, error) {
	return p.items, nil
}

type errorSearchProvider struct{}

func (p *errorSearchProvider) Name() string { return "error" }
func (p *errorSearchProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]ScoredItem, error) {
	return nil, context.DeadlineExceeded
}
