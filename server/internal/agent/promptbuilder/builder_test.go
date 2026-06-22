package promptbuilder

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
		b := &PromptBuilder{
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
		b := &PromptBuilder{
			basePrompt:     basePrompt,
			promptResolver: &mockPromptResolver{active: "user-custom-prompt"},
		}
		result := b.resolveRolePrompt(context.Background(), BuildOptions{})
		if result != "user-custom-prompt" {
			t.Errorf("expected user-custom-prompt, got %q", result)
		}
	})

	t.Run("base prompt when no agent or user prompt", func(t *testing.T) {
		b := &PromptBuilder{
			basePrompt:     basePrompt,
			promptResolver: &mockPromptResolver{active: ""},
		}
		result := b.resolveRolePrompt(context.Background(), BuildOptions{})
		if result != basePrompt {
			t.Errorf("expected base prompt, got %q", result)
		}
	})

	t.Run("nil prompt resolver falls back to base", func(t *testing.T) {
		b := &PromptBuilder{
			basePrompt:     basePrompt,
			promptResolver: nil,
		}
		result := b.resolveRolePrompt(context.Background(), BuildOptions{})
		if result != basePrompt {
			t.Errorf("expected base prompt, got %q", result)
		}
	})
}

// ==================== Integration Tests ====================

type testStaticProvider struct {
	name         string
	content      string
	sectionTitle string
}

func (p *testStaticProvider) Name() string                      { return p.name }
func (p *testStaticProvider) SectionTitle() string              { return p.sectionTitle }
func (p *testStaticProvider) Render(ctx context.Context) string { return p.content }

func TestBuildLayer1Order(t *testing.T) {
	p1 := &testStaticProvider{name: "first", content: "A"}
	p2 := &testStaticProvider{name: "second", content: "B"}
	p3 := &testStaticProvider{name: "third", content: "C"}

	b := &PromptBuilder{
		preRoleStatic:  []StaticProvider{p1},
		postRoleStatic: []StaticProvider{p2, p3},
	}

	result, err := b.Build(context.Background(), BuildOptions{})
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
