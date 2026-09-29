package retrieval

import (
	"context"
	"time"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NewSummarySource creates an EntitySource for conversation summaries.
// Reuses the generic chunk+full-embedding two-phase retrieval via EntitySource[model.ConversationSummary].
// sinceDays 为内存保留窗（>0 时只 warmup 最近 N 天）。
func NewSummarySource(
	vs VectorStore,
	summaryRepo *service.ConversationSummaryRepo,
	embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo,
	chunkBuilder app.ChunkEmbedder,
	decayer *TimeDecay,
	sinceDays int,
) *EntitySource[model.ConversationSummary] {
	return NewEntitySource(model.EntityTypeSummary, vs, summaryRepo, embeddingRepo, chunkRepo, chunkBuilder, decayer,
		EntityConfig[model.ConversationSummary]{
			EntityType:   model.EntityTypeSummary,
			FullType:     model.VectorTypeSummary,
			IDPrefix:     model.VectorPrefixSummary,
			SinceDays:    sinceDays,
			ConfigPicker: func(o SearchOptions) SourceConfig { return o.Summary },
			ListAll: func(ctx context.Context, since time.Time, limit, offset int) ([]model.ConversationSummary, error) {
				return summaryRepo.ListAll(ctx, since, limit, offset)
			},
		},
	)
}
