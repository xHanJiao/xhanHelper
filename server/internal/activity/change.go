package activity

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// —— 系统变更事件（doc.* / memory.* / bill.* / reminder.*）的归组标记 ——
//
// 仓储层写库时发变更事件，「谁做的 / 挂谁下面」由 ctx 上的标记决定：
//   - 工具路径：Registry.ExecuteByName 注入 WithToolCall(tool.call.started 的 event_id)
//     → actor=assistant、trigger=manual、父=该 tool.call.started
//   - 用户链路（REST）：认证中间件注入 WithUserActor → actor=user、trigger=manual、根
//   - 两者都没有（后台 worker / 脚本）：actor=assistant、trigger=batch、根

type toolCallCtxKey struct{}
type userActorCtxKey struct{}
type noEventCtxKey struct{}

// WithNoEvent 让本次写库不触发系统变更回调（用于自行埋点的例外，如 BatchDelete）。
func WithNoEvent(ctx context.Context) context.Context {
	return context.WithValue(ctx, noEventCtxKey{}, true)
}

// NoEventFromContext 判断本次写是否被显式要求不采集。
func NoEventFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(noEventCtxKey{}).(bool)
	return v
}

// WithToolCall 注入当前工具调用事件 id（= tool.call.started 的 event_id）。
func WithToolCall(ctx context.Context, startEventID string) context.Context {
	return context.WithValue(ctx, toolCallCtxKey{}, startEventID)
}

// ToolCallFromContext 取当前工具调用事件 id；空/无表示非工具触发。
func ToolCallFromContext(ctx context.Context) (string, bool) {
	id, _ := ctx.Value(toolCallCtxKey{}).(string)
	return id, id != ""
}

// WithUserActor 标记「这是用户经 REST 亲手做的」（工具标记优先于它）。
func WithUserActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, userActorCtxKey{}, true)
}

// EmitChange 发系统变更事件：调用方只给语义内容，actor/trigger/parent/conv/turn 从 ctx 推导。
func (s *Store) EmitChange(ctx context.Context, typ, bizKey string, attrs map[string]any) {
	if s == nil {
		return
	}
	turn, hasTurn := TurnFromContext(ctx)
	_, isUser := ctx.Value(userActorCtxKey{}).(bool)
	actor, trigger, parent := ActorAssistant, TriggerBatch, ""
	if hasTurn || isUser {
		trigger = TriggerManual
	}
	if toolID, ok := ToolCallFromContext(ctx); ok {
		parent = toolID
	} else if isUser {
		actor = ActorUser
	}
	s.Emit(Event{
		Type:           typ,
		EventID:        uuid.NewString(),
		OccurredAt:     time.Now(),
		Actor:          actor,
		Trigger:        trigger,
		ConversationID: turn.ConvID,
		TurnID:         turn.TurnID,
		BizKey:         bizKey,
		ParentEventID:  parent,
		Attrs:          attrs,
	})
}
