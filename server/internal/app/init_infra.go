package app

import (
	"log"
	"os"
	"strings"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"

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
		ImageStore:  initImageStore(cfg, db),
	}
}

// initImageStore 创建图床。file_id 归属于 API key，因此上传器只能用在 DeepSeek 主链路上；
// 其他 provider 不注入上传器（图片仍可存取，只是送不进模型，EnsureFileID 会返回错误）。
func initImageStore(cfg *config.Config, db *gorm.DB) *service.ImageStore {
	var uploader service.FileUploader
	if cfg.LLM.Provider == "deepseek" && cfg.LLM.APIKey != "" && !strings.Contains(cfg.LLM.APIKey, "${") {
		uploader = llm.NewFilesClient(cfg.LLM.APIKey, cfg.LLM.BaseURL)
	}
	if err := os.MkdirAll(cfg.Storage.ImageDir, 0o755); err != nil {
		log.Printf("[image] create dir %s failed: %v", cfg.Storage.ImageDir, err)
	}
	log.Printf("[image] store dir=%s max_bytes=%d ttl=%dd orphan_ttl=%dd uploader=%v",
		cfg.Storage.ImageDir, cfg.Storage.MaxImageBytes,
		cfg.Storage.ImageTTLDays, cfg.Storage.ImageOrphanTTLDays, uploader != nil)
	return service.NewImageStore(cfg.Storage.ImageDir, cfg.Storage.MaxImageBytes, service.NewImageAssetRepo(db), uploader)
}
