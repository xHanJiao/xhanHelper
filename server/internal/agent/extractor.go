package agent

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// extractResult — 记忆提取 LLM 输出结构
type extractResult struct {
	Extracted bool     `json:"extracted"`
	Summary   string   `json:"summary"`
	Facts     []string `json:"facts,omitempty"`
	Topic     string   `json:"topic,omitempty"`
	Tags      string   `json:"tags,omitempty"` // 逗号分隔标签
}

// memoryInstructionXML — 记忆提取指令消息的 XML 结构。
// 固定部分（task/rules/output_format）在文档前部，构成同 worker 内可命中的缓存前缀；
// 变化部分 current_time 放文档尾部，miss 的只有 tail。
type memoryInstructionXML struct {
	XMLName      struct{} `xml:"memory_extraction"`
	Task         string   `xml:"task"`
	Rules        []string `xml:"rules>rule"`
	OutputFormat string   `xml:"output_format"`
	CurrentTime  string   `xml:"current_time,omitempty"`
}

// buildMemoryInstructionXML 构建记忆提取 worker 的指令 user 消息。
func buildMemoryInstructionXML(now string) string {
	doc := memoryInstructionXML{
		Task: "从以下对话中提取有长期价值的信息，包括：用户的事实/偏好/习惯/计划、用户反复讨论的话题、用户表达的观点和判断、用户分享的外部信息、用户的反思/自我剖析/复盘、用户做的重大决定及理由、用户从经历中总结的经验教训、用户偏好或价值观的变化。合并同类信息，每次最多提取3条最重要的。逐日流水（睡眠打卡、体重、饮酒、运动、时间日志等按天读数）不提取为记忆，属于文档台账。",
		Rules: []string{
			"合并同类信息，不要逐条复述。不编造、不推测",
			"信息不足时返回 {\"extracted\":false}",
			"将\"今天\"、\"昨天\"等相对时间转为绝对日期（如 2026-05-15）",
			"逐日读数（睡眠时长、步数、体重这类时序数据，走台账）不提取",
		},
		OutputFormat: `{"extracted":true,"summary":"一句话概括核心信息","facts":["支持细节1"],"topic":"一个关键主题词（如睡眠管理、职业规划、家庭关系），禁止斜杠、逗号、多个主题","tags":"关键词1,关键词2"}
tags 是从内容中提取的逗号分隔关键词（如 褪黑素,早醒,生物钟）。没有值得提取的内容时返回 {"extracted":false}`,
		CurrentTime: now,
	}

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		log.Printf("[memory] encode instruction XML error: %v", err)
		return ""
	}
	return buf.String()
}

// MemoryExtractor — 从对话中提取记忆，向量去重。
// 由 MemoryWorker（PostChatWorker）调用；输入为 session 快照中的 user 消息。
type MemoryExtractor struct {
	llmClient     llm.LLMClient
	memRepo       *service.UserMemoryRepo
	vectorStore   app.VectorStore
	embedder      app.Embedder
	embeddingRepo *service.EmbeddingRepo
	dupeThreshold float64
	chunkBuilder  *ChunkBuilder
	conceptEmbed  *ConceptEmbedService
}

// MemoryExtractorConfig — 记忆提取器依赖配置
type MemoryExtractorConfig struct {
	LLMClient     llm.LLMClient
	MemRepo       *service.UserMemoryRepo
	VectorStore   app.VectorStore
	Embedder      app.Embedder
	EmbeddingRepo *service.EmbeddingRepo
	DupeThreshold float64
	ChunkBuilder  *ChunkBuilder
	ConceptEmbed  *ConceptEmbedService
}

// NewMemoryExtractor 创建提取器
func NewMemoryExtractor(cfg *MemoryExtractorConfig) *MemoryExtractor {
	if cfg.DupeThreshold <= 0 {
		cfg.DupeThreshold = 0.85
	}
	return &MemoryExtractor{
		llmClient:     cfg.LLMClient,
		memRepo:       cfg.MemRepo,
		vectorStore:   cfg.VectorStore,
		embedder:      cfg.Embedder,
		embeddingRepo: cfg.EmbeddingRepo,
		dupeThreshold: cfg.DupeThreshold,
		chunkBuilder:  cfg.ChunkBuilder,
		conceptEmbed:  cfg.ConceptEmbed,
	}
}

// ExtractFromMessages 从给定会话消息中提取记忆。
// 不做游标/进度更新（session-window 驱动，天然增量，无 LastExtractedMsgID 概念）。
// 返回提取到的记忆（可能为 nil）。
// 缓存友好：system prompt 用共享固定串，任务指令全部编码进 user message（与 profile worker 同模式）。
func (e *MemoryExtractor) ExtractFromMessages(ctx context.Context, userID string, msgs []model.Message) (*model.UserMemory, error) {
	now := time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")
	prompt := buildMemoryInstructionXML(now)
	promptHash := hashPrompt(prompt)

	chatMsgs := make([]llm.Message, 0, len(msgs)+1)
	for _, m := range msgs {
		chatMsgs = append(chatMsgs, llm.Message{Role: m.Role, Content: m.Content})
	}
	chatMsgs = append(chatMsgs, llm.Message{Role: "user", Content: prompt})

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: postChatSharedSystemPrompt,
		Messages:     chatMsgs,
	})
	if err != nil {
		return nil, fmt.Errorf("extract memory: %w", err)
	}

	result := parseExtractResult(msg.Content)
	if result == nil || isEmptyResult(result) {
		return nil, nil
	}

	if e.isDuplicate(ctx, userID, result.Summary) {
		return nil, nil
	}

	rawJSON, _ := json.Marshal(result)
	rawStr := string(rawJSON)
	memory := &model.UserMemory{
		ID:              uuid.New().String(),
		UserID:          userID,
		MemoryKey:       formatMemoryKey(result),
		MemoryValue:     result.Summary,
		RawJSON:         &rawStr,
		Source:          "conversation",
		Topic:           result.Topic,
		Tags:            result.Tags,
		Facts:           factsPtr(result.Facts),
		ConfirmCount:    1,
		LastConfirmedAt: time.Now(),
		ExtractPrompt:   &prompt,
		PromptHash:      promptHash,
		ContentHash:     hashPrompt(result.Summary),
	}
	e.storeMemory(memory)

	log.Printf("[memory] extracted memory: %s", truncate(result.Summary, 80))
	return memory, nil
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
	results := e.vectorStore.Search(vec, 3, func(_ string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == model.VectorTypeMemory && metadata["user_id"] == userID
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
	// 同步嵌入，确保 StateWorker 在后续步骤中能搜到该记忆的向量
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	e.conceptEmbed.EnsureTopicEmbedded(ctx, memory.UserID, memory.Topic, model.EntityTypeMemory, memory.ID)
	if memory.Tags != "" {
		e.conceptEmbed.EnsureTagsEmbedded(ctx, memory.UserID, memory.Tags, model.EntityTypeMemory, memory.ID)
	}
	// 全文嵌入
	vec, err := e.embedder.Embed(ctx, memory.MemoryValue)
	if err != nil {
		log.Printf("[memory] embed full mem %.8s error: %v", memory.ID, err)
	} else {
		e.vectorStore.Upsert(model.VectorPrefixMemory+memory.ID, vec, map[string]string{
			"type":    model.VectorTypeMemory,
			"user_id": memory.UserID,
			"topic":   memory.Topic,
		})
		embStr := llm.EmbeddingToString(vec)
		if err := e.embeddingRepo.Upsert(ctx, &model.Embedding{
			ID:         memory.ID,
			EntityType: model.EntityTypeMemory,
			EntityID:   memory.ID,
			UserID:     memory.UserID,
			Model:      e.embedder.ModelName(),
			Embedding:  &embStr,
		}); err != nil {
			log.Printf("[memory] upsert full embedding %.8s error: %v", memory.ID, err)
		}
	}

	n, err := e.chunkBuilder.BuildChunks(ctx, memory.UserID, model.EntityTypeMemory, memory.ID, memory.MemoryValue, map[string]string{"topic": memory.Topic})
	if err != nil {
		log.Printf("[memory] chunk build error: %v", err)
	} else {
		log.Printf("[memory] chunked mem %s: %d sentences", memory.ID[:8], n)
	}
}

func hashPrompt(text string) string {
	h := md5.Sum([]byte(text))
	return hex.EncodeToString(h[:])
}

func formatMemoryKey(r *extractResult) string {
	if r.Topic != "" {
		return r.Topic
	}
	return truncate(r.Summary, 50)
}

func factsPtr(facts []string) *string {
	if len(facts) == 0 {
		return nil
	}
	b, _ := json.Marshal(facts)
	s := string(b)
	return &s
}

func isEmptyResult(r *extractResult) bool {
	if r == nil {
		return true
	}
	// 新格式：explicit flag
	if !r.Extracted {
		return true
	}
	// 兜底：旧格式或兼容不标准输出
	s := strings.TrimSpace(r.Summary)
	return s == "" || s == "无"
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
