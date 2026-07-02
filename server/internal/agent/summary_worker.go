package agent

import (
	"context"
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

// SummaryWorker — 对话摘要生成器。
// 由 CronRunner 的 DelayedTask 驱动，在 session 结束后覆盖整段 session 生成导航级摘要。
type SummaryWorker struct {
	flashClient   llm.LLMClient
	msgRepo       *service.MessageRepo
	summaryRepo   *service.ConversationSummaryRepo
	embedder      app.Embedder
	chunkBuilder  app.ChunkEmbedder
	embeddingRepo *service.EmbeddingRepo
	vectorStore   app.VectorStore
}

// SummaryWorkerConfig — SummaryWorker 依赖配置
type SummaryWorkerConfig struct {
	FlashClient   llm.LLMClient
	MsgRepo       *service.MessageRepo
	SummaryRepo   *service.ConversationSummaryRepo
	Embedder      app.Embedder
	ChunkBuilder  app.ChunkEmbedder
	EmbeddingRepo *service.EmbeddingRepo
	VectorStore   app.VectorStore
}

// NewSummaryWorker 创建摘要生成器。
func NewSummaryWorker(cfg *SummaryWorkerConfig) *SummaryWorker {
	return &SummaryWorker{
		flashClient:   cfg.FlashClient,
		msgRepo:       cfg.MsgRepo,
		summaryRepo:   cfg.SummaryRepo,
		embedder:      cfg.Embedder,
		chunkBuilder:  cfg.ChunkBuilder,
		embeddingRepo: cfg.EmbeddingRepo,
		vectorStore:   cfg.VectorStore,
	}
}

// ProcessSessionEnd 处理 session 结束摘要——覆盖从 session 起始到 lastMsgID 的全部消息。
func (w *SummaryWorker) ProcessSessionEnd(ctx context.Context, convID, userID, lastMsgID string) (bool, error) {
	// 读取 lastMsgID 对应的消息，获取 SessionStartMsgID
	lastMsg, err := w.msgRepo.GetByID(ctx, lastMsgID)
	if err != nil {
		return false, fmt.Errorf("GetByID lastMsg: %w", err)
	}
	if lastMsg == nil {
		return false, fmt.Errorf("summary: lastMsg %s not found", lastMsgID[:8])
	}
	sessionStart := GetSessionStart(lastMsg)
	if sessionStart == "" {
		return false, nil // 没有 session 起始标记，跳过
	}

	// 查起始消息和结束消息的 created_at
	startMsg, err := w.msgRepo.GetByID(ctx, sessionStart)
	if err != nil {
		return false, fmt.Errorf("GetByID sessionStart: %w", err)
	}
	if startMsg == nil {
		return false, fmt.Errorf("summary: sessionStart %s not found", sessionStart[:8])
	}
	endMsg, err := w.msgRepo.GetByID(ctx, lastMsgID)
	if err != nil {
		return false, fmt.Errorf("GetByID endMsg: %w", err)
	}
	if endMsg == nil {
		return false, fmt.Errorf("summary: endMsg %s not found", lastMsgID[:8])
	}

	// 加载 session 内所有 user 消息
	msgs, err := w.msgRepo.ListUserMessagesBetween(ctx, convID, startMsg.CreatedAt, endMsg.CreatedAt, 0)
	if err != nil {
		return false, fmt.Errorf("ListUserMessagesBetween: %w", err)
	}
	if len(msgs) == 0 {
		return false, nil
	}

	log.Printf("[summary] session_end: conv=%s loading %d user msgs for previous session",
		convID[:8], len(msgs))

	// 查前序摘要，用于增量合并和 parent 链
	prev, err := w.summaryRepo.GetLatest(ctx, convID)
	if err != nil {
		log.Printf("[summary] GetLatest error: %v, proceeding without incremental merge", err)
	}
	var prevText string
	if prev != nil {
		prevText = prev.SummaryText
	}

	summaryText, reqJSON, respJSON, err := w.callFlash(ctx, prevText, msgs)
	if err != nil {
		return false, fmt.Errorf("callFlash: %w", err)
	}
	if summaryText == "" {
		return false, nil
	}

	return w.saveSummary(ctx, convID, userID, msgs, prev, summaryText, reqJSON, respJSON)
}

// callFlash 调 Flash 生成摘要。返回文本及 request/response JSON（调试用）。
func (w *SummaryWorker) callFlash(ctx context.Context, prevSummary string, msgs []model.Message) (string, *string, *string, error) {
	systemPrompt := `你是一个对话摘要生成器。你的任务是基于对话历史生成一段导航级摘要。

规则：
1. 只记录话题流、分岔和结论方向，不记录具体事实细节
2. 如果提供了上一段摘要，请基于它和新对话消息，生成更新后的完整摘要（增量合并）
3. 如果旧话题在新增对话中不再出现，可以在摘要中淡出或删除
4. 纯文本叙事输出，不分点不分节
5. 300 字以内
6. 只输出摘要正文，不要加任何前缀、后缀或解释`

	var userMsg strings.Builder
	if prevSummary != "" {
		userMsg.WriteString("上一段摘要：\n")
		userMsg.WriteString(prevSummary)
		userMsg.WriteString("\n\n")
	}
	userMsg.WriteString("新增对话消息（只含用户发言）：\n")
	userMsg.WriteString(buildMsgBlock(msgs))

	resp, err := w.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: userMsg.String()},
		},
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
) (bool, error) {
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
		RequestJSON:     reqJSON,
		ResponseJSON:    respJSON,
		Status:          "active",
	}
	if err := w.summaryRepo.Create(ctx, record); err != nil {
		return false, fmt.Errorf("Create summary: %w", err)
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
	return true, nil
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
