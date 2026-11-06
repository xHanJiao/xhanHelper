package appinit

import (
	"context"
	"log"
	"time"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/async"
	"xhan-helper/internal/config"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
)

// debugJSONQueueSink — async.Queue 适配 DebugJSONSink 接口
type debugJSONQueueSink struct{ q *async.Queue[*string] }

func (s *debugJSONQueueSink) Submit(msgID string, reqJSON *string) bool {
	return s.q.Submit(msgID, reqJSON)
}

// InitDebugJSON 按开关创建调试 JSON 异步持久化设施：
//   - debug_persist_json=false → noop sink（不启动任何 goroutine）
//   - 开启时：限频队列（间隔 + 字节预算）+ 保留清理周期任务
//
// 队列刷盘与保留清理在 bgCtx 下运行。
func InitDebugJSON(cfg *config.Config, msgRepo *service.MessageRepo, bgCtx context.Context) agent.DebugJSONSink {
	if !cfg.LLM.DebugPersist() {
		return agent.NoopDebugJSONSink{}
	}
	d := cfg.LLM.DebugJSON

	queue := async.NewQueue[*string](async.Config[*string]{
		FlushInterval:    time.Duration(d.FlushIntervalMs) * time.Millisecond,
		MaxBytesPerFlush: d.MaxBytesPerFlush,
		MaxQueueEntries:  d.QueueMaxEntries,
		MaxQueueBytes:    d.QueueMaxBytes,
		MaxPayloadBytes:  d.MaxPayloadBytes,
		SizeOf: func(p *string) int {
			if p == nil {
				return 0
			}
			return len(*p)
		},
	}, func(ctx context.Context, batch []async.Job[*string]) error {
		updates := make([]service.DebugJSONUpdate, 0, len(batch))
		for _, j := range batch {
			updates = append(updates, service.DebugJSONUpdate{MessageID: j.Key, RequestJSON: j.Payload})
		}
		if err := msgRepo.BatchUpdateDebugJSON(ctx, updates); err != nil {
			log.Printf("[debug_json] flush batch error (%d msgs): %v", len(batch), err)
			return err
		}
		return nil
	})
	monitor.Register("debug_json_queue", func() any { return queue.Stats() })
	go queue.Start(bgCtx)

	if !cfg.Server.BackgroundTasksDisabled() {
		// lastCutoff 记录上次成功清到的位置，本轮只扫「新过期」的窄带。
		// 首次运行（进程刚起）仍会全量扫一次；之后稳态下每轮只扫最近一个清理间隔，
		// 避免 0 命中时反复扫 595MB 的 messages 表。见 docs/bugs/047。
		// shortcut: 水位只在进程内，重启后首轮全量扫一次；要跨重启持久化再落表。
		var lastCutoff time.Time
		cleanup := func(ctx context.Context) {
			cutoff := time.Now().Add(-time.Duration(d.RetentionDays) * 24 * time.Hour)
			after := lastCutoff
			if !after.IsZero() && !after.Before(cutoff) {
				// 保留天数变长（新 cutoff 比水位更老）/时钟回拨：窗口失效，退回全量扫一次。
				after = time.Time{}
			}
			var cleared int64
			for {
				n, err := msgRepo.ClearDebugJSONRange(ctx, after, cutoff, d.CleanupBatchRows)
				if err != nil {
					log.Printf("[debug_json] retention cleanup error: %v", err)
					return
				}
				cleared += n
				if n < int64(d.CleanupBatchRows) || ctx.Err() != nil {
					if ctx.Err() == nil {
						lastCutoff = cutoff
					}
					log.Printf("[debug_json] retention cleanup done: cleared %d rows older than %d days (window %s)",
						cleared, d.RetentionDays, retentionWindowLabel(after, cutoff))
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(200 * time.Millisecond):
				}
			}
		}
		go async.NewIntervalRunner("debug_json_retention",
			time.Duration(d.CleanupIntervalHours)*time.Hour, cleanup).Start(bgCtx)
	} else {
		log.Printf("[debug_json] background tasks disabled, retention cleanup skipped")
	}

	return &debugJSONQueueSink{q: queue}
}

// InitExtraTraceCleanup 启动 message.extra 检索 trace（providers / user_message_prefix / prompt_trace 等）
// 的滚动清理，保留 usage/window_size/session_start_msg_id/no_merge 等功能字段。
// 独立于 debug_persist_json —— extra 里的 providers 是无条件写入的。
// extra_trace_retention_days<=0 或后台任务关闭时不启动。
func InitExtraTraceCleanup(cfg *config.Config, msgRepo *service.MessageRepo, bgCtx context.Context) {
	days := cfg.LLM.DebugJSON.ExtraTraceRetentionDays
	if days <= 0 {
		log.Printf("[extra_retention] disabled (extra_trace_retention_days=%d)", days)
		return
	}
	if cfg.Server.BackgroundTasksDisabled() {
		log.Printf("[extra_retention] background tasks disabled, cleanup skipped")
		return
	}
	batch := cfg.LLM.DebugJSON.CleanupBatchRows
	// 同 debug_json：只清「新过期」的窄带，首次运行全量扫一次。见 docs/bugs/047。
	// shortcut: 水位只在进程内，重启后首轮全量扫一次；要跨重启持久化再落表。
	var lastCutoff time.Time
	cleanup := func(ctx context.Context) {
		cutoff := time.Now().AddDate(0, 0, -days)
		after := lastCutoff
		if !after.IsZero() && !after.Before(cutoff) {
			// 保留天数变长（新 cutoff 比水位更老）/时钟回拨：窗口失效，退回全量扫一次。
			after = time.Time{}
		}
		var cleared int64
		for {
			n, err := msgRepo.ClearExtraDebugRange(ctx, after, cutoff, batch)
			if err != nil {
				log.Printf("[extra_retention] cleanup error: %v", err)
				return
			}
			cleared += n
			if n < int64(batch) || ctx.Err() != nil {
				if ctx.Err() == nil {
					lastCutoff = cutoff
				}
				log.Printf("[extra_retention] cleanup done: cleared %d rows older than %d days (window %s)",
					cleared, days, retentionWindowLabel(after, cutoff))
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	go async.NewIntervalRunner("extra_trace_retention",
		time.Duration(cfg.LLM.DebugJSON.CleanupIntervalHours)*time.Hour, cleanup).Start(bgCtx)
}

// retentionWindowLabel 日志用：zero after 表示首次全量扫。
func retentionWindowLabel(after, before time.Time) string {
	const layout = "2006-01-02 15:04"
	if after.IsZero() {
		return "all.." + before.Format(layout)
	}
	return after.Format(layout) + ".." + before.Format(layout)
}
