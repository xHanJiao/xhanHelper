package activity

import (
	"fmt"
	"log"
	"strconv"
	"time"
)

// recomputeMetaKey 记「当前派生层是用哪版规则算出来的」汇总版本键。
// 与 last_recompute_at 一起构成启动补算的判据：规则版本变了必须补算，否则派生层会带着旧规则
// 的产物继续服务到下一个夜间节拍。
const recomputeMetaKey = "recompute_stamp"

// RecomputeDue 判断派生数据是否已过期：规则版本变了、距上次成功重算超过 maxAge、从未算过、
// 或时间戳坏掉，都视为过期。供调度器「启动时按需补算」用——全量重建要读整个 events.db，
// 进程每次启动都跑会让部署后出现一次读 IO 尖峰；而重算 ticker 是从进程启动起算 24h，
// 频繁重启会永远等不到，所以不能简单去掉启动补算。
func (s *Store) RecomputeDue(maxAge time.Duration) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("events.db 未启用")
	}
	stamp, _, err := s.GetMeta(recomputeMetaKey)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", recomputeMetaKey, err)
	}
	if stamp != recomputeStamp() {
		// 版本升级（规则变化）后派生层必须立刻重建，不能等时间窗。
		return true, nil
	}
	v, ok, err := s.GetMeta("last_recompute_at")
	if err != nil {
		return false, fmt.Errorf("read last_recompute_at: %w", err)
	}
	if !ok || v == "" {
		return true, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		// 时间戳坏了按过期处理：重算一次会把它覆盖成合法值，比静默跳过安全。
		log.Printf("[activity] last_recompute_at 解析失败（%q），按过期处理: %v", v, err)
		return true, nil
	}
	return time.Since(t) > maxAge, nil
}

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
