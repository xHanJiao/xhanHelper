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
// 只用于注入常量内容。
type StaticProvider interface {
	Name() string
	SectionTitle() string
	Render(ctx context.Context) string
}

// ==================== ContextProvider ====================

// ContextProvider — 动态上下文注入槽（per-run 可变内容，区别于常量 StaticProvider）。
// 渲染所需数据由调用方经 BuildOptions 传入（如 StableContextXML），
// provider 只负责定位与装配，不自行取数（取数留在 orchestrator）。
type ContextProvider interface {
	Name() string
	SectionTitle() string
	Render(ctx context.Context, opts BuildOptions) string
}

// ==================== Build ====================

// BuildOptions — Build() 的入参，只含静态 prompt 所需字段。
type BuildOptions struct {
	UserID            string // 用于 PromptResolver
	AgentPrompt       string // 可选 agent 覆盖
	Mode              string // 会话模式：plan 模式下跳过工具定义段（工具全隐藏），空=work
	StableContextXML  string // 稳定上下文（画像+摘要）渲染后的 XML，会话内字节稳定，缓存友好
	ClientToolCatalog string // 客户端工具目录文本（组名+函数名，无 schema），由 orchestrator 渲染后传入，并入「工具使用」段
}

// BuildResult — Build() 的返回值。
type BuildResult struct {
	Prompt string // 静态 system prompt
}
