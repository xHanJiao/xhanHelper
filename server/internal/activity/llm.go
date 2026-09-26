package activity

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
)

// —— turn 上下文（随 context 传播，供 llm.* 事件归组；后台任务无）——

type turnCtxKey struct{}

// TurnCtx — 当前 turn 的归组信息。
type TurnCtx struct {
	TurnID       string
	StartEventID string // chat.turn.started 的 event_id：llm.call.* 的父
	ConvID       string // 所属会话；供变更事件挂 conversation_id
}

// WithTurn 把 turn 上下文注入 ctx；编排层 StartTurn 后调用。
func WithTurn(ctx context.Context, t TurnCtx) context.Context {
	return context.WithValue(ctx, turnCtxKey{}, t)
}

// TurnFromContext 取当前 turn 上下文；ok=false 表示后台任务（根事件）。
func TurnFromContext(ctx context.Context) (TurnCtx, bool) {
	t, ok := ctx.Value(turnCtxKey{}).(TurnCtx)
	return t, ok
}

// llmEmitter — LLM 客户端装饰器：给每次调用发 llm.call.started/ended（流式再发 llm.first_token）。
// 包在客户端链最外层；kind 是 wrap 时定的档位（main_flash/main_strong/bg_flash/bg_strong）。
type llmEmitter struct {
	inner llm.LLMClient
	store *Store
	kind  string
}

// WrapLLMClient 用活动埋点装饰 LLM 客户端；inner 或 store 为 nil 时原样返回。
func WrapLLMClient(inner llm.LLMClient, store *Store, kind string) llm.LLMClient {
	if inner == nil || store == nil {
		return inner
	}
	return &llmEmitter{inner: inner, store: store, kind: kind}
}

func (e *llmEmitter) ModelName() string { return e.inner.ModelName() }

func (e *llmEmitter) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return e.inner.BuildSystemPrompt(base, tools)
}

// ChatWithRawJSON 透传（当前无生产调用；不产生埋点）。
func (e *llmEmitter) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return e.inner.ChatWithRawJSON(ctx, requestBody)
}

func (e *llmEmitter) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	callID := uuid.NewString()
	start := time.Now()
	e.callStarted(ctx, callID)

	innerCh, err := e.inner.Chat(ctx, req)
	if err != nil {
		e.callEnded(ctx, callID, nil, OutcomeFailed)
		return nil, err
	}

	out := make(chan *llm.StreamEvent, 256)
	go func() {
		defer close(out)
		var usage *llm.Usage
		outcome := OutcomeCompleted
		firstToken := false
		for evt := range innerCh {
			// 非阻塞透传（同 CostTracker：消费者断开时不阻塞本 goroutine，仍能补 ended）
			select {
			case out <- evt:
			default:
			}
			switch evt.Type {
			case "text_delta", "thought_delta":
				if !firstToken && evt.Content != "" {
					firstToken = true
					e.firstToken(ctx, callID, time.Since(start))
				}
			case "done":
				if evt.Usage != nil {
					usage = evt.Usage
				}
			case "error":
				outcome = OutcomeFailed
			}
		}
		if ctx.Err() != nil {
			outcome = OutcomeCancelled
		}
		e.callEnded(ctx, callID, usage, outcome)
	}()
	return out, nil
}

func (e *llmEmitter) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	callID := uuid.NewString()
	e.callStarted(ctx, callID)
	msg, err := e.inner.ChatSync(ctx, req)
	if err != nil {
		e.callEnded(ctx, callID, nil, OutcomeFailed)
		return msg, err
	}
	e.callEnded(ctx, callID, msg.Usage, OutcomeCompleted)
	return msg, nil
}

func (e *llmEmitter) callStarted(ctx context.Context, callID string) {
	turn, _ := TurnFromContext(ctx)
	e.store.Emit(Event{
		Type:          TypeLLMCallStarted,
		EventID:       callID,
		OccurredAt:    time.Now(),
		Actor:         ActorAssistant,
		Trigger:       e.trigger(),
		TurnID:        turn.TurnID,
		BizKey:        callID,
		ParentEventID: turn.StartEventID,
		Attrs: map[string]any{
			"llm_call_id": callID,
			"model":       e.inner.ModelName(),
			"call_kind":   e.kind,
		},
	})
}

func (e *llmEmitter) callEnded(ctx context.Context, callID string, usage *llm.Usage, outcome string) {
	turn, _ := TurnFromContext(ctx)
	attrs := map[string]any{"llm_call_id": callID}
	if usage != nil {
		attrs["prompt_tokens"] = usage.PromptTokens
		attrs["completion_tokens"] = usage.CompletionTokens
	}
	e.store.Emit(Event{
		Type:          TypeLLMCallEnded,
		EventID:       uuid.NewString(),
		OccurredAt:    time.Now(),
		Actor:         ActorAssistant,
		Trigger:       e.trigger(),
		Outcome:       outcome,
		TurnID:        turn.TurnID,
		BizKey:        callID,
		ParentEventID: turn.StartEventID,
		Attrs:         attrs,
	})
}

func (e *llmEmitter) firstToken(ctx context.Context, callID string, d time.Duration) {
	turn, _ := TurnFromContext(ctx)
	e.store.Emit(Event{
		Type:          TypeLLMFirstToken,
		EventID:       uuid.NewString(),
		OccurredAt:    time.Now(),
		Actor:         ActorAssistant,
		Trigger:       e.trigger(),
		TurnID:        turn.TurnID,
		BizKey:        turn.TurnID, // 每 turn 一条（唯一索引自动去重多轮调用）
		ParentEventID: callID,
		Attrs: map[string]any{
			"llm_call_id": callID,
			"ttft_ms":     d.Milliseconds(),
		},
	})
}

func (e *llmEmitter) trigger() string {
	if strings.HasPrefix(e.kind, "bg_") {
		return TriggerBatch
	}
	return TriggerManual
}
