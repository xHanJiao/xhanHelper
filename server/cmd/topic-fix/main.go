package main

import (
	"context"
	"log"
	"strings"

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
	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		log.Fatal("llm client:", err)
	}

	memRepo := service.NewUserMemoryRepo(db)
	ctx := context.Background()

	for _, u := range cfg.Server.AdminUsers {
		// 1. 修复有问题的 memory topic
		all, _, err := memRepo.ListByUser(ctx, u, "", "", 2000, 0)
		if err != nil {
			log.Printf("list memories error: %v", err)
			continue
		}
		for _, m := range all {
			if !strings.Contains(m.Topic, "/") && !strings.Contains(m.Topic, ",") {
				continue
			}
			cleanTopic := extractTopic(ctx, llmClient, m.MemoryValue)
			if cleanTopic == "" || cleanTopic == m.Topic {
				continue
			}
			log.Printf("[fix] mem %s: %q → %q", m.ID[:8], m.Topic, cleanTopic)
			db.Model(&model.UserMemory{}).Where("id = ?", m.ID).Update("topic", cleanTopic)
		}

		// 2. 删除有问题的 concept embeddings（后续创建 note/memory 或触发反思时会自动重建）
		result := db.Where("type = 'topic' AND (value LIKE ? OR value LIKE ?)", "%/%", "%,%").Delete(&model.ConceptEmbedding{})
		if result.Error != nil {
			log.Printf("delete bad concepts error: %v", result.Error)
		} else {
			log.Printf("deleted %d bad concepts", result.RowsAffected)
		}
	}

	log.Println("done")
}

func extractTopic(ctx context.Context, client llm.LLMClient, content string) string {
	resp, err := client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是一个文本分类助手。给出一段内容，提取最核心的一个主题词（5字以内，一个词，禁止斜杠、逗号）。如：睡眠管理、职业规划。只输出主题词。",
		Messages:     []llm.Message{{Role: "user", Content: content}},
	})
	if err != nil {
		log.Printf("  extract topic error: %v", err)
		return ""
	}
	topic := strings.TrimSpace(resp.Content)
	topic = strings.Trim(topic, `"'`)
	if idx := strings.Index(topic, "\n"); idx > 0 {
		topic = topic[:idx]
	}
	return topic
}
