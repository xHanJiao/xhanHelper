package agent

import (
	"context"
	"log"
	"time"
)

// postChatEvent — 对话结束后的事件
type postChatEvent struct {
	userID string
	convID string
}

// CronRunner — 反思循环调度器
type CronRunner struct {
	orch          *ReflectionOrchestrator
	dailyHour     int                // 每天几点运行（0-23），默认 8
	postChatDelay time.Duration      // 对话后延迟触发，默认 30 分钟
	runTimeout    time.Duration      // 单次反思超时，默认 5 分钟
	eventCh       chan postChatEvent // 对话结束后通知
	stateUpdater  *StateUpdater
}

// NewCronRunner 创建调度器
func NewCronRunner(orch *ReflectionOrchestrator, stateUpdater *StateUpdater) *CronRunner {
	return &CronRunner{
		orch:          orch,
		stateUpdater:  stateUpdater,
		dailyHour:     8,
		postChatDelay: 30 * time.Minute,
		runTimeout:    5 * time.Minute,
		eventCh:       make(chan postChatEvent, 32),
	}
}

// SetDailyHour 设置每日运行小时（用于测试：改短验证）
func (cr *CronRunner) SetDailyHour(h int) { cr.dailyHour = h }

// SetPostChatDelay 设置对话后延迟
func (cr *CronRunner) SetPostChatDelay(d time.Duration) { cr.postChatDelay = d }

// SetRunTimeout 设置单次反思超时
func (cr *CronRunner) SetRunTimeout(d time.Duration) { cr.runTimeout = d }

// NotifyChatEnd 对话结束后通知调度器（由聊天 orchestrator 调用）
func (cr *CronRunner) NotifyChatEnd(userID, convID string) {
	log.Printf("[cron] NotifyChatEnd: user=%s conv=%s", userID[:8], convID[:8])
	select {
	case cr.eventCh <- postChatEvent{userID, convID}:
		log.Printf("[cron] eventCh sent ok")
	default:
		log.Printf("[cron] event channel full, dropping notification for %s", userID)
	}
}

// Start 启动调度循环（阻塞，用 goroutine 调用）
func (cr *CronRunner) Start(ctx context.Context) {
	log.Printf("[cron] started, daily=%d:00, post_chat_delay=%v", cr.dailyHour, cr.postChatDelay)

	// 计算下一次 daily 触发时间
	now := time.Now()
	nextDaily := time.Date(now.Year(), now.Month(), now.Day(), cr.dailyHour, 0, 0, 0, now.Location())
	if nextDaily.Before(now) {
		nextDaily = nextDaily.Add(24 * time.Hour)
	}

	dailyTimer := time.NewTimer(nextDaily.Sub(now))
	defer dailyTimer.Stop()

	// 后对话延迟的定时器（nil = 未激活）
	var postChatTimer *time.Timer
	var pendingUserID string
	var pendingConvID string

	for {
		select {
		case <-ctx.Done():
			log.Printf("[cron] stopped")
			return

		case <-dailyTimer.C:
			log.Printf("[cron] daily trigger")
			cr.runReflection(ctx, "system", "cron")
			dailyTimer.Reset(24 * time.Hour)

		case evt := <-cr.eventCh:
			log.Printf("[cron] eventCh received: user=%s conv=%s", evt.userID[:8], evt.convID[:8])
			if postChatTimer != nil {
				postChatTimer.Stop()
			}
			pendingUserID = evt.userID
			pendingConvID = evt.convID
			postChatTimer = time.NewTimer(cr.postChatDelay)
			log.Printf("[cron] timer set: delay=%v", cr.postChatDelay)

		case <-func() <-chan time.Time {
			if postChatTimer == nil {
				return nil
			}
			return postChatTimer.C
		}():
			log.Printf("[cron] post-chat trigger for user=%s conv=%s", pendingUserID, pendingConvID)
			cr.dispatchPostChat(ctx, pendingUserID, pendingConvID)
			postChatTimer = nil
		}
	}
}

// dispatchPostChat 根据会话是否绑定主题分发：绑定→StateUpdater，未绑定→静默发现草稿
func (cr *CronRunner) dispatchPostChat(ctx context.Context, userID, convID string) {
	runCtx, cancel := context.WithTimeout(ctx, cr.runTimeout)
	defer cancel()

	// 查会话是否绑定了主题
	extra, err := cr.stateUpdater.convRepo.GetExtra(runCtx, convID)
	if err != nil || extra == nil || extra.Context == nil || extra.Context.ThemeID == "" {
		// 未绑定主题 → 从当前会话消息中静默发现草稿
		log.Printf("[cron] post-chat: no theme binding, discovering draft from conv=%s", convID[:8])
		if err := cr.orch.DiscoverDraftFromConversation(runCtx, userID, convID); err != nil {
			log.Printf("[cron] discover draft error: %v", err)
		}
		return
	}

	// 绑定主题 → 增量更新状态页
	log.Printf("[cron] post-chat: theme %s, running StateUpdater for user=%s conv=%s", extra.Context.ThemeID[:8], userID, convID)
	if err := cr.stateUpdater.UpdateFromConversation(runCtx, userID, convID, extra.Context.ThemeID); err != nil {
		log.Printf("[cron] StateUpdater error: %v", err)
	}
}

func (cr *CronRunner) runReflection(ctx context.Context, userID, triggerType string) {
	runCtx, cancel := context.WithTimeout(ctx, cr.runTimeout)
	defer cancel()

	if err := cr.orch.RunOnce(runCtx, userID, triggerType); err != nil {
		log.Printf("[cron] reflection error: %v", err)
	}
}
