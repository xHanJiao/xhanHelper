package promptbuilder

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestCorePrinciplesListStructure — 行为准则是一个连续的有序列表，每项单段。
// 列表项里一旦夹空行，CommonMark 会把该项续段判成列表外（准则 4 内嵌含空行的 MemoryDefinition
// 时就被劈到列表外，markdown 预览里 4 的正文飘在列表中间——见 docs/bugs/022）。
// 渲染器在服务端之外，Go 侧只钉这条原始结构不变量。
func TestCorePrinciplesListStructure(t *testing.T) {
	if strings.Contains(corePrinciples, "\n\n") {
		t.Fatal("行为准则列表里出现空行：会把某一项劈成多段、续段掉出编号列表")
	}
	for n := 1; n <= 8; n++ {
		marker := fmt.Sprintf("%d. ", n)
		if !strings.HasPrefix(corePrinciples, marker) &&
			!strings.Contains(corePrinciples, "\n"+marker) {
			t.Errorf("准则 %d 不在行首（编号列表被并进上一段）", n)
		}
	}
	// 准则 4 的行为规则不能被掏空（定义部分已移出，只留行为）
	for _, want := range []string{"record_memory", "不留旧数据", "内部动作"} {
		if !strings.Contains(corePrinciples, want) {
			t.Errorf("准则 4 缺行为规则 %q", want)
		}
	}
}

// TestUserContextTriggerToggle — user_context 触发规则由开关控制注入。
func TestUserContextTriggerToggle(t *testing.T) {
	reg := tool.NewRegistry()

	// 关闭：不注入触发规则
	off := NewPromptBuilder("base", reg, nil, false)
	out, err := off.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Prompt, "user_context") {
		t.Error("开关关闭时不应注入 user_context 触发规则")
	}

	// 开启：注入触发规则
	on := NewPromptBuilder("base", reg, nil, true)
	out2, err := on.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.Prompt, "9. 用户信息深挖") || !strings.Contains(out2.Prompt, "user_context") {
		t.Error("开关开启时应注入 user_context 触发规则")
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

// planBuildFakeTool — 最小工具实现，验证工具定义段是否注入（含一读一写函数）
type planBuildFakeTool struct{ name string }

func (f *planBuildFakeTool) Name() string        { return f.name }
func (f *planBuildFakeTool) Description() string { return "fake tool desc" }
func (f *planBuildFakeTool) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{
		{Name: "read_fn", Description: "read sth"},
		{Name: "write_fn", Description: "write sth"},
	}
}
func (f *planBuildFakeTool) ReadOnlyFuncs() map[string]bool { return map[string]bool{"read_fn": true} }
func (f *planBuildFakeTool) Execute(_ *tool.ExecuteContext, _ string, _ json.RawMessage) *tool.Result {
	return &tool.Result{Success: true}
}

func TestBuild_PlanModeOnlyReadOnlyTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&planBuildFakeTool{name: "fake_tool"})

	b := NewPromptBuilder("base", reg, nil, false)

	work, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(work.Prompt, "## 工具使用") || !strings.Contains(work.Prompt, "read_fn") || !strings.Contains(work.Prompt, "write_fn") {
		t.Error("work 模式目录应列出全部函数")
	}
	if !strings.Contains(work.Prompt, "函数调用接口") {
		t.Error("work 模式应提示必须使用函数调用接口")
	}

	plan, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModePlan})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Prompt, "仅提供只读工具") {
		t.Error("plan 模式应声明仅提供只读工具")
	}
	if strings.Contains(plan.Prompt, "read_fn") || strings.Contains(plan.Prompt, "write_fn") {
		t.Error("plan 模式只给只读提示，不列函数定义")
	}
	if strings.Contains(plan.Prompt, "函数调用接口") {
		t.Error("plan 模式不应提示使用 expose_tools 函数调用接口")
	}
}

func TestBuild_StrictModeNoTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&planBuildFakeTool{name: "fake_tool"})

	b := NewPromptBuilder("base", reg, nil, false)
	strict, err := b.Build(context.Background(), BuildOptions{Mode: model.ConvModeStrict})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strict.Prompt, "read_fn") || strings.Contains(strict.Prompt, "write_fn") {
		t.Error("strict 模式不应包含任何函数定义")
	}
	if !strings.Contains(strict.Prompt, "严格计划模式") {
		t.Error("strict 模式应含无工具提示")
	}
	if strings.Contains(strict.Prompt, "函数调用接口") {
		t.Error("strict 模式不应提示调用工具")
	}
}

func TestBuild_StableContextInjected(t *testing.T) {
	reg := tool.NewRegistry()
	b := NewPromptBuilder("base", reg, nil, false)
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
	b := NewPromptBuilder("base", reg, nil, false)
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
}

// TestBriefAnswerToggle — brief_answer 轮才注入长度约束；不注入时 prompt 逐字节不变。
func TestBriefAnswerToggle(t *testing.T) {
	reg := tool.NewRegistry()
	b := NewPromptBuilder("base", reg, nil, true)
	ctx := context.Background()

	base, err := b.Build(ctx, BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(base.Prompt, "回答长度") || strings.Contains(base.Prompt, "精简回答") {
		t.Error("默认（非 brief_answer）不应注入长度约束")
	}

	brief, err := b.Build(ctx, BuildOptions{Mode: model.ConvModeWork, BriefAnswer: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.Prompt, "## 回答长度") || !strings.Contains(brief.Prompt, "精简回答") {
		t.Error("brief_answer 轮应注入长度约束章节")
	}
	// 追加，不是替换：行为准则与角色定义都还在
	for _, want := range []string{"## 行为准则", "## 角色定义", "9. 用户信息深挖"} {
		if !strings.Contains(brief.Prompt, want) {
			t.Errorf("brief_answer 轮丢了 %q", want)
		}
	}
	// 长度约束不能插在行为准则编号列表中间（会把 9 截断）
	if strings.Index(brief.Prompt, "9. 用户信息深挖") > strings.Index(brief.Prompt, "## 回答长度") {
		t.Error("回答长度章节插到了行为准则编号列表中间")
	}
	// 开与不开只差插入的那一块，其余逐字节相同
	want := strings.Replace(base.Prompt, "\n## 角色定义",
		"## 回答长度\n\n"+answerLengthNotice+"\n\n## 角色定义", 1)
	if brief.Prompt != want {
		t.Errorf("brief_answer 轮应等于默认输出插入长度章节\n--- got ---\n%s\n--- want ---\n%s", brief.Prompt, want)
	}
}

// TestBriefAnswerOffIsByteIdentical — 关掉时与"没有这个 provider"完全一致（保前缀缓存）
func TestBriefAnswerOffIsByteIdentical(t *testing.T) {
	reg := tool.NewRegistry()
	b := NewPromptBuilder("base", reg, nil, true)
	ctx := context.Background()

	a, err := b.Build(ctx, BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}
	bb, err := b.Build(ctx, BuildOptions{Mode: model.ConvModeWork, BriefAnswer: false})
	if err != nil {
		t.Fatal(err)
	}
	if a.Prompt != bb.Prompt {
		t.Fatal("BriefAnswer=false 时输出应与默认完全一致")
	}
}

// TestBriefAnswerOffKeepsPrefixCache — 关掉时输出逐字节等于"从未有过这个 provider"。
// 用反射去掉 answerLengthProvider 再构建，与 BriefAnswer=false 的输出比对，
// 确保跳过逻辑没有留下任何空白/换行残留。
func TestBriefAnswerOffKeepsPrefixCache(t *testing.T) {
	reg := tool.NewRegistry()
	ctx := context.Background()

	full := NewPromptBuilder("base", reg, nil, true)
	off, err := full.Build(ctx, BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}

	// 模拟"没有这个 provider"：直接把 preRoleStatic 里的它摘掉
	stripped := *full
	stripped.preRoleStatic = nil
	for _, sp := range full.preRoleStatic {
		if sp.Name() != "answer_length" {
			stripped.preRoleStatic = append(stripped.preRoleStatic, sp)
		}
	}
	noProvider, err := stripped.Build(ctx, BuildOptions{Mode: model.ConvModeWork})
	if err != nil {
		t.Fatal(err)
	}

	if off.Prompt != noProvider.Prompt {
		t.Fatalf("跳过 answer_length 后与没有它不一致\n--- got ---\n%q\n--- want ---\n%q", off.Prompt, noProvider.Prompt)
	}
}
