package agent

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util"
)

// HookStatus — 系统钩子状态快照
type HookStatus struct {
	PendingDelayedTasks int64  `json:"pending_delayed_tasks"`
	NextDelayedTaskAt   string `json:"next_delayed_task_at"`
	DeliverRemaining    string `json:"deliver_remaining"`
	DiscoverRemaining   string `json:"discover_remaining"`
	MissionRemaining    string `json:"mission_remaining"`
	CommitmentTimers    int    `json:"commitment_timers"`
}

// CronRunner — 反思循环调度器
type CronRunner struct {
	orch              *ReflectionOrchestrator
	userRepo          *service.UserRepo
	discoverTimes     []string      // 发现候选主题定时列表
	deliverInterval   time.Duration // 投递窗口间隔，默认 1 小时
	missionInterval   time.Duration // Mission 检查间隔，默认 30 分钟
	postChatDelay     time.Duration // 对话后延迟触发，默认 30 分钟
	runTimeout        time.Duration // 单次反思超时，默认 30 分钟
	taskPollInterval  time.Duration // 延迟任务轮询间隔，默认 3 分钟
	delayedTaskRepo   *service.DelayedTaskRepo
	stateUpdater      *StateUpdater
	memExtractor      *MemoryExtractor
	commitTimer       *CommitmentTimer
	convRepo          *service.ConversationRepo
	memoryAutoExtract bool
	missionRunner     *MissionRunner
	missionRepo       *service.MissionRepo

	// 钩子状态追踪
	mu              sync.Mutex
	deliverNext     time.Time
	discoverNext    time.Time
	missionNext     time.Time
	taskPollRunning bool
}

// CronRunnerConfig — 调度器依赖配置
type CronRunnerConfig struct {
	Orch              *ReflectionOrchestrator
	StateUpdater      *StateUpdater
	UserRepo          *service.UserRepo
	MemExtractor      *MemoryExtractor
	CommitTimer       *CommitmentTimer
	ConvRepo          *service.ConversationRepo
	DelayedTaskRepo   *service.DelayedTaskRepo
	MissionRunner     *MissionRunner
	MissionRepo       *service.MissionRepo
	DiscoverSchedules []string
	DeliverInterval   time.Duration
	MissionInterval   time.Duration
	PostChatDelay     time.Duration
	RunTimeout        time.Duration
	MemoryAutoExtract bool
}

// NewCronRunner 创建调度器
func NewCronRunner(cfg *CronRunnerConfig) *CronRunner {
	if len(cfg.DiscoverSchedules) == 0 {
		cfg.DiscoverSchedules = []string{"09:00", "18:00"}
	}
	if cfg.DeliverInterval <= 0 {
		cfg.DeliverInterval = 1 * time.Hour
	}
	if cfg.PostChatDelay <= 0 {
		cfg.PostChatDelay = 30 * time.Minute
	}
	if cfg.RunTimeout <= 0 {
		cfg.RunTimeout = 30 * time.Minute
	}
	if cfg.MissionInterval <= 0 {
		cfg.MissionInterval = 30 * time.Minute
	}
	return &CronRunner{
		orch:              cfg.Orch,
		stateUpdater:      cfg.StateUpdater,
		userRepo:          cfg.UserRepo,
		memExtractor:      cfg.MemExtractor,
		commitTimer:       cfg.CommitTimer,
		convRepo:          cfg.ConvRepo,
		delayedTaskRepo:   cfg.DelayedTaskRepo,
		missionRunner:     cfg.MissionRunner,
		missionRepo:       cfg.MissionRepo,
		discoverTimes:     cfg.DiscoverSchedules,
		deliverInterval:   cfg.DeliverInterval,
		missionInterval:   cfg.MissionInterval,
		postChatDelay:     cfg.PostChatDelay,
		runTimeout:        cfg.RunTimeout,
		memoryAutoExtract: cfg.MemoryAutoExtract,
		taskPollInterval:  3 * time.Minute,
	}
}

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
			s.NextDelayedTaskAt = earliest.In(config.BJ).Format("15:04:05")
		}
	}
	s.DeliverRemaining = cr.deliverNext.Sub(now).Round(time.Second).String()
	s.DiscoverRemaining = cr.discoverNext.Sub(now).Round(time.Second).String()
	s.MissionRemaining = cr.missionNext.Sub(now).Round(time.Second).String()

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
					log.Printf("[cron] delayed task debounced: id=%s new_process_at=%s", t.ID[:8], newTime.In(config.BJ).Format("15:04:05"))
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
		log.Printf("[cron] delayed task created: id=%s process_at=%s", job.ID[:8], job.ProcessAt.In(config.BJ).Format("15:04:05"))
	}
}

// Start 启动调度循环（阻塞，用 goroutine 调用）
func (cr *CronRunner) Start(ctx context.Context) {
	log.Printf("[cron] started, discover_times=%v deliver_interval=%v mission_interval=%v post_chat_delay=%v",
		cr.discoverTimes, cr.deliverInterval, cr.missionInterval, cr.postChatDelay)

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

	missionTicker := time.NewTicker(cr.missionInterval)
	defer missionTicker.Stop()
	if cr.missionRunner != nil {
		cr.missionNext = time.Now().Add(cr.missionInterval)
	}

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

		case <-missionTicker.C:
			log.Printf("[cron] mission check trigger")
			go cr.processMissions(ctx)
			cr.missionNext = time.Now().Add(cr.missionInterval)
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

	// Step 1: 提取记忆（仅当前会话，同步嵌入）
	var memory *model.UserMemory
	if cr.memoryAutoExtract {
		conv, err := cr.convRepo.FindByID(runCtx, convID)
		if err != nil {
			log.Printf("[cron] post-chat: find conv error: %v", err)
		} else if conv != nil {
			memory, err = cr.memExtractor.ExtractFromConversation(runCtx, conv)
			if err != nil {
				log.Printf("[cron] post-chat: memory extract error: %v", err)
			}
		}
	}

	// Step 2: 用提取的记忆匹配活跃主题并增量更新
	// （注：主题发现已移至每日定时任务，对话后处理不再做主题发现）
	if memory != nil {
		cr.stateUpdater.UpdateThemesFromMemory(runCtx, userID, memory)
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
	now := time.Now()
	hour := now.Hour()
	if hour < 12 {
		if _, err := cr.orch.MorningReview(runCtx, userID); err != nil {
			log.Printf("[cron] morning review error: %v", err)
		}
	} else {
		if _, err := cr.orch.EveningReview(runCtx, userID); err != nil {
			log.Printf("[cron] evening review error: %v", err)
		}
	}
}

// processMissions 扫到期的 active Mission 并执行检查
func (cr *CronRunner) processMissions(parentCtx context.Context) {
	if cr.missionRunner == nil || cr.missionRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parentCtx, cr.runTimeout)
	defer cancel()

	missions, err := cr.missionRepo.ListDueForCheck(ctx, 20)
	if err != nil {
		log.Printf("[cron] ListDueForCheck error: %v", err)
		return
	}
	if len(missions) == 0 {
		return
	}
	log.Printf("[cron] processing %d missions", len(missions))
	tasks := make([]func(context.Context) (struct{}, error), len(missions))
	for i := range missions {
		m := &missions[i]
		tasks[i] = func(ctx context.Context) (struct{}, error) {
			if err := cr.missionRunner.Run(ctx, m); err != nil {
				log.Printf("[cron] mission run error: %v", err)
			}
			return struct{}{}, nil
		}
	}
	if _, err := util.LoadConcurrent(ctx, 3, tasks); err != nil {
		log.Printf("[cron] LoadConcurrent error: %v", err)
	}
}
