package agent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
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
	llmClient     llm.LLMClient
	memRepo       *service.UserMemoryRepo
	vectorStore   *VectorStore
	embedder      *llm.Embedder
	msgRepo       *service.MessageRepo
	convRepo      *service.ConversationRepo
	embeddingRepo *service.EmbeddingRepo
	dupeThreshold float64
}

// NewMemoryExtractor 创建提取器
func NewMemoryExtractor(llmClient llm.LLMClient, memRepo *service.UserMemoryRepo,
	vectorStore *VectorStore, embedder *llm.Embedder,
	msgRepo *service.MessageRepo, convRepo *service.ConversationRepo,
	embeddingRepo *service.EmbeddingRepo,
	dupeThreshold float64) *MemoryExtractor {
	if dupeThreshold <= 0 {
		dupeThreshold = 0.85 // 默认值
	}
	return &MemoryExtractor{
		llmClient:     llmClient,
		memRepo:       memRepo,
		vectorStore:   vectorStore,
		embedder:      embedder,
		msgRepo:       msgRepo,
		convRepo:      convRepo,
		embeddingRepo: embeddingRepo,
		dupeThreshold: dupeThreshold,
	}
}

type extractResult struct {
	Summary string   `json:"summary"`
	Facts   []string `json:"facts,omitempty"`
}

const extractSystemPrompt = "你是记忆提取助手。从对话中归纳出关于用户的有价值的洞察。合并同类信息，提炼规律和偏好，不要逐条罗列原始数据。如果信息不足以归纳就回复\"无\"。请只输出 JSON 格式。重要：将\"今天\"、\"昨天\"、\"上周\"等相对时间转换为绝对日期（如 2026-05-12）。"

const extractPromptPrefix = "从以下对话中归纳关于用户的事实、偏好、习惯。合并同类信息，提炼出有价值的洞察（如消费水平、周期规律、分类偏好），不要逐条复述原始消息。如果没有值得提取的信息就回复\"无\"。\n请严格按以下 JSON 格式输出，不要包含其他内容：\n{\"summary\":\"一句话概括\",\"facts\":[\"支持细节1\",\"支持细节2\"]}"

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

	now := time.Now().Format("2006-01-02 15:04:05 Monday")
	prompt := fmt.Sprintf("%s\n\n当前时间: %s\n\n%s", extractPromptPrefix, now, sb.String())
	promptHash := hashPrompt(prompt)

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: extractSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return fmt.Errorf("extract memory: %w", err)
	}

	lastMsg := msgs[len(msgs)-1]
	result := parseExtractResult(msg.Content)
	if result == nil || isEmptySummary(result.Summary) {
		e.updateProgress(ctx, conv, lastMsg.ID)
		return nil
	}

	if e.isDuplicate(ctx, conv.UserID, result.Summary) {
		e.updateProgress(ctx, conv, lastMsg.ID)
		return nil
	}

	rawJSON, _ := json.Marshal(result)
	rawStr := string(rawJSON)
	memory := &model.UserMemory{
		ID:              uuid.New().String(),
		UserID:          conv.UserID,
		MemoryKey:       truncate(result.Summary, 50),
		MemoryValue:     result.Summary,
		RawJSON:         &rawStr,
		Source:          "conversation",
		ConfirmCount:    1,
		LastConfirmedAt: time.Now(),
		ExtractPrompt:   &prompt,
		PromptHash:      promptHash,
		ContentHash:     hashPrompt(result.Summary),
	}
	e.storeMemory(memory)

	log.Printf("[memory] extracted memory from conv %s: %s", conv.ID[:8], truncate(result.Summary, 80))
	e.updateProgress(ctx, conv, lastMsg.ID)
	return nil
}

// ExtractFromPrompt 从任意文本中提取记忆（CSV 导入等场景）
func (e *MemoryExtractor) ExtractFromPrompt(ctx context.Context, userID, promptText, source string) {
	promptHash := hashPrompt(promptText)
	existing, err := e.memRepo.FindByPromptHash(ctx, userID, promptHash)
	if err != nil {
		log.Printf("[memory] FindByPromptHash error: %v", err)
	}
	if existing != nil {
		log.Printf("[memory] prompt hash matched, skip extraction (source=%s)", source)
		return
	}

	now := time.Now().Format("2006-01-02 15:04:05 Monday")
	prompt := fmt.Sprintf("%s\n\n当前时间: %s\n\n%s", extractPromptPrefix, now, promptText)

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: extractSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		log.Printf("[memory] extract from prompt error: %v", err)
		return
	}

	result := parseExtractResult(msg.Content)
	if result == nil || isEmptySummary(result.Summary) {
		return
	}
	if e.isDuplicate(ctx, userID, result.Summary) {
		return
	}

	rawJSON, _ := json.Marshal(result)
	rawStr := string(rawJSON)
	memory := &model.UserMemory{
		ID:              uuid.New().String(),
		UserID:          userID,
		MemoryKey:       truncate(result.Summary, 50),
		MemoryValue:     result.Summary,
		RawJSON:         &rawStr,
		Source:          source,
		ConfirmCount:    1,
		LastConfirmedAt: time.Now(),
		ExtractPrompt:   &prompt,
		PromptHash:      promptHash,
		ContentHash:     hashPrompt(result.Summary),
	}
	e.storeMemory(memory)

	log.Printf("[memory] extracted memory from prompt (source=%s): %s", source, truncate(result.Summary, 80))
}

// parseExtractResult 从 LLM 回复中解析 JSON
func parseExtractResult(text string) *extractResult {
	var result extractResult
	_, err := ParseFlashJSON(text, &result)
	if err != nil {
		return nil
	}
	return &result
}

func (e *MemoryExtractor) isDuplicate(ctx context.Context, userID, text string) bool {
	// 1. 先尝试用 content_hash 做精确去重
	contentHash := hashPrompt(text)
	existing, err := e.memRepo.FindByContentHash(ctx, userID, contentHash)
	if err != nil {
		log.Printf("[memory] FindByContentHash error: %v", err)
	}
	if existing != nil {
		return true
	}

	// 2. 再用向量相似度做模糊去重
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
		if r.Score > e.dupeThreshold {
			return true
		}
	}
	return false
}

func (e *MemoryExtractor) storeMemory(memory *model.UserMemory) {
	if err := e.memRepo.Create(context.Background(), memory); err != nil {
		log.Printf("[memory] create error: %v", err)
		return
	}
	if e.vectorStore != nil && e.embedder != nil && e.embeddingRepo != nil && memory.MemoryValue != "" {
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
			err = e.embeddingRepo.Upsert(ctx, &model.Embedding{
				ID:         memory.ID,
				EntityType: model.EntityTypeMemory,
				EntityID:   memory.ID,
				UserID:     memory.UserID,
				Model:      e.embedder.ModelName(),
				Embedding:  &embStr,
			})
			if err != nil {
				log.Printf("[memory] save embedding error: %v", err)
			}
		}()
	}
}

func (e *MemoryExtractor) updateProgress(ctx context.Context, conv *model.Conversation, lastMsgID string) {
	now := time.Now()
	conv.LastExtractedMsgID = &lastMsgID
	conv.LastExtractedAt = &now
	if err := e.convRepo.Update(ctx, conv); err != nil {
		log.Printf("[memory] update progress error: %v", err)
	}
}

func hashPrompt(text string) string {
	h := md5.Sum([]byte(text))
	return hex.EncodeToString(h[:])
}

func isEmptySummary(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || s == "无"
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
