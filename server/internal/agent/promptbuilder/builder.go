package promptbuilder

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/tool"
)

// ==================== Builder ====================

// BuildOptions — Build() 的入参。
// orchestrator 在 Run() 中预计算 embedding 后传入，Builder 不自行 embed。
type BuildOptions struct {
	UserID         string
	ConversationID string
	UserMessage    string    // 用户原始输入（检索查询文本）
	Embedding      []float64 // 用户消息 embedding（nil 则跳过检索）
	AgentPrompt    string    // 自定义 agent 的 prompt（空则用默认 basePrompt）

	// 可选检索覆盖（0 值表示使用 PromptConfig 默认值）
	MemoryThreshold  float64
	MemoryLimit      int
	NoteThreshold    float64
	NoteLimit        int
	HistoryThreshold float64
	HistoryLimit     int
}

// BuildResult — Build() 的返回值。
// Prompt 是完整的 system prompt，IDsByType 按 type 聚合注入资源的 ID（memory/note），
// 用于前端 done 事件和 memory_search 去重。Trace 写入 assistant message Extra。
// RetrievedItem — 检索命中的单条内容，供校验模块使用
type RetrievedItem struct {
	Type string // "memory" | "note" | "history" | "theme"
	Text string // 注入 system prompt 的完整文本
}

type BuildResult struct {
	Prompt         string              // 最终 system prompt（Layer 1 + 2 + 3 拼接完成）
	IDsByType      map[string][]string // type → ids，如 "memory" → [...], "note" → [...]
	Trace          *PromptTrace        // provider 构建追溯（写入 assistant message Extra）
	RetrievedItems []RetrievedItem     // 检索命中的内容，供校验模块判断幻觉
}

// SystemPromptBuilder — system prompt 构建器，orchestrator 的唯一入口。
// 创建后 provider 列表固定，每次 Build() 按三层模型重新构建。
type SystemPromptBuilder struct {
	preRoleStatic   []StaticProvider // 角色定义之前的静态规则（行为准则）
	postRoleStatic  []StaticProvider // 角色定义之后的静态规则（输出规范 + 工具定义）
	basePrompt      string           // 默认角色定义
	searchProviders []SearchProvider // 动态上下文（分组渲染）
	promptResolver  PromptResolver   // 用户自定义 Prompt（备选）
}

// NewSystemPromptBuilder 创建构建器。
//
// 依赖注入：
//   - registry: 渲染工具定义和输出规范
//   - promptResolver: 用户自定义 Prompt 解析
//   - searchProviders: 动态上下文 provider（theme / memory / note / history）
func NewSystemPromptBuilder(
	basePrompt string,
	registry *tool.Registry,
	promptResolver PromptResolver,
	searchProviders ...SearchProvider,
) *SystemPromptBuilder {
	return &SystemPromptBuilder{
		basePrompt:     basePrompt,
		promptResolver: promptResolver,
		preRoleStatic: []StaticProvider{
			&corePrinciplesProvider{},
			&currentTimeProvider{},
		},
		postRoleStatic: []StaticProvider{
			&toolDefinitionsProvider{registry: registry},
		},
		searchProviders: searchProviders,
	}
}

// Build 构建完整 system prompt。
//
// 流程:
//  1. Layer 1: 顺序渲染所有 StaticProvider，记录 trace
//  2. Layer 2: AgentPrompt > UserPrompt > basePrompt 优先级解析角色定义
//  3. Layer 3: 各 SearchProvider 独立检索，返回 SearchResult{Items, TraceItems}
//  4. 所有 Items 按 FinalScore 降序排列（各 provider 的真实 ranker 分，非写死）
//  5. 按序拼接 Text，同时从 Type 字段提取 MemoryIDs / NoteIDs
//
// 任一 provider 出错不影响整体，该 provider 的结果被跳过（trace 仍记录为空）。
func (b *SystemPromptBuilder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	var sb strings.Builder
	trace := &PromptTrace{}

	// —— 行为准则（角色定义之前） ——
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
		trace.addStatic(sp.Name())
	}

	// —— 角色定义 ——
	// 用 blockquote 包裹，避免 agent 内部 markdown 标题与外层结构冲突
	sb.WriteString("\n## 角色定义\n\n")
	rolePrompt := b.resolveRolePrompt(ctx, opts)
	for _, line := range strings.Split(rolePrompt, "\n") {
		sb.WriteString("> ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// —— 输出规范 + 工具定义（角色定义之后） ——
	for _, sp := range b.postRoleStatic {
		if s := sp.Render(ctx); s != "" {
			if title := sp.SectionTitle(); title != "" {
				sb.WriteString("## ")
				sb.WriteString(title)
				sb.WriteString("\n\n")
			}
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		trace.addStatic(sp.Name())
	}

	// —— 动态上下文 ——
	idsByType, retrievedItems := b.buildDynamicContext(ctx, opts, &sb, trace)
	result := &BuildResult{
		Prompt:         sb.String(),
		Trace:          trace,
		IDsByType:      idsByType,
		RetrievedItems: retrievedItems,
	}
	return result, nil
}

// buildDynamicContext 检索所有 SearchProvider，排序、分组、渲染到 sb，返回 type→ids 映射和检索命中内容。
func (b *SystemPromptBuilder) buildDynamicContext(ctx context.Context, opts BuildOptions, sb *strings.Builder, trace *PromptTrace) (map[string][]string, []RetrievedItem) {
	if len(opts.Embedding) == 0 {
		return nil, nil
	}

	searchOpts := SearchOptions{
		Query:            opts.UserMessage,
		UserID:           opts.UserID,
		ConversationID:   opts.ConversationID,
		Embedding:        opts.Embedding,
		MemoryThreshold:  opts.MemoryThreshold,
		MemoryLimit:      opts.MemoryLimit,
		NoteThreshold:    opts.NoteThreshold,
		NoteLimit:        opts.NoteLimit,
		HistoryThreshold: opts.HistoryThreshold,
		HistoryLimit:     opts.HistoryLimit,
	}

	// —— 1. 并发检索 ——
	type slot struct {
		items      []retrieval.ContextPacket
		traceItems []TraceItem
		err        error
	}
	slots := make([]slot, len(b.searchProviders))
	var wg sync.WaitGroup
	for i, sp := range b.searchProviders {
		wg.Add(1)
		go func(i int, sp SearchProvider) {
			defer wg.Done()
			sr, err := sp.Search(ctx, searchOpts)
			if err != nil {
				slots[i].err = err
				return
			}
			slots[i].items = sr.Items
			slots[i].traceItems = sr.TraceItems
		}(i, sp)
	}
	wg.Wait()

	// —— 2. 记录 trace + 按 provider 分组渲染（组内按 FinalScore 排序） ——
	sb.WriteString("\n## 上下文信息\n")
	idsByType := make(map[string][]string)
	var retrievedItems []RetrievedItem
	for i, sp := range b.searchProviders {
		s := slots[i]
		if s.err != nil {
			log.Printf("[prompt] %s search error: %v", sp.Name(), s.err)
			trace.addSearch(sp.Name(), &TraceInput{Query: opts.UserMessage}, nil, s.err)
			continue
		}
		trace.addSearch(sp.Name(), &TraceInput{Query: opts.UserMessage}, s.traceItems, nil)

		// 组内按 FinalScore 排序
		items := s.items
		sort.Slice(items, func(i, j int) bool {
			return items[i].FinalScore > items[j].FinalScore
		})

		header := sp.SectionHeader()
		var group strings.Builder
		for _, item := range items {
			group.WriteString(item.Text)
			idsByType[item.Type] = append(idsByType[item.Type], item.ID)
			retrievedItems = append(retrievedItems, RetrievedItem{Type: item.Type, Text: item.Text})
		}
		if group.Len() > 0 && header != "" {
			sb.WriteString(header)
		}
		sb.WriteString(group.String())
	}
	return idsByType, retrievedItems
}

// resolveRolePrompt — Layer 2 角色定义解析。
// 优先级: AgentPrompt > UserPrompt > basePrompt
// AgentPrompt 是会话级选择（用户主动切换 agent），UserPrompt 是用户全局默认。
func (b *SystemPromptBuilder) resolveRolePrompt(ctx context.Context, opts BuildOptions) string {
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
