package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MemoryExtractor — 从会话中提取记忆
type MemoryExtractor struct {
	llmClient llm.LLMClient
	store     MemoryStore
	msgRepo   *service.MessageRepo
	convRepo  *service.ConversationRepo
}

// NewMemoryExtractor 创建提取器
func NewMemoryExtractor(llmClient llm.LLMClient, store MemoryStore,
	msgRepo *service.MessageRepo, convRepo *service.ConversationRepo) *MemoryExtractor {
	return &MemoryExtractor{
		llmClient: llmClient,
		store:     store,
		msgRepo:   msgRepo,
		convRepo:  convRepo,
	}
}

// ExtractFromConversation 从指定会话中增量提取记忆
func (e *MemoryExtractor) ExtractFromConversation(ctx context.Context, conv *model.Conversation) error {
	msgs, err := e.msgRepo.ListByConversation(ctx, conv.ID, 200)
	if err != nil || len(msgs) == 0 {
		return err
	}

	// 构建对话文本
	var sb strings.Builder
	lastExtractedID := ""
	if conv.LastExtractedMsgID != nil {
		lastExtractedID = *conv.LastExtractedMsgID
	}

	lastIdx := 0
	for i, m := range msgs {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
		lastIdx = i
	}

	// 构建提取 prompt
	prompt := "从以下对话中提取关于用户的事实、偏好、习惯。每条一句话，用\"key: value\"格式。"
	prompt += "如果没有值得提取的信息就回复\"无\"。不要编造。"
	if lastExtractedID != "" {
		prompt += fmt.Sprintf("\n上次已提取到第 %s 条，请关注后面的新信息。", lastExtractedID)
	}
	prompt += "\n\n" + sb.String()

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是记忆提取助手。只提取用户明确表达的事实、偏好、习惯。不确定的不提取。",
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return fmt.Errorf("extract memory: %w", err)
	}

	text := strings.TrimSpace(msg.Content)
	if text == "无" || text == "" {
		// 没有新记忆，仍然更新提取进度
		e.updateProgress(ctx, conv, msgs[lastIdx].ID)
		return nil
	}

	// 解析 key: value 格式的记忆
	lines := strings.Split(text, "\n")
	extracted := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "无" {
			continue
		}
		// key: value
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}

		// 去重：检查是否已有相似记忆
		existing, _ := e.store.FindSimilar(ctx, conv.DeviceID, key)
		if existing != nil {
			// 已存在，更新确认计数
			if existing.MemoryValue == value {
				e.store.Store(ctx, existing) // 触发 updated_at 更新
				extracted++
				continue
			}
			// 值不同，版本化：追加新记录
		}

		memory := &model.UserMemory{
			ID:          uuid.New().String(),
			DeviceID:    conv.DeviceID,
			MemoryKey:   key,
			MemoryValue: value,
			Source:      "conversation",
			ConfirmCount: 1,
			LastConfirmedAt: time.Now(),
		}
		if err := e.store.Store(ctx, memory); err != nil {
			log.Printf("[memory] store error: %v", err)
			continue
		}
		extracted++
	}

	log.Printf("[memory] extracted %d memories from conv %s", extracted, conv.ID[:8])
	e.updateProgress(ctx, conv, msgs[lastIdx].ID)
	return nil
}

// ExtractFromPrompt 从任意文本中提取记忆（CSV 导入等场景）
func (e *MemoryExtractor) ExtractFromPrompt(ctx context.Context, deviceID, prompt, source string) {
	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是记忆提取助手。只提取明确的事实、偏好、规律。不确定的不提取。",
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		log.Printf("[memory] extract from prompt error: %v", err)
		return
	}

	text := strings.TrimSpace(msg.Content)
	if text == "无" || text == "" {
		return
	}

	lines := strings.Split(text, "\n")
	extracted := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "无" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}

		existing, _ := e.store.FindSimilar(ctx, deviceID, key)
		if existing != nil && existing.MemoryValue == value {
			continue
		}

		memory := &model.UserMemory{
			ID:             uuid.New().String(),
			DeviceID:       deviceID,
			MemoryKey:      key,
			MemoryValue:    value,
			Source:         source,
			ConfirmCount:   1,
			LastConfirmedAt: time.Now(),
		}
		if err := e.store.Store(ctx, memory); err != nil {
			log.Printf("[memory] store error: %v", err)
			continue
		}
		extracted++
	}
	log.Printf("[memory] extracted %d memories from prompt (source=%s)", extracted, source)
}

func (e *MemoryExtractor) updateProgress(ctx context.Context, conv *model.Conversation, lastMsgID string) {
	now := time.Now()
	conv.LastExtractedMsgID = &lastMsgID
	conv.LastExtractedAt = &now
	e.convRepo.Update(ctx, conv)
}
