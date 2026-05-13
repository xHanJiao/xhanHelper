package monitor

import (
	"context"

	"xhan-helper/internal/llm"
)

// MonitoredLLMClient — LLM 客户端装饰器，自动统计调用次数 + 成功率
type MonitoredLLMClient struct {
	inner  llm.LLMClient
	caller string // "orchestrator" | "extractor" | "autoRoute" | "agent" | "title"
}

// WrapLLMClient 包一层 LLM 客户端，调用方无感知
func WrapLLMClient(inner llm.LLMClient, caller string) llm.LLMClient {
	if inner == nil {
		return nil
	}
	return &MonitoredLLMClient{inner: inner, caller: caller}
}

func (m *MonitoredLLMClient) ModelName() string {
	return m.inner.ModelName()
}

func (m *MonitoredLLMClient) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return m.inner.BuildSystemPrompt(base, tools)
}

func (m *MonitoredLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	ch, err := m.inner.Chat(ctx, req)
	global.IncLLMCall(m.caller, err != nil)
	return ch, err
}

func (m *MonitoredLLMClient) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	msg, err := m.inner.ChatSync(ctx, req)
	global.IncLLMCall(m.caller, err != nil)
	return msg, err
}
