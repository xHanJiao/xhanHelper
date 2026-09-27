package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

// ErrInvalidBatch — 上报体本身不合法（缺 event_id/type、空批次、segment 模式缺 segment_id），调用方应回 400 而非 500。
var ErrInvalidBatch = errors.New("invalid batch event")

// MaxBatchEvents — 单段上报上限（实施技术方案 5.5：每段最多 500 条或 1 MiB）。
const MaxBatchEvents = 500

// 水位口径（activity.watermark_mode；客户端按响应 watermark_kind 自适应）。
// 值须与 config.validate 的字面量（time/segment）同步。
const (
	WatermarkTime    = "time"    // watermark = 本批最大 occurred_at，客户端删「≤ 水位的本地段」
	WatermarkSegment = "segment" // watermark = 回显 segment_id，客户端逐段删除（不依赖客户端时钟）
)

// IngestResult — 整段接收结果（POST /api/events/batch 响应体）。
type IngestResult struct {
	Accepted      int    `json:"accepted"`
	Duplicate     int    `json:"duplicate"`
	Watermark     string `json:"watermark"`
	WatermarkKind string `json:"watermark_kind"`
}

// BatchEvent 是客户端上报的单个事件（POST /api/events/batch）。
// 字段与 events 表对齐，occurred_at 为客户端生成的 ISO 串；服务端补 received_at/schema_version 后落库。
// schema_version 不接受客户端覆盖（表级版本以服务端为准）。
type BatchEvent struct {
	EventID             string         `json:"event_id"`
	Type                string         `json:"type"`
	OccurredAt          string         `json:"occurred_at"`
	Actor               string         `json:"actor"`
	Trigger             string         `json:"trigger"`
	Outcome             string         `json:"outcome"`
	ErrorType           string         `json:"error_type"`
	ErrorMsg            string         `json:"error_msg"`
	ParentEventID       string         `json:"parent_event_id"`
	PairID              string         `json:"pair_id"`
	LaunchID            string         `json:"launch_id"`
	FocusEpochID        string         `json:"focus_epoch_id"`
	ComponentInstanceID string         `json:"component_instance_id"`
	WakeEpochID         string         `json:"wake_epoch_id"`
	ConversationID      string         `json:"conversation_id"`
	TurnID              string         `json:"turn_id"`
	CallID              string         `json:"call_id"`
	ClientInstanceID    string         `json:"client_instance_id"`
	ClientBuild         string         `json:"client_build"`
	OSVersion           string         `json:"os_version"`
	BizKey              string         `json:"biz_key"`
	Attrs               map[string]any `json:"attrs"`
}

// IngestBatch 整段接收客户端事件：整段一个事务、INSERT OR IGNORE（event_id 和 (type,biz_key) 幂等），
// 按实际插入行数统计 accepted/duplicate，返回水位（口径由 watermarkMode 决定，见 WatermarkTime/Segment）。
// 走同步写而非 emit 队列——上传要当场给客户端 ack。
func (s *Store) IngestBatch(segmentID string, evs []BatchEvent) (IngestResult, error) {
	if s == nil {
		return IngestResult{}, fmt.Errorf("events.db 未启用")
	}
	mode := s.WatermarkMode()
	if mode == WatermarkSegment && segmentID == "" {
		return IngestResult{}, fmt.Errorf("%w: segment 模式要求 segment_id", ErrInvalidBatch)
	}
	if len(evs) == 0 {
		return IngestResult{}, fmt.Errorf("%w: 空批次", ErrInvalidBatch)
	}
	now := time.Now().UTC().Format(isoMillis)
	rows := make([]eventRow, 0, len(evs))
	var maxOccurred time.Time
	for i, e := range evs {
		if e.EventID == "" || e.Type == "" {
			return IngestResult{}, fmt.Errorf("%w: 第 %d 条事件缺少 event_id 或 type", ErrInvalidBatch, i)
		}
		occurred := e.OccurredAt
		if occurred == "" {
			occurred = now
		} else if t, perr := time.Parse(isoMillis, e.OccurredAt); perr == nil && t.After(maxOccurred) {
			maxOccurred = t
		}
		actor, trigger := e.Actor, e.Trigger
		if actor == "" {
			actor = ActorSystem
		}
		if trigger == "" {
			trigger = TriggerManual
		}
		row := eventRow{
			EventID:             e.EventID,
			Type:                e.Type,
			OccurredAt:          occurred,
			ReceivedAt:          now,
			Actor:               actor,
			Trigger:             trigger,
			Outcome:             optStr(e.Outcome),
			ErrorType:           optStr(e.ErrorType),
			ErrorMsg:            optStr(e.ErrorMsg),
			ParentEventID:       optStr(e.ParentEventID),
			PairID:              optStr(e.PairID),
			LaunchID:            optStr(e.LaunchID),
			FocusEpochID:        optStr(e.FocusEpochID),
			ComponentInstanceID: optStr(e.ComponentInstanceID),
			WakeEpochID:         optStr(e.WakeEpochID),
			ConversationID:      optStr(e.ConversationID),
			TurnID:              optStr(e.TurnID),
			CallID:              optStr(e.CallID),
			ClientInstanceID:    optStr(e.ClientInstanceID),
			ClientBuild:         optStr(e.ClientBuild),
			OSVersion:           optStr(e.OSVersion),
			BizKey:              optStr(e.BizKey),
			SchemaVersion:       schemaVersion,
		}
		if len(e.Attrs) > 0 {
			b, merr := json.Marshal(e.Attrs)
			if merr != nil {
				return IngestResult{}, fmt.Errorf("第 %d 条事件 attrs 序列化: %w", i, merr)
			}
			row.Attrs = optStr(string(b))
		}
		rows = append(rows, row)
	}

	// 事件落库一个事务（客户端要当场 ack）；资源归一在**独立事务**里做——边是纯派生、可重建，
	// 索引失败不该让已接收的事件回滚、也不该把上传打成 500（客户端会重投，但事件其实已到）。
	preExisting := s.preExistingEventIDs(rows)
	// 分块插入：eventRow ~28 列，SQLite 变量上限 32766 → 单条 INSERT 最多约 1170 行；
	// 客户端一批可能上万条，必须分块（gorm 默认 CreateBatchSize==0 不分块）。
	res := s.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, 500)
	if res.Error != nil {
		return IngestResult{}, fmt.Errorf("insert %d client events: %w", len(rows), res.Error)
	}
	s.indexResourcesBestEffort(rows, preExisting)
	inserted := res.RowsAffected
	out := IngestResult{
		Accepted:      int(inserted),
		Duplicate:     len(rows) - int(inserted),
		WatermarkKind: mode,
	}
	switch mode {
	case WatermarkSegment:
		out.Watermark = segmentID
	default:
		out.Watermark = now
		if !maxOccurred.IsZero() {
			out.Watermark = maxOccurred.UTC().Format(isoMillis)
		}
	}
	return out, nil
}
