package promptbuilder

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/tool"
)

// ==================== Builder ====================

// BuildOptions — Build() 的入参。
// orchestrator 在 Run() 中预计算 embedding 后传入，Builder 不自行 embed。
type BuildOptions struct {
	UserID            string
	ConversationID    string
	UserMessage       string      // 用户原始输入（检索查询文本）
	Embedding         []float64   // 当前用户消息 embedding（nil 则跳过检索）
	AgentPrompt       string      // 自定义 agent 的 prompt（空则用默认 basePrompt）
	HistoryEmbeddings [][]float64 // 最近 N 条用户消息 embedding，透传给 retrieval.SearchOptions → ThemeSource 做多历史主题搜索

	// 可选检索覆盖（0 值表示使用 PromptConfig 默认值）
	Memory  retrieval.SourceConfig
	Note    retrieval.SourceConfig
	History retrieval.SourceConfig
	Theme   retrieval.SourceConfig

	// 排除已在当前消息列表中的消息 ID
	ExcludeMsgIDs map[string]bool
}

// BuildResult — Build() 的返回值。
//
// Prompt 是固定的 system prompt（行为准则 + 角色定义 + 工具定义，不含检索内容），
// 每次请求完全一致 → 100% Prompt Caching 命中。
//
// UserMessagePrefix 是检索内容的 XML 标签文本，由 orchestrator 拼接到当前轮
// user message 前面（<memories>...</memories> 等 XML 标签包裹），
// 让 system 段完全固定，提升缓存命中率。
//
// IDsByType 按 type 聚合注入资源的 ID（memory/note），
// 用于前端 done 事件和 memory_search 去重。Trace 写入 assistant message Extra。
// RetrievedItem — 检索命中的单条内容，供校验模块使用
type RetrievedItem struct {
	Type string // "memory" | "note" | "history" | "theme"
	Text string // 注入上下文中的完整文本
}

type BuildResult struct {
	Prompt            string              // 固定 system prompt（行为准则 + 角色定义 + 工具定义，不含检索内容）
	UserMessagePrefix string              // 检索内容 XML 标签文本，拼接到 user message 前面
	IDsByType         map[string][]string // type → ids，如 "memory" → [...], "note" → [...]
	Trace             *PromptTrace        // provider 构建追溯（写入 assistant message Extra）
	RetrievedItems    []RetrievedItem     // 检索命中的内容，供校验模块判断幻觉
}

// PromptBuilder — system prompt 构建器，orchestrator 的唯一入口。
// 创建后 provider 列表固定，每次 Build() 按三层模型重新构建。
type PromptBuilder struct {
	preRoleStatic   []StaticProvider // 角色定义之前的静态规则（行为准则）
	postRoleStatic  []StaticProvider // 角色定义之后的静态规则（输出规范 + 工具定义）
	basePrompt      string           // 默认角色定义
	searchProviders []SearchProvider // 动态上下文（分组渲染）
	promptResolver  PromptResolver   // 用户自定义 Prompt（备选）
}

// NewPromptBuilder 创建构建器。
//
// 依赖注入：
//   - registry: 渲染工具定义和输出规范
//   - promptResolver: 用户自定义 Prompt 解析
//   - searchProviders: 动态上下文 provider（theme / memory / note / history）
func NewPromptBuilder(
	basePrompt string,
	registry *tool.Registry,
	promptResolver PromptResolver,
	useNativeFC bool,
	searchProviders ...SearchProvider,
) *PromptBuilder {
	postRoleStatic := []StaticProvider{&currentTimeProvider{}}
	if useNativeFC {
		postRoleStatic = append([]StaticProvider{&nativeFCNoticeProvider{}}, postRoleStatic...)
	} else {
		// prompt engineering：需要 JSON 输出格式 + 工具定义文本
		postRoleStatic = append([]StaticProvider{&toolDefinitionsProvider{registry: registry}}, postRoleStatic...)
	}
	return &PromptBuilder{
		basePrompt:     basePrompt,
		promptResolver: promptResolver,
		preRoleStatic: []StaticProvider{
			&corePrinciplesProvider{},
		},
		postRoleStatic:  postRoleStatic,
		searchProviders: searchProviders,
	}
}

// Build 构建完整 system prompt。
//
// 流程:
//  1. Layer 1: 顺序渲染所有 StaticProvider，记录 trace
//  2. Layer 2: AgentPrompt > UserPrompt > basePrompt 优先级解析角色定义
//  3. Layer 3: 各 SearchProvider 独立检索，返回 SearchResult{Items, TraceItems}
//  4. 所有 Items 按 Score（向量分）降序排列
//  5. 按序拼接 Text，同时从 Type 字段提取 MemoryIDs / NoteIDs
//
// 任一 provider 出错不影响整体，该 provider 的结果被跳过（trace 仍记录为空）。
func (b *PromptBuilder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
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

	// —— 动态上下文（不再拼入 system prompt，改为生成 user message 前缀） ——
	idsByType, retrievedItems, userMsgPrefix := b.buildDynamicContext(ctx, opts, trace)
	userMsgPrefix = b.appendCurrentTimeBlock(userMsgPrefix)
	result := &BuildResult{
		Prompt:            sb.String(),
		UserMessagePrefix: userMsgPrefix,
		Trace:             trace,
		IDsByType:         idsByType,
		RetrievedItems:    retrievedItems,
	}
	return result, nil
}

// buildDynamicContext 检索所有 SearchProvider，排序、按 XML 标签分组，返回 user message 前缀文本。
// 返回的 userMsgPrefix 是由 <memories>...</memories> 等 XML 标签包裹的检索内容，
// 由 orchestrator 拼接到当前轮 user message 前面，使 system prompt 保持完全固定。
func (b *PromptBuilder) buildDynamicContext(ctx context.Context, opts BuildOptions, trace *PromptTrace) (map[string][]string, []RetrievedItem, string) {
	if len(opts.Embedding) == 0 {
		return nil, nil, ""
	}

	searchOpts := retrieval.SearchOptions{
		Query:             opts.UserMessage,
		UserID:            opts.UserID,
		ConversationID:    opts.ConversationID,
		Embedding:         opts.Embedding,
		HistoryEmbeddings: opts.HistoryEmbeddings,
		Memory:            opts.Memory,
		Note:              opts.Note,
		History:           opts.History,
		Theme:             opts.Theme,
		ExcludeMsgIDs:     opts.ExcludeMsgIDs,
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

	// —— 2. 记录 trace + 按 provider 分组渲染为 XML 标签（组内按 Score 排序） ——
	var userPrefix strings.Builder
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

		// 组内按 Score 排序
		items := s.items
		sort.Slice(items, func(i, j int) bool {
			return items[i].Score > items[j].Score
		})

		if len(items) == 0 {
			continue
		}

		xmlBlock := buildProviderXML(sp.Name(), items)
		if xmlBlock != "" {
			userPrefix.WriteString(xmlBlock)
			userPrefix.WriteString("\n")
		}
		for _, item := range items {
			idsByType[item.Type] = append(idsByType[item.Type], item.ID)
			retrievedItems = append(retrievedItems, RetrievedItem{Type: item.Type, Text: item.Text})
		}
	}
	return idsByType, retrievedItems, userPrefix.String()
}

// appendCurrentTimeBlock 在 user message 前缀末尾追加 <current_time> XML 块，
// 使 LLM 在 user message 中直接获得精确到秒的当前时间。
// 即使无检索内容（prefix 为空），也返回纯时间块。
func (b *PromptBuilder) appendCurrentTimeBlock(prefix string) string {
	now := time.Now()
	timeBlock := fmt.Sprintf("<current_time>%s 北京时间 (UTC+8)</current_time>\n",
		now.Format("2006-01-02 15:04:05 Monday"))
	if prefix != "" {
		return prefix + timeBlock
	}
	return timeBlock
}

// buildProviderXML 根据 provider 类型和 packets 生成结构化 XML 块。
func buildProviderXML(providerName string, items []retrieval.ContextPacket) string {
	switch providerName {
	case "memory":
		return buildMemoryXML(items)
	case "note":
		return buildNoteXML(items)
	case "history":
		return buildHistoryXML(items)
	case "theme":
		return buildThemeXML(items)
	}
	return ""
}

// buildMemoryXML 生成 <memories> XML 块。
func buildMemoryXML(items []retrieval.ContextPacket) string {
	w := xmlMemories{}
	for _, pkt := range items {
		m := xmlMemory{Content: pkt.Text}
		if meta, ok := pkt.Meta.(retrieval.EntityMeta); ok {
			m.Content = meta.Content
			if meta.Topic != "" {
				m.Topic = meta.Topic
			}
			if !meta.CreatedAt.IsZero() {
				m.Date = meta.CreatedAt.Format("2006年01月02日")
			}
		}
		w.Items = append(w.Items, m)
	}
	b, err := xml.MarshalIndent(w, "", "  ")
	if err != nil {
		log.Printf("[xml] buildMemoryXML: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// buildNoteXML 生成 <notes> XML 块。
func buildNoteXML(items []retrieval.ContextPacket) string {
	w := xmlNotes{}
	for _, pkt := range items {
		n := xmlNote{Content: pkt.Text}
		if meta, ok := pkt.Meta.(retrieval.EntityMeta); ok {
			n.Content = meta.Content
			if meta.Topic != "" {
				n.Topic = meta.Topic
			}
			if !meta.CreatedAt.IsZero() {
				n.Date = meta.CreatedAt.Format("2006年01月02日")
			}
		}
		w.Items = append(w.Items, n)
	}
	b, err := xml.MarshalIndent(w, "", "  ")
	if err != nil {
		log.Printf("[xml] buildNoteXML: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// buildHistoryXML 生成 <history> XML 块，每轮对话嵌套 <round><message>。
func buildHistoryXML(items []retrieval.ContextPacket) string {
	w := xmlHistory{}
	for _, pkt := range items {
		var messages []xmlHistoryMsg
		var dateStr string
		if meta, ok := pkt.Meta.(retrieval.HistoryMeta); ok {
			for _, m := range meta.Messages {
				if m == nil {
					continue
				}
				if dateStr == "" {
					dateStr = m.CreatedAt.Format("2006年01月02日")
				}
				messages = append(messages, xmlHistoryMsg{Role: m.Role, Content: m.Content})
			}
		}
		if len(messages) == 0 {
			continue
		}
		w.Rounds = append(w.Rounds, xmlHistoryRound{Messages: messages, Date: dateStr})
	}
	b, err := xml.MarshalIndent(w, "", "  ")
	if err != nil {
		log.Printf("[xml] buildHistoryXML: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// buildThemeXML 生成 <topics> XML 块。
func buildThemeXML(items []retrieval.ContextPacket) string {
	w := xmlThemes{}
	for _, pkt := range items {
		t := xmlTheme{Content: pkt.Text}
		if meta, ok := pkt.Meta.(retrieval.ThemeMeta); ok {
			t.Content = meta.Description
			if meta.StateText != "" {
				t.Description = meta.StateText
			}
		}
		w.Items = append(w.Items, t)
	}
	b, err := xml.MarshalIndent(w, "", "  ")
	if err != nil {
		log.Printf("[xml] buildThemeXML: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// resolveRolePrompt — Layer 2 角色定义解析。
// 优先级: AgentPrompt > UserPrompt > basePrompt
// AgentPrompt 是会话级选择（用户主动切换 agent），UserPrompt 是用户全局默认。
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
