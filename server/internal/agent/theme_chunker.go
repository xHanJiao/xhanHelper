package agent

import (
	"context"
	"log"
)

// BuildFactChunk 对单条 fact 增量建 chunk（不清除旧 chunk）
func BuildFactChunk(ctx context.Context, chunkBuilder *ChunkBuilder, userID, parentType, parentID, factText string) {
	n, err := chunkBuilder.BuildChunks(ctx, userID, parentType, parentID, factText)
	if err != nil {
		log.Printf("[theme_chunker] BuildChunks error: theme=%s err=%v", parentID[:8], err)
	} else if n > 0 {
		log.Printf("[theme_chunker] built %d chunks for theme %s", n, parentID[:8])
	}
}
