package retrieval

import (
	"context"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NewNoteSource creates an EntitySource for notes.
func NewNoteSource(
	vs VectorStore,
	noteRepo *service.NoteRepo,
	embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo,
	chunkBuilder app.ChunkEmbedder,
	decayer *TimeDecay,
) *EntitySource[model.Note] {
	return NewEntitySource("note", vs, noteRepo, embeddingRepo, chunkRepo, chunkBuilder, decayer,
		EntityConfig[model.Note]{
			EntityType:   model.EntityTypeNote,
			FullType:     "note",
			IDPrefix:     "note:",
			ConfigPicker: func(o SearchOptions) SourceConfig { return o.Note },
			AfterFetch:   nil,
			ListAll: func(ctx context.Context, limit, offset int) ([]model.Note, error) {
				return noteRepo.ListAllPaginated(ctx, limit, offset)
			},
		},
	)
}
