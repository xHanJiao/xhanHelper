package promptbuilder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// ==================== StaticProvider Tests ====================

func TestCorePrinciplesProvider(t *testing.T) {
	p := &corePrinciplesProvider{}
	if p.Name() != "core_principles" {
		t.Errorf("name = %q", p.Name())
	}
	out := p.Render(context.Background())
	if !strings.Contains(out, "诚实") {
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

// ==================== plan 模式工具隐藏 ====================

// planBuildFakeTool — 最小工具实现，验证工具定义段是否注入
type planBuildFakeTool struct{ name string }

func (f *planBuildFakeTool) Name() string        { return f.name }
func (f *planBuildFakeTool) Description() string { return "fake tool desc" }
func (f *planBuildFakeTool) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{Name: "do", Description: "do sth"}}
}
func (f *planBuildFakeTool) ReadOnlyFuncs() map[string]bool { return map[string]bool{"do": true} }
func (f *planBuildFakeTool) Execute(_ *tool.ExecuteContext, _ string, _ json.RawMessage) *tool.Result {
	return &tool.Result{Success: true}
}

func TestBuild_PlanModeHidesToolDefinitions(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&planBuildFakeTool{name: "fake_tool"})

	// prompt-eng 模式
	b := NewPromptBuilder("base", reg, nil, false)

	work, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(work.Prompt, "## 工具使用") || !strings.Contains(work.Prompt, "fake_tool") {
		t.Error("work 模式应包含工具定义段")
	}

	plan, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModePlan})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Prompt, "## 工具使用") || strings.Contains(plan.Prompt, "fake_tool") || strings.Contains(plan.Prompt, "你可以使用以下工具") {
		t.Error("plan 模式不应包含任何工具定义")
	}

	// native FC 模式
	b2 := NewPromptBuilder("base", reg, nil, true)
	work2, err := b2.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(work2.Prompt, "函数调用接口") {
		t.Error("work 模式 native FC 通知应说明函数调用接口")
	}
	plan2, err := b2.Build(context.Background(), BuildOptions{Mode: model.ConvModePlan})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan2.Prompt, "不提供任何工具调用能力") {
		t.Error("plan 模式 native FC 通知应声明无工具可用")
	}
	if strings.Contains(plan2.Prompt, "函数调用接口") {
		t.Error("plan 模式不应提示使用函数调用接口")
	}
}

func TestBuild_StableContextInjected(t *testing.T) {
	reg := tool.NewRegistry()
	b := NewPromptBuilder("base", reg, nil, true)
	stableXML := "<profile description=\"画像\">\n  <field key=\"工作\">后端工程师</field>\n</profile>"

	// work 模式注入
	work, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork, StableContextXML: stableXML})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(work.Prompt, "## 稳定上下文") || !strings.Contains(work.Prompt, "<profile") {
		t.Errorf("work 模式应注入稳定上下文段, got:\n%s", work.Prompt)
	}

	// plan 模式同样注入（讨论也需要画像）
	plan, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModePlan, StableContextXML: stableXML})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Prompt, "## 稳定上下文") || !strings.Contains(plan.Prompt, "<profile") {
		t.Errorf("plan 模式应注入稳定上下文段, got:\n%s", plan.Prompt)
	}

	// 空 XML → 不出现空段
	empty, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty.Prompt, "## 稳定上下文") {
		t.Error("StableContextXML 为空时不应出现稳定上下文段")
	}
}

func TestBuild_ClientToolCatalogInjected(t *testing.T) {
	reg := tool.NewRegistry()
	b := NewPromptBuilder("base", reg, nil, true)
	catalog := "客户端工具组（Mac 客户端在线时可用，同样按需启用：先调用 expose_tools.enable）：\n- browser: Mac 客户端本地工具（browser）\n  函数: agent_browser_open, agent_browser_click"

	// work 模式：目录并入「工具使用」段
	work, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork, ClientToolCatalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	sectionIdx := strings.Index(work.Prompt, "## 工具使用")
	catalogIdx := strings.Index(work.Prompt, "客户端工具组")
	stableIdx := strings.Index(work.Prompt, "## 稳定上下文")
	if sectionIdx < 0 || catalogIdx < 0 {
		t.Fatal("work 模式应包含工具使用段与客户端目录")
	}
	if catalogIdx < sectionIdx {
		t.Error("客户端目录应位于工具使用段之内")
	}
	if stableIdx >= 0 && catalogIdx > stableIdx {
		t.Error("客户端目录应位于稳定上下文段之前（在工具使用段内）")
	}
	if !strings.Contains(work.Prompt, "agent_browser_open") {
		t.Error("客户端目录应包含函数名")
	}

	// 目录为空 → 不注入
	empty, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty.Prompt, "客户端工具组") {
		t.Error("ClientToolCatalog 为空时不应出现客户端目录")
	}

	// plan 模式：目录不注入（无工具声明替换）
	plan, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModePlan, ClientToolCatalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Prompt, "客户端工具组") || strings.Contains(plan.Prompt, "agent_browser_open") {
		t.Error("plan 模式不应注入客户端目录")
	}

	// prompt-eng 模式：目录不注入（无 tool_catalog provider）
	b3 := NewPromptBuilder("base", reg, nil, false)
	pe, err := b3.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork, ClientToolCatalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pe.Prompt, "客户端工具组") {
		t.Error("prompt-eng 模式不应注入客户端目录")
	}
}
