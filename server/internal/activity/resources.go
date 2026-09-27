package activity

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 资源抽取约定（写进附表 A）：事件 attrs 里认这几格——
//   url_raw / url → url    doc_id → doc    memory_id → memory
//   repo_path → repo       file_path（配 repo_path 可锚到仓库）→ file
// 这是"原始值落事件、归一在写入时做"的落点（设计篇 6.1）。

// ResourceRef 是一条事件抽出的一个资源引用（归一后）。
type ResourceRef struct {
	Kind      string // url / repo / file / doc / memory
	ID        string // 归一后的规范 id（附表 B resources.resource_id）
	Canonical string // 规范形态
	Raw       string // 事件上的原始写法（别名表用）
}

type resourceRow struct {
	ResourceID   string `gorm:"column:resource_id;primaryKey"`
	ResourceKind string `gorm:"column:resource_kind"`
	Canonical    string `gorm:"column:canonical"`
	CreatedAt    string `gorm:"column:created_at"`
	UpdatedAt    string `gorm:"column:updated_at"`
}

func (resourceRow) TableName() string { return "resources" }

type resourceAliasRow struct {
	ResourceKind string `gorm:"column:resource_kind;primaryKey"`
	Alias        string `gorm:"column:alias;primaryKey"`
	ResourceID   string `gorm:"column:resource_id"`
	FirstSeen    string `gorm:"column:first_seen"`
	LastSeen     string `gorm:"column:last_seen"`
}

func (resourceAliasRow) TableName() string { return "resource_aliases" }

type eventResourceRow struct {
	EventID      string  `gorm:"column:event_id;primaryKey"`
	ResourceKind string  `gorm:"column:resource_kind;primaryKey"`
	ResourceID   string  `gorm:"column:resource_id;primaryKey"`
	RawValue     *string `gorm:"column:raw_value"`
}

func (eventResourceRow) TableName() string { return "event_resources" }

// maxResourceValueLen 是进主数据/边的字符串上限（id / canonical / raw 同一把尺）。单条 attrs 可到
// 1 MiB，不设限会让异常长的值整串进主数据与别名表；resource_id 也必须一起收，否则主键与 canonical
// 不再相等（口径「url/repo/file 用归一后字符串本身」就被破坏）。超限的 ref 直接丢弃（归一产物不可用，
// 丢的是资源边而非事件，RebuildResources 也不能救——规则是确定的，下次同样丢，但不会污染主数据）。
const maxResourceValueLen = 2048

// truncateRunes 按 rune 边界截断，避免切断多字节字符往 TEXT 列写非法 UTF-8。
func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// existingEvents 过滤出这次**新插入**且确实入库的行（保持原顺序）。
// 传入的 preExisting 是「插入前已在库」的 event_id 集合：这些是纯重放，不索引——
// 否则会用新 attrs 给旧事件补边、污染只增不清的别名表。批内重复只算一次。
func existingEvents(tx *gorm.DB, rows []eventRow, preExisting map[string]struct{}) []eventRow {
	var found []string
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.EventID)
	}
	if err := tx.Raw(`SELECT event_id FROM events WHERE event_id IN ?`, ids).Scan(&found).Error; err != nil {
		// 查不到就当没有可索引的（决定不索引不是丢数据：边是纯派生，RebuildResources 能补）。
		log.Printf("[activity] 归一：查询在库事件失败，本批不建资源边: %v", err)
		return nil
	}
	// 在库且不是插入前就在的 = 本次新插进来的。
	set := make(map[string]struct{}, len(found))
	for _, id := range found {
		if _, pre := preExisting[id]; !pre {
			set[id] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(rows))
	out := make([]eventRow, 0, len(rows))
	for _, r := range rows {
		if _, ok := set[r.EventID]; !ok {
			continue
		}
		if _, dup := seen[r.EventID]; dup {
			continue
		}
		seen[r.EventID] = struct{}{}
		out = append(out, r)
	}
	return out
}

// extractRefs 从事件的 attrs JSON 抽出资源引用；attrs 解析失败记一条日志后跳过（该事件无资源，
// 不影响其它事件与主流程——由此错误不影响落库契约，属可降级）。
// 每个 ref 的 ID/Canonical/Raw 统一按 maxResourceValueLen 截断（rune 边界）；截断后 ID 与 Canonical
// 仍相等，口径不漂；任一字段超限的 ref 直接丢弃并计数，不进主数据。
func extractRefs(attrsJSON, home string) []ResourceRef {
	var attrs map[string]any
	if err := json.Unmarshal([]byte(attrsJSON), &attrs); err != nil {
		log.Printf("[activity] 归一：attrs 解析失败，跳过该事件资源: %v", err)
		return nil
	}
	var out []ResourceRef
	add := func(kind, id, canonical, raw string) {
		id = truncateRunes(id, maxResourceValueLen)
		canonical = truncateRunes(canonical, maxResourceValueLen)
		raw = truncateRunes(raw, maxResourceValueLen)
		if id == "" {
			log.Printf("[activity] 归一：%s 资源 id 超限被丢弃", kind)
			return
		}
		if id != canonical {
			// 正常情况下 id 与 canonical 同源同值；截断不同步说明归一产物自相矛盾，宁可丢弃。
			log.Printf("[activity] 归一：%s 资源 id/canonical 不一致，丢弃: id=%q canonical=%q", kind, id, canonical)
			return
		}
		out = append(out, ResourceRef{Kind: kind, ID: id, Canonical: canonical, Raw: raw})
	}

	if rawURL := firstString(attrs, "url_raw", "url"); rawURL != "" {
		if id, ok := NormalizeURL(rawURL); ok {
			// raw 落别名/边前再脱敏一次：服务端自己发的 url 事件可能带 token，不能进 events.db。
			add(ResourceKindURL, id, id, ScrubbedURL(rawURL))
		}
	}
	repoRaw := firstString(attrs, "repo_path")
	var repoAnchor string
	if repoRaw != "" {
		if id, ok := NormalizeRepo(repoRaw, home); ok {
			repoAnchor = id
			add(ResourceKindRepo, id, id, repoRaw)
		}
	}
	// 附表 A：file_path 必须配 repo_path。没有仓库锚的相对路径会跨仓库合并成一个资源，故不产 file ref。
	if fileRaw := firstString(attrs, "file_path"); fileRaw != "" && repoRaw != "" {
		if id, ok := NormalizeFilePath(fileRaw, repoRaw, repoAnchor, home); ok {
			add(ResourceKindFile, id, id, fileRaw)
		}
	}
	if docID := firstString(attrs, "doc_id"); docID != "" {
		add(ResourceKindDoc, docID, docID, docID)
	}
	if memID := firstString(attrs, "memory_id"); memID != "" {
		add(ResourceKindMemory, memID, memID, memID)
	}
	// 账单/提醒也直接用系统内 id 当锚（设计篇 6.1），与 doc/memory 同理。
	if billID := firstString(attrs, "bill_id"); billID != "" {
		add(ResourceKindBill, billID, billID, billID)
	}
	if remID := firstString(attrs, "reminder_id"); remID != "" {
		add(ResourceKindReminder, remID, remID, remID)
	}
	return out
}

func firstString(attrs map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := attrs[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// indexResources 增量维护主数据（resources / resource_aliases，别名只增不清、可重指），
// 并写纯派生的 event_resources（幂等）。主数据先建，再落边。
// preExisting 是「插入前已在库」的 event_id 集合（用于区分新插与纯重放）。
func (s *Store) indexResources(tx *gorm.DB, rows []eventRow, preExisting map[string]struct{}) error {
	// 只索引真正落库的事件：DoNothing 会跳过 (type,biz_key) 冲突的行（新 event_id、旧 biz_key），
	// 若照索引会留下 event_id 不在 events 的孤儿边（附表 B 清理 SQL 也防这个，这里从源头挡掉）。
	rows = existingEvents(tx, rows, preExisting)
	if len(rows) == 0 {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[activity] 取家目录失败，仓库/文件路径按绝对路径处理: %v", err)
		home = ""
	}
	now := time.Now().UTC().Format(isoMillis)

	resSeen := map[string]struct{}{}
	aliasSeen := map[string]struct{}{}
	erSeen := map[string]struct{}{}
	var resRows []resourceRow
	var aliasRows []resourceAliasRow
	var erRows []eventResourceRow

	for _, row := range rows {
		if row.Attrs == nil {
			continue
		}
		for _, ref := range extractRefs(*row.Attrs, home) {
			// ref 的 ID/Canonical/Raw 已在 extractRefs 内统一截断（rune 边界），这里直接用。
			if _, ok := resSeen[ref.ID]; !ok {
				resSeen[ref.ID] = struct{}{}
				resRows = append(resRows, resourceRow{
					ResourceID: ref.ID, ResourceKind: ref.Kind, Canonical: ref.Canonical,
					CreatedAt: now, UpdatedAt: now,
				})
			}
			if _, ok := aliasSeen[ref.Kind+"\x00"+ref.Raw]; !ok {
				aliasSeen[ref.Kind+"\x00"+ref.Raw] = struct{}{}
				// first_seen/last_seen 记「服务端见到这个写法」的入库时刻（received-time 语义）：
				// 重放同一事件会刷新 last_seen，这是别名维护的既定口径（附表 B：只增不清、可重指）。
				aliasRows = append(aliasRows, resourceAliasRow{
					ResourceKind: ref.Kind, Alias: ref.Raw, ResourceID: ref.ID,
					FirstSeen: now, LastSeen: now,
				})
			}
			if _, ok := erSeen[row.EventID+"\x00"+ref.Kind+"\x00"+ref.ID]; !ok {
				erSeen[row.EventID+"\x00"+ref.Kind+"\x00"+ref.ID] = struct{}{}
				rawCopy := ref.Raw
				erRows = append(erRows, eventResourceRow{
					EventID: row.EventID, ResourceKind: ref.Kind, ResourceID: ref.ID, RawValue: &rawCopy,
				})
			}
		}
	}

	if len(resRows) > 0 {
		err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "resource_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"resource_kind": gorm.Expr("excluded.resource_kind"),
				"canonical":     gorm.Expr("excluded.canonical"),
				"updated_at":    gorm.Expr("excluded.updated_at"),
			}),
		}).Create(&resRows).Error
		if err != nil {
			return fmt.Errorf("upsert %d resources: %w", len(resRows), err)
		}
	}
	if len(aliasRows) > 0 {
		err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "resource_kind"}, {Name: "alias"}},
			DoUpdates: clause.Assignments(map[string]any{
				"resource_id": gorm.Expr("excluded.resource_id"),
				"last_seen":   gorm.Expr("excluded.last_seen"),
			}),
		}).Create(&aliasRows).Error
		if err != nil {
			return fmt.Errorf("upsert %d resource_aliases: %w", len(aliasRows), err)
		}
	}
	if len(erRows) > 0 {
		err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&erRows).Error
		if err != nil {
			return fmt.Errorf("insert %d event_resources: %w", len(erRows), err)
		}
	}
	return nil
}

// RebuildResources 全量重跑资源归一：清空纯派生的 event_resources 后按事件上留的原始值重建；
// 主数据（resources / resource_aliases）不删——别名只增不清，两个锚合并只重指不丢写法。
// 归一规则变更后调用；规则版本与重建在同一事务内写进 meta（崩溃不会留下"版本没写"的半步）。
//
// ⚠️ 重建是「清空 + 分页重扫」，整段在一个写事务里，期间 emit 的写入会撞 busy_timeout 被丢批。
// 当前只在手动/将来 P16 的定时节拍里调；接定时任务时必须在重建前停 ingest（或分事务重建）。
func (s *Store) RebuildResources() error {
	if s == nil {
		return fmt.Errorf("events.db 未启用")
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM event_resources`).Error; err != nil {
			return fmt.Errorf("清空 event_resources: %w", err)
		}
		const page = 1000
		var lastRowid int64
		for {
			var rows []struct {
				Rowid   int64   `gorm:"column:rowid"`
				EventID string  `gorm:"column:event_id"`
				Attrs   *string `gorm:"column:attrs"`
			}
			err := tx.Raw(
				`SELECT rowid, event_id, attrs FROM events WHERE rowid > ? ORDER BY rowid LIMIT ?`,
				lastRowid, page,
			).Scan(&rows).Error
			if err != nil {
				return fmt.Errorf("扫描 events: %w", err)
			}
			if len(rows) == 0 {
				break
			}
			batch := make([]eventRow, 0, len(rows))
			for _, r := range rows {
				batch = append(batch, eventRow{EventID: r.EventID, Attrs: r.Attrs})
				lastRowid = r.Rowid
			}
			if err := s.indexResources(tx, batch, nil); err != nil {
				return err
			}
		}
		return tx.Exec(
			`INSERT INTO meta("key", value, updated_at) VALUES(?, ?, ?)
			 ON CONFLICT("key") DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			"normalize_rule_version", strconv.Itoa(NormalizeRuleVersion), time.Now().UTC().Format(time.RFC3339),
		).Error
	}); err != nil {
		return err
	}
	return nil
}
