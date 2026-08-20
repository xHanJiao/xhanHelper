package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"
)

var fastCfg = RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 4 * time.Millisecond, Factor: 2}

type retryMockClient struct {
	calls    int
	failures int
	failErr  error
}

func (m *retryMockClient) ModelName() string { return "mock" }

func (m *retryMockClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	return base
}

func (m *retryMockClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	return nil, errors.New("not used")
}

func (m *retryMockClient) ChatWithRawJSON(ctx context.Context, body json.RawMessage) (Message, error) {
	return Message{}, errors.New("not used")
}

func (m *retryMockClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	m.calls++
	if m.calls <= m.failures {
		return Message{}, m.failErr
	}
	return Message{Content: "ok"}, nil
}

func TestRetryChatSync_SucceedsAfterFailures(t *testing.T) {
	mock := &retryMockClient{failures: 2, failErr: &StatusError{Code: 503, Body: "overloaded"}}
	c := &RetryLLMClient{inner: mock, cfg: fastCfg}

	msg, err := c.ChatSync(context.Background(), &ChatRequest{})
	if err != nil {
		t.Fatalf("expected success after retries, got err=%v", err)
	}
	if msg.Content != "ok" {
		t.Errorf("expected content ok, got %q", msg.Content)
	}
	if mock.calls != 3 {
		t.Errorf("expected 3 calls (1 + 2 retries), got %d", mock.calls)
	}
}

func TestRetryChatSync_RetriesOnTimeout(t *testing.T) {
	mock := &retryMockClient{failures: 1, failErr: &url.Error{Op: "Post", URL: "https://api.siliconflow.cn/v1/chat/completions", Err: context.DeadlineExceeded}}
	c := &RetryLLMClient{inner: mock, cfg: fastCfg}

	if _, err := c.ChatSync(context.Background(), &ChatRequest{}); err != nil {
		t.Fatalf("expected success, got err=%v", err)
	}
	if mock.calls != 2 {
		t.Errorf("expected 2 calls, got %d", mock.calls)
	}
}

func TestRetryChatSync_NoRetryOn4xx(t *testing.T) {
	mock := &retryMockClient{failures: 1, failErr: &StatusError{Code: 400, Body: "bad request"}}
	c := &RetryLLMClient{inner: mock, cfg: fastCfg}

	_, err := c.ChatSync(context.Background(), &ChatRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call (4xx not retried), got %d", mock.calls)
	}
}

func TestRetryChatSync_NoRetryOnCanceled(t *testing.T) {
	mock := &retryMockClient{failures: 1, failErr: context.Canceled}
	c := &RetryLLMClient{inner: mock, cfg: fastCfg}

	if _, err := c.ChatSync(context.Background(), &ChatRequest{}); err == nil {
		t.Fatal("expected error")
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call (canceled not retried), got %d", mock.calls)
	}
}

func TestRetryChatSync_ExhaustsRetries(t *testing.T) {
	mock := &retryMockClient{failures: 10, failErr: &StatusError{Code: 500, Body: "boom"}}
	c := &RetryLLMClient{inner: mock, cfg: fastCfg}

	_, err := c.ChatSync(context.Background(), &ChatRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if mock.calls != 3 {
		t.Errorf("expected 3 calls (max attempts), got %d", mock.calls)
	}
}
