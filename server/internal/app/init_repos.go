package app

import (
	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/service"
)

// Repos 聚合全部 DB 级别的 repo，按需传入下游
type Repos struct {
	User                *service.UserRepo
	Conversation        *service.ConversationRepo
	Message             *service.MessageRepo
	Bill                *service.BillRepo
	Mem                 *service.UserMemoryRepo
	Note                *service.NoteRepo
	Agent               *service.AgentRepo
	Prompt              *service.UserPromptRepo
	Tool                *service.ToolRepo
	Embedding           *service.EmbeddingRepo
	Mailbox             *service.MailboxRepo
	ThemeState          *service.ThemeStateRepo
	Trace               *service.ReflectionTraceRepo
	Chunk               *service.EmbeddingChunkRepo
	ConceptEmbed        *service.ConceptEmbeddingRepo
	PendingFinding      *service.PendingFindingRepo
	Reminder            *service.ReminderRepo
	DelayedTask         *service.DelayedTaskRepo
	Document            *service.DocumentRepo
	DocumentShare       *service.DocumentShareRepo
	MessageShare        *service.MessageShareRepo
	Version             *service.VersionRepo
	Mission             *service.MissionRepo
	SearchUsage         *service.SearchUsageRepo
	ConversationSummary *service.ConversationSummaryRepo
}

// InitRepos 创建所有 repo 实例
func InitRepos(cfg *config.Config, db *gorm.DB) *Repos {
	docRepo := service.NewDocumentRepo(db)
	msgRepo := service.NewMessageRepo(db)
	return &Repos{
		User:                service.NewUserRepo(db, cfg.Server.TokenTTLMinutes, cfg.Server.MaxTokensPerUser),
		Conversation:        service.NewConversationRepo(db),
		Message:             msgRepo,
		Bill:                service.NewBillRepo(db),
		Mem:                 service.NewUserMemoryRepo(db),
		Note:                service.NewNoteRepo(db),
		Agent:               service.NewAgentRepo(db),
		Prompt:              service.NewUserPromptRepo(db),
		Tool:                service.NewToolRepo(db),
		Embedding:           service.NewEmbeddingRepo(db),
		Mailbox:             service.NewMailboxRepo(db),
		ThemeState:          service.NewThemeStateRepo(db),
		Trace:               service.NewReflectionTraceRepo(db),
		Chunk:               service.NewEmbeddingChunkRepo(db),
		ConceptEmbed:        service.NewConceptEmbeddingRepo(db),
		PendingFinding:      service.NewPendingFindingRepo(db),
		Reminder:            service.NewReminderRepo(db),
		DelayedTask:         service.NewDelayedTaskRepo(db),
		Document:            docRepo,
		DocumentShare:       service.NewDocumentShareRepo(db, docRepo),
		MessageShare:        service.NewMessageShareRepo(db, msgRepo),
		Version:             service.NewVersionRepo(db),
		Mission:             service.NewMissionRepo(db),
		SearchUsage:         service.NewSearchUsageRepo(db),
		ConversationSummary: service.NewConversationSummaryRepo(db),
	}
}
