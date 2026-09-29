package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/activity"
)

// tool.call.* 埋点（附表 A）：服务端工具调度器统一收口，覆盖主循环/子代理/反思注册表。
// 业务键 = 调用 id（LLM 提供；旧文本路径没有则 NULL）；父 = 本次 turn 的 chat.turn.started；
// 无 turn（后台/反思）算根、trigger=batch。

// toolCallMeta 从执行上下文取归组键：turn_id、父事件、trigger。
func toolCallMeta(ec *ExecuteContext) (turnID, parentID, trigger string) {
	turn, ok := activity.TurnFromContext(ec.Ctx)
	turnID = ec.TurnID
	if turnID == "" {
		turnID = turn.TurnID
	}
	trigger = activity.TriggerBatch
	if ok || turnID != "" {
		trigger = activity.TriggerManual
	}
	return turnID, turn.StartEventID, trigger
}

// emitToolCallStarted 发 tool.call.started，返回预先分配的 (startID, endID) 供 pair_id 互指。
func (r *Registry) emitToolCallStarted(ec *ExecuteContext, toolName, funcName string, args json.RawMessage) (startID, endID string) {
	startID, endID = uuid.NewString(), uuid.NewString()
	turnID, parentID, trigger := toolCallMeta(ec)
	r.activity.Emit(activity.Event{
		Type:           activity.TypeToolCallStarted,
		EventID:        startID,
		OccurredAt:     time.Now(),
		Actor:          activity.ActorAssistant,
		Trigger:        trigger,
		ConversationID: ec.ConvID,
		TurnID:         turnID,
		CallID:         ec.ToolCallID,
		BizKey:         ec.ToolCallID,
		ParentEventID:  parentID,
		PairID:         endID,
		Attrs: map[string]any{
			"tool_name":   toolName + "__" + funcName,
			"args_digest": argsDigest(args),
			"args_len":    len(args),
		},
	})
	return startID, endID
}

// emitToolCallEnded 发 tool.call.ended；res 为 nil 表示执行 panic（标 failed）。
func (r *Registry) emitToolCallEnded(ec *ExecuteContext, toolName, funcName, startID, endID string, res *Result) {
	turnID, parentID, trigger := toolCallMeta(ec)
	outcome, errType, resultLen := activity.OutcomeCompleted, "", 0
	switch {
	case res == nil:
		outcome, errType = activity.OutcomeFailed, "panic"
	case !res.Success:
		outcome, errType = activity.OutcomeFailed, "tool_error"
		resultLen = len(res.Error)
	default:
		resultLen = len(res.Data)
	}
	evt := activity.Event{
		Type:           activity.TypeToolCallEnded,
		EventID:        endID,
		OccurredAt:     time.Now(),
		Actor:          activity.ActorAssistant,
		Trigger:        trigger,
		Outcome:        outcome,
		ErrorType:      errType,
		ConversationID: ec.ConvID,
		TurnID:         turnID,
		CallID:         ec.ToolCallID,
		BizKey:         ec.ToolCallID,
		ParentEventID:  parentID,
		PairID:         startID,
		Attrs: map[string]any{
			"tool_name":  toolName + "__" + funcName,
			"result_len": resultLen,
		},
	}
	r.activity.Emit(evt)
}

func argsDigest(args json.RawMessage) string {
	sum := sha256.Sum256(args)
	return hex.EncodeToString(sum[:])[:16]
}
