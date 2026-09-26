package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

// actor / trigger 取值（附表 A 公共字段）。
const (
	ActorUser      = "user"
	ActorAssistant = "assistant"
	ActorSystem    = "system"

	TriggerManual   = "manual"
	TriggerBatch    = "batch"
	TriggerSchedule = "schedule"
)

// 事件 type（附表 A 事件字典契约；用到才登记一行）。
const (
	TypeChatTurnStarted = "chat.turn.started"
	TypeChatTurnEnded   = "chat.turn.ended"
)

// outcome 取值（附表 A 公共字段）。
const (
	OutcomeCompleted = "completed"
	OutcomeCancelled = "cancelled"
	OutcomeFailed    = "failed"
)

// 批量与背压参数：1 秒或 100 条先到先刷，一批一个事务（设计篇 5.2/5.6）。
const (
	batchSize     = 100
	flushInterval = time.Second
	defaultQueue  = 1024
)

const isoMillis = "2006-01-02T15:04:05.000Z07:00"

// Event 是调用方投递的原始事件：只填语义字段。
// 公共字段（event_id / received_at / occurred_at / schema_version / actor / trigger 默认值）
// 由 emit 入口补，调用方给出的非空值不会被覆盖。
type Event struct {
	Type       string
	Actor      string
	Trigger    string
	OccurredAt time.Time
	EventID    string // 留空则由入口生成；客户端事件自带

	Outcome       string
	ErrorType     string
	ErrorMsg      string
	ParentEventID string
	PairID        string
	BizKey        string // 一支全局唯一 id；没有就留空（存 NULL）

	ConversationID      string
	TurnID              string
	CallID              string
	LaunchID            string
	FocusEpochID        string
	ComponentInstanceID string
	WakeEpochID         string
	ClientInstanceID    string
	ClientBuild         string
	OSVersion           string

	Attrs map[string]any
}

// eventRow 是 events 表的一行。可空列用指针，保证空值落 NULL——
// 尤其 biz_key：空串会撞上 (type, biz_key) 的部分唯一索引，把没有业务键的事件互相吞掉。
type eventRow struct {
	EventID    string `gorm:"column:event_id;primaryKey"`
	Type       string `gorm:"column:type"`
	OccurredAt string `gorm:"column:occurred_at"`
	ReceivedAt string `gorm:"column:received_at"`
	Actor      string `gorm:"column:actor"`
	Trigger    string `gorm:"column:trigger"`

	Outcome             *string `gorm:"column:outcome"`
	ErrorType           *string `gorm:"column:error_type"`
	ErrorMsg            *string `gorm:"column:error_msg"`
	ParentEventID       *string `gorm:"column:parent_event_id"`
	PairID              *string `gorm:"column:pair_id"`
	LaunchID            *string `gorm:"column:launch_id"`
	FocusEpochID        *string `gorm:"column:focus_epoch_id"`
	ComponentInstanceID *string `gorm:"column:component_instance_id"`
	WakeEpochID         *string `gorm:"column:wake_epoch_id"`
	ConversationID      *string `gorm:"column:conversation_id"`
	TurnID              *string `gorm:"column:turn_id"`
	CallID              *string `gorm:"column:call_id"`
	ClientInstanceID    *string `gorm:"column:client_instance_id"`
	ClientBuild         *string `gorm:"column:client_build"`
	OSVersion           *string `gorm:"column:os_version"`
	Attrs               *string `gorm:"column:attrs"`
	BizKey              *string `gorm:"column:biz_key"`
	SchemaVersion       int     `gorm:"column:schema_version"`
}

func (eventRow) TableName() string { return "events" }

// Emitter 是服务端唯一 emit 入口：内存有界队列 + 后台批量入库，fire-and-forget，绝不阻塞业务。
type Emitter struct {
	store    *Store
	maxQueue int

	mu      sync.Mutex
	queue   []eventRow
	dropped int64

	wake chan struct{}
	done chan struct{}
	wg   sync.WaitGroup
}

func startEmitter(s *Store) *Emitter {
	e := &Emitter{
		store:    s,
		maxQueue: defaultQueue,
		queue:    make([]eventRow, 0, batchSize),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	e.wg.Add(1)
	go e.loop()
	return e
}

func (e *Emitter) loop() {
	defer e.wg.Done()
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-e.done:
			return
		case <-t.C:
			e.flush()
		case <-e.wake:
			e.flush()
		}
	}
}

// Emit 投递一条事件，立即返回。队列满则丢最旧并计数（设计篇 5.3）。
func (e *Emitter) Emit(ev Event) {
	row, err := e.build(ev)
	if err != nil {
		log.Printf("[activity] 丢弃事件 %q：%v", ev.Type, err)
		return
	}
	e.mu.Lock()
	if len(e.queue) >= e.maxQueue {
		e.queue = e.queue[1:]
		e.dropped++
	}
	e.queue = append(e.queue, row)
	n := len(e.queue)
	e.mu.Unlock()

	if n >= batchSize {
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
}

// Flush 立即把队列刷盘；关键里程碑（turn 结束、工具结束、写入完成）可调。
func (e *Emitter) Flush() { e.flush() }

// Dropped 返回累计因队列满被丢弃的条数。
func (e *Emitter) Dropped() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dropped
}

func (e *Emitter) flush() {
	e.mu.Lock()
	if len(e.queue) == 0 {
		e.mu.Unlock()
		return
	}
	batch := e.queue
	e.queue = make([]eventRow, 0, batchSize)
	dropped := e.dropped
	e.dropped = 0
	e.mu.Unlock()

	if err := e.store.insertEvents(batch); err != nil {
		// 服务端按 fire-and-forget，不做落盘重投；失败记录后丢弃这批，不影响业务。
		log.Printf("[activity] 写入 %d 条事件失败（本批丢弃，累计队列满丢 %d）：%v", len(batch), dropped, err)
		return
	}
	if dropped > 0 {
		log.Printf("[activity] 写入 %d 条事件，队列满丢弃最旧 %d 条", len(batch), dropped)
	}
}

func (e *Emitter) close() {
	close(e.done)
	e.wg.Wait()
	e.flush()
}

// build 补公共字段并转成表行。
func (e *Emitter) build(ev Event) (eventRow, error) {
	if strings.TrimSpace(ev.Type) == "" {
		return eventRow{}, errors.New("type 为空")
	}
	id := ev.EventID
	if id == "" {
		id = uuid.NewString()
	}
	occurred := ev.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now()
	}
	actor := ev.Actor
	if actor == "" {
		actor = ActorSystem
	}
	trigger := ev.Trigger
	if trigger == "" {
		trigger = TriggerManual
	}

	row := eventRow{
		EventID:             id,
		Type:                ev.Type,
		OccurredAt:          occurred.Format(isoMillis),
		ReceivedAt:          time.Now().UTC().Format(isoMillis),
		Actor:               actor,
		Trigger:             trigger,
		Outcome:             optStr(ev.Outcome),
		ErrorType:           optStr(ev.ErrorType),
		ErrorMsg:            optStr(ev.ErrorMsg),
		ParentEventID:       optStr(ev.ParentEventID),
		PairID:              optStr(ev.PairID),
		LaunchID:            optStr(ev.LaunchID),
		FocusEpochID:        optStr(ev.FocusEpochID),
		ComponentInstanceID: optStr(ev.ComponentInstanceID),
		WakeEpochID:         optStr(ev.WakeEpochID),
		ConversationID:      optStr(ev.ConversationID),
		TurnID:              optStr(ev.TurnID),
		CallID:              optStr(ev.CallID),
		ClientInstanceID:    optStr(ev.ClientInstanceID),
		ClientBuild:         optStr(ev.ClientBuild),
		OSVersion:           optStr(ev.OSVersion),
		BizKey:              optStr(ev.BizKey),
		SchemaVersion:       schemaVersion,
	}
	if len(ev.Attrs) > 0 {
		b, err := json.Marshal(ev.Attrs)
		if err != nil {
			return eventRow{}, fmt.Errorf("attrs 序列化: %w", err)
		}
		row.Attrs = optStr(string(b))
	}
	return row, nil
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// insertEvents 一批一个事务；event_id 与 (type, biz_key) 冲突一律忽略（幂等，设计篇 3.3）。
func (s *Store) insertEvents(rows []eventRow) error {
	if len(rows) == 0 {
		return nil
	}
	err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	if err != nil {
		return fmt.Errorf("insert %d events: %w", len(rows), err)
	}
	return nil
}
