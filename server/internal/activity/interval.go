package activity

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// IntervalRuleVersion 是区间闭合规则的版本号；规则定义见设计篇 3.4 的「闭合规则的取值」。
// 变更规则时 +1，派生结果按 rule_version 判定过期、触发重算（设计篇 6.5）。
const IntervalRuleVersion = 1

// 闭合档位（附表 B interval_closed.closed_by 取值）。
const (
	ClosedPaired     = "paired"
	ClosedBoundary   = "boundary"
	ClosedInferred   = "inferred"
	ClosedFallback   = "fallback"
	ClosedSuperseded = "superseded"
	// ClosedDaySplit 是五层 session 按本地午夜切段产生的强结束（不是 boundary 事件闭合）。
	ClosedDaySplit = "day_split"
)

// 闭合规则常量（2026-09-27 定，改动须同时 +IntervalRuleVersion）。
const (
	intervalMaxDuration       = 24 * time.Hour   // 硬上限：任何区间不得超过 24h
	intervalFallbackDuration  = 5 * time.Minute  // fallback 保守上限：宁可低估不虚报
	intervalMinSamples        = 5                // inferred 门槛：同类型已配对样本 ≥ 5
	intervalSupersededEpsilon = time.Millisecond // superseded：闭合到新 start 前 1ms
)

// boundaryType 只认显式 system.boundary（设计篇 3.4 定）。
const boundaryType = TypeSystemBoundary

// intervalPair 描述一对区间事件：start 的 type → 配对 end 的 type（附表 A 形态列）。
// 命名不统一（task.spawned/ended、client.input.active/ended），故用表而非后缀推断。
var intervalPairEndType = map[string]string{
	TypeChatTurnStarted:       TypeChatTurnEnded,
	TypeLLMCallStarted:        TypeLLMCallEnded,
	TypeToolCallStarted:       TypeToolCallEnded,
	TypeClientToolExecStarted: TypeClientToolExecEnded,
	TypeTaskSpawned:           TypeTaskEnded,
	TypeBrowserNavStarted:     TypeBrowserNavEnded,
	TypeClientInputActive:     TypeClientInputEnded,
}

// intervalRow 是 interval_closed 表的一行。
type intervalRow struct {
	StartEventID string  `gorm:"column:start_event_id;primaryKey"`
	EndEventID   *string `gorm:"column:end_event_id"`
	EndAt        string  `gorm:"column:end_at"`
	DurationMs   int64   `gorm:"column:duration_ms"`
	ClosedBy     string  `gorm:"column:closed_by"`
	RuleVersion  int     `gorm:"column:rule_version"`
	ComputedAt   string  `gorm:"column:computed_at"`
}

func (intervalRow) TableName() string { return "interval_closed" }

// sourceEvent 是闭合算法需要的事件列。
type sourceEvent struct {
	EventID    string
	Type       string
	OccurredAt time.Time
	PairID     *string
}

// closeIntervals 全量重跑区间闭合：清空 interval_closed 后按事件重算，规则版本+结果同事务写。
// 与 RebuildResources 同样是「清空 + 重扫」，整段一个写事务；接 P16 定时前须先停 ingest。
func (s *Store) closeIntervals(tx *gorm.DB) error {
	if err := tx.Exec(`DELETE FROM interval_closed`).Error; err != nil {
		return fmt.Errorf("清空 interval_closed: %w", err)
	}
	events, err := scanSourceEvents(tx)
	if err != nil {
		return err
	}
	rows := computeIntervals(events)
	if len(rows) > 0 {
		// 分块插入：间隔行 7 列，SQLite 变量上限 32766 → 单条 INSERT 最多约 4680 行；
		// 90 天真实数据（单 tool.call 就可能上万）会超限，必须分块（gorm 默认 CreateBatchSize==0 不分块）。
		if err := tx.CreateInBatches(&rows, 500).Error; err != nil {
			return fmt.Errorf("insert %d interval_closed: %w", len(rows), err)
		}
	}
	return tx.Exec(
		`INSERT INTO meta("key", value, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT("key") DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		"rule_version", strconv.Itoa(IntervalRuleVersion), time.Now().UTC().Format(time.RFC3339),
	).Error
}

// RebuildIntervals 全量重跑区间闭合（可在规则变更后调用）。
func (s *Store) RebuildIntervals() error {
	if s == nil {
		return fmt.Errorf("events.db 未启用")
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.closeIntervals(tx)
	}); err != nil {
		return fmt.Errorf("rebuild intervals: %w", err)
	}
	return nil
}

// scanSourceEvents 读出全部事件，按 (occurred_at, received_at, event_id) 三级排序
// （时钟回拨会让 occurred_at 乱序，tie-break 见设计篇 3.4）。
//
// ⚠️ 全量读入内存、全表排序：闭合需要全局视图（paired 回查、superseded 看同类型下一条），
// 无法真分页。90 天几十万行下重建期内存/CPU 峰值明显——故这是**离线夜间重建**（P16）的活，
// 不是请求路径；接定时时须先停 ingest（见 closeIntervals）。
func scanSourceEvents(tx *gorm.DB) ([]sourceEvent, error) {
	var raw []struct {
		EventID    string  `gorm:"column:event_id"`
		Type       string  `gorm:"column:type"`
		OccurredAt string  `gorm:"column:occurred_at"`
		ReceivedAt string  `gorm:"column:received_at"`
		PairID     *string `gorm:"column:pair_id"`
	}
	err := tx.Raw(
		`SELECT event_id, type, occurred_at, received_at, pair_id FROM events
		 ORDER BY occurred_at, received_at, event_id`,
	).Scan(&raw).Error
	if err != nil {
		return nil, fmt.Errorf("扫描 events: %w", err)
	}
	out := make([]sourceEvent, 0, len(raw))
	for _, r := range raw {
		at, err := time.Parse(isoMillis, r.OccurredAt)
		if err != nil {
			// 单条时间戳坏掉不该拖垮整轮闭合：跳过并计数（该事件的区间无法闭合）。
			log.Printf("[activity] 区间闭合：解析 occurred_at 失败，跳过 %s: %v", r.EventID, err)
			continue
		}
		out = append(out, sourceEvent{EventID: r.EventID, Type: r.Type, OccurredAt: at, PairID: r.PairID})
	}
	return out, nil
}

// computeIntervals 是闭合算法本体（纯函数，便于单测）。
// 输入须已按 occurred_at 排好序（scanSourceEvents 负责）。
func computeIntervals(events []sourceEvent) []intervalRow {
	byID := make(map[string]sourceEvent, len(events))
	for _, e := range events {
		byID[e.EventID] = e
	}
	// 按共享 pair_id 找对端：客户端用一条 UUID 作共享键（两端 pair_id 相同）。
	// 服务端预分配 event_id 互指，被 byID 直接命中，不走这里。
	byPairKey := make(map[string][]string, len(events))
	for _, e := range events {
		if e.PairID != nil && *e.PairID != "" {
			byPairKey[*e.PairID] = append(byPairKey[*e.PairID], e.EventID)
		}
	}
	// 收集 start 事件（type 在配对表里）。
	starts := make([]sourceEvent, 0)
	for _, e := range events {
		if _, ok := intervalPairEndType[e.Type]; ok {
			starts = append(starts, e)
		}
	}
	// 同类型下一条 start：用于 superseded 修复（同类型 = 字面 type 相同）。
	nextSameTypeStart := nextStartIndexByType(starts)
	// boundary 事件时刻列表，二分找「start 之后第一个 boundary」。
	boundaries := boundaryTimes(events)
	// 同类型已配对时长样本（供 inferred 取 p95）；扫描一遍预先收集。
	samples := pairedDurations(events, byID, byPairKey)

	now := time.Now().UTC().Format(isoMillis)
	rows := make([]intervalRow, 0, len(starts))
	for i, st := range starts {
		endAt, endID, closedBy := closeOne(st, byID, byPairKey, boundaries, samples)
		// superseded 修复：只对**仍悬挂**的闭合（inferred/fallback）生效——paired/boundary 是
		// 有实据的闭合，即便与后来的同类型区间重叠（并发工具/后台调用天然重叠），也不抢、不截断，
		// 否则会丢掉真实配对的 end_event_id。
		if closedBy == ClosedInferred || closedBy == ClosedFallback {
			if next, ok := nextSameTypeStart[i]; ok {
				cutoff := next.OccurredAt.Add(-intervalSupersededEpsilon)
				if endAt.After(cutoff) {
					endAt = cutoff
					endID = nil
					closedBy = ClosedSuperseded
				}
			}
		}
		// 24h 硬上限：boundary 可能是任意远的时刻（甚至 >24h），故这里仍要再兜一次。
		if d := endAt.Sub(st.OccurredAt); d > intervalMaxDuration {
			endAt = st.OccurredAt.Add(intervalMaxDuration)
			endID = nil
			closedBy = ClosedFallback
		}
		d := endAt.Sub(st.OccurredAt)
		if d < 0 {
			d = 0
		}
		rows = append(rows, intervalRow{
			StartEventID: st.EventID,
			EndEventID:   endID,
			EndAt:        endAt.UTC().Format(isoMillis),
			DurationMs:   d.Milliseconds(),
			ClosedBy:     closedBy,
			RuleVersion:  IntervalRuleVersion,
			ComputedAt:   now,
		})
	}
	return rows
}

// closeOne 对单条 start 套 paired>boundary>inferred>fallback。
func closeOne(
	st sourceEvent,
	byID map[string]sourceEvent,
	byPairKey map[string][]string,
	boundaries []time.Time,
	samples map[string][]time.Duration,
) (time.Time, *string, string) {
	// 1. paired：服务端互指（pair_id 指向对端 event_id）或客户端共享键。
	if e, ok := pairedEnd(st, byID, byPairKey); ok {
		return e.OccurredAt, &e.EventID, ClosedPaired
	}
	// 2. boundary：start 之后第一个 boundary 时刻。
	if b, ok := firstBoundaryAfter(boundaries, st.OccurredAt); ok {
		return b, nil, ClosedBoundary
	}
	// 3. inferred：同类型已配对样本 ≥ intervalMinSamples → start + p95。
	if ds := samples[st.Type]; len(ds) >= intervalMinSamples {
		return st.OccurredAt.Add(p95(ds)), nil, ClosedInferred
	}
	// 4. fallback：保守上限，标记不可信。
	return st.OccurredAt.Add(intervalFallbackDuration), nil, ClosedFallback
}

// pairedEnd 找 start 的配对 end：服务端互指（pair_id == end.event_id）优先，
// 退回客户端共享键（同 pair_id、不同 event_id、type 为配对端）。对端不存在则无配对。
// 且要求 end.OccurredAt >= start.OccurredAt——时钟回拨/乱序时端在开始之前的配对不可信，
// 不作为 paired（否则会产出 end_at 早于 start 的自相矛盾行），落到后续档位。
func pairedEnd(st sourceEvent, byID map[string]sourceEvent, byPairKey map[string][]string) (sourceEvent, bool) {
	endType, ok := intervalPairEndType[st.Type]
	if !ok {
		return sourceEvent{}, false
	}
	valid := func(e sourceEvent) bool {
		return e.Type == endType && e.EventID != st.EventID && !e.OccurredAt.Before(st.OccurredAt)
	}
	if st.PairID != nil && *st.PairID != "" {
		// 服务端互指：pair_id 直接是 end 的 event_id。
		if e, ok := byID[*st.PairID]; ok && valid(e) {
			return e, true
		}
		// 客户端共享键：同 pair_id、对端 type。
		for _, id := range byPairKey[*st.PairID] {
			if e := byID[id]; valid(e) {
				return e, true
			}
		}
	}
	return sourceEvent{}, false
}

// nextStartIndexByType 返回 starts[i] 之后「同类型下一条 start」的下标。
func nextStartIndexByType(starts []sourceEvent) map[int]sourceEvent {
	lastIdx := make(map[string]int)
	out := make(map[int]sourceEvent)
	for i := len(starts) - 1; i >= 0; i-- {
		if j, ok := lastIdx[starts[i].Type]; ok {
			out[i] = starts[j]
		}
		lastIdx[starts[i].Type] = i
	}
	return out
}

func boundaryTimes(events []sourceEvent) []time.Time {
	var out []time.Time
	for _, e := range events {
		if e.Type == boundaryType {
			out = append(out, e.OccurredAt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func firstBoundaryAfter(boundaries []time.Time, t time.Time) (time.Time, bool) {
	i := sort.Search(len(boundaries), func(i int) bool { return boundaries[i].After(t) })
	if i < len(boundaries) {
		return boundaries[i], true
	}
	return time.Time{}, false
}

// pairedDurations 收集同类型已配对区间的时长，供 inferred 取 p95。
// pairedEnd 已保证 end >= start，故差值必非负。
func pairedDurations(events []sourceEvent, byID map[string]sourceEvent, byPairKey map[string][]string) map[string][]time.Duration {
	out := make(map[string][]time.Duration)
	for _, e := range events {
		if _, ok := intervalPairEndType[e.Type]; !ok {
			continue
		}
		if end, ok := pairedEnd(e, byID, byPairKey); ok {
			out[e.Type] = append(out[e.Type], end.OccurredAt.Sub(e.OccurredAt))
		}
	}
	return out
}

// p95 返回 95 分位时长（最近秩法：ceil(0.95*n)-1）。空切片返回 0。
func p95(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(ds))
	copy(sorted, ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted))*0.95+0.9999) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
