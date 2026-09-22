package retrieval

import (
	"context"
	"time"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NewMemorySource creates an EntitySource for user memories.
func NewMemorySource(
	vs VectorStore,
	memRepo *service.UserMemoryRepo,
	embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo,
	chunkBuilder app.ChunkEmbedder,
	decayer *TimeDecay,
) *EntitySource[model.UserMemory] {
	return NewEntitySource(model.EntityTypeMemory, vs, memRepo, embeddingRepo, chunkRepo, chunkBuilder, decayer,
		EntityConfig[model.UserMemory]{
			EntityType:   model.EntityTypeMemory,
			FullType:     model.VectorTypeMemory,
			IDPrefix:     model.VectorPrefixMemory,
			ConfigPicker: func(o SearchOptions) SourceConfig { return o.Memory },
			AfterFetch: func(ctx context.Context, ids []string) {
				_ = memRepo.BatchUpdateConfirm(ctx, ids)
			},
			ListAll: func(ctx context.Context, _ time.Time, limit, offset int) ([]model.UserMemory, error) {
				return memRepo.ListAllUsersMemoriesPaginated(ctx, limit, offset)
			},
		},
	)
}
