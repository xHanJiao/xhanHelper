package app

import (
	"log"
	"os"
	"path/filepath"
	"strings"

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
		&model.ToolData{},
		&model.UserPrompt{},
		&model.Embedding{},
		&model.MCPServerRecord{},
		&model.ThemeState{},
		&model.Event{},
		&model.ReflectionTrace{},
		&model.Reminder{},
		&model.ReflectionLLMCall{},
		&model.ReflectionDecision{},
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
	log.Printf("[config] db.driver=%s db.dsn=%s", cfg.DB.Driver, cfg.DB.DSN())
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
		// 本地模式带坏库运行没有意义，直接致命；云端保持「打日志继续」的现状
		if strings.HasPrefix(os.Getenv("APP_ENV"), "local") {
			log.Fatalf("[db] AutoMigrate failed (local mode, refusing to start): %v", err)
		}
		log.Printf("[db] AutoMigrate error: %v", err)
	}

	return db
}
