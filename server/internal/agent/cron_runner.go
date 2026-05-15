package agent

import (
	"context"
	"log"
	"time"
)

// CronRunner — 反思循环调度器
type CronRunner struct {
	orch          *ReflectionOrchestrator
	dailyHour     int           // 每天几点运行（0-23），默认 8
	postChatDelay time.Duration // 对话后延迟触发，默认 30 分钟
	runTimeout    time.Duration // 单次反思超时，默认 5 分钟
	eventCh       chan string   // 对话结束后通知的 userID
}

// NewCronRunner 创建调度器
func NewCronRunner(orch *ReflectionOrchestrator) *CronRunner {
	return &CronRunner{
		orch:          orch,
		dailyHour:     8,
		postChatDelay: 30 * time.Minute,
		runTimeout:    5 * time.Minute,
		eventCh:       make(chan string, 32),
	}
}

// SetDailyHour 设置每日运行小时（用于测试：改短验证）
func (cr *CronRunner) SetDailyHour(h int) { cr.dailyHour = h }

// SetPostChatDelay 设置对话后延迟
func (cr *CronRunner) SetPostChatDelay(d time.Duration) { cr.postChatDelay = d }

// SetRunTimeout 设置单次反思超时
func (cr *CronRunner) SetRunTimeout(d time.Duration) { cr.runTimeout = d }

// NotifyChatEnd 对话结束后通知调度器（由聊天 orchestrator 调用）
func (cr *CronRunner) NotifyChatEnd(userID string) {
	select {
	case cr.eventCh <- userID:
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

	for {
		select {
		case <-ctx.Done():
			log.Printf("[cron] stopped")
			return

		case <-dailyTimer.C:
			log.Printf("[cron] daily trigger")
			cr.runReflection(ctx, "system", "cron")
			dailyTimer.Reset(24 * time.Hour)

		case userID := <-cr.eventCh:
			// 收到对话结束通知 → 启动延迟定时器
			if postChatTimer != nil {
				postChatTimer.Stop()
			}
			pendingUserID = userID
			postChatTimer = time.NewTimer(cr.postChatDelay)

		case <-func() <-chan time.Time {
			if postChatTimer == nil {
				return nil // nil channel, never selected
			}
			return postChatTimer.C
		}():
			log.Printf("[cron] post-chat trigger for user=%s", pendingUserID)
			cr.runReflection(ctx, pendingUserID, "event")
			postChatTimer = nil
		}
	}
}

func (cr *CronRunner) runReflection(ctx context.Context, userID, triggerType string) {
	runCtx, cancel := context.WithTimeout(ctx, cr.runTimeout)
	defer cancel()

	if err := cr.orch.RunOnce(runCtx, userID, triggerType); err != nil {
		log.Printf("[cron] reflection error: %v", err)
	}
}
