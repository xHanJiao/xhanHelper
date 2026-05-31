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
// 委托 ThemeSource 做向量检索，自身只处理信箱关联和格式化。
type themeProvider struct {
	source         retrieval.ContextSource   // ThemeSource
	themeStateRepo *service.ThemeStateRepo   // 加载主题详情（名称/描述）
	convRepo       *service.ConversationRepo // 读取信箱关联的主题 ID
	cfg            *PromptConfig
}

func (p *themeProvider) Name() string          { return "theme" }
func (p *themeProvider) SectionHeader() string { return "\n### 用户身上正在发生的事\n" }
func (p *themeProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	if opts.MemoryThreshold <= 0 {
		opts.MemoryThreshold = 0.5
	}
	opts.MemoryLimit = p.cfg.ThemeMaxCount

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("theme fetch: %w", err)
	}

	linkedThemeID := p.getLinkedThemeID(ctx, opts.ConversationID)

	// 信箱关联主题：已在 items 中则提权，不在则追加
	if linkedThemeID != "" {
		found := false
		for i := range packets {
			if packets[i].ID == linkedThemeID {
				packets[i].FinalScore = 1.0
				found = true
				break
			}
		}
		if !found {
			for i := range nearMiss {
				if nearMiss[i].ID == linkedThemeID {
					nearMiss = append(nearMiss[:i], nearMiss[i+1:]...)
					break
				}
			}
			packets = append(packets, retrieval.ContextPacket{
				ID:         linkedThemeID,
				Type:       "theme",
				FinalScore: 1.0,
			})
		}
	}

	traceItems := buildTraceItems(packets, nearMiss)

	// 渲染：从 themeStateRepo 获取主题详情（名称/描述/StateText）
	themeMap := p.loadThemeDetails(ctx, packets, opts.UserID)

	items := make([]retrieval.ContextPacket, 0, len(packets))
	for _, pkt := range packets {
		t, ok := themeMap[pkt.ID]
		if !ok {
			items = append(items, pkt)
			continue
		}
		items = append(items, renderThemePacket(t, pkt.FinalScore))
	}

	return &SearchResult{Items: items, TraceItems: traceItems}, nil
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

func (p *themeProvider) loadThemeDetails(ctx context.Context, packets []retrieval.ContextPacket, userID string) map[string]model.ThemeState {
	if len(packets) == 0 {
		return nil
	}
	ids := make([]string, len(packets))
	for i, pkt := range packets {
		ids[i] = pkt.ID
	}
	tsMap, err := p.themeStateRepo.BatchGetByIDs(ctx, ids, userID)
	if err != nil {
		log.Printf("[theme] batch get themes error: %v", err)
		return nil
	}
	themeMap := make(map[string]model.ThemeState, len(tsMap))
	for id, ts := range tsMap {
		if ts != nil {
			themeMap[id] = *ts
		}
	}
	return themeMap
}

func renderThemePacket(t model.ThemeState, themeScore float64) retrieval.ContextPacket {
	var content string
	if t.Description != "" {
		content = fmt.Sprintf("\n\n【主题】%s (相关度 %.2f)\n描述：%s\n%s", t.Name, themeScore, t.Description, t.StateText)
	} else {
		content = fmt.Sprintf("\n\n【主题】%s (相关度 %.2f)\n%s", t.Name, themeScore, t.StateText)
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

func (p *memoryProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || opts.Query == "" {
		return nil, nil
	}

	if opts.MemoryThreshold <= 0 {
		opts.MemoryThreshold = p.cfg.MemoryThreshold
	}
	if opts.MemoryThreshold <= 0 {
		opts.MemoryThreshold = 0.5
	}
	if opts.MemoryLimit <= 0 {
		opts.MemoryLimit = p.cfg.MemoryLimit
	}
	if opts.MemoryLimit <= 0 {
		opts.MemoryLimit = 8
	}

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
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

func (p *noteProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || opts.Query == "" {
		return nil, nil
	}

	if opts.NoteThreshold <= 0 {
		opts.NoteThreshold = p.cfg.NoteThreshold
	}
	if opts.NoteThreshold <= 0 {
		opts.NoteThreshold = 0.5
	}
	if opts.NoteLimit <= 0 {
		opts.NoteLimit = p.cfg.NoteLimit
	}
	if opts.NoteLimit <= 0 {
		opts.NoteLimit = 5
	}

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
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

func (p *historyProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || opts.Query == "" {
		return nil, nil
	}

	if opts.HistoryThreshold <= 0 {
		opts.HistoryThreshold = p.cfg.HistoryThreshold
	}
	if opts.HistoryThreshold <= 0 {
		opts.HistoryThreshold = 0.8
	}
	if opts.HistoryLimit <= 0 {
		opts.HistoryLimit = p.cfg.HistoryLimit
	}
	if opts.HistoryLimit <= 0 {
		opts.HistoryLimit = 5
	}

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("history fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// ==================== Provider Factory Functions ====================

func NewThemeProvider(source retrieval.ContextSource, themeStateRepo *service.ThemeStateRepo, convRepo *service.ConversationRepo, cfg *PromptConfig) SearchProvider {
	return &themeProvider{source: source, themeStateRepo: themeStateRepo, convRepo: convRepo, cfg: cfg}
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
