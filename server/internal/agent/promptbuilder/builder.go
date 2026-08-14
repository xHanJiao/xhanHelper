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
	preRoleStatic  []StaticProvider
	postRoleStatic []StaticProvider
	basePrompt     string
	promptResolver PromptResolver
}

// NewPromptBuilder 创建构建器。
func NewPromptBuilder(
	basePrompt string,
	registry *tool.Registry,
	promptResolver PromptResolver,
	useNativeFC bool,
) *PromptBuilder {
	postRoleStatic := []StaticProvider{}
	if useNativeFC {
		postRoleStatic = append([]StaticProvider{&nativeFCNoticeProvider{}}, postRoleStatic...)
	} else {
		postRoleStatic = append([]StaticProvider{&toolDefinitionsProvider{registry: registry}}, postRoleStatic...)
	}
	return &PromptBuilder{
		basePrompt:     basePrompt,
		promptResolver: promptResolver,
		preRoleStatic: []StaticProvider{
			&corePrinciplesProvider{},
		},
		postRoleStatic: postRoleStatic,
	}
}

// Build 构建静态 system prompt（行为准则 + 角色定义 + 工具定义）。
func (b *PromptBuilder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	var sb strings.Builder

	// Layer 1: 行为准则
	for _, sp := range b.preRoleStatic {
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
	// plan 模式：工具全隐藏——prompt-eng 跳过工具定义段；native FC 的通知替换为"无工具"声明
	plan := opts.Mode == model.ConvModePlan
	for _, sp := range b.postRoleStatic {
		var section string
		switch {
		case plan && sp.Name() == "tool_definitions":
			continue
		case plan && sp.Name() == "native_fc_notice":
			section = planModeNoToolsNotice
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
