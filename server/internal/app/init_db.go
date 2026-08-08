package app

import (
	"log"
	"os"

	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// InitDB 创建并迁移数据库
func InitDB(cfg *config.Config) *gorm.DB {
	log.Printf("[config] env=%s server.port=%d server.mode=%s",
		os.Getenv("APP_ENV"), cfg.Server.Port, cfg.Server.Mode)
	log.Printf("[config] db.driver=%s db.dsn=%s", cfg.DB.Driver, cfg.DB.DSN())
	log.Printf("[config] llm.provider=%s llm.model=%s llm.strong_model=%s llm.key=%s...",
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.StrongModel, cfg.LLM.APIKey[:min(8, len(cfg.LLM.APIKey))])
	log.Printf("[config] embedding.model=%s embedding.key=%s... embedding.base_url=%s",
		cfg.Embedding.Model, cfg.Embedding.APIKey[:min(8, len(cfg.Embedding.APIKey))], cfg.Embedding.BaseURL)

	db := service.NewDB(cfg.DB)

	if err := db.AutoMigrate(
		&model.User{},
		&model.UserToken{},
		&model.Conversation{},
		&model.Message{},
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
		&model.MailboxItem{},
		&model.ThemeState{},
		&model.ReflectionTrace{},
		&model.Reminder{},
		&model.ReflectionLLMCall{},
		&model.ReflectionDecision{},
		&model.EmbeddingChunk{},
		&model.ConceptEmbedding{},
		&model.PendingFinding{},
		&model.DelayedTask{},
		&model.Document{},
		&model.DocumentShare{},
		&model.MessageShare{},
		&model.ContentVersion{},
		&model.Mission{},
		&model.SearchUsage{},
		&model.ConversationSummary{},
		&model.ConversationPlan{},
		&model.UserProfile{},
	); err != nil {
		log.Printf("[db] AutoMigrate error: %v", err)
	}

	return db
}
