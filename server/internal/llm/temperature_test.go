package llm

import (
	"context"
	"encoding/json"
	"testing"
)

// tempProbeClient 记录最后一次请求，用来断言温度被填成什么。
type tempProbeClient struct {
	last *ChatRequest
}

func (m *tempProbeClient) ModelName() string { return "probe" }

func (m *tempProbeClient) BuildSystemPrompt(base string, tools []ToolDefinition) string { return base }

func (m *tempProbeClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	m.last = req
	return nil, nil
}

func (m *tempProbeClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	m.last = req
	return Message{Content: "ok"}, nil
}

func (m *tempProbeClient) ChatWithRawJSON(ctx context.Context, body json.RawMessage) (Message, error) {
	return Message{}, nil
}

func TestWrapTemperature_FillsWhenUnset(t *testing.T) {
	probe := &tempProbeClient{}
	c := WrapTemperature(probe, 0)

	if _, err := c.ChatSync(context.Background(), &ChatRequest{}); err != nil {
		t.Fatalf("chat sync: %v", err)
	}
	if probe.last.Temperature == nil || *probe.last.Temperature != 0 {
		t.Errorf("want temperature 0 filled, got %v", probe.last.Temperature)
	}
}

func TestWrapTemperature_KeepsExplicit(t *testing.T) {
	probe := &tempProbeClient{}
	c := WrapTemperature(probe, 0)

	want := 1.0
	if _, err := c.ChatSync(context.Background(), &ChatRequest{Temperature: &want}); err != nil {
		t.Fatalf("chat sync: %v", err)
	}
	if probe.last.Temperature == nil || *probe.last.Temperature != 1.0 {
		t.Errorf("explicit temperature must not be overwritten, got %v", probe.last.Temperature)
	}
}

func TestWrapTemperature_DoesNotAliasAcrossCalls(t *testing.T) {
	probe := &tempProbeClient{}
	c := WrapTemperature(probe, 0)

	first := &ChatRequest{}
	if _, err := c.ChatSync(context.Background(), first); err != nil {
		t.Fatalf("chat sync: %v", err)
	}
	// 调用方改写第一次拿到的指针，不应污染装饰器的默认值。
	*first.Temperature = 2
	second := &ChatRequest{}
	if _, err := c.ChatSync(context.Background(), second); err != nil {
		t.Fatalf("chat sync: %v", err)
	}
	if *second.Temperature != 0 {
		t.Errorf("want 0 on second call, got %v", *second.Temperature)
	}
}
