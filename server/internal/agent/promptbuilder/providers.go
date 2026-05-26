package promptbuilder

import (
	"context"
	"fmt"
	"log"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util"
)

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
	vectorStore    retrieval.VectorStore     // 主题向量检索
	cfg            *PromptConfig             // 阈值配置
}

func (p *themeProvider) Name() string          { return "theme" }
func (p *themeProvider) SectionHeader() string { return "\n### 用户身上正在发生的事\n" }
func (p *themeProvider) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	activeThemes, err := p.themeStateRepo.ListActive(ctx, opts.UserID)
	if err != nil {
		return nil, fmt.Errorf("list active themes: %w", err)
	}
	if len(activeThemes) == 0 {
		return nil, nil
	}

	linkedThemeID := p.getLinkedThemeID(ctx, opts.ConversationID)

	vectorMatched, allVectorResults := p.matchByVector(ctx, opts)
	vectorScores := make(map[string]float64, len(allVectorResults))
	for _, vr := range allVectorResults {
		vectorScores[vr.ThemeID] = vr.Score
	}

	var items []retrieval.ContextPacket
	var traceItems []TraceItem

	for _, t := range activeThemes {
		isLinked := t.ID == linkedThemeID
		isVectorMatch := vectorMatched[t.ID]
		vecScore, hasVec := vectorScores[t.ID]

		if !isLinked && !isVectorMatch {
			// 未选中：near-miss trace
			reason := "other_active"
			traceScore := 0.0
			if hasVec {
				reason = fmt.Sprintf("vector_below_threshold(%.3f)", vecScore)
				traceScore = vecScore
			}
			traceItems = append(traceItems, TraceItem{
				ID:            t.ID,
				Text:          util.TruncateContent(t.Name, 80),
				Score:         traceScore,
				VectorScore:   vecScore,
				DiscardReason: reason,
			})
			continue
		}

		// 已选中：构建 item + trace
		themeScore := vecScore
		if isLinked {
			themeScore = 1.0 // 信箱关联是明确的用户意图信号
		}

		items = append(items, p.renderTheme(t, themeScore))
		traceItems = append(traceItems, TraceItem{
			ID:          t.ID,
			Text:        util.TruncateContent(t.Name, 80),
			Score:       themeScore,
			VectorScore: vecScore,
		})
	}

	return &SearchResult{Items: items, TraceItems: traceItems}, nil
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

	results := p.vectorStore.Search(opts.Embedding, 20, func(entry retrieval.Entry) bool {
		match := entry.Metadata["type"] == "theme" && entry.Metadata["user_id"] == opts.UserID
		return match
	})

	log.Printf("[theme] vector search: embedding_len=%d, user_id=%s, total_vectors=%d, matched=%d, threshold=%.2f",
		len(opts.Embedding), opts.UserID, p.vectorStore.Size(), len(results), threshold)
	for i, r := range results {
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

func (p *themeProvider) renderTheme(t model.ThemeState, themeScore float64) retrieval.ContextPacket {
	var content string
	if t.Description != "" {
		content = fmt.Sprintf("\n\n【主题】%s\n描述：%s\n%s", t.Name, t.Description, t.StateText)
	} else {
		content = fmt.Sprintf("\n\n【主题】%s\n%s", t.Name, t.StateText)
	}

	if t.Metadata != nil && *t.Metadata != "" {
		content += fmt.Sprintf("\n补充信息：%s", *t.Metadata)
	}

	return retrieval.ContextPacket{
		Text:       content,
		FinalScore: themeScore,
		Type:       "theme",
	}
}

// ==================== Layer 3 — Retrieval Providers ====================
//
// memoryProvider / noteProvider / historyProvider 各自独立检索一个数据源。
// 每条命中作为一个 ContextPacket，以 ranker FinalScore 参与跨 provider 排序。
// trace + 元数据（memory_ids / note_ids）挂载在首个 packet 的 Meta 上。

// memoryProvider — 记忆检索。
type memoryProvider struct {
	source retrieval.ContextSource
	cfg    *PromptConfig
}

func (p *memoryProvider) Name() string          { return "memory" }
func (p *memoryProvider) SectionHeader() string { return "\n### 相关记忆\n" }

func (p *memoryProvider) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || opts.Query == "" {
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

	packets, nearMiss, err := p.source.Fetch(ctx, opts.Embedding, opts.Query, opts.UserID, opts.ConversationID, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("memory fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// noteProvider — 笔记检索。
type noteProvider struct {
	source retrieval.ContextSource
	cfg    *PromptConfig
}

func (p *noteProvider) Name() string          { return "note" }
func (p *noteProvider) SectionHeader() string { return "\n### 相关笔记\n" }

func (p *noteProvider) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || opts.Query == "" {
		return nil, nil
	}

	threshold := p.cfg.NoteThreshold
	if threshold <= 0 {
		threshold = 0.5
	}
	limit := p.cfg.NoteLimit
	if limit <= 0 {
		limit = 5
	}

	packets, nearMiss, err := p.source.Fetch(ctx, opts.Embedding, opts.Query, opts.UserID, opts.ConversationID, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("note fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// historyProvider — 历史消息检索。
type historyProvider struct {
	source retrieval.ContextSource
	cfg    *PromptConfig
}

func (p *historyProvider) Name() string          { return "history" }
func (p *historyProvider) SectionHeader() string { return "\n### 历史相关对话\n" }

func (p *historyProvider) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || opts.Query == "" {
		return nil, nil
	}

	threshold := p.cfg.HistoryThreshold
	if threshold <= 0 {
		threshold = 0.8
	}
	limit := p.cfg.HistoryLimit
	if limit <= 0 {
		limit = 5
	}

	packets, nearMiss, err := p.source.Fetch(ctx, opts.Embedding, opts.Query, opts.UserID, opts.ConversationID, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("history fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// ==================== Provider Factory Functions ====================

func NewThemeProvider(themeStateRepo *service.ThemeStateRepo, convRepo *service.ConversationRepo, vectorStore retrieval.VectorStore, cfg *PromptConfig) SearchProvider {
	return &themeProvider{themeStateRepo: themeStateRepo, convRepo: convRepo, vectorStore: vectorStore, cfg: cfg}
}

func NewMemoryProvider(source retrieval.ContextSource, cfg *PromptConfig) SearchProvider {
	return &memoryProvider{source: source, cfg: cfg}
}

func NewNoteProvider(source retrieval.ContextSource, cfg *PromptConfig) SearchProvider {
	return &noteProvider{source: source, cfg: cfg}
}

func NewHistoryProvider(source retrieval.ContextSource, cfg *PromptConfig) SearchProvider {
	return &historyProvider{source: source, cfg: cfg}
}

// buildTraceItems 将选中的 packets 和 near-miss 统一构建为 TraceItem 列表
func buildTraceItems(packets []retrieval.ContextPacket, nearMiss []retrieval.NearMissItem) []TraceItem {
	items := make([]TraceItem, 0, len(packets)+len(nearMiss))
	for _, pkt := range packets {
		score := pkt.FinalScore
		if score == 0 {
			score = pkt.Score
		}
		items = append(items, TraceItem{
			ID:           pkt.ID,
			Text:         util.TruncateContent(pkt.Text, 100),
			Score:        score,
			VectorScore:  pkt.Score,
			KeywordScore: pkt.KeywordScore,
		})
	}
	for _, nm := range nearMiss {
		items = append(items, TraceItem{
			ID:            nm.ID,
			Text:          util.TruncateContent(nm.Text, 100),
			VectorScore:   nm.VectorScore,
			DiscardReason: nm.Reason,
		})
	}
	return items
}
