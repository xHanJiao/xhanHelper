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

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ExtractEvent — 流式提取事件
type ExtractEvent struct {
	Type       string            // "status" | "memory" | "duplicate" | "done" | "error"
	Phase      string            // Type=status 时: "fetching" | "extracting"
	Message    string            // Type=status/error 时的消息
	Memory     *model.UserMemory // Type=memory 时
	DupValue   string            // Type=duplicate 时: 被跳过的内容
	DupReason  string            // Type=duplicate 时: 跳过原因
	Created    int               // Type=done 时: 创建条数
	Duplicates int               // Type=done 时: 跳过条数
}

// ExtractCallback — 流式提取回调，每产生一条记忆或事件时调用
type ExtractCallback func(event ExtractEvent)

// MemoryExtractor — 从会话中提取记忆，向量去重
type MemoryExtractor struct {
	llmClient     llm.LLMClient
	memRepo       *service.UserMemoryRepo
	vectorStore   app.VectorStore
	embedder      *llm.Embedder
	msgRepo       *service.MessageRepo
	convRepo      *service.ConversationRepo
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
	Embedder      *llm.Embedder
	MsgRepo       *service.MessageRepo
	ConvRepo      *service.ConversationRepo
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
		msgRepo:       cfg.MsgRepo,
		convRepo:      cfg.ConvRepo,
		embeddingRepo: cfg.EmbeddingRepo,
		dupeThreshold: cfg.DupeThreshold,
		chunkBuilder:  cfg.ChunkBuilder,
		conceptEmbed:  cfg.ConceptEmbed,
	}
}

type extractResult struct {
	Extracted bool     `json:"extracted"`
	Summary   string   `json:"summary"`
	Facts     []string `json:"facts,omitempty"`
	Topic     string   `json:"topic,omitempty"`
	Tags      string   `json:"tags,omitempty"` // 逗号分隔标签
}

const extractSystemPrompt = "你是记忆提取助手。合并同类信息，不要逐条复述。不编造、不推测。信息不足时返回 {\"extracted\":false}。重要：将\"今天\"、\"昨天\"等相对时间转为绝对日期（如 2026-05-15）。"

const extractPromptPrefix = `从以下对话中提取有长期价值的信息，包括：用户的事实/偏好/习惯/计划、用户反复讨论的话题、用户表达的观点和判断、用户分享的外部信息、用户的反思/自我剖析/复盘、用户做的重大决定及理由、用户从经历中总结的经验教训、用户偏好或价值观的变化。合并同类信息，每次最多提取3条最重要的。

请严格按以下 JSON 格式输出，不要包含其他内容：
{"extracted":true,"summary":"一句话概括核心信息","facts":["支持细节1"],"topic":"一个关键主题词（如睡眠管理、职业规划、家庭关系），禁止斜杠、逗号、多个主题","tags":"关键词1,关键词2"}
tags 是从内容中提取的逗号分隔关键词（如 褪黑素,早醒,生物钟）。没有值得提取的内容时返回 {"extracted":false}。`

// BuildConversationText 读取会话新增消息，构建 "[role]: content\n" 格式文本
func BuildConversationText(ctx context.Context, msgRepo *service.MessageRepo, conv *model.Conversation, limit int) ([]model.Message, string, error) {
	var msgs []model.Message
	var err error
	if conv.LastExtractedMsgID != nil && *conv.LastExtractedMsgID != "" {
		msgs, err = msgRepo.ListByConversationSince(ctx, conv.ID, *conv.LastExtractedMsgID, limit)
	} else {
		msgs, err = msgRepo.ListByConversation(ctx, conv.ID, limit)
	}
	if err != nil || len(msgs) == 0 {
		return nil, "", err
	}
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}
	return msgs, sb.String(), nil
}

// ExtractFromConversation 从指定会话中增量提取记忆。返回提取到的记忆（可能为 nil）。
func (e *MemoryExtractor) ExtractFromConversation(ctx context.Context, conv *model.Conversation) (*model.UserMemory, error) {
	msgs, convText, err := BuildConversationText(ctx, e.msgRepo, conv, 400)
	if err != nil || len(msgs) == 0 {
		return nil, err
	}
	return e.ExtractFromMessages(ctx, conv, msgs, convText)
}

// ExtractFromMessages 用已构建的消息文本提取记忆，统一 BuildConversationText 输入。
func (e *MemoryExtractor) ExtractFromMessages(ctx context.Context, conv *model.Conversation, msgs []model.Message, convText string) (*model.UserMemory, error) {

	now := time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")
	prompt := fmt.Sprintf("%s\n\n当前时间: %s\n\n%s", extractPromptPrefix, now, convText)
	promptHash := hashPrompt(prompt)

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: extractSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("extract memory: %w", err)
	}

	lastMsg := msgs[len(msgs)-1]
	result := parseExtractResult(msg.Content)
	if result == nil || isEmptyResult(result) {
		e.updateProgress(ctx, conv, lastMsg.ID)
		return nil, nil
	}

	if e.isDuplicate(ctx, conv.UserID, result.Summary) {
		e.updateProgress(ctx, conv, lastMsg.ID)
		return nil, nil
	}

	rawJSON, _ := json.Marshal(result)
	rawStr := string(rawJSON)
	memory := &model.UserMemory{
		ID:              uuid.New().String(),
		UserID:          conv.UserID,
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

	log.Printf("[memory] extracted memory from conv %s: %s", conv.ID[:8], truncate(result.Summary, 80))
	e.updateProgress(ctx, conv, lastMsg.ID)
	return memory, nil
}

// ExtractFromConversationWithCallback 流式版本——通过回调逐条推送提取结果
func (e *MemoryExtractor) ExtractFromConversationWithCallback(ctx context.Context, conv *model.Conversation, cb ExtractCallback) error {
	if cb == nil {
		_, err := e.ExtractFromConversation(ctx, conv)
		return err
	}

	msgs, convText, err := BuildConversationText(ctx, e.msgRepo, conv, 400)
	if err != nil {
		cb(ExtractEvent{Type: "error", Message: err.Error()})
		return err
	}
	if len(msgs) == 0 {
		cb(ExtractEvent{Type: "done", Created: 0})
		return nil
	}

	cb(ExtractEvent{Type: "status", Phase: "fetching", Message: fmt.Sprintf("加载了 %d 条新消息", len(msgs))})

	now := time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")
	prompt := fmt.Sprintf("%s\n\n当前时间: %s\n\n%s", extractPromptPrefix, now, convText)
	promptHash := hashPrompt(prompt)

	cb(ExtractEvent{Type: "status", Phase: "extracting", Message: "正在调用 LLM 提取记忆..."})

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: extractSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		cb(ExtractEvent{Type: "error", Message: fmt.Sprintf("LLM 调用失败: %v", err)})
		return fmt.Errorf("extract memory: %w", err)
	}

	lastMsg := msgs[len(msgs)-1]
	result := parseExtractResult(msg.Content)
	if result == nil || isEmptyResult(result) {
		cb(ExtractEvent{Type: "done", Created: 0})
		e.updateProgress(ctx, conv, lastMsg.ID)
		return nil
	}

	created := 0
	dupes := 0

	// 先处理 summary
	if !e.isDuplicate(ctx, conv.UserID, result.Summary) {
		rawJSON, _ := json.Marshal(result)
		rawStr := string(rawJSON)
		memory := &model.UserMemory{
			ID:              uuid.New().String(),
			UserID:          conv.UserID,
			MemoryKey:       truncate(result.Summary, 50),
			MemoryValue:     result.Summary,
			RawJSON:         &rawStr,
			Source:          "conversation",
			Topic:           result.Topic,
			Tags:            result.Tags,
			ConfirmCount:    1,
			LastConfirmedAt: time.Now(),
			ExtractPrompt:   &prompt,
			PromptHash:      promptHash,
			ContentHash:     hashPrompt(result.Summary),
		}
		e.storeMemory(memory)
		cb(ExtractEvent{Type: "memory", Memory: memory})
		created++
		log.Printf("[memory] extracted memory from conv %s: %s", conv.ID[:8], truncate(result.Summary, 80))
	} else {
		cb(ExtractEvent{Type: "duplicate", DupValue: result.Summary, DupReason: "已存在相似记忆"})
		dupes++
	}

	cb(ExtractEvent{Type: "done", Created: created, Duplicates: dupes})
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

	now := time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")
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
	if result == nil || isEmptyResult(result) {
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
		MemoryKey:       formatMemoryKey(result),
		MemoryValue:     result.Summary,
		RawJSON:         &rawStr,
		Source:          source,
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
	results := e.vectorStore.Search(vec, 3, func(_ string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == "mem" && metadata["user_id"] == userID
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
	// 同步嵌入，确保 StateUpdater 在后续步骤中能搜到该记忆的向量
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	e.conceptEmbed.EnsureTopicEmbedded(ctx, memory.UserID, memory.Topic, "memory", memory.ID)
	if memory.Tags != "" {
		e.conceptEmbed.EnsureTagsEmbedded(ctx, memory.UserID, memory.Tags, "memory", memory.ID)
	}
	if memory.MemoryValue != "" {
		n, err := e.chunkBuilder.BuildChunks(ctx, memory.UserID, "memory", memory.ID, memory.MemoryValue)
		if err != nil {
			log.Printf("[memory] chunk build error: %v", err)
		} else {
			log.Printf("[memory] chunked mem %s: %d sentences", memory.ID[:8], n)
		}
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
