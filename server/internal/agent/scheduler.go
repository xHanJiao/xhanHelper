package agent

import (
	"context"
	"log"
	"time"

	"xhan-helper/internal/service"
)

// MemoryScheduler — 定期扫描静默会话并提取记忆
type MemoryScheduler struct {
	extractor *MemoryExtractor
	convRepo  *service.ConversationRepo
	interval  time.Duration
	quietDur  time.Duration
}

// NewMemoryScheduler 创建调度器
func NewMemoryScheduler(extractor *MemoryExtractor, convRepo *service.ConversationRepo) *MemoryScheduler {
	return &MemoryScheduler{
		extractor: extractor,
		convRepo:  convRepo,
		interval:  10 * time.Minute,
		quietDur:  1 * time.Hour,
	}
}

// Start 启动定时任务（阻塞，用 goroutine 调用）
func (s *MemoryScheduler) Start(ctx context.Context) {
	log.Printf("[memory] scheduler started, interval=%v, quiet=%v", s.interval, s.quietDur)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[memory] scheduler stopped")
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *MemoryScheduler) runOnce(ctx context.Context) {
	cutoff := time.Now().Add(-s.quietDur)

	convs, err := s.convRepo.ListQuiet(ctx, cutoff, 10)
	if err != nil {
		log.Printf("[memory] scan error: %v", err)
		return
	}

	for _, conv := range convs {
		if err := s.extractor.ExtractFromConversation(ctx, &conv); err != nil {
			log.Printf("[memory] extract conv %s error: %v", conv.ID[:8], err)
		}
	}
}
