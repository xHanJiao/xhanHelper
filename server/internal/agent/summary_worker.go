package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// SummaryWorker — 对话摘要生成器。
// 由 CronRunner 的 DelayedTask 驱动，检查窗口外是否有足够的未覆盖 user messages，
// 达到阈值时调 Flash 做增量合并生成导航级摘要。
type SummaryWorker struct {
	flashClient      llm.LLMClient
	msgRepo          *service.MessageRepo
	summaryRepo      *service.ConversationSummaryRepo
	historyRounds    int // N，原始窗口大小
	maxSummaryRounds int // M，单次摘要最多覆盖的 user message 条数
}

// SummaryWorkerConfig — SummaryWorker 依赖配置
type SummaryWorkerConfig struct {
	FlashClient      llm.LLMClient
	MsgRepo          *service.MessageRepo
	SummaryRepo      *service.ConversationSummaryRepo
	HistoryRounds    int
	MaxSummaryRounds int
}

// NewSummaryWorker 创建摘要生成器。
func NewSummaryWorker(cfg *SummaryWorkerConfig) *SummaryWorker {
	m := cfg.MaxSummaryRounds
	if m <= 0 {
		m = cfg.HistoryRounds / 2
		if m < 1 {
			m = 1
		}
	}
	return &SummaryWorker{
		flashClient:      cfg.FlashClient,
		msgRepo:          cfg.MsgRepo,
		summaryRepo:      cfg.SummaryRepo,
		historyRounds:    cfg.HistoryRounds,
		maxSummaryRounds: m,
	}
}

// ProcessTask 处理一条 summary_generation 延迟任务。路由到 bootstrap 或 append。
func (w *SummaryWorker) ProcessTask(ctx context.Context, convID, userID string) (bool, error) {
	windowStart, err := w.findWindowStart(ctx, convID)
	if err != nil {
		return false, err
	}
	if windowStart.IsZero() {
		return false, nil
	}

	latest, err := w.summaryRepo.GetLatest(ctx, convID)
	if err != nil {
		return false, fmt.Errorf("GetLatest: %w", err)
	}

	if latest == nil {
		return w.bootstrap(ctx, convID, userID, windowStart)
	}
	return w.append(ctx, convID, userID, latest, windowStart)
}

// findWindowStart 返回最近 N 轮中最老一条 user message 的 created_at。
func (w *SummaryWorker) findWindowStart(ctx context.Context, convID string) (time.Time, error) {
	rounds, err := w.msgRepo.ListRecentRounds(ctx, convID, w.historyRounds)
	if err != nil {
		return time.Time{}, fmt.Errorf("ListRecentRounds: %w", err)
	}
	if len(rounds) == 0 || len(rounds[0]) == 0 {
		return time.Time{}, nil
	}
	return rounds[0][0].CreatedAt, nil
}

// bootstrap — 冷启动：加载窗口外最近的 M 条消息，生成第一段摘要。
func (w *SummaryWorker) bootstrap(ctx context.Context, convID, userID string, windowStart time.Time) (bool, error) {
	msgs, err := w.msgRepo.ListUserMessagesBefore(ctx, convID, windowStart, w.maxSummaryRounds)
	if err != nil {
		return false, fmt.Errorf("ListUserMessagesBefore: %w", err)
	}
	if len(msgs) == 0 {
		return false, nil
	}

	// 冷启动也需要 K >= N/2 门槛
	threshold := w.historyRounds / 2
	if threshold < 1 {
		threshold = 1
	}
	k, _ := w.msgRepo.CountUserMessagesBetween(ctx, convID, time.Time{}, windowStart)
	if k < int64(threshold) {
		log.Printf("[summary] bootstrap: conv=%s K=%d < threshold=%d, skipping", convID[:8], k, threshold)
		return false, nil
	}

	log.Printf("[summary] bootstrap: conv=%s loading %d user messages (K=%d, limit=%d)",
		convID[:8], len(msgs), k, w.maxSummaryRounds)

	summaryText, reqJSON, respJSON, err := w.callFlash(ctx, "", msgs)
	if err != nil {
		return false, fmt.Errorf("callFlash: %w", err)
	}
	if summaryText == "" {
		return false, nil
	}

	return w.saveSummary(ctx, convID, userID, msgs, nil, summaryText, reqJSON, respJSON)
}

// append — 增量追加：从上次 cover_to 之后到窗口边界之间，K >= N/2 时增量生成。
func (w *SummaryWorker) append(ctx context.Context, convID, userID string, latest *model.ConversationSummary, windowStart time.Time) (bool, error) {
	afterTime, err := w.resolveAfterTime(ctx, latest)
	if err != nil {
		log.Printf("[summary] append: resolveAfterTime error: %v", err)
		return false, nil
	}

	// 如果已覆盖到窗口边界，无需生成
	if !afterTime.Before(windowStart) {
		return false, nil
	}

	k, err := w.msgRepo.CountUserMessagesBetween(ctx, convID, afterTime, windowStart)
	if err != nil {
		return false, fmt.Errorf("CountUserMessagesBetween: %w", err)
	}

	threshold := w.historyRounds / 2
	if threshold < 1 {
		threshold = 1
	}
	if k < int64(threshold) {
		log.Printf("[summary] append: conv=%s K=%d < threshold=%d, skipping", convID[:8], k, threshold)
		return false, nil
	}

	msgs, err := w.msgRepo.ListUserMessagesBetween(ctx, convID, afterTime, windowStart, w.maxSummaryRounds)
	if err != nil {
		return false, fmt.Errorf("ListUserMessagesBetween: %w", err)
	}
	if len(msgs) == 0 {
		return false, nil
	}

	log.Printf("[summary] append: conv=%s loading %d user messages (K=%d, limit=%d)",
		convID[:8], len(msgs), k, w.maxSummaryRounds)

	summaryText, reqJSON, respJSON, err := w.callFlash(ctx, latest.SummaryText, msgs)
	if err != nil {
		return false, fmt.Errorf("callFlash: %w", err)
	}
	if summaryText == "" {
		return false, nil
	}

	return w.saveSummary(ctx, convID, userID, msgs, latest, summaryText, reqJSON, respJSON)
}

// resolveAfterTime 根据上段摘要的 cover_to_msg_id 找到对应消息的 created_at。
func (w *SummaryWorker) resolveAfterTime(ctx context.Context, latest *model.ConversationSummary) (time.Time, error) {
	coverMsg, err := w.msgRepo.GetByIDCols(ctx, latest.CoverToMsgID, "id", "created_at")
	if err != nil {
		return time.Time{}, err
	}
	if coverMsg == nil {
		// 退化为从 cover_from_msg_id 查
		coverMsg, err = w.msgRepo.GetByIDCols(ctx, latest.CoverFromMsgID, "id", "created_at")
		if err != nil || coverMsg == nil {
			return time.Time{}, fmt.Errorf("cover range messages missing for summary=%s", latest.ID[:8])
		}
	}
	return coverMsg.CreatedAt, nil
}

// callFlash 调 Flash 生成增量摘要。返回文本及 request/response JSON（调试用）。
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

	if prev != nil {
		if err := w.summaryRepo.MarkSuperseded(ctx, prev.ID); err != nil {
			log.Printf("[summary] MarkSuperseded error (old summary=%s still active): %v", prev.ID[:8], err)
		}
	}

	log.Printf("[summary] created: id=%s conv=%s cover=%s..%s text=%d chars",
		record.ID[:8], convID[:8], coverFrom[:8], coverTo[:8], len(text))
	return true, nil
}

// truncateRunes 按 rune 截断文本。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
