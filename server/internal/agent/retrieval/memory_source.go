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
			ListAll: func(ctx context.Context, _ time.Time, limit, offset int) ([]model.UserMemory, error) {
				return memRepo.ListAllUsersMemoriesPaginated(ctx, limit, offset)
			},
			// 已归属事件的记忆只通过事件块注入，普通记忆块不再重复（设计 §6.2）。
			Filter: func(m model.UserMemory) bool { return m.EventID == nil },
		},
	)
}
