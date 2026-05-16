package main

import (
	"context"
	"log"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("load config:", err)
	}

	if err := idgen.Init(cfg.Snowflake.NodeID); err != nil {
		log.Fatal("init idgen:", err)
	}

	db := service.NewDB(cfg.DB)
	if err := db.AutoMigrate(&model.ConceptEmbedding{}); err != nil {
		log.Fatal("migrate:", err)
	}

	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)

	embeddingRepo := service.NewEmbeddingRepo(db)
	noteRepo := service.NewNoteRepo(db)
	memRepo := service.NewUserMemoryRepo(db)
	conceptEmbedRepo := service.NewConceptEmbeddingRepo(db)

	vectorStore := agent.NewVectorStore()
	vsAdapter := agent.NewVectorSearchAdapter(vectorStore)
	conceptEmbedService := agent.NewConceptEmbedService(conceptEmbedRepo, embeddingRepo, embedder, vsAdapter)

	ctx := context.Background()

	for _, u := range cfg.Server.AdminUsers {
		log.Printf("=== user: %s ===", u)

		notes, _, err := noteRepo.List(ctx, u, "", "", 2000, 0)
		if err != nil {
			log.Printf("list notes error: %v", err)
		}
		for _, n := range notes {
			if n.Topic != "" {
				conceptEmbedService.EnsureTopicEmbedded(ctx, u, n.Topic, "note", n.ID)
			}
			if n.Tags != "" {
				conceptEmbedService.EnsureTagsEmbedded(ctx, u, n.Tags, "note", n.ID)
			}
		}
		log.Printf("notes done: %d", len(notes))

		memories, _, err := memRepo.ListByUser(ctx, u, "", "", 2000, 0)
		if err != nil {
			log.Printf("list memories error: %v", err)
		}
		for _, m := range memories {
			if m.Topic != "" {
				conceptEmbedService.EnsureTopicEmbedded(ctx, u, m.Topic, "memory", m.ID)
			}
			if m.Tags != "" {
				conceptEmbedService.EnsureTagsEmbedded(ctx, u, m.Tags, "memory", m.ID)
			}
		}
		log.Printf("memories done: %d", len(memories))
	}

	log.Printf("done. vector store size: %d", vectorStore.Size())
}
