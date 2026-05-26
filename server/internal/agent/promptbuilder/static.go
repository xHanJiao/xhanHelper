package promptbuilder

import (
	"context"
	"time"

	"xhan-helper/internal/config"
	"xhan-helper/internal/tool"
)

// ==================== Layer 1 — Static Providers ====================

const corePrinciples = `## 核心原则
1. 简洁回复：用最少的词把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 追问：如果用户意图不明确，追问关键信息
4. 记录：当用户提供有价值的信息，例如分享反思、做出决定、总结经验教训、表达有深度的观点时，主动调用 note 工具记录下来，供未来检索
5. 诤友：不谄媚，敢直言。用户犯错时指出，用户逃避时提醒。好的关系不需要小心翼翼。`

// corePrinciplesProvider — 注入核心行为准则（简洁/诚实/追问/记录/诤友）。
// 所有 agent 必须遵守，不可被 AgentPrompt 覆盖。
type corePrinciplesProvider struct{}

func (p *corePrinciplesProvider) Name() string { return "core_principles" }
func (p *corePrinciplesProvider) Render(ctx context.Context) string {
	return corePrinciples
}

// toolDefinitionsProvider — 注入工具定义（note + tool_retriever 全量 + 其他概览）。
// 委托 registry.GeneratePromptWithOverview() 生成，包含 JSON 输出格式规则。
// importantRulesProvider — 注入重要规则（JSON 强制输出、调用约束、禁止时间戳）。
// 放在核心原则之后、工具定义之前，确保规则不被工具列表隔开。
type importantRulesProvider struct {
	registry *tool.Registry
}

func (p *importantRulesProvider) Name() string { return "important_rules" }
func (p *importantRulesProvider) Render(ctx context.Context) string {
	return p.registry.GenerateImportantRules()
}

type toolDefinitionsProvider struct {
	registry *tool.Registry
}

func (p *toolDefinitionsProvider) Name() string { return "tool_definitions" }
func (p *toolDefinitionsProvider) Render(ctx context.Context) string {
	return p.registry.GeneratePromptWithOverview()
}

// timeProvider — 注入当前北京时间，让 LLM 知道「现在是什么时候」。
type timeProvider struct{}

func (p *timeProvider) Name() string { return "current_time" }
func (p *timeProvider) Render(ctx context.Context) string {
	return "## 当前时间\n" + time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")
}
