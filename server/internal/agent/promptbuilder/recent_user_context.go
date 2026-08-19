package promptbuilder

import (
	"context"
	"strings"
)

// recentUserContextProvider — 新窗口（SkipHistory）注入最近用户消息段。
// 只含用户原文 + 时间戳（无 XMLContext/tool 调用），让 LLM 知道用户之前最后说了什么，
// 又不被历史 tool 调用细节浪费 token。内容随每条消息变化，放在稳定上下文之后，
// 不破坏前缀稳定段的 prompt cache 命中。
type recentUserContextProvider struct{}

func (p *recentUserContextProvider) Name() string { return "recent_user_context" }

func (p *recentUserContextProvider) SectionTitle() string { return "用户最近消息" }

func (p *recentUserContextProvider) Render(ctx context.Context, opts BuildOptions) string {
	return strings.TrimSpace(opts.RecentUserContext)
}
