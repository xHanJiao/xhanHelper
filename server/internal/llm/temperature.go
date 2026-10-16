package llm

import (
	"context"
	"encoding/json"
)

// TemperatureClient — 给「没显式指定温度」的请求补一个默认采样温度。
//
// 用途：采样温度按**调用点**决定，而不是按客户端。后台客户端是共用的（BgFlash 供
// 抽取/决策循环/摘要/画像/主题共用），但只有抽取段这类「逐字抄录」的任务需要贪心，
// 所以由注入方在构造处套一层，别的消费者一点不受影响。
//
// 显式传了 Temperature 的请求原样透传（不改调用方的意图）。
type TemperatureClient struct {
	inner LLMClient
	temp  float64
}

// WrapTemperature 用默认温度装饰一个 LLM 客户端。
func WrapTemperature(inner LLMClient, temp float64) LLMClient {
	return &TemperatureClient{inner: inner, temp: temp}
}

func (t *TemperatureClient) ModelName() string { return t.inner.ModelName() }

func (t *TemperatureClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	return t.inner.BuildSystemPrompt(base, tools)
}

func (t *TemperatureClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	t.fill(req)
	return t.inner.Chat(ctx, req)
}

func (t *TemperatureClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	t.fill(req)
	return t.inner.ChatSync(ctx, req)
}

// ChatWithRawJSON 请求体是原样透传的 JSON，注入不了字段，只能透传。
func (t *TemperatureClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (Message, error) {
	return t.inner.ChatWithRawJSON(ctx, requestBody)
}

func (t *TemperatureClient) fill(req *ChatRequest) {
	if req.Temperature == nil {
		temp := t.temp
		req.Temperature = &temp
	}
}
