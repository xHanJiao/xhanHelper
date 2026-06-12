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
	{Name: "bookkeeping", Description: "记账工具，新增/修改/删除/查询账单，月度/分类统计，分类选项查询", Category: "finance"},
	{Name: "datetime", Description: "获取当前日期时间信息", Category: "utility"},
	{Name: "calculator", Description: "安全的数学表达式计算器，支持加减乘除和括号", Category: "utility"},
	{Name: "note", Description: "自由笔记工具，记录/查询/更新/回滚笔记，获取主题列表", Category: "note"},
	{Name: "document", Description: "文档管理工具，创建/查看/更新/删除文档，支持版本回滚和标签检索", Category: "document"},
	{Name: "agent", Description: "管理智能体：创建、查看、删除自定义 AI 助手", Category: "agent"},
	{Name: "search_notes_v2", Description: "向量检索笔记（chunk 级），返回匹配片段和全文", Category: "search"},
	{Name: "search_memories_v2", Description: "向量检索长期记忆（chunk 级），返回匹配片段和全文", Category: "search"},
	{Name: "message_search", Description: "搜索历史对话消息，支持语义检索", Category: "search"},
	{Name: "reminder", Description: "设置提醒事项，支持一次性/每日/每周/每月/工作日重复", Category: "utility"},
	{Name: "search", Description: "联网搜索工具，搜索实时信息——新闻、技术文档、百科、金融数据等", Category: "search"},
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
			SchemaJSON:  "[]",
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
