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
	toolRules        []StaticProvider  // Layer 3.5：工具选择/动作规则（work 模式渲染）
	contextProviders []ContextProvider // 动态上下文注入（per-run 可变内容）
	basePrompt       string
	promptResolver   PromptResolver
}

// NewPromptBuilder 创建构建器。
func NewPromptBuilder(
	basePrompt string,
	registry *tool.Registry,
	promptResolver PromptResolver,
	enableUserContextTrigger bool,
) *PromptBuilder {
	preRoleStatic := []StaticProvider{&corePrinciplesProvider{}}
	// reply_form 常驻（不随请求变化，保前缀缓存稳定）
	preRoleStatic = append(preRoleStatic, &replyFormProvider{})
	// answer_length 排在最后，仍按 opts.BriefAnswer 条件注入
	preRoleStatic = append(preRoleStatic, &answerLengthProvider{})
	// 主聊天只走原生 FC：常驻渐进式工具目录（完整函数定义随请求体 tools 下发）
	postRoleStatic := []StaticProvider{&toolCatalogProvider{registry: registry}}

	// 工具规则：跟着工具目录走，不再混进行为准则（人格 ≠ 工具操作）。
	// 这些规则对应的动作在 plan 模式都被拦（task 非只读、event.enable / html.patch 写操作），
	// 因此只在 work 模式渲染，见 Build 的 Layer 3.5。
	toolRules := []StaticProvider{&subagentPriorityProvider{}, &eventOverviewProvider{}}
	if enableUserContextTrigger {
		toolRules = append(toolRules, &userContextTriggerProvider{})
	}
	// HTML 页面改版的动作选择：不依赖渐进暴露，必须在 enable html 之前可见
	// （docs/design/html-artifact/fine-grained-edit.md §4.7）
	toolRules = append(toolRules, &artifactEditProvider{})

	return &PromptBuilder{
		basePrompt:     basePrompt,
		promptResolver: promptResolver,
		preRoleStatic:  preRoleStatic,
		postRoleStatic: postRoleStatic,
		toolRules:      toolRules,
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
// （system prompt 在 |S| 处分叉）。低频动作（如用户主动开「精炼回答」）可接受，
// 高频路径不要往这里加条件。
func (b *PromptBuilder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	var sb strings.Builder

	// Layer 1: 行为准则
	for _, sp := range b.preRoleStatic {
		// 回答长度约束只在 brief_answer 轮注入；语音轮由末尾的 voice_mode 段接管
		if sp.Name() == "answer_length" && (!opts.BriefAnswer || opts.VoiceMode) {
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
	//   plan（计划模式）：只注入只读函数定义/提示，写工具对 LLM 不可见
	// 执行级为最终兜底。
	plan := opts.Mode == model.ConvModePlan
	for _, sp := range b.postRoleStatic {
		var section string
		switch {
		case plan:
			// 只读函数定义已随请求体 tools 下发，此处仅给只读提示
			section = planModeReadOnlyNotice
		default:
			section = sp.Render(ctx)
			if opts.ClientToolCatalog != "" {
				section += "\n" + opts.ClientToolCatalog
			}
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

	// Layer 3.5: 工具规则（work 模式）——子任务/事件全貌/用户信息深挖/HTML 改版。
	// 紧跟工具目录，规则贴着它管的工具；plan 模式下这些动作都被拦，不渲染免得教模型撞墙。
	if !plan {
		for _, sp := range b.toolRules {
			s := sp.Render(ctx)
			if s == "" {
				continue
			}
			if title := sp.SectionTitle(); title != "" {
				sb.WriteString("## ")
				sb.WriteString(title)
				sb.WriteString("\n\n")
			}
			sb.WriteString(s)
			sb.WriteString("\n")
		}
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

	// Layer 5: 语音约束（仅语音轮）——放最后一段，位置贴近用户消息，遵循度最高。
	// 非语音轮整段不输出，system prompt 与平时逐字节相同（前缀缓存不受影响）。
	if opts.VoiceMode {
		sp := &voiceModeProvider{}
		sb.WriteString("## ")
		sb.WriteString(sp.SectionTitle())
		sb.WriteString("\n\n")
		sb.WriteString(sp.Render(ctx))
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
