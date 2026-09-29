package activity

import (
	"fmt"
	"log"
	"strconv"
	"time"
)

// recomputeMetaKey 记「当前派生层是用哪版规则算出来的」汇总版本键（可观测，供查库/调试）。
// 注意：它不再用于触发判定——重算由调度器每节拍无条件触发（见 cron_runner.recomputeActivity）。
const recomputeMetaKey = "recompute_stamp"

// Recompute 全量重跑所有派生层：区间闭合 → 资源归一 → 五层在场 → 每日分布/体检。
// 每张派生表各自一个写事务（单表内「清空 + 重建」原子；跨表不追求全局一致，读取端按表单表取）。
//
// 重建前先停 ingest（emitter 暂停刷盘），跑完恢复并补刷，避免 emit 写入与重建的写事务撞 busy_timeout。
func (s *Store) Recompute() error {
	if s == nil {
		return fmt.Errorf("events.db 未启用")
	}
	// 写锁独占：挡住 emit 刷盘与客户端上报（二者持读锁），重建期间无人与之撞锁。
	// 顺序：先停 ingest（让 emitter 不再尝试写），再拿写锁（等在途批次结束），最后重建。
	if s.em != nil {
		s.em.pause()
		defer s.em.resume()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	start := time.Now()
	if err := s.RebuildIntervals(); err != nil {
		return fmt.Errorf("recompute intervals: %w", err)
	}
	if err := s.RebuildResources(); err != nil {
		return fmt.Errorf("recompute resources: %w", err)
	}
	if err := s.RebuildSessions(); err != nil {
		return fmt.Errorf("recompute sessions: %w", err)
	}
	if err := s.RebuildRefs(); err != nil {
		return fmt.Errorf("recompute refs: %w", err)
	}
	if err := s.RebuildDayStats(); err != nil {
		return fmt.Errorf("recompute day stats: %w", err)
	}
	stamp := recomputeStamp()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.SetMeta(recomputeMetaKey, stamp); err != nil {
		return fmt.Errorf("write recompute stamp: %w", err)
	}
	// last_recompute_at 记的是「完成时刻」，供调试/监控看节拍是否跑过。
	if err := s.SetMeta("last_recompute_at", now); err != nil {
		return fmt.Errorf("write last_recompute_at: %w", err)
	}
	log.Printf("[activity] 重算完成：stamp=%s 耗时=%s", stamp, time.Since(start).Round(time.Millisecond))
	return nil
}

// recomputeStamp 把派生版本拼成稳定串。
func recomputeStamp() string {
	return "r" + strconv.Itoa(IntervalRuleVersion) +
		"/t" + strconv.Itoa(SessionThresholdVersion) +
		"/n" + strconv.Itoa(NormalizeRuleVersion) +
		"/f" + strconv.Itoa(RefRuleVersion)
}
