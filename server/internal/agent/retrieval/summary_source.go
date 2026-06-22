package retrieval

import (
	"context"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NewSummarySource creates an EntitySource for conversation summaries.
// Reuses the generic chunk+full-embedding two-phase retrieval via EntitySource[model.ConversationSummary].
func NewSummarySource(
	vs VectorStore,
	summaryRepo *service.ConversationSummaryRepo,
	embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo,
	chunkBuilder app.ChunkEmbedder,
	decayer *TimeDecay,
) *EntitySource[model.ConversationSummary] {
	return NewEntitySource("summary", vs, summaryRepo, embeddingRepo, chunkRepo, chunkBuilder, decayer,
		EntityConfig[model.ConversationSummary]{
			EntityType:   model.EntityTypeSummary,
			FullType:     "summary",
			IDPrefix:     "summary:",
			ConfigPicker: func(o SearchOptions) SourceConfig { return o.Summary },
			AfterFetch:   nil,
			ListAll: func(ctx context.Context, limit, offset int) ([]model.ConversationSummary, error) {
				return summaryRepo.ListAll(ctx, limit, offset)
			},
		},
	)
}
