package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// ReflectionOrchestrator — 反思循环编排器（独立于 HTTP server）
type ReflectionOrchestrator struct {
	flashClient        llm.LLMClient
	reflectionReg      *tool.Registry
	traceRepo          *service.ReflectionTraceRepo
	themeStore         *ThemeStore
	mailboxRepo        *service.MailboxRepo
	msgRepo            *service.MessageRepo
	noteRepo           *service.NoteRepo
	memRepo            *service.UserMemoryRepo
	pendingFindingRepo *service.PendingFindingRepo
	maxIterations      int
	maxDeliveryRetries int

	activeDurationDays map[string]int
}

// ReflectionOrchestratorConfig — 反思编排器依赖配置
type ReflectionOrchestratorConfig struct {
	FlashClient        llm.LLMClient
	ReflectionReg      *tool.Registry
	TraceRepo          *service.ReflectionTraceRepo
	ThemeStore         *ThemeStore
	MailboxRepo        *service.MailboxRepo
	MsgRepo            *service.MessageRepo
	NoteRepo           *service.NoteRepo
	MemRepo            *service.UserMemoryRepo
	PendingFindingRepo *service.PendingFindingRepo
	MaxIterations      int
	MaxDeliveryRetries int
	ActiveDurationDays map[string]int
}

func NewReflectionOrchestrator(cfg *ReflectionOrchestratorConfig) *ReflectionOrchestrator {
	if cfg.MaxDeliveryRetries <= 0 {
		cfg.MaxDeliveryRetries = 3
	}
	if len(cfg.ActiveDurationDays) == 0 {
		cfg.ActiveDurationDays = map[string]int{"long": 90, "medium": 30, "short": 14, "ephemeral": 7}
	}
	return &ReflectionOrchestrator{
		flashClient:        cfg.FlashClient,
		reflectionReg:      cfg.ReflectionReg,
		traceRepo:          cfg.TraceRepo,
		themeStore:         cfg.ThemeStore,
		mailboxRepo:        cfg.MailboxRepo,
		msgRepo:            cfg.MsgRepo,
		noteRepo:           cfg.NoteRepo,
		memRepo:            cfg.MemRepo,
		pendingFindingRepo: cfg.PendingFindingRepo,
		maxIterations:      cfg.MaxIterations,
		maxDeliveryRetries: cfg.MaxDeliveryRetries,
		activeDurationDays: cfg.ActiveDurationDays,
	}
}

// DiscoverDraftFromConversation 从单个会话的消息中发现主题。
// 注：此方法当前未使用。主题发现已收敛至每日定时任务（MorningReview/EveningReview），
// 对话后处理只做记忆提取和主题更新。保留此方法以备后续需要。
func (o *ReflectionOrchestrator) DiscoverDraftFromConversation(ctx context.Context, userID, convID string) error {
	msgs, err := o.msgRepo.ListByConversation(ctx, convID, 50)
	if err != nil || len(msgs) == 0 {
		return err
	}
	var sb string
	for _, m := range msgs {
		if m.Role == "tool" {
			continue
		}
		sb += fmt.Sprintf("[%s]: %s\n", m.Role, m.Content)
	}
	if sb == "" {
		return nil
	}

	t, ok := o.reflectionReg.GetToolMap()["theme_discoverer"]
	if !ok {
		return nil
	}
	args, _ := json.Marshal(map[string]interface{}{
		"mode":     "conversation",
		"messages": sb,
	})
	res := t.Execute(&tool.ExecuteContext{Ctx: ctx, UserID: userID}, "discover", args)
	if !res.Success || res.Data == nil {
		return nil
	}

	var output DiscoverResult
	if json.Unmarshal(res.Data, &output) != nil {
		return nil
	}
	log.Printf("[reflection] draft discovery done: created=%d rejected=%d",
		len(output.Created), len(output.Rejected))
	return nil
}

// RunSilent 静默模式：只发现草稿主题，不投递信箱
func (o *ReflectionOrchestrator) RunSilent(ctx context.Context, userID, triggerType string) error {
	return o.runOnce(ctx, userID, triggerType, true)
}

// RunOnce 执行一次完整的反思循环
func (o *ReflectionOrchestrator) RunOnce(ctx context.Context, userID, triggerType string) error {
	return o.runOnce(ctx, userID, triggerType, false)
}

// DeliverPendingFindings 扫暂存发现，经 Scorer → Secretary → 投递信箱
func (o *ReflectionOrchestrator) DeliverPendingFindings(ctx context.Context, userID string) (int, error) {
	traceID := uuid.New().String()
	log.Printf("[reflection] trace=%s trigger=deliver_window user=%s START", traceID[:8], userID)

	tr := &model.ReflectionTrace{
		ID: traceID, UserID: userID, TriggerType: "deliver_window",
		StartedAt: time.Now(), Status: "running",
	}
	o.traceRepo.CreateTrace(ctx, tr)

	// 从表拉 pending findings
	rawFindings, err := o.pendingFindingRepo.ListPending(ctx, userID)
	if err != nil {
		endTraceSafe(o.traceRepo, ctx, traceID, "error", 0, 0, strPtr(err.Error()))
		return 0, err
	}

	if len(rawFindings) == 0 {
		endTraceSafe(o.traceRepo, ctx, traceID, "empty", 0, 0, nil)
		log.Printf("[reflection] trace=%s DONE: no pending findings", traceID[:8])
		return 0, nil
	}

	// 转换格式 + 收集 IDs
	ids := make([]string, 0, len(rawFindings))
	allFindings := make([]map[string]interface{}, 0, len(rawFindings))
	themeSet := make(map[string]string)
	for _, f := range rawFindings {
		ids = append(ids, f.ID)
		m := map[string]interface{}{
			"finding_id": f.ID,
			"theme_id":   f.ThemeID,
			"theme_name": f.ThemeName,
			"title":      f.Title,
			"content":    f.Content,
			"urgency":    f.Urgency,
		}
		if f.SourceRefs != "" {
			var refs []string
			json.Unmarshal([]byte(f.SourceRefs), &refs)
			m["source_refs"] = refs
		}
		allFindings = append(allFindings, m)
		themeSet[f.ThemeID] = f.ThemeName
	}

	// Scorer → Secretary → Mailbox 管道
	scorerT, _ := o.reflectionReg.GetToolMap()["scorer"]
	secT, _ := o.reflectionReg.GetToolMap()["secretary"]
	mailT, _ := o.reflectionReg.GetToolMap()["mailbox"]
	ec := &tool.ExecuteContext{Ctx: ctx, UserID: userID}

	scorerPayload, _ := json.Marshal(map[string]interface{}{"findings": allFindings})
	scorerRes := scorerT.Execute(ec, "score", scorerPayload)
	if !scorerRes.Success || scorerRes.Data == nil {
		o.incrementRetries(ctx, ids)
		endTraceSafe(o.traceRepo, ctx, traceID, "error", len(allFindings), 0, strPtr("scorer failed"))
		return 0, fmt.Errorf("scorer failed")
	}

	var scorerOutput struct {
		Ranked []json.RawMessage `json:"ranked"`
	}
	json.Unmarshal(scorerRes.Data, &scorerOutput)
	secPayload, _ := json.Marshal(map[string]interface{}{"findings": scorerOutput.Ranked})
	secRes := secT.Execute(ec, "humanize", secPayload)
	if !secRes.Success || secRes.Data == nil {
		o.incrementRetries(ctx, ids)
		endTraceSafe(o.traceRepo, ctx, traceID, "error", len(allFindings), 0, strPtr("secretary failed"))
		return 0, fmt.Errorf("secretary failed")
	}

	var secOutput struct {
		Messages []struct {
			Title        string `json:"title"`
			Notification string `json:"notification"`
		} `json:"messages"`
	}
	json.Unmarshal(secRes.Data, &secOutput)
	mailItems := make([]map[string]interface{}, 0, len(secOutput.Messages))
	for _, msg := range secOutput.Messages {
		item := map[string]interface{}{
			"title":   msg.Title,
			"content": msg.Notification,
			"type":    "theme_finding",
			"source":  "主题简报",
		}
		if len(themeSet) == 1 {
			for tid, tname := range themeSet {
				item["entity_type"] = "theme"
				item["entity_id"] = tid
				item["source"] = tname
			}
		}
		mailItems = append(mailItems, item)
	}
	mailPayload, _ := json.Marshal(map[string]interface{}{"items": mailItems})
	mailRes := mailT.Execute(ec, "deliver", mailPayload)

	delivered := 0
	if mailRes.Success {
		delivered = len(mailItems)
		o.pendingFindingRepo.MarkDelivered(ctx, ids)
	} else {
		o.incrementRetries(ctx, ids)
	}

	endTraceSafe(o.traceRepo, ctx, traceID, "completed", len(allFindings), delivered, nil)
	log.Printf("[reflection] trace=%s DONE findings=%d delivered=%d", traceID[:8], len(allFindings), delivered)
	return delivered, nil
}

func (o *ReflectionOrchestrator) incrementRetries(ctx context.Context, ids []string) {
	log.Printf("[reflection] delivery failed, incrementing retries for %d findings", len(ids))
	for _, id := range ids {
		o.pendingFindingRepo.IncrementRetry(ctx, id)
	}
}

// MorningReview 早安回顾
func (o *ReflectionOrchestrator) MorningReview(ctx context.Context, userID string) (int, error) {
	return o.dailyReview(ctx, userID, "morning_review")
}

// EveningReview 晚安回顾
func (o *ReflectionOrchestrator) EveningReview(ctx context.Context, userID string) (int, error) {
	return o.dailyReview(ctx, userID, "evening_review")
}

// dailyReview 早晚回顾：expire draft + active 衰减 + 对有新活动的主题运行分析
func (o *ReflectionOrchestrator) dailyReview(ctx context.Context, userID, triggerType string) (int, error) {
	traceID := uuid.New().String()
	log.Printf("[reflection] trace=%s trigger=%s user=%s START", traceID[:8], triggerType, userID)

	tr := &model.ReflectionTrace{
		ID: traceID, UserID: userID, TriggerType: triggerType,
		StartedAt: time.Now(), Status: "running",
	}
	o.traceRepo.CreateTrace(ctx, tr)

	o.expireActiveThemes(ctx, userID)

	// Phase 15: 状态更新已收敛至 StateUpdater（post-chat 触发），
	// dailyReview 只做 expire + discover，不再做全量分析
	discovered := o.discoverNewThemes(ctx, userID, traceID)

	endTraceSafe(o.traceRepo, ctx, traceID, "completed", 0, 0, nil)
	log.Printf("[reflection] trace=%s DONE %s discovered=%d", traceID[:8], triggerType, discovered)
	return discovered, nil
}

// durationDays 返回 duration 对应的衰减天数（从配置读取）
func (o *ReflectionOrchestrator) durationDays(duration string) int {
	if d, ok := o.activeDurationDays[duration]; ok {
		return d
	}
	return 30
}

// discoverNewThemes 运行 ThemeDiscoverer 从 topic/note/memory 聚类中发现新主题
func (o *ReflectionOrchestrator) discoverNewThemes(ctx context.Context, userID, traceID string) int {
	discovererT, ok := o.reflectionReg.GetToolMap()["theme_discoverer"]
	if !ok {
		log.Printf("[reflection] discover: theme_discoverer tool not found")
		return 0
	}

	args, _ := json.Marshal(map[string]interface{}{
		"mode": "stats",
	})
	ec := &tool.ExecuteContext{Ctx: WithTraceID(ctx, traceID), UserID: userID}
	res := discovererT.Execute(ec, "discover", args)
	if !res.Success || res.Data == nil {
		if res.Error != "" {
			log.Printf("[reflection] discover: theme_discoverer error: %s", res.Error)
		}
		return 0
	}

	var output DiscoverResult
	if err := json.Unmarshal(res.Data, &output); err != nil {
		log.Printf("[reflection] discover: unmarshal DiscoverResult error: %v", err)
		return 0
	}
	log.Printf("[reflection] discover: created=%d rejected=%d", len(output.Created), len(output.Rejected))
	return len(output.Created)
}

// expireActiveThemes 检查 active 主题是否已过衰减周期，是则 close
func (o *ReflectionOrchestrator) expireActiveThemes(ctx context.Context, userID string) {
	actives, err := o.themeStore.ListActives(ctx, userID)
	if err != nil {
		log.Printf("[theme] expire: list actives error: %v", err)
		return
	}

	now := time.Now()
	var toClose []string
	for _, a := range actives {
		days := o.durationDays(a.Duration)
		cutoff := now.AddDate(0, 0, -days)
		if a.UpdatedAt.Before(cutoff) {
			log.Printf("[theme] active decay: name=%s id=%s duration=%s days=%d",
				a.Name, a.ID[:8], a.Duration, days)
			toClose = append(toClose, a.ID)
		}
	}

	for _, id := range toClose {
		if err := o.themeStore.UpdateStatus(ctx, id, userID, "closed"); err != nil {
			log.Printf("[theme] expire close error: %v", err)
		}
	}
	if len(toClose) > 0 {
		log.Printf("[theme] expired %d active themes for user %s", len(toClose), userID[:8])
	}
}

// reviewActiveThemes 对有新活动的活跃主题运行分析，更新状态页。
// 跳过 UpdatedAt 未超过 LastRunAt 的主题（无新数据，无需重复分析）。
func (o *ReflectionOrchestrator) reviewActiveThemes(ctx context.Context, userID, traceID string) int {
	actives, err := o.themeStore.ListActives(ctx, userID)
	if err != nil {
		log.Printf("[reflection] review: list actives error: %v", err)
		return 0
	}
	if len(actives) == 0 {
		log.Printf("[reflection] review: no active themes")
		return 0
	}

	analystT, ok := o.reflectionReg.GetToolMap()["theme_analyst"]
	if !ok {
		log.Printf("[reflection] review: theme_analyst tool not found")
		return 0
	}

	analyzed := 0
	skipped := 0
	for _, active := range actives {
		// 按需分析：LastRunAt 为 nil（新建主题）或 UpdatedAt > LastRunAt（有新活动）才跑
		fullTheme, err := o.themeStore.GetByID(ctx, active.ID, userID)
		if err != nil || fullTheme == nil {
			continue
		}
		if fullTheme.LastRunAt != nil && !fullTheme.UpdatedAt.After(*fullTheme.LastRunAt) {
			skipped++
			continue
		}

		args, _ := json.Marshal(map[string]interface{}{
			"theme_id":   active.ID,
			"theme_name": active.Name,
		})
		ec := &tool.ExecuteContext{Ctx: WithTraceID(ctx, traceID), UserID: userID}
		analystT.Execute(ec, "analyze", args)
		analyzed++
	}
	log.Printf("[theme] review: analyzed=%d skipped=%d (total active=%d)", analyzed, skipped, len(actives))
	return analyzed
}

// runOnce 保留兼容
func (o *ReflectionOrchestrator) runOnce(ctx context.Context, userID, _ string, silent bool) error {
	if silent {
		return nil
	}
	delivered, _ := o.DeliverPendingFindings(ctx, userID)
	log.Printf("[reflection] runOnce: delivered=%d", delivered)
	return nil
}

// countRecentActivity 统计最近 7 天用户活动数量
func (o *ReflectionOrchestrator) countRecentActivity(ctx context.Context, userID string) (msgCount, noteCount, memCount int) {
	since := time.Now().AddDate(0, 0, -7)
	if n, err := o.msgRepo.CountRecent(ctx, userID, since); err != nil {
		log.Printf("[reflection] CountRecent(msg) error: %v", err)
	} else {
		msgCount = int(n)
	}
	if n, err := o.noteRepo.CountRecent(ctx, userID, since); err != nil {
		log.Printf("[reflection] CountRecent(note) error: %v", err)
	} else {
		noteCount = int(n)
	}
	if n, err := o.memRepo.CountRecent(ctx, userID, since); err != nil {
		log.Printf("[reflection] CountRecent(mem) error: %v", err)
	} else {
		memCount = int(n)
	}
	return
}

func strPtr(s string) *string { return &s }

func logErr(format string, args ...interface{}) {
	log.Printf(format, args...)
}

func endTraceSafe(repo *service.ReflectionTraceRepo, ctx context.Context, traceID, status string, totalFindings, deliveredCount int, errMsg *string) {
	if err := repo.EndTrace(ctx, traceID, status, totalFindings, deliveredCount, errMsg); err != nil {
		log.Printf("[reflection] EndTrace error: %v", err)
	}
}

func createDecisionSafe(repo *service.ReflectionTraceRepo, ctx context.Context, decision *model.ReflectionDecision) {
	if err := repo.CreateDecision(ctx, decision); err != nil {
		log.Printf("[reflection] CreateDecision error: %v", err)
	}
}
