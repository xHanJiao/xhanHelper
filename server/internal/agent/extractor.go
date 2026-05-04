package agent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MemoryExtractor — 从会话中提取记忆，向量去重
type MemoryExtractor struct {
	llmClient   llm.LLMClient
	memRepo     *service.UserMemoryRepo
	vectorStore *VectorStore
	embedder    *llm.Embedder
	msgRepo     *service.MessageRepo
	convRepo    *service.ConversationRepo
}

// NewMemoryExtractor 创建提取器
func NewMemoryExtractor(llmClient llm.LLMClient, memRepo *service.UserMemoryRepo,
	vectorStore *VectorStore, embedder *llm.Embedder,
	msgRepo *service.MessageRepo, convRepo *service.ConversationRepo) *MemoryExtractor {
	return &MemoryExtractor{
		llmClient:   llmClient,
		memRepo:     memRepo,
		vectorStore: vectorStore,
		embedder:    embedder,
		msgRepo:     msgRepo,
		convRepo:    convRepo,
	}
}

// ExtractFromConversation 从指定会话中增量提取记忆
func (e *MemoryExtractor) ExtractFromConversation(ctx context.Context, conv *model.Conversation) error {
	var msgs []model.Message
	var err error
	if conv.LastExtractedMsgID != nil && *conv.LastExtractedMsgID != "" {
		msgs, err = e.msgRepo.ListByConversationSince(ctx, conv.ID, *conv.LastExtractedMsgID, 200)
	} else {
		msgs, err = e.msgRepo.ListByConversation(ctx, conv.ID, 200)
	}
	if err != nil || len(msgs) == 0 {
		return err
	}

	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}

	prompt := "从以下对话中提取关于用户的事实、偏好、习惯。每条一行纯文本，不要使用 markdown 标题（如 **xxx**）或列表符号（如 - xxx）。如果没有值得提取的信息就回复\"无\"。不要编造。"
	prompt += "\n\n" + sb.String()
	promptHash := hashPrompt(prompt)

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是记忆提取助手。只提取用户明确表达的事实、偏好、习惯。不确定的不提取。每条一行纯文本输出，不使用 markdown 格式。每一行都必须是提取到的记忆信息，标题、分类等信息不需要返回",
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return fmt.Errorf("extract memory: %w", err)
	}

	lastMsg := msgs[len(msgs)-1]
	text := strings.TrimSpace(msg.Content)
	if text == "无" || text == "" {
		e.updateProgress(ctx, conv, lastMsg.ID)
		return nil
	}

	extracted := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "无" {
			continue
		}
		line = strings.TrimLeft(line, "0123456789. -•·")

		if e.isDuplicate(ctx, conv.UserID, line) {
			continue
		}

		memory := &model.UserMemory{
			ID:              uuid.New().String(),
			UserID:          conv.UserID,
			MemoryKey:       truncate(line, 50),
			MemoryValue:     line,
			Source:          "conversation",
			ConfirmCount:    1,
			LastConfirmedAt: time.Now(),
			ExtractPrompt:   &prompt,
			PromptHash:      promptHash,
			ContentHash:     hashPrompt(line),
		}
		e.storeMemory(memory)
		extracted++
	}

	log.Printf("[memory] extracted %d memories from conv %s", extracted, conv.ID[:8])
	e.updateProgress(ctx, conv, lastMsg.ID)
	return nil
}

// ExtractFromPrompt 从任意文本中提取记忆（CSV 导入等场景）
func (e *MemoryExtractor) ExtractFromPrompt(ctx context.Context, userID, prompt, source string) {
	promptHash := hashPrompt(prompt)
	if existing, _ := e.memRepo.FindByPromptHash(ctx, userID, promptHash); existing != nil {
		log.Printf("[memory] prompt hash matched, skip extraction (source=%s)", source)
		return
	}

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是记忆提取助手。只提取明确的事实、偏好、规律。不确定的不提取。每条一行纯文本输出，不使用 markdown 格式。",
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

	extracted := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "无" {
			continue
		}
		line = strings.TrimLeft(line, "0123456789. -•·")

		if e.isDuplicate(ctx, userID, line) {
			continue
		}

		memory := &model.UserMemory{
			ID:              uuid.New().String(),
			UserID:          userID,
			MemoryKey:       truncate(line, 50),
			MemoryValue:     line,
			Source:          source,
			ConfirmCount:    1,
			LastConfirmedAt: time.Now(),
			ExtractPrompt:   &prompt,
			PromptHash:      promptHash,
			ContentHash:     hashPrompt(line),
		}
		e.storeMemory(memory)
		extracted++
	}
	log.Printf("[memory] extracted %d memories from prompt (source=%s)", extracted, source)
}

// isDuplicate 用向量相似度判断记忆是否重复
func (e *MemoryExtractor) isDuplicate(ctx context.Context, userID, text string) bool {
	if e.vectorStore == nil || e.embedder == nil {
		return false
	}
	vec, err := e.embedder.Embed(ctx, text)
	if err != nil {
		return false
	}
	results := e.vectorStore.Search(vec, 3, func(entry vectorEntry) bool {
		return entry.Metadata["type"] == "mem" && entry.Metadata["user_id"] == userID
	})
	for _, r := range results {
		if r.Score > 0.85 {
			return true
		}
	}
	return false
}

// storeMemory 写入 DB 并异步嵌入向量
func (e *MemoryExtractor) storeMemory(memory *model.UserMemory) {
	if err := e.memRepo.Create(context.Background(), memory); err != nil {
		log.Printf("[memory] create error: %v", err)
		return
	}
	if e.vectorStore != nil && e.embedder != nil && memory.MemoryValue != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			vec, err := e.embedder.Embed(ctx, memory.MemoryValue)
			if err != nil {
				return
			}
			e.vectorStore.Upsert("mem:"+memory.ID, vec, map[string]string{
				"type":    "mem",
				"user_id": memory.UserID,
			})
			embStr := llm.EmbeddingToString(vec)
			e.memRepo.UpdateEmbedding(context.Background(), memory.ID, &embStr)
		}()
	}
}

func (e *MemoryExtractor) updateProgress(ctx context.Context, conv *model.Conversation, lastMsgID string) {
	now := time.Now()
	conv.LastExtractedMsgID = &lastMsgID
	conv.LastExtractedAt = &now
	e.convRepo.Update(ctx, conv)
}

func hashPrompt(text string) string {
	h := md5.Sum([]byte(text))
	return hex.EncodeToString(h[:])
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
