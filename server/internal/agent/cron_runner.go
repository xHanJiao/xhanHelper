package agent

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// HookStatus — 系统钩子状态快照
type HookStatus struct {
	PendingDelayedTasks int64  `json:"pending_delayed_tasks"`
	NextDelayedTaskAt   string `json:"next_delayed_task_at"`
	DeliverRemaining    string `json:"deliver_remaining"`
	DiscoverRemaining   string `json:"discover_remaining"`
	CommitmentTimers    int    `json:"commitment_timers"`
}

// CronRunner — 反思循环调度器
type CronRunner struct {
	orch              *ReflectionOrchestrator
	userRepo          *service.UserRepo
	discoverTimes     []string      // 发现候选主题定时列表
	deliverInterval   time.Duration // 投递窗口间隔，默认 1 小时
	postChatDelay     time.Duration // 对话后延迟触发，默认 30 分钟
	runTimeout        time.Duration // 单次反思超时，默认 30 分钟
	taskPollInterval  time.Duration // 延迟任务轮询间隔，默认 3 分钟
	delayedTaskRepo   *service.DelayedTaskRepo
	stateUpdater      *StateUpdater
	memExtractor      *MemoryExtractor
	commitTimer       *CommitmentTimer
	convRepo          *service.ConversationRepo
	memoryAutoExtract bool

	// 钩子状态追踪
	mu              sync.Mutex
	deliverNext     time.Time
	discoverNext    time.Time
	taskPollRunning bool
}

// NewCronRunner 创建调度器
func NewCronRunner(orch *ReflectionOrchestrator, stateUpdater *StateUpdater, userRepo *service.UserRepo, memExtractor *MemoryExtractor, commitTimer *CommitmentTimer, convRepo *service.ConversationRepo, delayedTaskRepo *service.DelayedTaskRepo) *CronRunner {
	return &CronRunner{
		orch:             orch,
		stateUpdater:     stateUpdater,
		userRepo:         userRepo,
		memExtractor:     memExtractor,
		commitTimer:      commitTimer,
		convRepo:         convRepo,
		delayedTaskRepo:  delayedTaskRepo,
		discoverTimes:    []string{"09:00", "18:00"},
		deliverInterval:  1 * time.Hour,
		postChatDelay:    30 * time.Minute,
		runTimeout:       30 * time.Minute,
		taskPollInterval: 3 * time.Minute,
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

func (cr *CronRunner) setDeliverNext(t time.Time) {
	cr.mu.Lock()
	cr.deliverNext = t
	cr.mu.Unlock()
}

func (cr *CronRunner) setDiscoverNext(t time.Time) {
	cr.mu.Lock()
	cr.discoverNext = t
	cr.mu.Unlock()
}

// GetHookStatus 返回当前钩子状态快照
func (cr *CronRunner) GetHookStatus() HookStatus {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	now := time.Now()
	s := HookStatus{
		CommitmentTimers: cr.commitTimer.ActiveTimerCount(),
	}
	if cr.delayedTaskRepo != nil {
		if n, err := cr.delayedTaskRepo.CountPending(context.Background()); err == nil {
			s.PendingDelayedTasks = n
		}
		if earliest, err := cr.delayedTaskRepo.EarliestPending(context.Background()); err == nil && !earliest.IsZero() {
			s.NextDelayedTaskAt = earliest.In(time.FixedZone("UTC+8", 8*3600)).Format("15:04:05")
		}
	}
	s.DeliverRemaining = cr.deliverNext.Sub(now).Round(time.Second).String()
	s.DiscoverRemaining = cr.discoverNext.Sub(now).Round(time.Second).String()

	return s
}

// NotifyChatEnd 对话结束后写入延迟任务表，由轮询处理（由聊天 orchestrator 调用）
func (cr *CronRunner) NotifyChatEnd(userID, convID string) {
	log.Printf("[cron] NotifyChatEnd: user=%s conv=%s", userID[:8], convID[:8])
	if cr.delayedTaskRepo == nil {
		log.Printf("[cron] delayedTaskRepo is nil, skipping")
		return
	}

	// 查重：如果已有同 user+conv 的 pending 任务，重置 process_at 而不是新建
	pending, err := cr.delayedTaskRepo.FindPendingByUserAndType(context.Background(), userID, "post_chat")
	if err != nil {
		log.Printf("[cron] FindPendingByUserAndType error: %v, fallback to insert", err)
	} else {
		for _, t := range pending {
			meta, err := t.ParseMeta()
			if err == nil && meta.ConvID == convID {
				newTime := time.Now().Add(cr.postChatDelay)
				if err := cr.delayedTaskRepo.UpdateProcessAt(context.Background(), t.ID, newTime); err != nil {
					log.Printf("[cron] UpdateProcessAt error: %v", err)
				} else {
					log.Printf("[cron] delayed task debounced: id=%s new_process_at=%s", t.ID[:8], newTime.Format("15:04:05"))
				}
				return
			}
		}
	}

	job := &model.DelayedTask{
		ID:        uuid.New().String(),
		UserID:    userID,
		TaskType:  "post_chat",
		Status:    "pending",
		ProcessAt: time.Now().Add(cr.postChatDelay),
	}
	if err := job.SetMeta(model.DelayedTaskMeta{ConvID: convID}); err != nil {
		log.Printf("[cron] SetMeta error: %v", err)
		return
	}
	if err := cr.delayedTaskRepo.Create(context.Background(), job); err != nil {
		log.Printf("[cron] create delayed task error: %v", err)
	} else {
		log.Printf("[cron] delayed task created: id=%s process_at=%s", job.ID[:8], job.ProcessAt.Format("15:04:05"))
	}
}

// Start 启动调度循环（阻塞，用 goroutine 调用）
func (cr *CronRunner) Start(ctx context.Context) {
	log.Printf("[cron] started, discover_times=%v deliver_interval=%v post_chat_delay=%v",
		cr.discoverTimes, cr.deliverInterval, cr.postChatDelay)

	// 启动时加载所有 pending 承诺，注册 timer
	cr.commitTimer.LoadPending(ctx)
	go cr.commitTimer.StartFallbackScan(ctx)

	deliverTimer := time.NewTimer(cr.deliverInterval)
	cr.setDeliverNext(time.Now().Add(cr.deliverInterval))
	defer deliverTimer.Stop()
	discoverTimer := time.NewTimer(nextDailyDelay(cr.discoverTimes))
	cr.setDiscoverNext(time.Now().Add(nextDailyDelay(cr.discoverTimes)))
	defer discoverTimer.Stop()

	taskPollTicker := time.NewTicker(cr.taskPollInterval)
	defer taskPollTicker.Stop()

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
			cr.setDeliverNext(time.Now().Add(cr.deliverInterval))

		case <-discoverTimer.C:
			log.Printf("[cron] discover trigger")
			users, _ := cr.userRepo.ListAll(context.Background())
			for _, u := range users {
				cr.discoverOnce(ctx, u.ID)
			}
			discoverTimer.Reset(nextDailyDelay(cr.discoverTimes))
			cr.setDiscoverNext(time.Now().Add(nextDailyDelay(cr.discoverTimes)))

		case <-taskPollTicker.C:
			cr.mu.Lock()
			if cr.taskPollRunning {
				cr.mu.Unlock()
				continue
			}
			cr.taskPollRunning = true
			cr.mu.Unlock()
			go func() {
				cr.processPostChatTasks(ctx)
				cr.mu.Lock()
				cr.taskPollRunning = false
				cr.mu.Unlock()
			}()
		}
	}
}

// processPostChatTasks 扫延迟任务表中到期的 post_chat 任务并执行
func (cr *CronRunner) processPostChatTasks(ctx context.Context) {
	if cr.delayedTaskRepo == nil {
		return
	}
	tasks, err := cr.delayedTaskRepo.ListDue(ctx, "post_chat", 20)
	if err != nil {
		log.Printf("[cron] ListDue error: %v", err)
		return
	}
	seen := map[string]bool{}
	for _, t := range tasks {
		meta, err := t.ParseMeta()
		if err != nil || meta.ConvID == "" {
			log.Printf("[cron] bad task meta for %s, marking done", t.ID[:8])
			cr.delayedTaskRepo.MarkDone(ctx, t.ID)
			continue
		}
		key := t.UserID + "|" + meta.ConvID
		if seen[key] {
			log.Printf("[cron] post-chat duplicate skipped: user=%s conv=%s task=%s", t.UserID[:8], meta.ConvID[:8], t.ID[:8])
			cr.delayedTaskRepo.MarkDone(ctx, t.ID)
			continue
		}
		seen[key] = true
		log.Printf("[cron] post-chat task: user=%s conv=%s", t.UserID[:8], meta.ConvID[:8])
		cr.dispatchPostChat(ctx, t.UserID, meta.ConvID)
		if err := cr.delayedTaskRepo.MarkDone(ctx, t.ID); err != nil {
			log.Printf("[cron] MarkDone error: %v", err)
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

	// Step 2: 查会话是否绑定了主题，已绑定则增量更新状态页
	extra, err := cr.stateUpdater.convRepo.GetExtra(runCtx, convID)
	if err != nil {
		log.Printf("[cron] post-chat: GetExtra error: %v", err)
		return
	}
	if extra == nil || extra.Context == nil || extra.Context.ThemeID == "" {
		return
	}

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
