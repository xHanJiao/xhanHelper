package app

import (
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"

	"gorm.io/gorm"
)

// EmbedderFactory 用于创建向量嵌入器，可注入 mock 测试
type EmbedderFactory func(apiKey, baseURL, model string) *llm.Embedder

// InitInfra 创建基础设施依赖
func InitInfra(cfg *config.Config, db *gorm.DB, newEmbedder EmbedderFactory, vs VectorStore) *Infra {
	embedder := newEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)

	return &Infra{
		DB:          db,
		Embedder:    embedder,
		VectorStore: vs,
	}
}
