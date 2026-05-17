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

// CommitmentTimer — 承诺定时管理器（timer + 兜底扫描）
type CommitmentTimer struct {
	commitRepo   *service.CommitmentRepo
	mailboxRepo  *service.MailboxRepo
	mu           sync.Mutex
	timers       map[string]*time.Timer // commitmentID → timer
	scanInterval time.Duration
}

// NewCommitmentTimer 创建定时管理器
func NewCommitmentTimer(commitRepo *service.CommitmentRepo, mailboxRepo *service.MailboxRepo) *CommitmentTimer {
	return &CommitmentTimer{
		commitRepo:   commitRepo,
		mailboxRepo:  mailboxRepo,
		timers:       make(map[string]*time.Timer),
		scanInterval: 5 * time.Minute,
	}
}

// Schedule 注册一次性或循环承诺的 timer
func (t *CommitmentTimer) Schedule(c *model.Commitment) {
	if c.NextDeadline == nil {
		return
	}
	delay := time.Until(*c.NextDeadline)
	if delay <= 0 {
		t.fire(c)
		return
	}

	t.mu.Lock()
	if old, ok := t.timers[c.ID]; ok {
		old.Stop()
	}
	t.timers[c.ID] = time.AfterFunc(delay, func() {
		t.fire(c)
	})
	t.mu.Unlock()

	log.Printf("[commitment] scheduled timer for %s in %v", c.ID[:8], delay.Round(time.Second))
}

// fire 触发推送
func (t *CommitmentTimer) fire(c *model.Commitment) {
	t.mu.Lock()
	delete(t.timers, c.ID)
	t.mu.Unlock()

	// 创建信箱消息
	item := &model.MailboxItem{
		ID:      uuid.New().String(),
		UserID:  c.UserID,
		Title:   "待办提醒",
		Content: c.Content,
		Type:    "reminder",
		Source:  "待办提醒",
	}
	if err := t.mailboxRepo.Create(context.Background(), item); err != nil {
		log.Printf("[commitment] create mailbox item error: %v", err)
		return
	}
	log.Printf("[commitment] delivered reminder: %s", c.ID[:8])

	// 循环承诺：续约
	if c.RecurrenceRule != nil && *c.RecurrenceRule != "" {
		rule, err := ParseRecurrenceRule(*c.RecurrenceRule)
		if err != nil {
			log.Printf("[commitment] parse recurrence rule error: %v", err)
			t.commitRepo.MarkTriggered(context.Background(), c.ID)
			return
		}
		next, err := NextDeadline(rule, time.Now())
		if err != nil {
			log.Printf("[commitment] next deadline error: %v", err)
			t.commitRepo.MarkTriggered(context.Background(), c.ID)
			return
		}
		t.commitRepo.UpdateNextDeadline(context.Background(), c.ID, next)
		c.NextDeadline = &next
		t.Schedule(c) // 续约
		log.Printf("[commitment] renewed %s for %v", c.ID[:8], next)
	} else {
		t.commitRepo.MarkTriggered(context.Background(), c.ID)
	}
}

// StartFallbackScan 启动兜底扫描（阻塞，goroutine 调用）
func (t *CommitmentTimer) StartFallbackScan(ctx context.Context) {
	log.Printf("[commitment] fallback scan started, interval=%v", t.scanInterval)
	ticker := time.NewTicker(t.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[commitment] fallback scan stopped")
			return
		case <-ticker.C:
			t.scanDue()
		}
	}
}

func (t *CommitmentTimer) scanDue() {
	// 扫 pending 且 next_deadline 在 5 分钟以内的承诺
	// 用 scan 不传 userID 限制，用全局扫。ListPendingByDeadline 需要适配。
	// 简单做法：从 DB 用原始 SQL
	users, _ := t.commitRepo.ListPendingByDeadline(context.Background(), "", time.Now().Add(t.scanInterval), 100)
	for i := range users {
		c := &users[i]
		t.mu.Lock()
		if _, exists := t.timers[c.ID]; !exists {
			t.mu.Unlock()
			t.Schedule(c)
			continue
		}
		t.mu.Unlock()
	}
}

// LoadPending 启动时从 DB 加载所有 pending 承诺并注册 timer
func (t *CommitmentTimer) LoadPending(ctx context.Context) {
	commitments, err := t.commitRepo.ListPendingByDeadline(ctx, "", time.Now().Add(30*24*time.Hour), 500)
	if err != nil {
		log.Printf("[commitment] load pending error: %v", err)
		return
	}
	for i := range commitments {
		c := &commitments[i]
		t.Schedule(c)
	}
	log.Printf("[commitment] loaded %d pending commitments", len(commitments))
}
