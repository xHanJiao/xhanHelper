// Package agent — SystemPromptBuilder
//
// system prompt 构建采用三层模型，解决规则散落、自定义 agent 绕过公共规则、
// 上下文扩展困难等问题。详见 docs/design/008-system-prompt-builder.md。
//
//	Layer 1: 公共规则（StaticProvider，跨 agent 不可覆盖）
//	Layer 2: 角色定义（AgentPrompt > UserPrompt > basePrompt，可覆盖）
//	Layer 3: 动态上下文（SearchProvider，按 score 降序注入）
//
// orchestrator 只需调用 builder.Build(opts)，不感知内部 provider 实现。
package agent

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// ==================== Interfaces ====================

// StaticProvider — Layer 1/2 使用，不需要检索，直接返回文本块。
// 实现示例：核心原则、工具定义、当前时间。
type StaticProvider interface {
	Name() string
	Render(ctx context.Context) string
}

// ScoredItem — SearchProvider 返回的单条上下文条目。
// Score 为 0~1，Builder 按 score 降序排列后拼入 prompt。
// Meta 字段可携带 provider 自定义元数据（如记忆 ID 列表），供 BuildResult 收集。
type ScoredItem struct {
	Content string  `json:"content"` // 注入 prompt 的文本
	Score   float64 `json:"score"`   // 0~1，越高越靠前
	Source  string  `json:"source"`  // provider 名称，调试用
	Meta    any     `json:"-"`       // 自定义元数据，不会被序列化到 prompt
}

// SearchOptions — SearchProvider.Search() 的检索参数。
// 和 BuildOptions 分离，避免 SearchProvider 依赖 BuildOptions 的所有字段。
type SearchOptions struct {
	UserID         string
	ConversationID string
	Embedding      []float64 // 用户消息的 embedding（由 orchestrator 预计算）
	DeepMode       bool
}

// SearchProvider — Layer 3 使用，需要检索 + 打分。
// 每个 provider 独立负责：检索 → 打分 → 返回 ScoredItem 列表。
// Builder 不关心内部怎么检索的，只管收集结果后按 score 排序。
type SearchProvider interface {
	Name() string
	Search(ctx context.Context, query string, opts SearchOptions) ([]ScoredItem, error)
}

// ==================== Prompt Trace ====================

// PromptTrace — system prompt 构建的完整追溯，写入 assistant message 的 Extra 字段。
type PromptTrace struct {
	Providers []ProviderTrace `json:"providers"`
}

// ProviderTrace — 单个 provider 的构建记录
type ProviderTrace struct {
	Name  string      `json:"name"`
	Type  string      `json:"type"`            // "static" | "search"
	Input *TraceInput `json:"input,omitempty"` // 检索参数（仅 search）
	Items []TraceItem `json:"items,omitempty"` // 检索结果（仅 search）
}

// TraceInput — 检索输入参数
type TraceInput struct {
	Query     string  `json:"query"`
	Threshold float64 `json:"threshold,omitempty"`
	Limit     int     `json:"limit,omitempty"`
}

// TraceItem — 单条检索结果的得分明细
type TraceItem struct {
	ID             string  `json:"id"`
	Text           string  `json:"text,omitempty"`            // 截断的文本摘要
	Score          float64 `json:"score"`                     // 综合得分
	VectorScore    float64 `json:"vector_score,omitempty"`    // 向量相似度
	KeywordScore   float64 `json:"keyword_score,omitempty"`   // 关键词匹配
	FreshnessScore float64 `json:"freshness_score,omitempty"` // 新鲜度衰减
}

// addStaticTrace 将 StaticProvider 的渲染记录追加到 trace
func (t *PromptTrace) addStatic(name string) {
	t.Providers = append(t.Providers, ProviderTrace{
		Name: name,
		Type: "static",
	})
}

// addSearchTrace 将 SearchProvider 的检索记录追加到 trace
func (t *PromptTrace) addSearch(name string, input *TraceInput, items []TraceItem) {
	t.Providers = append(t.Providers, ProviderTrace{
		Name:  name,
		Type:  "search",
		Input: input,
		Items: items,
	})
}

// ==================== Config ====================

// PromptConfig — 集中管理所有阈值、数量、开关。
// 所有魔法数字统一在此定义，调参只需改一个文件。
type PromptConfig struct {
	// —— 主题 ——
	ThemeVectorThreshold float64 // 向量匹配 primary 主题的相似度阈值（默认 0.5）
	ThemeMaxCount        int     // 最多注入主题数（默认 10）

	// —— 记忆 ——
	MemoryThreshold float64 // 记忆检索相似度阈值（默认 0.5）
	MemoryLimit     int     // 记忆检索最大条数（默认 8）

	// —— 笔记 ——
	NoteThreshold float64 // 笔记检索相似度阈值（默认 0.5）
	NoteLimit     int     // 笔记检索最大条数（默认 5）

	// —— 构建行为 ——
	SkipMemory bool // 跳过记忆检索（短输入等场景）
}

// DefaultPromptConfig 返回生产环境验证过的默认配置。
func DefaultPromptConfig() PromptConfig {
	return PromptConfig{
		ThemeVectorThreshold: 0.5,
		ThemeMaxCount:        10,
		MemoryThreshold:      0.5,
		MemoryLimit:          8,
		NoteThreshold:        0.5,
		NoteLimit:            5,
	}
}

// ==================== Builder ====================

// BuildOptions — Build() 的入参。
// orchestrator 在 Run() 中预计算 embedding 后传入，Builder 不自行 embed。
type BuildOptions struct {
	UserID         string
	ConversationID string
	UserMessage    string    // 用户原始输入（检索查询文本）
	Embedding      []float64 // 用户消息 embedding（nil 则跳过检索）
	AgentPrompt    string    // 自定义 agent 的 prompt（空则用默认 basePrompt）
	DeepMode       bool
}

// BuildResult — Build() 的返回值。
// Prompt 是完整的 system prompt，MemoryIDs/NoteIDs 用于前端 done 事件和调试。
// Trace 记录每个 provider 的贡献，写入 assistant message 的 Extra 字段供追溯。
type BuildResult struct {
	Prompt    string       // 最终 system prompt（Layer 1 + 2 + 3 拼接完成）
	MemoryIDs []string     // 注入的记忆 ID（用于前端高亮 + memory_search 排除列表）
	NoteIDs   []string     // 注入的笔记 ID（用于前端 done 事件）
	Trace     *PromptTrace // provider 构建追溯（写入 assistant message Extra）
}

// SystemPromptBuilder — system prompt 构建器，orchestrator 的唯一入口。
// 创建后 provider 列表固定，每次 Build() 按三层模型重新构建。
type SystemPromptBuilder struct {
	cfg             PromptConfig
	basePrompt      string           // Layer 2 默认角色定义
	staticProviders []StaticProvider // Layer 1 公共规则（按注册顺序渲染）
	searchProviders []SearchProvider // Layer 3 动态上下文（按 score 排序）
	promptResolver  PromptResolver   // 用户自定义 Prompt（Layer 2 备选）
	vectorStore     *VectorStore     // 主题向量检索（Layer 3 ThemeProvider 使用）
}

// NewSystemPromptBuilder 创建构建器并注册所有 provider。
//
// 依赖注入：
//   - registry: 渲染工具定义（Layer 1）
//   - retriever: 记忆+笔记检索（Layer 3）
//   - themeStateRepo: 活跃主题查询（Layer 3）
//   - convRepo: 读取信箱关联的主题 ID（Layer 3）
//   - promptResolver: 用户自定义 Prompt 解析（Layer 2）
func NewSystemPromptBuilder(
	cfg PromptConfig,
	basePrompt string,
	registry *tool.Registry,
	retriever *Retriever,
	themeStateRepo *service.ThemeStateRepo,
	convRepo *service.ConversationRepo,
	promptResolver PromptResolver,
	vectorStore *VectorStore,
) *SystemPromptBuilder {
	b := &SystemPromptBuilder{
		cfg:            cfg,
		basePrompt:     basePrompt,
		promptResolver: promptResolver,
		vectorStore:    vectorStore,
	}

	// Layer 1: 公共规则 — 按此顺序注入，所有 agent 必须携带
	b.staticProviders = []StaticProvider{
		&corePrinciplesProvider{},
		&importantRulesProvider{registry: registry},
		&toolDefinitionsProvider{registry: registry},
		&timeProvider{},
	}

	// Layer 3: 动态上下文 — 按 score 排序后注入
	b.searchProviders = []SearchProvider{
		&themeProvider{
			themeStateRepo: themeStateRepo,
			convRepo:       convRepo,
			vectorStore:    vectorStore,
			cfg:            &cfg,
		},
		&retrievalProvider{
			retriever: retriever,
			cfg:       &cfg,
		},
	}

	return b
}

// Build 构建完整 system prompt。
//
// 流程:
//  1. Layer 1: 顺序渲染所有 StaticProvider，自动记录 trace
//  2. Layer 2: AgentPrompt > UserPrompt > basePrompt 优先级解析
//  3. Layer 3: 各 SearchProvider 独立检索，收集结果 + trace
//  4. 所有 ScoredItem 按 Score 降序排列
//  5. 依次拼接，同时收集 MemoryIDs / NoteIDs / Trace 到 BuildResult
//
// 任一 provider 出错不影响整体，该 provider 的结果被跳过（trace 仍记录为空）。
func (b *SystemPromptBuilder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	var sb strings.Builder
	trace := &PromptTrace{}

	// —— Layer 1: 公共规则（不可覆盖） ——
	for _, sp := range b.staticProviders {
		if s := sp.Render(ctx); s != "" {
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		trace.addStatic(sp.Name())
	}

	// —— Layer 2: 角色定义（可覆盖） ——
	sb.WriteString("\n===== 角色定义 =====\n")
	rolePrompt := b.resolveRolePrompt(ctx, opts)
	sb.WriteString(rolePrompt)
	sb.WriteString("\n")

	// —— Layer 3: 动态上下文（按 score 排序） ——
	searchOpts := SearchOptions{
		UserID:         opts.UserID,
		ConversationID: opts.ConversationID,
		Embedding:      opts.Embedding,
		DeepMode:       opts.DeepMode,
	}

	// 收集所有 provider 的结果
	var allItems []ScoredItem
	for _, sp := range b.searchProviders {
		items, err := sp.Search(ctx, opts.UserMessage, searchOpts)
		if err != nil {
			// provider 失败 recording 空 trace
			trace.addSearch(sp.Name(), &TraceInput{Query: opts.UserMessage}, nil)
			continue
		}
		// 从 Meta 中提取 trace items（由 provider 自己填充）
		traceItems := extractTraceItems(items)
		trace.addSearch(sp.Name(), &TraceInput{Query: opts.UserMessage}, traceItems)
		allItems = append(allItems, items...)
	}

	// 按 score 降序：主题天然排在记忆/笔记前面
	sort.Slice(allItems, func(i, j int) bool {
		return allItems[i].Score > allItems[j].Score
	})

	// 拼接结果 + 收集元数据
	result := &BuildResult{Trace: trace}
	for _, item := range allItems {
		sb.WriteString(item.Content)
		// 从 retrieval provider 的 Meta 中提取记忆/笔记 ID
		if metaMap, ok := item.Meta.(map[string]any); ok {
			if memIDs, ok := metaMap["memory_ids"].([]string); ok {
				result.MemoryIDs = append(result.MemoryIDs, memIDs...)
			}
			if noteIDs, ok := metaMap["note_ids"].([]string); ok {
				result.NoteIDs = append(result.NoteIDs, noteIDs...)
			}
		}
	}

	result.Prompt = sb.String()
	return result, nil
}

// extractTraceItems 从 ScoredItem 的 Meta 中提取 trace_items。
// provider 负责在 Search() 时将 TraceItem 列表写入 item.Meta["trace_items"]。
func extractTraceItems(items []ScoredItem) []TraceItem {
	var all []TraceItem
	for _, item := range items {
		if metaMap, ok := item.Meta.(map[string]any); ok {
			if traceItems, ok := metaMap["trace_items"].([]TraceItem); ok {
				all = append(all, traceItems...)
			}
		}
	}
	// 如果没有明细 trace_items，从 ScoredItem 本身构造摘要
	if len(all) == 0 {
		for _, item := range items {
			all = append(all, TraceItem{
				Text:  truncateText(item.Content, 80),
				Score: item.Score,
			})
		}
	}
	return all
}

// truncateText 截断文本到 maxLen 个字符
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
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

// ==================== Layer 1 — Static Providers ====================

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
	return "当前时间: " + time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")
}

// ==================== Layer 3 — Theme Provider ====================

// themeProvider — 主题上下文注入。
//
// 匹配策略（按优先级）:
//  1. 信箱关联（ConversationCtx.ThemeID） → primary, score=1.0
//  2. 向量匹配（user message embedding 相似度 > 阈值） → primary, score=0.95
//  3. 其他活跃主题 → 轻量注入, score=0.85
type themeProvider struct {
	themeStateRepo *service.ThemeStateRepo   // 查询活跃主题列表
	convRepo       *service.ConversationRepo // 读取信箱关联的主题 ID
	vectorStore    *VectorStore              // 主题向量检索
	cfg            *PromptConfig             // 阈值配置
}

func (p *themeProvider) Name() string { return "theme" }
func (p *themeProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]ScoredItem, error) {
	activeThemes, err := p.themeStateRepo.ListActive(ctx, opts.UserID)
	if err != nil {
		return nil, fmt.Errorf("list active themes: %w", err)
	}
	if len(activeThemes) == 0 {
		return nil, nil
	}

	linkedThemeID := p.getLinkedThemeID(ctx, opts.ConversationID)

	// 向量匹配：返回达标 ID 集合 + 全部原始结果（含未达标的，供 trace）
	vectorMatched, vectorResults := p.matchByVector(ctx, opts)

	// 构建完整 trace（含所有活跃主题 + 向量原始分数）
	traceItems := p.buildThemeTrace(activeThemes, vectorResults, linkedThemeID)

	items := make([]ScoredItem, 0, len(activeThemes))
	firstItem := true
	for _, t := range activeThemes {
		isLinked := t.ID == linkedThemeID
		isVectorMatch := vectorMatched[t.ID]

		// 只注入命中的主题，未命中不注入（避免无关主题干扰）
		if !isLinked && !isVectorMatch {
			continue
		}

		matchReason := "vector"
		if isLinked {
			matchReason = "linked"
		}

		item := p.renderTheme(t, true, matchReason)
		if firstItem {
			item.Meta = map[string]any{
				"theme_id":    t.ID,
				"trace_items": traceItems,
			}
			firstItem = false
		}
		items = append(items, item)
	}
	return items, nil
}

// vectorMatchResult — 单条向量搜索结果
type vectorMatchResult struct {
	ThemeID string
	Score   float64
	Passed  bool
}

// matchByVector 在 VectorStore 中搜索主题向量。
// 返回: 达标的主题 ID 集合 + 全部原始搜索结果（含未达标的 near-miss）
func (p *themeProvider) matchByVector(_ context.Context, opts SearchOptions) (map[string]bool, []vectorMatchResult) {
	matched := make(map[string]bool)
	var raw []vectorMatchResult
	if p.vectorStore == nil || len(opts.Embedding) == 0 {
		return matched, raw
	}

	threshold := p.cfg.ThemeVectorThreshold
	if threshold <= 0 {
		threshold = 0.5
	}

	results := p.vectorStore.Search(opts.Embedding, 20, func(entry vectorEntry) bool {
		match := entry.Metadata["type"] == "theme" && entry.Metadata["user_id"] == opts.UserID
		return match
	})

	log.Printf("[theme] vector search: embedding_len=%d, user_id=%s, total_vectors=%d, matched=%d, threshold=%.2f",
		len(opts.Embedding), opts.UserID, p.vectorStore.Size(), len(results), threshold)
	for i, r := range results {
		if i >= 3 {
			break
		}
		log.Printf("[theme]   result[%d]: theme_id=%s score=%.4f", i, r.Metadata["theme_id"], r.Score)
	}

	for _, r := range results {
		themeID := r.Metadata["theme_id"]
		if themeID == "" {
			continue
		}
		passed := r.Score >= threshold
		raw = append(raw, vectorMatchResult{ThemeID: themeID, Score: r.Score, Passed: passed})
		if passed {
			matched[themeID] = true
		}
	}
	return matched, raw
}

// buildThemeTrace 构建主题 provider 的完整 trace。
// 包含所有活跃主题 + 原始向量搜索结果（含未达阈值项，标注 vector_below_threshold）。
func (p *themeProvider) buildThemeTrace(
	activeThemes []model.ThemeState,
	vectorResults []vectorMatchResult,
	linkedThemeID string,
) []TraceItem {
	vecScoreMap := make(map[string]float64)
	for _, vr := range vectorResults {
		vecScoreMap[vr.ThemeID] = vr.Score
	}

	items := make([]TraceItem, 0, len(activeThemes))
	for _, t := range activeThemes {
		vecScore, hasVec := vecScoreMap[t.ID]
		var reason string
		var score float64
		switch {
		case t.ID == linkedThemeID:
			reason = "linked"
			score = 1.0
		case hasVec && vecScore >= p.cfg.ThemeVectorThreshold:
			reason = fmt.Sprintf("vector(%.3f)", vecScore)
			score = 0.95
		case hasVec:
			reason = fmt.Sprintf("vector_below_threshold(%.3f)", vecScore)
			score = 0.85
		default:
			reason = "other_active"
			score = 0.85
		}
		items = append(items, TraceItem{
			ID:    t.ID,
			Text:  fmt.Sprintf("%s (%s)", t.Name, reason),
			Score: score,
		})
	}
	return items
}

func (p *themeProvider) getLinkedThemeID(ctx context.Context, convID string) string {
	if p.convRepo == nil || convID == "" {
		return ""
	}
	extra, err := p.convRepo.GetExtra(ctx, convID)
	if err != nil || extra == nil || extra.Context == nil {
		return ""
	}
	return extra.Context.ThemeID
}

func (p *themeProvider) renderTheme(t model.ThemeState, isPrimary bool, matchReason string) ScoredItem {
	score := 0.85
	if isPrimary {
		score = 1.0
		if matchReason == "vector" {
			score = 0.95
		}
	}

	var content string
	if t.Description != "" {
		content = fmt.Sprintf("\n\n## 主题：%s\n描述：%s\n%s", t.Name, t.Description, t.StateText)
	} else {
		content = fmt.Sprintf("\n\n## 主题：%s\n%s", t.Name, t.StateText)
	}

	if t.Metadata != nil && *t.Metadata != "" && isPrimary {
		content += fmt.Sprintf("\n补充信息：%s", *t.Metadata)
	}

	return ScoredItem{
		Content: content,
		Score:   score,
		Source:  "theme",
	}
}

// ==================== Layer 3 — Retrieval Provider ====================

// retrievalProvider — 记忆 + 笔记检索，统一打包为一个 SearchProvider。
//
// 当前 retriever 内部已将记忆和笔记合并检索、统一打分（记忆有多时间衰减，笔记无），
// 所以这里作为一个 provider 返回，score 固定 0.8。
// 未来可以拆成独立的 MemoryProvider 和 NoteProvider，各自控制阈值和条数。
type retrievalProvider struct {
	retriever *Retriever
	cfg       *PromptConfig
}

func (p *retrievalProvider) Name() string { return "retrieval" }

func (p *retrievalProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]ScoredItem, error) {
	// 跳过条件: 配置关闭 / 无 retriever / 无 embedding / 空查询
	if p.cfg.SkipMemory || p.retriever == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || query == "" {
		return nil, nil
	}

	threshold := p.cfg.MemoryThreshold
	if threshold <= 0 {
		threshold = 0.5
	}
	limit := p.cfg.MemoryLimit
	if limit <= 0 {
		limit = 8
	}

	// 委托 retriever 进行向量检索 + 关键词匹配 + 时间衰减综合打分
	packets, memIDs, noteIDs := p.retriever.Retrieve(
		ctx, opts.Embedding, query,
		opts.UserID, opts.ConversationID,
		opts.DeepMode, threshold, limit,
	)

	// retriever 内部已按综合打分排序，FormatContext 直接拼接文本
	memText := p.retriever.FormatContext(packets)
	if memText == "" {
		return nil, nil
	}

	// 从 packets 构建 per-item trace（保留旧 retrieval_scores 的细粒度）
	traceItems := make([]TraceItem, 0, len(packets))
	for _, pkt := range packets {
		traceItems = append(traceItems, TraceItem{
			ID:           pkt.ID,
			Text:         truncateText(pkt.Text, 100),
			Score:        pkt.Score,        // 向量相似度
			KeywordScore: pkt.KeywordScore, // 关键词匹配 boost
		})
	}

	return []ScoredItem{
		{
			Content: "\n\n===== 关于用户的信息 =====\n" + memText,
			Score:   0.8, // 固定 score，排在主题（0.85~1.0）之后
			Source:  "retrieval",
			Meta: map[string]any{
				"memory_ids":  memIDs,
				"note_ids":    noteIDs,
				"trace_items": traceItems,
			},
		},
	}, nil
}
