package llm

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"time"
)

// RetryConfig — 重试参数。硬编码默认值，需要可调时再接入配置。
type RetryConfig struct {
	MaxAttempts int           // 总尝试次数（含首次）
	BaseDelay   time.Duration // 首次退避基数
	MaxDelay    time.Duration // 退避上限
	Factor      float64       // 指数因子
}

var defaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   1 * time.Second,
	MaxDelay:    8 * time.Second,
	Factor:      2,
}

// RetryLLMClient — LLM 客户端装饰器：ChatSync 失败时指数退避重试。
// 只对同步调用重试（后台 worker 全走 ChatSync）；流式 Chat 透传不重试。
type RetryLLMClient struct {
	inner LLMClient
	cfg   RetryConfig
}

// WrapRetry 用重试装饰器包装 LLM 客户端。
func WrapRetry(inner LLMClient) LLMClient {
	return &RetryLLMClient{inner: inner, cfg: defaultRetryConfig}
}

func (r *RetryLLMClient) ModelName() string { return r.inner.ModelName() }

func (r *RetryLLMClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	return r.inner.BuildSystemPrompt(base, tools)
}

func (r *RetryLLMClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	return r.inner.Chat(ctx, req)
}

func (r *RetryLLMClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (Message, error) {
	return r.inner.ChatWithRawJSON(ctx, requestBody)
}

// ChatSync 同步调用，失败按 isRetryableError 判断是否退避重试。
func (r *RetryLLMClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	var lastErr error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		msg, err := r.inner.ChatSync(ctx, req)
		if err == nil {
			return msg, nil
		}
		lastErr = err
		if attempt == r.cfg.MaxAttempts || !isRetryableError(err) {
			return msg, err
		}
		d := r.backoff(attempt)
		log.Printf("[llm] ChatSync %s attempt %d/%d failed, retrying in %s: %v",
			r.inner.ModelName(), attempt, r.cfg.MaxAttempts, d, err)
		if !sleepCtx(ctx, d) {
			return msg, err
		}
	}
	return Message{}, lastErr
}

// backoff 指数退避 + 抖动。
func (r *RetryLLMClient) backoff(attempt int) time.Duration {
	d := r.cfg.BaseDelay
	for i := 1; i < attempt; i++ {
		d = time.Duration(float64(d) * r.cfg.Factor)
	}
	if d > r.cfg.MaxDelay {
		d = r.cfg.MaxDelay
	}
	return d + time.Duration(rand.Int63n(int64(d)/2+1))
}

// sleepCtx 退避睡眠，ctx 取消时立即返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// StatusError — HTTP 非 200 状态错误，携带状态码供重试判定。
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return "status " + strconv.Itoa(e.Code) + ": " + e.Body
}

// isRetryableError 判断错误是否值得重试：
// 超时（net.Error.Timeout()，含 http.Client.Timeout）、5xx/429、连接类错误 → 重试；
// context.Canceled、4xx（非 429）、JSON 解析错误 → 不重试。
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var status *StatusError
	if errors.As(err, &status) {
		return status.Code == 429 || status.Code >= 500
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	return false
}
