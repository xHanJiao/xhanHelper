package agent

import (
	"context"
	"fmt"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MemoryStore — 记忆存储接口（今天用 LLM 实现，未来可换向量实现）
type MemoryStore interface {
	GetRelevant(ctx context.Context, userID, currentMessage string) ([]model.UserMemory, error)
	Store(ctx context.Context, m *model.UserMemory) error
	FindSimilar(ctx context.Context, userID, key string) (*model.UserMemory, error)
}

// LLMMemoryStore — 用 LLM 做检索和去重的实现
type LLMMemoryStore struct {
	llmClient llm.LLMClient
	memRepo   *service.UserMemoryRepo
}

// NewLLMMemoryStore 创建 LLM 记忆存储
func NewLLMMemoryStore(llmClient llm.LLMClient, memRepo *service.UserMemoryRepo) *LLMMemoryStore {
	return &LLMMemoryStore{llmClient: llmClient, memRepo: memRepo}
}

// GetRelevant 用 LLM 从所有记忆中选出与当前消息相关的
func (s *LLMMemoryStore) GetRelevant(ctx context.Context, userID, currentMessage string) ([]model.UserMemory, error) {
	all, err := s.memRepo.ListByUser(ctx, userID)
	if err != nil || len(all) == 0 {
		return nil, err
	}

	// 构建检索 prompt
	var sb strings.Builder
	sb.WriteString("以下是关于用户的历史记忆（按时间倒序，最新的排最前）。\n")
	sb.WriteString("请选出和当前对话相关的条目，最多5条。返回编号列表，如: 1,3,5。如果没有相关的就返回 无。\n\n")
	for i, m := range all {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s (来源:%s, 确认%d次)\n",
			i+1, m.MemoryKey, m.MemoryValue, m.Source, m.ConfirmCount))
	}
	sb.WriteString("\n当前用户消息: " + currentMessage)

	msg, err := s.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是记忆检索助手。只从给定列表中选择相关条目，返回编号。不要编造。",
		Messages:     []llm.Message{{Role: "user", Content: sb.String()}},
	})
	if err != nil {
		return nil, err
	}

	// 解析 LLM 返回的编号
	var result []model.UserMemory
	resp := strings.TrimSpace(msg.Content)
	if resp == "无" || resp == "" {
		return result, nil
	}

	for _, numStr := range strings.Split(resp, ",") {
		numStr = strings.TrimSpace(numStr)
		// 尝试解析数字
		var idx int
		if _, err := fmt.Sscanf(numStr, "%d", &idx); err == nil && idx > 0 && idx <= len(all) {
			result = append(result, all[idx-1])
		}
	}
	// 最多 5 条
	if len(result) > 5 {
		result = result[:5]
	}
	return result, nil
}

// Store 写入记忆
func (s *LLMMemoryStore) Store(ctx context.Context, m *model.UserMemory) error {
	return s.memRepo.Create(ctx, m)
}

// FindSimilar 查找相似记忆（同一 device 下相同 key）
func (s *LLMMemoryStore) FindSimilar(ctx context.Context, userID, key string) (*model.UserMemory, error) {
	return s.memRepo.FindByKey(ctx, userID, key)
}
