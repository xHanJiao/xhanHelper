package agent

import (
	"context"
	"encoding/json"
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
}

// CronRunner — 反思循环调度器
type CronRunner struct {
	orch              *ReflectionOrchestrator
	userRepo          *service.UserRepo
	discoverTimes     []string      // 发现候选主题定时列表
	deliverInterval   time.Duration // 投递窗口间隔，默认 1 小时
	missionInterval   time.Duration // Mission 检查间隔，默认 30 分钟
	runTimeout        time.Duration // 单次反思超时，默认 30 分钟
	delayedTaskRepo   *service.DelayedTaskRepo
	missionRunner     *MissionRunner
	missionRepo       *service.MissionRepo
	sessionEndGroup   *PostChatWorkerGroup // 对话后处理组（摘要/记忆/主题）
	sessionGapMinutes int                  // session 间隔阈值（分钟）

	// 钩子状态追踪
	mu                sync.Mutex
	deliverNext       time.Time
	discoverNext      time.Time
	missionNext       time.Time
	sessionEndRunning bool
}

// CronRunnerConfig — 调度器依赖配置
type CronRunnerConfig struct {
	Orch              *ReflectionOrchestrator
	UserRepo          *service.UserRepo
	DelayedTaskRepo   *service.DelayedTaskRepo
	MissionRunner     *MissionRunner
	MissionRepo       *service.MissionRepo
	SessionEndGroup   *PostChatWorkerGroup
	SessionGapMinutes int // session 间隔阈值（分钟），用于 session_end 任务延迟
	DiscoverSchedules []string
	DeliverInterval   time.Duration
	MissionInterval   time.Duration
	RunTimeout        time.Duration
}

// NewCronRunner 创建调度器
func NewCronRunner(cfg *CronRunnerConfig) *CronRunner {
	if len(cfg.DiscoverSchedules) == 0 {
		cfg.DiscoverSchedules = []string{"09:00", "18:00"}
	}
	if cfg.DeliverInterval <= 0 {
		cfg.DeliverInterval = 1 * time.Hour
	}
	if cfg.RunTimeout <= 0 {
		cfg.RunTimeout = 30 * time.Minute
	}
	if cfg.MissionInterval <= 0 {
		cfg.MissionInterval = 30 * time.Minute
	}
	return &CronRunner{
		orch:              cfg.Orch,
		userRepo:          cfg.UserRepo,
		delayedTaskRepo:   cfg.DelayedTaskRepo,
		missionRunner:     cfg.MissionRunner,
		missionRepo:       cfg.MissionRepo,
		discoverTimes:     cfg.DiscoverSchedules,
		deliverInterval:   cfg.DeliverInterval,
		missionInterval:   cfg.MissionInterval,
		runTimeout:        cfg.RunTimeout,
		sessionEndGroup:   cfg.SessionEndGroup,
		sessionGapMinutes: cfg.SessionGapMinutes,
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
	s := HookStatus{}
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

// NotifySessionEnd 对话结束时入队 session_end 后处理任务。
// 每次 chat 结束都调——enqueue 内部的去重 + 延迟重置保证不重复处理。
func (cr *CronRunner) NotifySessionEnd(userID, convID, userMsgID string) {
	cr.enqueueSessionEndTask(userID, convID, userMsgID)
}

// enqueueSessionEndTask 入队一条 session_end 任务，暂存 userMsgID 防止竞态，带去重（同 conv 的 pending 任务推迟 process_at）。
func (cr *CronRunner) enqueueSessionEndTask(userID, convID, userMsgID string) {
	// 去重：查同 user+type 的 pending 任务
	pending, err := cr.delayedTaskRepo.FindPendingByUserAndType(context.Background(), userID, "session_end")
	// 查重是优化——出错时不 return，直接走 insert 分支。
	// 最坏情况多做一条 insert，seen map 在 processSessionEndTasks 里兜底去重。
	if err != nil {
		log.Printf("[cron] FindPendingByUserAndType session_end error: %v, fallback to insert", err)
	} else {
		for _, t := range pending {
			meta, err := t.ParseMeta()
			if err == nil && meta.ConvID == convID {
				cr.debounceSessionEndTask(&t, convID, userMsgID)
				return
			}
		}
	}

	cr.createSessionEndTask(userID, convID, userMsgID)
}

// debounceSessionEndTask 推迟已有 pending task，更新 LastMsgID。
func (cr *CronRunner) debounceSessionEndTask(t *model.DelayedTask, convID, userMsgID string) {
	gapDuration := time.Duration(cr.sessionGapMinutes) * time.Minute
	newTime := time.Now().Add(gapDuration)

	newMeta, err := json.Marshal(model.DelayedTaskMeta{ConvID: convID, LastMsgID: userMsgID})
	if err != nil {
		log.Printf("[cron] debounceSessionEndTask marshal error: %v", err)
		return
	}
	if err := cr.delayedTaskRepo.UpdateProcessAtAndMeta(context.Background(), t.ID, newTime, string(newMeta)); err != nil {
		log.Printf("[cron] debounceSessionEndTask error: %v", err)
		return
	}
	log.Printf("[cron] session_end task debounced: id=%s new_process_at=%s last_msg=%s",
		t.ID[:8], newTime.In(config.BJ).Format("15:04:05"), userMsgID[:8])
}

// createSessionEndTask 新建一条 session_end 延迟任务。
func (cr *CronRunner) createSessionEndTask(userID, convID, userMsgID string) {
	gapDuration := time.Duration(cr.sessionGapMinutes) * time.Minute
	job := &model.DelayedTask{
		ID:        uuid.New().String(),
		UserID:    userID,
		TaskType:  "session_end",
		Status:    "pending",
		ProcessAt: time.Now().Add(gapDuration),
	}
	if err := job.SetMeta(model.DelayedTaskMeta{ConvID: convID, LastMsgID: userMsgID}); err != nil {
		log.Printf("[cron] createSessionEndTask SetMeta error: %v", err)
		return
	}
	if err := cr.delayedTaskRepo.Create(context.Background(), job); err != nil {
		log.Printf("[cron] createSessionEndTask error: %v", err)
		return
	}
	log.Printf("[cron] session_end task created: id=%s conv=%s process_at=%s last_msg=%s",
		job.ID[:8], convID[:8], job.ProcessAt.In(config.BJ).Format("15:04:05"), userMsgID[:8])
}

// Start 启动调度循环（阻塞，用 goroutine 调用）
func (cr *CronRunner) Start(ctx context.Context) {
	log.Printf("[cron] started, discover_times=%v deliver_interval=%v mission_interval=%v",
		cr.discoverTimes, cr.deliverInterval, cr.missionInterval)

	deliverTimer := time.NewTimer(cr.deliverInterval)
	cr.setDeliverNext(time.Now().Add(cr.deliverInterval))
	defer deliverTimer.Stop()
	discoverTimer := time.NewTimer(nextDailyDelay(cr.discoverTimes))
	cr.setDiscoverNext(time.Now().Add(nextDailyDelay(cr.discoverTimes)))
	defer discoverTimer.Stop()

	sessionEndTicker := time.NewTicker(30 * time.Second)
	defer sessionEndTicker.Stop()

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

		case <-sessionEndTicker.C:
			cr.mu.Lock()
			if cr.sessionEndRunning {
				cr.mu.Unlock()
				continue
			}
			cr.sessionEndRunning = true
			cr.mu.Unlock()
			go func() {
				cr.processSessionEndTasks(ctx)
				cr.mu.Lock()
				cr.sessionEndRunning = false
				cr.mu.Unlock()
			}()

		case <-missionTicker.C:
			log.Printf("[cron] mission check trigger")
			go cr.processMissions(ctx)
			cr.missionNext = time.Now().Add(cr.missionInterval)
		}
	}
}

// processSessionEndTasks 扫延迟任务表中到期的 session_end 任务并执行。
// 任务级拉取失败才触发重试；worker 内部失败不重试（独立失败不回滚）。
func (cr *CronRunner) processSessionEndTasks(ctx context.Context) {
	if cr.delayedTaskRepo == nil || cr.sessionEndGroup == nil {
		return
	}
	tasks, err := cr.delayedTaskRepo.ListDue(ctx, "session_end", 20)
	if err != nil {
		log.Printf("[cron] ListDue session_end error: %v", err)
		return
	}
	seen := map[string]bool{}
	for _, t := range tasks {
		meta, err := t.ParseMeta()
		if err != nil || meta.ConvID == "" {
			log.Printf("[cron] bad session_end task meta for %s, marking done", t.ID[:8])
			cr.delayedTaskRepo.MarkDone(ctx, t.ID)
			continue
		}
		key := t.UserID + "|" + meta.ConvID
		if seen[key] {
			log.Printf("[cron] session_end duplicate skipped: user=%s conv=%s task=%s", t.UserID[:8], meta.ConvID[:8], t.ID[:8])
			cr.delayedTaskRepo.MarkDone(ctx, t.ID)
			continue
		}
		seen[key] = true
		log.Printf("[cron] session_end task: user=%s conv=%s", t.UserID[:8], meta.ConvID[:8])

		if err := cr.sessionEndGroup.Process(ctx, meta.ConvID, t.UserID, meta.LastMsgID); err != nil {
			log.Printf("[cron] session_end group error: conv=%s err=%v", meta.ConvID[:8], err)
			if t.RetryCount < 3 {
				cr.delayedTaskRepo.ScheduleRetry(ctx, t.ID, t.RetryCount, 2*time.Minute)
			} else {
				cr.delayedTaskRepo.MarkFailed(ctx, t.ID, t.RetryCount)
			}
			continue
		}

		// 处理期间可能有新对话结束触发了 debounce（ProcessAt 被推到未来、
		// LastMsgID 被更新）。若直接 MarkDone 会丢掉新的 LastMsgID，导致该范围
		// 永不处理。检测到被推迟就跳过 MarkDone，下轮轮询会用新 LastMsgID 重处理。
		if current, err := cr.delayedTaskRepo.FindByID(ctx, t.ID); err != nil {
			log.Printf("[cron] session_end FindByID error: %v, still marking done", err)
		} else if current != nil && current.ProcessAt.After(t.ProcessAt) {
			log.Printf("[cron] session_end task %s debounced while processing, skipping MarkDone", t.ID[:8])
			continue
		}
		if err := cr.delayedTaskRepo.MarkDone(ctx, t.ID); err != nil {
			log.Printf("[cron] MarkDone session_end error: %v", err)
		}
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
