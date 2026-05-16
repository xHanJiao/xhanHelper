package main

import (
	"context"
	"log"
	"os"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("load config:", err)
	}

	db := service.NewDB(cfg.DB)
	if err := db.AutoMigrate(&model.UserMemory{}, &model.EmbeddingChunk{}, &model.Embedding{}); err != nil {
		log.Fatal("migrate:", err)
	}

	// LLM
	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		log.Fatal("llm client:", err)
	}
	llmClient = monitor.WrapLLMClient(llmClient, "memory-rebuild")

	// Embedder
	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)

	// Repos
	memRepo := service.NewUserMemoryRepo(db)
	msgRepo := service.NewMessageRepo(db)
	convRepo := service.NewConversationRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	noteRepo := service.NewNoteRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)

	// Vector store (新建，不加载旧的)
	vectorStore := agent.NewVectorStore()

	vsAdapter := agent.NewVectorSearchAdapter(vectorStore)
	builder := agent.NewChunkBuilder(embedder, chunkRepo, embeddingRepo, vsAdapter)
	conceptEmbedRepo := service.NewConceptEmbeddingRepo(db)
	conceptEmbedService := agent.NewConceptEmbedService(conceptEmbedRepo, embeddingRepo, embedder, vsAdapter)

	extractor := agent.NewMemoryExtractor(llmClient, memRepo, vectorStore, embedder,
		msgRepo, convRepo, embeddingRepo, 0.85, builder, conceptEmbedService)

	ctx := context.Background()

	// 前置清理：删除所有旧数据
	log.Printf("cleaning old data...")
	// 删除旧记忆（旧 prompt 格式，与新字段不兼容）
	if result := db.Where("1=1").Delete(&model.UserMemory{}); result.Error != nil {
		log.Printf("delete old memories error: %v", result.Error)
	} else {
		log.Printf("deleted %d old memories", result.RowsAffected)
	}
	if n, err := chunkRepo.DeleteAllByType(ctx, "note"); err == nil {
		log.Printf("deleted %d note chunks", n)
	}
	if n, err := chunkRepo.DeleteAllByType(ctx, "memory"); err == nil {
		log.Printf("deleted %d memory chunks", n)
	}
	// 删除旧 embedding（entity_type = "note" | "memory" | "chunk"）
	for _, et := range []string{"note", "memory", "chunk", "concept"} {
		// 逐页删除
		for offset := 0; ; offset += 200 {
			embeddings, err := embeddingRepo.ListByType(ctx, et, 200, offset)
			if err != nil || len(embeddings) == 0 {
				break
			}
			for _, emb := range embeddings {
				if err := embeddingRepo.DeleteByEntity(ctx, et, emb.EntityID); err != nil {
					log.Printf("delete embedding %s/%s error: %v", et, emb.EntityID, err)
				}
			}
		}
	}
	log.Printf("cleanup done")

	// 遍历所有用户
	for _, u := range cfg.Server.AdminUsers {
		log.Printf("=== user: %s ===", u)

		// Step 1: 提取记忆
		convs, _, err := convRepo.ListByUser(ctx, u, 200, 0)
		if err != nil {
			log.Printf("list convs error: %v", err)
			continue
		}
		for i := range convs {
			convs[i].LastExtractedMsgID = nil
			if err := extractor.ExtractFromConversation(ctx, &convs[i]); err != nil {
				log.Printf("[conv %s] extract error: %v", convs[i].ID[:8], err)
			}
		}

		// Step 2: chunk 所有笔记
		notes, _, err := noteRepo.List(ctx, u, "", "", 500, 0)
		if err != nil {
			log.Printf("list notes error: %v", err)
		}
		for _, n := range notes {
			nChunks, err := builder.BuildChunks(ctx, u, "note", n.ID, n.Content)
			if err != nil {
				log.Printf("[note %s] chunk error: %v", n.ID[:8], err)
			} else {
				log.Printf("[note %s] chunked %d sentences", n.ID[:8], nChunks)
			}
		}

		// Step 3: chunk 所有记忆
		memories, _, err := memRepo.ListByUser(ctx, u, "", "", 500, 0)
		if err != nil {
			log.Printf("list memories error: %v", err)
		}
		for _, m := range memories {
			nChunks, err := builder.BuildChunks(ctx, u, "memory", m.ID, m.MemoryValue)
			if err != nil {
				log.Printf("[mem %s] chunk error: %v", m.ID[:8], err)
			} else {
				log.Printf("[mem %s] chunked %d sentences", m.ID[:8], nChunks)
			}
		}

		// Step 4: 回填 concept embeddings
		for _, n := range notes {
			if n.Topic != "" {
				conceptEmbedService.EnsureTopicEmbedded(ctx, u, n.Topic, "note", n.ID)
			}
			if n.Tags != "" {
				conceptEmbedService.EnsureTagsEmbedded(ctx, u, n.Tags, "note", n.ID)
			}
		}
		for _, m := range memories {
			if m.Topic != "" {
				conceptEmbedService.EnsureTopicEmbedded(ctx, u, m.Topic, "memory", m.ID)
			}
			if m.Tags != "" {
				conceptEmbedService.EnsureTagsEmbedded(ctx, u, m.Tags, "memory", m.ID)
			}
		}
		log.Printf("[concept] backfilled concepts for user %s", u[:8])
	}

	log.Printf("done. vector store size: %d", vectorStore.Size())
	os.Exit(0)
}
