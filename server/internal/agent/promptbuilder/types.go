// Package promptbuilder — system prompt 构建器
//
// 只负责静态 system prompt 拼装（行为准则 + 角色定义 + 工具定义），
// 上下文检索已迁移到 ctxflow 包。
package promptbuilder

import "context"

// PromptResolver — 查找用户自定义 system prompt。
type PromptResolver interface {
	ResolveActive(ctx context.Context, userID, promptType string) string
}

// ==================== StaticProvider ====================

// StaticProvider — Layer 1/2 使用，不需要检索，直接返回文本块。
type StaticProvider interface {
	Name() string
	SectionTitle() string
	Render(ctx context.Context) string
}

// ==================== Build ====================

// BuildOptions — Build() 的入参，只含静态 prompt 所需字段。
type BuildOptions struct {
	UserID      string // 用于 PromptResolver
	AgentPrompt string // 可选 agent 覆盖
	Mode        string // 会话模式：plan 模式下跳过工具定义段（工具全隐藏），空=work
}

// BuildResult — Build() 的返回值。
type BuildResult struct {
	Prompt string // 静态 system prompt
}
