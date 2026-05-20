package app

import (
	"context"

	"xhan-helper/internal/model"
)

// ChunkEmbedder 文本分句 + 嵌入接口，由 agent.ChunkBuilder 实现。
type ChunkEmbedder interface {
	BuildChunks(ctx context.Context, userID, parentType, parentID, text string) (int, error)
}

// ConceptEmbedder 概念（topic / tag）嵌入接口，由 agent.ConceptEmbedService 实现。
// 合并了原 tool.ConceptEmbedDeder 和 tool.TopicEmbedder。
type ConceptEmbedder interface {
	EnsureTopicEmbedded(ctx context.Context, userID, topic, parentType, parentID string)
	EnsureTagsEmbedded(ctx context.Context, userID, tags, parentType, parentID string)
}

// ThemeVectorWriter 主题向量写入接口，由 agent.ThemeEmbedder 实现。
type ThemeVectorWriter interface {
	EnsureEmbedded(ctx context.Context, userID, themeID, name, description string)
}

// CommitmentTimer 承诺定时接口，由 agent.CommitmentTimer 实现。
type CommitmentTimer interface {
	Schedule(c *model.Commitment)
}
