package app

import (
	"log"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// AllModels 返回需要建表/迁移的全部模型。
// InitDB 与运维工具（cmd/mysql2sqlite）共用，避免两处清单各自漂移。
func AllModels() []any {
	return []any{
		&model.User{},
		&model.UserToken{},
		&model.Conversation{},
		&model.Message{},
		&model.Artifact{},
		&model.Bill{},
		&model.UserMemory{},
		&model.ScheduledTask{},
		&model.Note{},
		&model.Agent{},
		&model.ToolRegistry{},
		&model.UserPrompt{},
		&model.Embedding{},
		&model.Event{},
		&model.Reminder{},
		&model.EmbeddingChunk{},
		&model.ConceptEmbedding{},
		&model.DelayedTask{},
		&model.Document{},
		&model.DocumentAnnotation{},
		&model.DocumentAnnotationReply{},
		&model.DocumentShare{},
		&model.MessageShare{},
		&model.ContentVersion{},
		&model.SearchUsage{},
		&model.ConversationSummary{},
		&model.ConversationPlan{},
		&model.UserProfile{},
		&model.UserProfileFact{},
		&model.LLMCallIO{},
		&model.LlmCostDetail{},
		&model.LlmModelPrice{},
		&model.ImageAsset{},
	}
}

// InitDB 创建并迁移数据库
func InitDB(cfg *config.Config) *gorm.DB {
	log.Printf("[config] env=%s server.port=%d server.mode=%s",
		os.Getenv("APP_ENV"), cfg.Server.Port, cfg.Server.Mode)
	// 不打完整 DSN：mysql 的 DSN 里带数据库密码，日志会流向文件/部署日志。
	if cfg.DB.Driver == "sqlite" {
		log.Printf("[config] db.driver=sqlite db.database=%s", cfg.DB.Database)
	} else {
		log.Printf("[config] db.driver=%s db.address=%s:%d db.name=%s db.user=%s",
			cfg.DB.Driver, cfg.DB.Host, cfg.DB.Port, cfg.DB.Database, cfg.DB.User)
	}
	log.Printf("[config] llm.provider=%s llm.model=%s llm.strong_model=%s llm.key=%s...",
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.StrongModel, cfg.LLM.APIKey[:min(8, len(cfg.LLM.APIKey))])
	log.Printf("[config] embedding.model=%s embedding.key=%s... embedding.base_url=%s",
		cfg.Embedding.Model, cfg.Embedding.APIKey[:min(8, len(cfg.Embedding.APIKey))], cfg.Embedding.BaseURL)

	if cfg.DB.Driver == "sqlite" {
		if dir := filepath.Dir(cfg.DB.Database); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Fatalf("[db] create sqlite dir %s: %v", dir, err)
			}
		}
	}

	db := service.NewDB(cfg.DB)

	if err := db.AutoMigrate(AllModels()...); err != nil {
		// schema 与代码不一致时带病运行只会在运行时炸（且可能写坏数据），所有环境都拒绝启动。
		log.Fatalf("[db] AutoMigrate failed (refusing to start): %v", err)
	}

	if n, err := purgeLegacyPlaintextTokens(db); err != nil {
		log.Fatalf("[db] purge legacy plaintext tokens failed (refusing to start): %v", err)
	} else if n > 0 {
		log.Printf("[db] purged %d legacy plaintext tokens; affected clients must log in again", n)
	}

	return db
}

// purgeLegacyPlaintextTokens 删除旧版明文 token（36 位 UUID）。
// token 现在存 SHA-256 的 64 位 hex；旧行无法再匹配，留着只会泄露明文，启动时直接清掉。
func purgeLegacyPlaintextTokens(db *gorm.DB) (int64, error) {
	res := db.Where("LENGTH(token) = 36").Delete(&model.UserToken{})
	return res.RowsAffected, res.Error
}
