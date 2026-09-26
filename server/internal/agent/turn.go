package agent

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/activity"
)

// Turn — 一次助手回复周期（一次 /api/chat 请求）的埋点句柄（设计篇 4.8）。
// 采集关闭时 ID 为空，所有补偿动作退化为 no-op，客户端工具 payload 的 turn_id 也为空。
type Turn struct {
	ID string

	conv     string
	trigger  string
	store    *activity.Store
	parentID string
	startID  string
	endID    string // 预先分配：区间事件靠 pair_id 互指，需要两端 event_id 都已知

	once sync.Once
}

// StartTurn 生成 turn_id 并发 chat.turn.started；结束时调用方调 Finish(outcome)。
// 生成点固定在 agent 层：/api/chat 只是调用者之一，将来主动搭话的调度器走同一入口。
// 父事件（入口那条 chat.message.sent）由调用方传入，无则为根。
func (o *Orchestrator) StartTurn(ctx context.Context, convID, trigger, parentEventID string) *Turn {
	t := &Turn{conv: convID, trigger: trigger, store: o.activity, parentID: parentEventID}
	if t.store == nil {
		return t
	}
	t.ID = uuid.NewString()
	t.startID = uuid.NewString()
	t.endID = uuid.NewString()
	t.store.Emit(activity.Event{
		Type:           activity.TypeChatTurnStarted,
		EventID:        t.startID,
		OccurredAt:     time.Now(),
		Actor:          activity.ActorAssistant,
		Trigger:        trigger,
		ConversationID: convID,
		TurnID:         t.ID,
		BizKey:         t.ID,
		ParentEventID:  parentEventID,
		PairID:         t.endID,
	})
	return t
}

// Finish 发 chat.turn.ended（幂等）；outcome 用 activity.Outcome*。
func (t *Turn) Finish(outcome string) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if t.store == nil {
			return
		}
		t.store.Emit(activity.Event{
			Type:           activity.TypeChatTurnEnded,
			EventID:        t.endID,
			OccurredAt:     time.Now(),
			Actor:          activity.ActorAssistant,
			Trigger:        t.trigger,
			Outcome:        outcome,
			ConversationID: t.conv,
			TurnID:         t.ID,
			BizKey:         t.ID,
			ParentEventID:  t.parentID,
			PairID:         t.startID,
		})
	})
}
