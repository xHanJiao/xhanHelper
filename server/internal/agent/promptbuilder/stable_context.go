package promptbuilder

import (
	"context"
	"strings"
)

// stableContextProvider — 稳定上下文注入：渲染 BuildOptions.StableContextXML。
// 画像（profile）与近期摘要（summary）会话内不变，放进 system prompt 可全程命中
// Prompt Cache（user 消息前缀里的可变检索内容保留在消息侧）。
type stableContextProvider struct{}

func (p *stableContextProvider) Name() string { return "stable_context" }

func (p *stableContextProvider) SectionTitle() string { return "稳定上下文" }

func (p *stableContextProvider) Render(ctx context.Context, opts BuildOptions) string {
	return strings.TrimSpace(opts.StableContextXML)
}
