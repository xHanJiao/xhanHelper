package promptbuilder

import (
	"context"
	"strings"

	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// PromptBuilder — system prompt 构建器，只负责静态 prompt 拼装。
// 上下文检索已迁移到 ctxflow 包。
type PromptBuilder struct {
	preRoleStatic    []StaticProvider
	postRoleStatic   []StaticProvider
	contextProviders []ContextProvider // 动态上下文注入（per-run 可变内容）
	basePrompt       string
	promptResolver   PromptResolver
}

// NewPromptBuilder 创建构建器。
func NewPromptBuilder(
	basePrompt string,
	registry *tool.Registry,
	promptResolver PromptResolver,
	useNativeFC bool,
	enableUserContextTrigger bool,
) *PromptBuilder {
	preRoleStatic := []StaticProvider{&corePrinciplesProvider{}}
	if enableUserContextTrigger {
		preRoleStatic = append(preRoleStatic, &userContextTriggerProvider{})
	}
	// reply_form 常驻（不随请求变化，保前缀缓存稳定）
	// 必须排在 user_context_trigger 之后：那条 "9. 用户信息深挖" 是行为准则编号列表的续写，
	// 中间插一个带标题的章节会把它截断
	preRoleStatic = append(preRoleStatic, &replyFormProvider{})
	// answer_length 排在最后，仍按 opts.BriefAnswer 条件注入
	preRoleStatic = append(preRoleStatic, &answerLengthProvider{})
	postRoleStatic := []StaticProvider{}
	if useNativeFC {
		postRoleStatic = append([]StaticProvider{&toolCatalogProvider{registry: registry}}, postRoleStatic...)
	} else {
		postRoleStatic = append([]StaticProvider{&toolDefinitionsProvider{registry: registry}}, postRoleStatic...)
	}
	return &PromptBuilder{
		basePrompt:     basePrompt,
		promptResolver: promptResolver,
		preRoleStatic:  preRoleStatic,
		postRoleStatic: postRoleStatic,
		contextProviders: []ContextProvider{
			&stableContextProvider{},
		},
	}
}

// Build 构建静态 system prompt（行为准则 + 角色定义 + 工具定义）。
//
// 前缀缓存说明：本函数输出是 system prompt，会话内字节稳定才命中 DeepSeek 前缀缓存。
// 现有两处按请求变化的判断（plan / ClientToolCatalog）与新增的 BriefAnswer 都属于
// 「刻意为之的逐请求变化」：带 brief_answer 的那一轮及其后一轮会丢失消息历史的缓存复用
// （system prompt 在 |S| 处分叉）。低频动作（用户主动点「问 AI」）可接受，
// 高频路径不要往这里加条件。
func (b *PromptBuilder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	var sb strings.Builder

	// Layer 1: 行为准则
	for _, sp := range b.preRoleStatic {
		// 回答长度约束只在 brief_answer 轮注入；不注入时整段跳过，prompt 与平时逐字节相同
		if sp.Name() == "answer_length" && !opts.BriefAnswer {
			continue
		}
		if s := sp.Render(ctx); s != "" {
			if title := sp.SectionTitle(); title != "" {
				sb.WriteString("## ")
				sb.WriteString(title)
				sb.WriteString("\n\n")
			}
			sb.WriteString(s)
			sb.WriteString("\n")
		}
	}

	// Layer 2: 角色定义
	sb.WriteString("\n## 角色定义\n\n")
	rolePrompt := b.resolveRolePrompt(ctx, opts)
	for _, line := range strings.Split(rolePrompt, "\n") {
		sb.WriteString("> ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Layer 3: 输出规范 + 工具定义
	//   plan（普通计划模式）：只注入只读函数定义/提示，写工具对 LLM 不可见
	//   strict（严格计划模式）：不注入任何工具定义/目录，仅给"无工具"提示
	// 两者执行级均为最终兜底。
	plan := opts.Mode == model.ConvModePlan
	strict := opts.Mode == model.ConvModeStrict
	for _, sp := range b.postRoleStatic {
		var section string
		switch {
		case strict && (sp.Name() == "tool_definitions" || sp.Name() == "tool_catalog"):
			// 严格档：两套工具段都换成"无工具"提示（请求体亦不下发 tools）
			section = strictModeNotice
		case plan && sp.Name() == "tool_definitions":
			// prompt-eng 路径：仅渲染只读函数定义
			if p, ok := sp.(*toolDefinitionsProvider); ok {
				section = p.registry.GenerateReadOnlyPrompt()
			}
		case plan && sp.Name() == "tool_catalog":
			// native FC 路径：只读函数定义已随请求体 tools 下发，此处仅给只读提示
			section = planModeReadOnlyNotice
		case sp.Name() == "tool_catalog":
			section = sp.Render(ctx)
			if opts.ClientToolCatalog != "" {
				section += "\n" + opts.ClientToolCatalog
			}
		default:
			section = sp.Render(ctx)
		}
		if section == "" {
			continue
		}
		if title := sp.SectionTitle(); title != "" {
			sb.WriteString("## ")
			sb.WriteString(title)
			sb.WriteString("\n\n")
		}
		sb.WriteString(section)
		sb.WriteString("\n")
	}

	// Layer 4: 动态上下文（稳定上下文，plan 模式同样注入——讨论也需要画像）
	for _, cp := range b.contextProviders {
		section := cp.Render(ctx, opts)
		if section == "" {
			continue
		}
		if title := cp.SectionTitle(); title != "" {
			sb.WriteString("## ")
			sb.WriteString(title)
			sb.WriteString("\n\n")
		}
		sb.WriteString(section)
		sb.WriteString("\n")
	}

	return &BuildResult{Prompt: sb.String()}, nil
}

// resolveRolePrompt — 角色定义解析。优先级: AgentPrompt > UserPrompt > basePrompt。
func (b *PromptBuilder) resolveRolePrompt(ctx context.Context, opts BuildOptions) string {
	if opts.AgentPrompt != "" {
		return opts.AgentPrompt
	}
	if b.promptResolver != nil {
		if custom := b.promptResolver.ResolveActive(ctx, opts.UserID, "flash"); custom != "" {
			return custom
		}
	}
	return b.basePrompt
}
