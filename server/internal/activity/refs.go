package activity

import (
	"fmt"
	"log"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RefRuleVersion 是归属推断规则的版本号；规则一变就 +1（并入 recomputeStamp）。
const RefRuleVersion = 1

// ref 来源常量（附表 B refs.source）。
//
// 本包**只产 `resource` 来源**的 ref（推断归属）：
//   - time_window / semantic：不启用（2026-09-28 定：真实使用是并发多线程，时间邻近不构成因果）。
//   - explicit：**不落 event_refs**（2026-09-28 定）。写入即知的直接关系（助手发起 → parent_event_id、
//     工具调用 → call_id、所属对话 → conversation_id）已由 events 表的列承载，因果与归属分属两套语义；
//     再往 refs 抄一份是双份事实（附表 B「两套语义不混一个字段」）。
//     保留 RefSourceExplicit 仅为重算防护（见 RebuildRefs：DELETE 只删 source<>'explicit'）。
const (
	RefSourceExplicit   = "explicit"
	RefSourceResource   = "resource"
	RefTypeConversation = "conversation"
	RefTypeMemory       = "memory"
	RefTypeReminder     = "reminder"
)

// resourceKindToRefType：资源 kind → 可直接映射的 ref_type（附表 B：ref_type 只有五类，
// conversation/memory/plan/reminder/subtask）。bill 不是 ref_type（跳过）；plan/subtask 无对应资源 kind。
var resourceKindToRefType = map[string]string{
	ResourceKindMemory:   RefTypeMemory,
	ResourceKindReminder: RefTypeReminder,
}

// resourceSpecificity 是资源具体度 → confidence 的映射（越具体越可信）。
// url 具体页 / file 具体文件最具体；repo、doc 次之；memory/bill/reminder 是系统内对象。
// 未列出的 kind 给 0.5（保守）。
var resourceSpecificity = map[string]float64{
	ResourceKindURL:      0.9,
	ResourceKindFile:     0.9,
	ResourceKindRepo:     0.7,
	ResourceKindDoc:      0.7,
	ResourceKindMemory:   0.6,
	ResourceKindBill:     0.6,
	ResourceKindReminder: 0.6,
}

type eventRefRow struct {
	EventID    string  `gorm:"column:event_id;primaryKey"`
	RefType    string  `gorm:"column:ref_type;primaryKey"`
	RefID      string  `gorm:"column:ref_id;primaryKey"`
	Source     string  `gorm:"column:source;primaryKey"`
	Confidence float64 `gorm:"column:confidence"`
}

func (eventRefRow) TableName() string { return "event_refs" }

// RebuildRefs 全量重跑归属推断（本包只产 source=resource）。
//
// 口径：给「没有 conversation_id 的事件」挂 ref(conversation, source=resource)，
// 当且仅当它与某条「有 conversation_id 的事件」共享同一归一资源（event_resources.resource_id）。
// 即：这条外部活动碰的资源，也在某场对话里出现过 → 认为它与那场对话相关。
// 另把 memory/reminder 资源直挂成对应 ref（见 deriveDirectRefs）。
//
// 只清 source<>'explicit'：本包不产 explicit 行（直接关系由 events 列承载，见顶部注释），
// 保留该条件是对附表 B「混合表」契约的防御——将来若补 explicit 生产者，天然不被重算删掉。
// 主键 (event_id,ref_type,ref_id,source) 天然去重：同 conversation 多资源只留一条（取最高 confidence）。
//
// ⚠️ 依赖 event_resources，**须在 RebuildResources 之后调用**（Recompute 已按此顺序）；
// 单独调用会基于过期/空的 event_resources 清空 refs。
func (s *Store) RebuildRefs() error {
	if s == nil {
		return fmt.Errorf("events.db 未启用")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM event_refs WHERE source <> ?`, RefSourceExplicit).Error; err != nil {
			return fmt.Errorf("清空派生 refs: %w", err)
		}
		rows, err := deriveResourceRefs(tx)
		if err != nil {
			return err
		}
		direct, err := deriveDirectRefs(tx)
		if err != nil {
			return err
		}
		rows = append(rows, direct...)
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, 500).Error; err != nil {
				return fmt.Errorf("insert %d event_refs: %w", len(rows), err)
			}
		}
		return nil
	})
}

// maxRefRows 是推导 resource refs 时读入的行数上界。自连接的中间结果是
// 「有会话的事件数 × 无会话的事件数」的和，热门资源（被反复碰的 repo_path、批量刷的 memory_id）
// 会平方级膨胀；这里软性截断并记日志降级（refs 是纯派生、可重算，宁可少挂不可拖垮夜间 cron）。
const maxRefRows = 200000

// deriveResourceRefs 用一条自连接算出「共享资源 → 挂 conversation ref」的行。
// a 侧 = 有 conversation_id 的锚事件；b 侧 = 无 conversation_id、共享同一资源的待挂事件。
// 同一 (b.event_id, a.conversation_id) 多资源命中时，按具体度取最大 confidence（写前内存归并）。
// 读入行数受 maxRefRows 软限，触顶时截断并记日志。
func deriveResourceRefs(tx *gorm.DB) ([]eventRefRow, error) {
	var raw []struct {
		EventID string `gorm:"column:event_id"`
		RefID   string `gorm:"column:ref_id"`
		Kind    string `gorm:"column:resource_kind"`
	}
	err := tx.Raw(`
		SELECT DISTINCT b.event_id, a.conversation_id AS ref_id, er.resource_kind
		FROM event_resources er
		JOIN events a ON a.event_id = er.event_id
			AND a.conversation_id IS NOT NULL AND a.conversation_id <> ''
		JOIN event_resources er2 ON er2.resource_kind = er.resource_kind
			AND er2.resource_id = er.resource_id
		JOIN events b ON b.event_id = er2.event_id
			AND (b.conversation_id IS NULL OR b.conversation_id = '')
		WHERE b.event_id <> a.event_id
		LIMIT ?`, maxRefRows+1).Scan(&raw).Error
	if err != nil {
		return nil, fmt.Errorf("推导 resource refs: %w", err)
	}
	if len(raw) > maxRefRows {
		log.Printf("[activity] 归属推导：共享资源中间结果超过 %d 行，截断（refs 为派生数据，可重算）", maxRefRows)
		raw = raw[:maxRefRows]
	}
	// 内存归并：key = event_id + ref_id，取最高具体度。
	type key struct{ eventID, refID string }
	best := make(map[key]float64)
	for _, r := range raw {
		c, ok := resourceSpecificity[r.Kind]
		if !ok {
			c = 0.5
		}
		k := key{r.EventID, r.RefID}
		if cur, seen := best[k]; !seen || c > cur {
			best[k] = c
		}
	}
	out := make([]eventRefRow, 0, len(best))
	for k, c := range best {
		out = append(out, eventRefRow{
			EventID: k.eventID, RefType: RefTypeConversation, RefID: k.refID,
			Source: RefSourceResource, Confidence: c,
		})
	}
	return out, nil
}

// deriveDirectRefs 把事件直接碰到的系统对象资源映射成 ref：memory:M → ref(memory, M)、
// reminder:R → ref(reminder, R)。覆盖范围由 resourceKindToRefType 单一决定（SQL 只拉全部边，
// kind 过滤在 Go 侧走 map，避免两处事实）。bill 不是 ref_type，不在 map 里，自然跳过。
// 这让 facts_by_memory（沿 ref_type='memory' 取）在 P21 后自动生效。
func deriveDirectRefs(tx *gorm.DB) ([]eventRefRow, error) {
	var raw []struct {
		EventID string `gorm:"column:event_id"`
		RefID   string `gorm:"column:ref_id"`
		Kind    string `gorm:"column:resource_kind"`
	}
	// 与 deriveResourceRefs 同口径的读入上界（event_resources 90 天可到几十万行，防一次读爆内存）。
	err := tx.Raw(`SELECT event_id, resource_id AS ref_id, resource_kind FROM event_resources
		LIMIT ?`, maxRefRows+1).Scan(&raw).Error
	if err != nil {
		return nil, fmt.Errorf("推导 direct refs: %w", err)
	}
	if len(raw) > maxRefRows {
		log.Printf("[activity] 直连 ref 推导：event_resources 超过 %d 行，截断（refs 为派生数据，可重算）", maxRefRows)
		raw = raw[:maxRefRows]
	}
	out := make([]eventRefRow, 0, len(raw))
	for _, r := range raw {
		rt, ok := resourceKindToRefType[r.Kind]
		if !ok {
			continue
		}
		c, ok := resourceSpecificity[r.Kind]
		if !ok {
			c = 0.5
		}
		out = append(out, eventRefRow{
			EventID: r.EventID, RefType: rt, RefID: r.RefID,
			Source: RefSourceResource, Confidence: c,
		})
	}
	return out, nil
}
