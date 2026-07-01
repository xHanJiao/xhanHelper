package agent

import (
	"context"
	"log"
)

// ChunkStateText 增量嵌入主题 state_text：对比旧 chunk，只嵌入变化的句子
func ChunkStateText(ctx context.Context, chunkBuilder *ChunkBuilder, userID, themeID, stateText string) {
	kept, added, err := chunkBuilder.DiffAndBuildChunks(ctx, userID, "theme", themeID, stateText, nil)
	if err != nil {
		log.Printf("[theme_chunker] DiffAndBuild error: theme=%s err=%v", themeID[:8], err)
	} else {
		log.Printf("[theme_chunker] chunked state_text for theme %s: kept=%d added=%d", themeID[:8], kept, added)
	}
}
