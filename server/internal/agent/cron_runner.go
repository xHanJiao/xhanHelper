package agent

import (
	"context"
	"log"
	"time"

	"xhan-helper/internal/service"
)

// postChatEvent — 对话结束后的事件
type postChatEvent struct {
	userID string
	convID string
}

// CronRunner — 反思循环调度器
type CronRunner struct {
	orch              *ReflectionOrchestrator
	userRepo          *service.UserRepo
	discoverTimes     []string           // 发现候选主题定时列表
	deliverInterval   time.Duration      // 投递窗口间隔，默认 1 小时
	postChatDelay     time.Duration      // 对话后延迟触发，默认 30 分钟
	runTimeout        time.Duration      // 单次反思超时，默认 5 分钟
	eventCh           chan postChatEvent // 对话结束后通知
	stateUpdater      *StateUpdater
	memExtractor      *MemoryExtractor
	convRepo          *service.ConversationRepo
	memoryAutoExtract bool
}

// NewCronRunner 创建调度器
func NewCronRunner(orch *ReflectionOrchestrator, stateUpdater *StateUpdater, userRepo *service.UserRepo, memExtractor *MemoryExtractor, convRepo *service.ConversationRepo) *CronRunner {
	return &CronRunner{
		orch:            orch,
		stateUpdater:    stateUpdater,
		userRepo:        userRepo,
		memExtractor:    memExtractor,
		convRepo:        convRepo,
		discoverTimes:   []string{"09:00", "18:00"},
		deliverInterval: 1 * time.Hour,
		postChatDelay:   30 * time.Minute,
		runTimeout:      30 * time.Minute,
		eventCh:         make(chan postChatEvent, 32),
	}
}

// SetDiscoverSchedules 设置发现定时列表
func (cr *CronRunner) SetDiscoverSchedules(times []string) { cr.discoverTimes = times }

// SetDeliverInterval 设置投递窗口间隔
func (cr *CronRunner) SetDeliverInterval(d time.Duration) { cr.deliverInterval = d }

// SetPostChatDelay 设置对话后延迟
func (cr *CronRunner) SetPostChatDelay(d time.Duration) { cr.postChatDelay = d }

// SetRunTimeout 设置单次反思超时
func (cr *CronRunner) SetRunTimeout(d time.Duration) { cr.runTimeout = d }

// SetMemoryAutoExtract 设置是否自动提取记忆
func (cr *CronRunner) SetMemoryAutoExtract(v bool) { cr.memoryAutoExtract = v }

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
	log.Printf("[cron] started, discover_times=%v deliver_interval=%v post_chat_delay=%v",
		cr.discoverTimes, cr.deliverInterval, cr.postChatDelay)

	deliverTimer := time.NewTimer(cr.deliverInterval)
	defer deliverTimer.Stop()
	discoverTimer := time.NewTimer(nextDailyDelay(cr.discoverTimes))
	defer discoverTimer.Stop()

	// 后对话延迟的定时器（nil = 未激活）
	var postChatTimer *time.Timer
	var pendingUserID string
	var pendingConvID string

	for {
		select {
		case <-ctx.Done():
			log.Printf("[cron] stopped")
			return

		case <-deliverTimer.C:
			log.Printf("[cron] deliver window trigger")
			users, _ := cr.userRepo.ListAll(context.Background())
			for _, u := range users {
				cr.deliverOnce(ctx, u.ID)
			}
			deliverTimer.Reset(cr.deliverInterval)

		case <-discoverTimer.C:
			log.Printf("[cron] discover trigger")
			users, _ := cr.userRepo.ListAll(context.Background())
			for _, u := range users {
				cr.discoverOnce(ctx, u.ID)
			}
			discoverTimer.Reset(nextDailyDelay(cr.discoverTimes))

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

// dispatchPostChat 对话后处理唯一入口（串行）
func (cr *CronRunner) dispatchPostChat(ctx context.Context, userID, convID string) {
	runCtx, cancel := context.WithTimeout(ctx, cr.runTimeout)
	defer cancel()

	// Step 1: 提取记忆（仅当前会话）
	if cr.memoryAutoExtract {
		conv, err := cr.convRepo.FindByID(runCtx, convID)
		if err != nil {
			log.Printf("[cron] post-chat: find conv error: %v", err)
		} else if conv != nil {
			if err := cr.memExtractor.ExtractFromConversation(runCtx, conv); err != nil {
				log.Printf("[cron] post-chat: memory extract error: %v", err)
			}
		}
	}

	// Step 2: 提取显式承诺（TODO: 后续实现）

	// Step 3: 查会话是否绑定了主题
	extra, err := cr.stateUpdater.convRepo.GetExtra(runCtx, convID)
	if err != nil || extra == nil || extra.Context == nil || extra.Context.ThemeID == "" {
		// 未绑定主题 → 从当前会话消息中静默发现草稿
		log.Printf("[cron] post-chat: no theme binding, discovering draft from conv=%s", convID[:8])
		if err := cr.orch.DiscoverDraftFromConversation(runCtx, userID, convID); err != nil {
			log.Printf("[cron] discover draft error: %v", err)
		}
		return
	}

	// 绑定主题 → 增量更新状态页 + 通用判定
	log.Printf("[cron] post-chat: theme %s, running StateUpdater for user=%s conv=%s", extra.Context.ThemeID[:8], userID, convID)
	if err := cr.stateUpdater.UpdateFromConversation(runCtx, userID, convID, extra.Context.ThemeID); err != nil {
		log.Printf("[cron] StateUpdater error: %v", err)
	}
}

// nextDailyDelay 计算距离下一个触发时间还需要多久
func nextDailyDelay(times []string) time.Duration {
	now := time.Now()
	minDelay := 24 * time.Hour
	for _, t := range times {
		parsed, err := time.Parse("15:04", t)
		if err != nil {
			continue
		}
		next := time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location())
		if next.Before(now) {
			next = next.Add(24 * time.Hour)
		}
		delay := next.Sub(now)
		if delay < minDelay {
			minDelay = delay
		}
	}
	return minDelay
}

func (cr *CronRunner) deliverOnce(ctx context.Context, userID string) {
	runCtx, cancel := context.WithTimeout(ctx, cr.runTimeout)
	defer cancel()
	if _, err := cr.orch.DeliverPendingFindings(runCtx, userID); err != nil {
		log.Printf("[cron] deliver error: %v", err)
	}
}

func (cr *CronRunner) discoverOnce(ctx context.Context, userID string) {
	runCtx, cancel := context.WithTimeout(ctx, cr.runTimeout)
	defer cancel()
	if _, err := cr.orch.DiscoverAndDeliver(runCtx, userID); err != nil {
		log.Printf("[cron] discover error: %v", err)
	}
}
