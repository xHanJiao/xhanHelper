package activity

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// EventRetentionDays 是明细的滚动保留期（附表 B §6：90 天硬删、不可逆）。
const EventRetentionDays = 90

// cleanupBatch 是每批删除的行数上限，避免长事务（附表 B §6：一律用 rowid 子查询限批，
// **不要**用 GORM 的 .Limit()——它在 DELETE 上被静默忽略）。
const cleanupBatch = 2000

// CleanupExpired 按保留天数清理到期明细；retentionDays<=0 时不清理、返回 (0,nil)。
// 供调度器按节拍调用（CronRunner.ActivityRecomputer 接口）。
func (s *Store) CleanupExpired(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	return s.CleanupBefore(time.Now().AddDate(0, 0, -retentionDays))
}

// CleanupBefore 硬删 cutoff 之前的事件及其派生（附表 B §6 的清理 SQL）。
// **每批一个短事务**（不把整个循环套进一个长写事务），循环直到没有可删的行；返回删除的事件数。
// 派生表与事实表同期清：先删到期 events，再删其派生孤儿（按 event_id 不在 events），
// 最后按 cutoff 清按天物化的 day_stats/day_health 与 sessions。
//
// 与 Recompute 一致：先 pause emitter（避免清理持写锁期间 emit 刷盘被阻塞、队列满丢事件），
// 再持写锁独占（挡住 IngestBatch 与在途 emit 刷盘），跑完恢复。
func (s *Store) CleanupBefore(cutoff time.Time) (int64, error) {
	if s == nil {
		return 0, fmt.Errorf("events.db 未启用")
	}
	cutoffStr := cutoff.UTC().Format(isoMillis)
	cutoffDay := cutoff.In(sessionDayLoc).Format("2006-01-02")
	if s.em != nil {
		s.em.pause()
		defer s.em.resume()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var deleted int64
	// 1. 到期 events（分批、每批一事务）。顺序：events 先删，派生孤儿随后按「不在 events」清。
	n, err := deleteBatched(s.db, `DELETE FROM events WHERE rowid IN
		(SELECT rowid FROM events WHERE occurred_at < ? LIMIT ?)`, cutoffStr)
	if err != nil {
		return deleted, fmt.Errorf("清理 events: %w", err)
	}
	deleted = n
	// 2. 纯派生边：event_id 已不在 events（含本次删掉的）。event_edges 未实现，但表先留、口径一致。
	for _, sql := range []string{
		`DELETE FROM event_refs WHERE rowid IN
			(SELECT rowid FROM event_refs WHERE event_id NOT IN (SELECT event_id FROM events) LIMIT ?)`,
		`DELETE FROM event_resources WHERE rowid IN
			(SELECT rowid FROM event_resources WHERE event_id NOT IN (SELECT event_id FROM events) LIMIT ?)`,
		`DELETE FROM event_session WHERE rowid IN
			(SELECT rowid FROM event_session WHERE event_id NOT IN (SELECT event_id FROM events) LIMIT ?)`,
		`DELETE FROM interval_closed WHERE rowid IN
			(SELECT rowid FROM interval_closed WHERE start_event_id NOT IN (SELECT event_id FROM events)
				OR end_event_id NOT IN (SELECT event_id FROM events) LIMIT ?)`,
		`DELETE FROM event_edges WHERE rowid IN
			(SELECT rowid FROM event_edges WHERE from_event_id NOT IN (SELECT event_id FROM events)
				OR to_event_id NOT IN (SELECT event_id FROM events) LIMIT ?)`,
	} {
		if _, err := deleteBatchedNoArg(s.db, sql); err != nil {
			return deleted, err
		}
	}
	// 3. 按天物化：sessions 按 started_at，day_stats/day_health 按 day 直接删（行数少，不必分批）。
	if _, err := deleteBatched(s.db, `DELETE FROM sessions WHERE rowid IN
		(SELECT rowid FROM sessions WHERE started_at < ? LIMIT ?)`, cutoffStr); err != nil {
		return deleted, fmt.Errorf("清理 sessions: %w", err)
	}
	if err := s.db.Exec(`DELETE FROM day_stats WHERE day < ?`, cutoffDay).Error; err != nil {
		return deleted, fmt.Errorf("清理 day_stats: %w", err)
	}
	if err := s.db.Exec(`DELETE FROM day_health WHERE day < ?`, cutoffDay).Error; err != nil {
		return deleted, fmt.Errorf("清理 day_health: %w", err)
	}
	return deleted, nil
}

// deleteBatched 反复执行带「? 值 + LIMIT ?」两个参数的批删 SQL，直到某批删除行为 0。
// 每批一个短事务，避免长事务长时间持写锁。
func deleteBatched(db *gorm.DB, sql, arg string) (int64, error) {
	var total int64
	for {
		var affected int64
		err := db.Transaction(func(tx *gorm.DB) error {
			res := tx.Exec(sql, arg, cleanupBatch)
			affected = res.RowsAffected
			return res.Error
		})
		if err != nil {
			return total, err
		}
		total += affected
		if affected < cleanupBatch {
			return total, nil
		}
	}
}

// deleteBatchedNoArg 同 deleteBatched，但 SQL 只带一个 LIMIT 参数（用于「不在 events 的孤儿」批删）。
func deleteBatchedNoArg(db *gorm.DB, sql string) (int64, error) {
	var total int64
	for {
		var affected int64
		err := db.Transaction(func(tx *gorm.DB) error {
			res := tx.Exec(sql, cleanupBatch)
			affected = res.RowsAffected
			return res.Error
		})
		if err != nil {
			return total, err
		}
		total += affected
		if affected < cleanupBatch {
			return total, nil
		}
	}
}

// CleanupResource 按资源手动清（附表 B §6）：清该资源的事件-资源边、主数据与别名，
// **不与内容对象联动**（删记忆/文档不连带删痕迹；这里反过来也只在手动点名时清）。
//
// 只清资源层（event_resources / resources / resource_aliases），**不动 events 本身**：
// 事件是事实层（append-only），按资源清的是"这件事挂了哪条资源"的映射。返回清掉的边数。
// 同时清掉受影响的派生 ref（按 event_id，避免指向已删资源的孤儿 ref）。
//
// ⚠️ **天花板：这不是"永久遗忘"**。events 不动，下次 `Recompute` 会 `RebuildResources` 从
// 事件 attrs 重新长出这些映射（`RebuildRefs` 也会重建 ref）——即手动清在**下次重算后还原**。
// 若要"永久遗忘"，需引入忽略名单（重算时跳过该资源），当前不做（附表 B §6 只承诺手动口子）。
func (s *Store) CleanupResource(kind, id string) (int64, error) {
	if s == nil {
		return 0, fmt.Errorf("events.db 未启用")
	}
	if kind == "" || id == "" {
		return 0, fmt.Errorf("resource_kind 与 resource_id 不能为空")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var edges int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 先记下受影响的事件，清边后连带清这些事件的派生 ref（避免孤儿 ref）。
		var affected []string
		if err := tx.Raw(`SELECT DISTINCT event_id FROM event_resources
			WHERE resource_kind = ? AND resource_id = ?`, kind, id).Scan(&affected).Error; err != nil {
			return fmt.Errorf("查受影响事件: %w", err)
		}
		res := tx.Exec(`DELETE FROM event_resources WHERE resource_kind = ? AND resource_id = ?`, kind, id)
		if res.Error != nil {
			return fmt.Errorf("清理 event_resources: %w", res.Error)
		}
		edges = res.RowsAffected
		if len(affected) > 0 {
			if err := tx.Exec(`DELETE FROM event_refs WHERE source = ? AND event_id IN ?`,
				RefSourceResource, affected).Error; err != nil {
				return fmt.Errorf("清理关联 refs: %w", err)
			}
		}
		if err := tx.Exec(`DELETE FROM resources WHERE resource_kind = ? AND resource_id = ?`, kind, id).Error; err != nil {
			return fmt.Errorf("清理 resources: %w", err)
		}
		if err := tx.Exec(`DELETE FROM resource_aliases WHERE resource_kind = ? AND resource_id = ?`, kind, id).Error; err != nil {
			return fmt.Errorf("清理 resource_aliases: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return edges, nil
}
