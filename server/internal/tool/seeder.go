package tool

import (
	"context"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// ToolRepo seeder 需要的工具仓储接口
type ToolRepo interface {
	ListActiveTools(ctx context.Context) ([]model.ToolRegistry, error)
	CreateTool(ctx context.Context, t *model.ToolRegistry) error
	UpdateTool(ctx context.Context, t *model.ToolRegistry) error
}

// EmbeddingRepoSaver seeder 需要的向量存储接口
type EmbeddingRepoSaver interface {
	Upsert(ctx context.Context, emb *model.Embedding) error
}

const systemUserID = "builtin"

// builtinToolSeed 内置工具的种子数据
type builtinToolSeed struct {
	Name        string
	Description string
	Category    string
}

var builtinToolSeeds = []builtinToolSeed{
	{Name: "bookkeeping", Description: "记账工具，可以新增、修改、删除、查询账单，以及查看账单的统计信息", Category: "finance"},
	{Name: "datetime", Description: "获取当前日期时间信息", Category: "utility"},
	{Name: "calculator", Description: "安全的数学表达式计算器，支持加减乘除和括号", Category: "utility"},
	{Name: "note", Description: "自由笔记工具，记录值得长期追踪的信息——事实、数据、观点、事件、个人状态等", Category: "note"},
	{Name: "agent", Description: "管理智能体：创建、查看、删除自定义 AI 助手", Category: "agent"},
	{Name: "tool_retriever", Description: "根据用户需求检索相关工具，当不确定该用什么工具时调用", Category: "system"},
}

// SeedBuiltinTools 启动时确保内置工具的 DB 记录和 embedding 存在（幂等）
func SeedBuiltinTools(ctx context.Context, toolRepo ToolRepo, embedder *llm.Embedder, embeddingRepo EmbeddingRepoSaver) error {
	existing, err := toolRepo.ListActiveTools(ctx)
	if err != nil {
		return err
	}

	existingByName := make(map[string]*model.ToolRegistry)
	for i := range existing {
		existingByName[existing[i].Name] = &existing[i]
	}

	for _, seed := range builtinToolSeeds {
		if t, ok := existingByName[seed.Name]; ok {
			// 已存在但 embedding 丢失，补生成
			if t.EmbeddingID == "" {
				genAndSetEmbedding(ctx, t, seed, embedder, embeddingRepo)
				if err := toolRepo.UpdateTool(ctx, t); err != nil {
					log.Printf("[seeder] update embedding_id for %s error: %v", seed.Name, err)
				} else {
					log.Printf("[seeder] repaired embedding for: %s", seed.Name)
				}
			}
			continue
		}

		toolID := uuid.New().String()
		t := &model.ToolRegistry{
			ID:          toolID,
			Name:        seed.Name,
			Description: seed.Description,
			Category:    seed.Category,
			Status:      "active",
			UserID:      systemUserID,
		}

		genAndSetEmbedding(ctx, t, seed, embedder, embeddingRepo)

		if err := toolRepo.CreateTool(ctx, t); err != nil {
			log.Printf("[seeder] create tool %s error: %v", seed.Name, err)
			continue
		}
		log.Printf("[seeder] created builtin tool: %s", seed.Name)
	}

	return nil
}

func genAndSetEmbedding(ctx context.Context, t *model.ToolRegistry, seed builtinToolSeed, embedder *llm.Embedder, embeddingRepo EmbeddingRepoSaver) {
	if embedder == nil || embeddingRepo == nil {
		return
	}
	searchText := seed.Name + " " + seed.Description
	vec, err := embedder.Embed(ctx, searchText)
	if err != nil {
		log.Printf("[seeder] embed %s error: %v", seed.Name, err)
		return
	}
	embStr := llm.EmbeddingToString(vec)
	if err := embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         t.ID,
		EntityType: model.EntityTypeTool,
		EntityID:   t.ID,
		UserID:     systemUserID,
		Model:      embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[seeder] upsert embedding for %s error: %v", seed.Name, err)
		return
	}
	t.EmbeddingID = t.ID
}
