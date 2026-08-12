package llmcost

import (
	"context"
	"encoding/json"
	"time"

	"xhan-helper/internal/llm"
)

// CostTracker 装饰器：拦截 Chat/ChatSync 调用，提取 token 用量并上报给 Recorder。
// 包装顺序（外层→内层）：CostTracker → MonitoredLLMClient → 真实客户端。
type CostTracker struct {
	inner    llm.LLMClient
	recorder Recorder
}

// WrapLLMClient 用 CostTracker 包装 LLMClient。recorder 为 nil 时直接返回 inner。
func WrapLLMClient(inner llm.LLMClient, recorder Recorder) llm.LLMClient {
	if inner == nil || recorder == nil {
		return inner
	}
	return &CostTracker{inner: inner, recorder: recorder}
}

// Chat 流式调用：包装返回的 channel，监听 done 事件提取 Usage。
func (t *CostTracker) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	innerCh, err := t.inner.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	outerCh := make(chan *llm.StreamEvent, 256)
	go func() {
		defer close(outerCh)
		for evt := range innerCh {
			// 非阻塞透传：消费者断开时不阻塞本 goroutine，done 事件仍可记录
			select {
			case outerCh <- evt:
			default:
			}
			if evt.Type == "done" && evt.Usage != nil {
				go t.record(ctx, req, evt.Usage)
			}
		}
	}()
	return outerCh, nil
}

// ChatSync 同步调用：从返回的 Message.Usage 提取 token 用量。
func (t *CostTracker) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	msg, err := t.inner.ChatSync(ctx, req)
	if err == nil && msg.Usage != nil {
		go t.record(ctx, req, msg.Usage)
	}
	return msg, err
}

// ChatWithRawJSON 透传，不记录（当前无生产调用）。
func (t *CostTracker) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return t.inner.ChatWithRawJSON(ctx, requestBody)
}

// BuildSystemPrompt 透传（不产生 LLM 调用）。
func (t *CostTracker) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return t.inner.BuildSystemPrompt(base, tools)
}

// ModelName 透传。
func (t *CostTracker) ModelName() string {
	return t.inner.ModelName()
}

// record 构造 UsageRecord 并上报。异步执行，不阻塞 LLM 响应。
func (t *CostTracker) record(ctx context.Context, req *llm.ChatRequest, usage *llm.Usage) {
	scenario, _ := ScenarioFromContext(ctx)
	rec := UsageRecord{
		Date:             time.Now(),
		UserID:           scenario.UserID,
		Category:         scenario.Category,
		SubCategory:      scenario.SubCategory,
		Model:            t.inner.ModelName(),
		ConvID:           scenario.ConvID,
		RefType:          scenario.RefType,
		RefID:            scenario.RefID,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		Meta:             scenario.Meta,
	}
	if rec.UserID == "" {
		rec.UserID = req.UserID
	}
	t.recorder.Record(rec)
}
