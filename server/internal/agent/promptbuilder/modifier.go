package promptbuilder

import (
	"context"

	"xhan-helper/internal/agent/retrieval"
)

// ScoreModifier 对 ContextPacket 的分数字段做变换，用于提权/降权。
type ScoreModifier func(pkt retrieval.ContextPacket) retrieval.ContextPacket

// WithScoreModifier 用 modifier 包装 provider，对 Search 返回的每条 Item 应用变换。
func WithScoreModifier(sp SearchProvider, modifier ScoreModifier) SearchProvider {
	return &scoreModifierProvider{inner: sp, modifier: modifier}
}

// BoostFactor 返回一个 ScoreModifier，将 Score 乘以 factor，
// 结果 clamp 到 [0, 1]。factor>1 提权，0<factor<1 降权。
func BoostFactor(factor float64) ScoreModifier {
	return func(pkt retrieval.ContextPacket) retrieval.ContextPacket {
		pkt.Score = clamp01(pkt.Score * factor)
		return pkt
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// scoreModifierProvider 包装 SearchProvider，对结果应用 modifier。
type scoreModifierProvider struct {
	inner    SearchProvider
	modifier ScoreModifier
}

func (p *scoreModifierProvider) Name() string          { return p.inner.Name() }
func (p *scoreModifierProvider) SectionHeader() string { return p.inner.SectionHeader() }
func (p *scoreModifierProvider) Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error) {
	sr, err := p.inner.Search(ctx, opts)
	if err != nil || sr == nil {
		return sr, err
	}

	// 应用 modifier 到 Items，并记录修改后的分数用于同步 TraceItems
	modifiedScores := make(map[string]float64, len(sr.Items))
	for i := range sr.Items {
		sr.Items[i] = p.modifier(sr.Items[i])
		modifiedScores[sr.Items[i].ID] = sr.Items[i].Score
	}

	// 同步 TraceItems 中已选中条目的分数（DiscardReason 为空 = 已选中）
	for i := range sr.TraceItems {
		if sr.TraceItems[i].DiscardReason == "" {
			if score, ok := modifiedScores[sr.TraceItems[i].ID]; ok {
				sr.TraceItems[i].Score = score
			}
		}
	}

	return sr, nil
}
