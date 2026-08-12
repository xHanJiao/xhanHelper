package agent

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// SummaryWorker — 对话摘要生成器（PostChatWorker）。
// 由 PostChatWorkerGroup 分发 session 快照，覆盖整段 session 生成导航级摘要。
type SummaryWorker struct {
	flashClient   llm.LLMClient
	summaryRepo   *service.ConversationSummaryRepo
	embedder      app.Embedder
	chunkBuilder  app.ChunkEmbedder
	embeddingRepo *service.EmbeddingRepo
	vectorStore   app.VectorStore
	persistDebug  bool
}

// SummaryWorkerConfig — SummaryWorker 依赖配置
type SummaryWorkerConfig struct {
	FlashClient   llm.LLMClient
	SummaryRepo   *service.ConversationSummaryRepo
	Embedder      app.Embedder
	ChunkBuilder  app.ChunkEmbedder
	EmbeddingRepo *service.EmbeddingRepo
	VectorStore   app.VectorStore
	PersistDebug  bool // 是否持久化完整 LLM 请求/响应 JSON（仅 dev 调试用）
}

// NewSummaryWorker 创建摘要生成器。
func NewSummaryWorker(cfg *SummaryWorkerConfig) *SummaryWorker {
	return &SummaryWorker{
		flashClient:   cfg.FlashClient,
		summaryRepo:   cfg.SummaryRepo,
		embedder:      cfg.Embedder,
		chunkBuilder:  cfg.ChunkBuilder,
		embeddingRepo: cfg.EmbeddingRepo,
		vectorStore:   cfg.VectorStore,
		persistDebug:  cfg.PersistDebug,
	}
}

// Name 实现 PostChatWorker。
func (w *SummaryWorker) Name() string { return "summary" }

// Process 处理一个 session：拉旧摘要（增量合并）→ LLM 生成 → 持久化 + embedding。
// 实现 PostChatWorker。
func (w *SummaryWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubSummary,
		UserID:      sess.UserID,
		ConvID:      sess.ConvID,
	})
	// 查前序摘要，用于增量合并和 parent 链
	prev, err := w.summaryRepo.GetLatest(ctx, sess.ConvID)
	if err != nil {
		log.Printf("[summary] GetLatest error: %v, proceeding without incremental merge", err)
	}
	var prevText string
	if prev != nil {
		prevText = prev.SummaryText
	}

	summaryText, reqJSON, respJSON, err := w.callFlash(ctx, prevText, sess.Messages)
	if err != nil {
		return fmt.Errorf("callFlash: %w", err)
	}
	if summaryText == "" {
		return nil
	}

	return w.saveSummary(ctx, sess.ConvID, sess.UserID, sess.Messages, prev, summaryText, reqJSON, respJSON)
}

// summaryInstructionXML — 摘要生成指令消息的 XML 结构。
// 固定部分（task/rules/output_format）在文档前部，构成同 worker 内可命中的缓存前缀；
// 变化部分 previous_summary 放文档尾部，miss 的只有 tail。
type summaryInstructionXML struct {
	XMLName         struct{} `xml:"summary_extraction"`
	Task            string   `xml:"task"`
	Rules           []string `xml:"rules>rule"`
	OutputFormat    string   `xml:"output_format"`
	PreviousSummary string   `xml:"previous_summary,omitempty"`
}

// buildSummaryInstructionXML 构建摘要 worker 的指令 user 消息。
func buildSummaryInstructionXML(prevSummary string) string {
	doc := summaryInstructionXML{
		Task: "基于对话历史生成一段导航级摘要",
		Rules: []string{
			"只记录话题流、分岔和结论方向，不记录具体事实细节",
			"如果提供了上一段摘要，请基于它和新对话消息，生成更新后的完整摘要（增量合并）",
			"如果旧话题在新增对话中不再出现，可以在摘要中淡出或删除",
			"纯文本叙事输出，不分点不分节",
			"300 字以内",
			"只输出摘要正文，不要加任何前缀、后缀或解释",
		},
		OutputFormat: "输出摘要正文纯文本，不要用 markdown 代码块包裹，不要加任何前后缀",
	}
	if prevSummary != "" {
		doc.PreviousSummary = prevSummary
	}

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		log.Printf("[summary] encode instruction XML error: %v", err)
		return ""
	}
	return buf.String()
}

// callFlash 调 Flash 生成摘要。返回文本及 request/response JSON（调试用）。
// 缓存友好：system prompt 共享固定串；历史消息逐条展开（跨 worker 同 session 前缀命中）；
// 指令 XML 放最后一条消息（固定前缀命中，previous_summary tail miss）。
func (w *SummaryWorker) callFlash(ctx context.Context, prevSummary string, msgs []model.Message) (string, *string, *string, error) {
	chatMsgs := make([]llm.Message, 0, len(msgs)+1)
	for _, m := range msgs {
		chatMsgs = append(chatMsgs, llm.Message{Role: m.Role, Content: m.Content})
	}
	chatMsgs = append(chatMsgs, llm.Message{Role: "user", Content: buildSummaryInstructionXML(prevSummary)})

	resp, err := w.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: postChatSharedSystemPrompt,
		Messages:     chatMsgs,
	})
	if err != nil {
		return "", nil, nil, err
	}
	return strings.TrimSpace(resp.Content), resp.RequestJSON, resp.ResponseJSON, nil
}

// buildMsgBlock 将消息列表格式化为带时间的文本块。
func buildMsgBlock(msgs []model.Message) string {
	var lines []string
	for _, m := range msgs {
		timeStr := m.CreatedAt.In(config.BJ).Format("01-02 15:04")
		lines = append(lines, fmt.Sprintf("[%s] %s", timeStr, m.Content))
	}
	return strings.Join(lines, "\n")
}

// saveSummary 持久化新摘要。先建新，后 mark 旧段 superseded 保证重试安全。
func (w *SummaryWorker) saveSummary(
	ctx context.Context,
	convID, userID string,
	msgs []model.Message,
	prev *model.ConversationSummary,
	text string,
	reqJSON, respJSON *string,
) error {
	coverFrom := msgs[0].ID
	coverTo := msgs[len(msgs)-1].ID
	var parentID *string
	if prev != nil {
		parentID = &prev.ID
	}

	record := &model.ConversationSummary{
		ID:              uuid.New().String(),
		ConversationID:  convID,
		UserID:          userID,
		SummaryText:     text,
		CoverFromMsgID:  coverFrom,
		CoverToMsgID:    coverTo,
		ParentSummaryID: parentID,
		Status:          "active",
	}
	if w.persistDebug {
		record.RequestJSON = reqJSON
		record.ResponseJSON = respJSON
	}
	if err := w.summaryRepo.Create(ctx, record); err != nil {
		return fmt.Errorf("Create summary: %w", err)
	}

	// 异步做全文 embedding + chunk 建设（不阻塞摘要保存流程）
	go func() {
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer bgCancel()
		w.buildEmbeddingAndChunks(bgCtx, record)
	}()

	if prev != nil {
		if err := w.summaryRepo.MarkSuperseded(ctx, prev.ID); err != nil {
			log.Printf("[summary] MarkSuperseded error (old summary=%s still active): %v", prev.ID[:8], err)
		}
	}

	log.Printf("[summary] created: id=%s conv=%s cover=%s..%s text=%d chars",
		record.ID[:8], convID[:8], coverFrom[:8], coverTo[:8], len(text))
	return nil
}

// buildEmbeddingAndChunks 对新生成的摘要做全文 embedding 和 chunk 建设。
// embedder / chunkBuilder / embeddingRepo 为强依赖，nil 时 panic 暴露接线 bug。
func (w *SummaryWorker) buildEmbeddingAndChunks(ctx context.Context, s *model.ConversationSummary) {
	// 1. 全文 embedding（供 full-embedding 检索）
	vec, err := w.embedder.Embed(ctx, s.SummaryText)
	if err != nil {
		log.Printf("[summary] embed %s error: %v", s.ID[:8], err)
		return
	}
	embStr := llm.EmbeddingToString(vec)
	if err := w.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         s.ID,
		EntityType: model.EntityTypeSummary,
		EntityID:   s.ID,
		UserID:     s.UserID,
		Model:      w.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[summary] save embedding %s error: %v", s.ID[:8], err)
	}
	w.vectorStore.Upsert("summary:"+s.ID, vec, map[string]string{
		"type":       "summary",
		"user_id":    s.UserID,
		"summary_id": s.ID,
	})

	// 2. Chunk 建设（供 chunk 级检索）
	cnt, err := w.chunkBuilder.BuildChunks(ctx, s.UserID, model.EntityTypeSummary, s.ID, s.SummaryText, nil)
	if err != nil {
		log.Printf("[summary] build chunks %s error: %v", s.ID[:8], err)
	} else if cnt > 0 {
		log.Printf("[summary] built %d chunks for %s", cnt, s.ID[:8])
	}
}
