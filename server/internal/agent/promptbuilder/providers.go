package promptbuilder

import (
	"context"
	"fmt"

	"xhan-helper/internal/agent/retrieval"
)

// ==================== theme provider ====================

type themeProvider struct {
	source retrieval.ContextSource
	cfg    *PromptConfig
}

func NewThemeProvider(source retrieval.ContextSource, cfg *PromptConfig) *themeProvider {
	return &themeProvider{source: source, cfg: cfg}
}

func (p *themeProvider) Name() string          { return "theme" }
func (p *themeProvider) SectionHeader() string { return "\n### 当前关注\n" }

func (p *themeProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 {
		return nil, nil
	}

	opts.Theme.Resolve(p.cfg.Theme)

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("theme fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// ==================== memory provider ====================

type memoryProvider struct {
	source retrieval.ContextSource
	cfg    *PromptConfig
}

func NewMemoryProvider(source retrieval.ContextSource, cfg *PromptConfig) *memoryProvider {
	return &memoryProvider{source: source, cfg: cfg}
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

	opts.Memory.Resolve(p.cfg.Memory)

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("memory fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// ==================== note provider ====================

type noteProvider struct {
	source retrieval.ContextSource
	cfg    *PromptConfig
}

func NewNoteProvider(source retrieval.ContextSource, cfg *PromptConfig) *noteProvider {
	return &noteProvider{source: source, cfg: cfg}
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

	opts.Note.Resolve(p.cfg.Note)

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("note fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// ==================== history provider ====================

type historyProvider struct {
	source retrieval.ContextSource
	cfg    *PromptConfig
}

func NewHistoryProvider(source retrieval.ContextSource, cfg *PromptConfig) *historyProvider {
	return &historyProvider{source: source, cfg: cfg}
}

func (p *historyProvider) Name() string          { return "history" }
func (p *historyProvider) SectionHeader() string { return "\n### 最近对话\n" }

func (p *historyProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	if p.source == nil {
		return nil, nil
	}
	if len(opts.Embedding) == 0 || opts.Query == "" {
		return nil, nil
	}

	opts.History.Resolve(p.cfg.History)

	packets, nearMiss, err := p.source.Fetch(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("history fetch: %w", err)
	}

	traceItems := buildTraceItems(packets, nearMiss)
	return &SearchResult{Items: packets, TraceItems: traceItems}, nil
}

// ==================== helpers ====================

// ==================== trace ====================

func buildTraceItems(packets []retrieval.ContextPacket, nearMiss []retrieval.NearMissItem) []TraceItem {
	items := make([]TraceItem, 0, len(packets)+len(nearMiss))
	for _, p := range packets {
		items = append(items, TraceItem{
			ID:            p.ID,
			Text:          truncateTraceText(p.Text, 100),
			Score:         p.Score,
			DiscardReason: "",
		})
	}
	for _, n := range nearMiss {
		items = append(items, TraceItem{
			ID:            n.ID,
			Text:          truncateTraceText(n.Text, 100),
			Score:         n.VectorScore,
			DiscardReason: n.Reason,
		})
	}
	return items
}

func truncateTraceText(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
