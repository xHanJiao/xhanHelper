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
	flashClient   llm.LLMClient
	reflectionReg *tool.Registry
	traceRepo     *service.ReflectionTraceRepo
	themeStore    *ThemeStore
	msgRepo       *service.MessageRepo
	noteRepo      *service.NoteRepo
	memRepo       *service.UserMemoryRepo
	maxIterations int

	activeDurationDays map[string]int
}

// ReflectionOrchestratorConfig — 反思编排器依赖配置
type ReflectionOrchestratorConfig struct {
	FlashClient        llm.LLMClient
	ReflectionReg      *tool.Registry
	TraceRepo          *service.ReflectionTraceRepo
	ThemeStore         *ThemeStore
	MsgRepo            *service.MessageRepo
	NoteRepo           *service.NoteRepo
	MemRepo            *service.UserMemoryRepo
	MaxIterations      int
	ActiveDurationDays map[string]int
}

func NewReflectionOrchestrator(cfg *ReflectionOrchestratorConfig) *ReflectionOrchestrator {
	if len(cfg.ActiveDurationDays) == 0 {
		cfg.ActiveDurationDays = map[string]int{"long": 90, "medium": 30, "short": 14, "ephemeral": 7}
	}
	return &ReflectionOrchestrator{
		flashClient:        cfg.FlashClient,
		reflectionReg:      cfg.ReflectionReg,
		traceRepo:          cfg.TraceRepo,
		themeStore:         cfg.ThemeStore,
		msgRepo:            cfg.MsgRepo,
		noteRepo:           cfg.NoteRepo,
		memRepo:            cfg.MemRepo,
		maxIterations:      cfg.MaxIterations,
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

// MorningReview 早安回顾
func (o *ReflectionOrchestrator) MorningReview(ctx context.Context, userID string) (int, error) {
	return o.dailyReview(ctx, userID, "morning_review")
}

// EveningReview 晚安回顾
func (o *ReflectionOrchestrator) EveningReview(ctx context.Context, userID string) (int, error) {
	return o.dailyReview(ctx, userID, "evening_review")
}

// dailyReview 早晚回顾：旧主题维护已停写，只写一条 trace 记录节拍。
func (o *ReflectionOrchestrator) dailyReview(ctx context.Context, userID, triggerType string) (int, error) {
	traceID := uuid.New().String()
	log.Printf("[reflection] trace=%s trigger=%s user=%s START", traceID[:8], triggerType, userID)

	tr := &model.ReflectionTrace{
		ID: traceID, UserID: userID, TriggerType: triggerType,
		StartedAt: time.Now(), Status: "running",
	}
	o.traceRepo.CreateTrace(ctx, tr)

	// 旧主题状态页已停写（事件系统接管）：不再 expire active 主题、不再跑主题发现。
	// 保留 trace 记录，方便确认每日节拍仍在运行。
	endTraceSafe(o.traceRepo, ctx, traceID, "completed", 0, 0, nil)
	log.Printf("[reflection] trace=%s DONE %s (theme maintenance disabled)", traceID[:8], triggerType)
	return 0, nil
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

	msgCount, noteCount, memCount := o.countRecentActivity(ctx, userID)
	args, _ := json.Marshal(map[string]interface{}{
		"mode":              "stats",
		"recent_msg_count":  msgCount,
		"recent_note_count": noteCount,
		"recent_mem_count":  memCount,
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
