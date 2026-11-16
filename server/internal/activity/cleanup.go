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
