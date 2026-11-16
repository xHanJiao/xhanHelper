package activity

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
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
	TypeChatMessageSent = "chat.message.sent"
	TypeChatTurnStarted = "chat.turn.started"
	TypeChatTurnEnded   = "chat.turn.ended"
	TypeLLMCallStarted  = "llm.call.started"
	TypeLLMCallEnded    = "llm.call.ended"
	TypeLLMFirstToken   = "llm.first_token"
	TypeToolCallStarted = "tool.call.started"
	TypeToolCallEnded   = "tool.call.ended"

	TypeToolBridgeDispatched = "tool.bridge.dispatched"
	TypeToolBridgeReturned   = "tool.bridge.returned"

	TypeTaskSpawned = "task.spawned"
	TypeTaskEnded   = "task.ended"

	// 客户端区间事件（服务端只做归一/闭合，不发，但 type 要认）。
	TypeClientToolExecStarted = "client.tool.exec.started"
	TypeClientToolExecEnded   = "client.tool.exec.ended"
	TypeBrowserNavStarted     = "browser.nav.started"
	TypeBrowserNavEnded       = "browser.nav.ended"
	TypeClientInputActive     = "client.input.active"
	TypeClientInputEnded      = "client.input.ended"

	TypeSystemBoundary = "system.boundary"

	TypeDocCreated          = "doc.created"
	TypeDocUpdated          = "doc.updated"
	TypeDocDeleted          = "doc.deleted"
	TypeMemoryEntityChanged = "memory.entity.changed"
	TypeMemoryTopicCleaned  = "memory.topic.cleaned"

	TypeBillCreated          = "bill.created"
	TypeReminderCreated      = "reminder.created"
	TypeReminderAcknowledged = "reminder.acknowledged"

	// TypeReadQuery 是读取自记（明细级留痕，附表 A「读取自记」）：actor=assistant，默认不参与统计。
	TypeReadQuery = "read.query"
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

	// flushMu 串行化刷盘，pause 时借此等待在途批次结束；paused 时 loop 不刷（队列只在 Emit 侧堆）。
	flushMu sync.Mutex
	paused  atomic.Bool

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
		log.Printf("[activity] 丢弃事件：%v", err)
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

// pause 停 ingest：置标志并等当前在途批次结束，保证后续重建持有唯一的写连接。
func (e *Emitter) pause() {
	e.paused.Store(true)
	e.flushMu.Lock()
	e.flushMu.Unlock()
}

// resume 恢复 ingest，并把重建期间积压的队列刷掉。
func (e *Emitter) resume() {
	e.paused.Store(false)
	e.flush()
}

func (e *Emitter) flush() {
	// 串行化：pause 会拿走 flushMu 以等待在途批次结束。
	e.flushMu.Lock()
	defer e.flushMu.Unlock()
	if e.paused.Load() {
		// 重建期间不刷盘（避免与写事务撞 busy_timeout）；队列只在 Emit 侧继续堆，resume 后统一刷。
		return
	}
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
	if !validEventType(ev.Type) {
		return eventRow{}, fmt.Errorf("type %q 违反「域.对象.动作」契约（小写、以点分隔）", ev.Type)
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
		OccurredAt:          occurred.UTC().Format(isoMillis),
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

// validEventType 校验事件类型符合附表 A 的契约：^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$。
// 每段**首字符必须是小写字母**（不许数字/下划线），其余可为 [a-z0-9_]；段数不固定。
// 例：bill.created（2 段）与 client.tool.exec.started（4 段）都合法。
// 不带点会让按 type 前缀取 source 的读取端漏判，首字符放宽还会与 validSource（^[a-z_]+$）冲突，
// 故在写入端严格拒绝（对齐文档声明的正则）。
func validEventType(t string) bool {
	if t == "" {
		return false
	}
	dots := 0
	segStart := true
	for _, c := range t {
		switch {
		case c == '.':
			if segStart {
				return false // 连续点或首字符是点
			}
			segStart = true
			dots++
		case c >= 'a' && c <= 'z':
			segStart = false
		case (c >= '0' && c <= '9') || c == '_':
			// 数字/下划线只在段内合法，不能做段首。
			if segStart {
				return false
			}
		default:
			return false
		}
	}
	return dots > 0 && !segStart
}

// insertEvents 一批一个事务；event_id 与 (type, biz_key) 冲突一律忽略（幂等，设计篇 3.3）。
// 资源归一落主数据/边（归一遍写在写入时做，设计篇 6.1）——但**不在同一事务里**：边是纯派生、
// RebuildResources 能补，索引失败不该把已落的事件一起回滚（事实层只增，落进去就不能收回）。
func (s *Store) insertEvents(rows []eventRow) error {
	if len(rows) == 0 {
		return nil
	}
	// 写闸独占：SQLite 同库单写者，与 IngestBatch 串行、被 Recompute 挡在外面（重建期间不写）。
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	preExisting := s.preExistingEventIDs(rows)
	// 分块插入：eventRow ~28 列，SQLite 变量上限 32766 → 单条 INSERT 最多约 1170 行；
	// 一批可能上万条（大文件回放/积压重投），必须分块（gorm 默认 CreateBatchSize==0 不分块）。
	if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, 500).Error; err != nil {
		return fmt.Errorf("insert %d events: %w", len(rows), err)
	}
	// 索引走独立事务；失败只打日志（边可重建），不影响事件已落库的事实。
	s.indexResourcesBestEffort(rows, preExisting)
	return nil
}

// preExistingEventIDs 返回这批里「插入前已在库」的 event_id 集合（纯重放，不该重新索引资源）。
func (s *Store) preExistingEventIDs(rows []eventRow) map[string]struct{} {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.EventID)
	}
	set := make(map[string]struct{}, len(rows))
	// 分块查询：单条 `IN (...)` 也受 SQLite 变量上限约束（一根 id 一个变量）。
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		var found []string
		if err := s.db.Raw(`SELECT event_id FROM events WHERE event_id IN ?`, ids[start:end]).Scan(&found).Error; err != nil {
			// 查不到就当作没有重复（宁可多索引一次，也不丢边的写入；重放污染是次要风险）。
			log.Printf("[activity] 查询已存在事件失败，按全部为新插处理: %v", err)
			return nil
		}
		for _, id := range found {
			set[id] = struct{}{}
		}
	}
	return set
}

// indexResourcesBestEffort 用独立事务索引资源；失败降级为日志（边是派生，RebuildResources 可补）。
func (s *Store) indexResourcesBestEffort(rows []eventRow, preExisting map[string]struct{}) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.indexResources(tx, rows, preExisting)
	})
	if err != nil {
		log.Printf("[activity] 资源索引失败（事件已落库，边可经 RebuildResources 补）: %v", err)
	}
}
