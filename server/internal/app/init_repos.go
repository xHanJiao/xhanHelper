package app

import (
	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/service"
)

// Repos 聚合全部 DB 级别的 repo，按需传入下游
type Repos struct {
	User           *service.UserRepo
	Conversation   *service.ConversationRepo
	Message        *service.MessageRepo
	Bill           *service.BillRepo
	Mem            *service.UserMemoryRepo
	Note           *service.NoteRepo
	Agent          *service.AgentRepo
	Prompt         *service.UserPromptRepo
	Tool           *service.ToolRepo
	Embedding      *service.EmbeddingRepo
	Mailbox        *service.MailboxRepo
	ThemeState     *service.ThemeStateRepo
	Trace          *service.ReflectionTraceRepo
	Chunk          *service.EmbeddingChunkRepo
	ConceptEmbed   *service.ConceptEmbeddingRepo
	PendingFinding *service.PendingFindingRepo
	Commitment     *service.CommitmentRepo
	DelayedTask    *service.DelayedTaskRepo
	Document       *service.DocumentRepo
	DocumentShare  *service.DocumentShareRepo
	Version        *service.VersionRepo
	Mission        *service.MissionRepo
}

// InitRepos 创建所有 repo 实例
func InitRepos(cfg *config.Config, db *gorm.DB) *Repos {
	docRepo := service.NewDocumentRepo(db)
	return &Repos{
		User:           service.NewUserRepo(db, cfg.Server.TokenTTLHours, cfg.Server.MaxTokensPerUser),
		Conversation:   service.NewConversationRepo(db),
		Message:        service.NewMessageRepo(db),
		Bill:           service.NewBillRepo(db),
		Mem:            service.NewUserMemoryRepo(db),
		Note:           service.NewNoteRepo(db),
		Agent:          service.NewAgentRepo(db),
		Prompt:         service.NewUserPromptRepo(db),
		Tool:           service.NewToolRepo(db),
		Embedding:      service.NewEmbeddingRepo(db),
		Mailbox:        service.NewMailboxRepo(db),
		ThemeState:     service.NewThemeStateRepo(db),
		Trace:          service.NewReflectionTraceRepo(db),
		Chunk:          service.NewEmbeddingChunkRepo(db),
		ConceptEmbed:   service.NewConceptEmbeddingRepo(db),
		PendingFinding: service.NewPendingFindingRepo(db),
		Commitment:     service.NewCommitmentRepo(db),
		DelayedTask:    service.NewDelayedTaskRepo(db),
		Document:       docRepo,
		DocumentShare:  service.NewDocumentShareRepo(db, docRepo),
		Version:        service.NewVersionRepo(db),
		Mission:        service.NewMissionRepo(db),
	}
}
