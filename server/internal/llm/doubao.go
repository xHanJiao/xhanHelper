package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"xhan-helper/internal/config"
)

const defaultSystemPrompt = `你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

核心原则：
1. 简洁回复：用最少的字把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 主动：如果用户意图不明确，追问关键信息`

// DoubaoClient — 豆包 API 客户端
type DoubaoClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewDoubao 创建豆包客户端
func NewDoubao(cfg config.LLMConfig) *DoubaoClient {
	return &DoubaoClient{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Chat 流式对话 — 底层调非流式 /responses API，拿到完整回复后模拟流式输出
func (c *DoubaoClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	fullText, err := c.callResponses(ctx, req)
	if err != nil {
		return nil, err
	}

	events := make(chan *StreamEvent, 32)
	go func() {
		defer close(events)
		// 按字符逐一发送，模拟流式输出
		for _, r := range fullText {
			select {
			case <-ctx.Done():
				return
			default:
			}
			events <- &StreamEvent{Type: "text_delta", Content: string(r)}
		}
		events <- &StreamEvent{Type: "done"}
	}()
	return events, nil
}

// ChatSync 非流式对话 — 直接返回完整回复
func (c *DoubaoClient) ChatSync(ctx context.Context, req *ChatRequest) (string, error) {
	return c.callResponses(ctx, req)
}

// callResponses 调用豆包 /responses API（非流式）
func (c *DoubaoClient) callResponses(ctx context.Context, req *ChatRequest) (string, error) {
	body := c.buildBody(req)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/responses", body)
	if err != nil {
		return "", err
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, string(respBody))
	}

	return c.parseResponse(respBody)
}

// buildBody 构造请求体
func (c *DoubaoClient) buildBody(req *ChatRequest) io.Reader {
	input := make([]map[string]string, 0)

	sp := req.SystemPrompt
	if sp == "" {
		sp = defaultSystemPrompt
	}
	input = append(input, map[string]string{"role": "system", "content": sp})

	for _, m := range req.Messages {
		input = append(input, map[string]string{"role": m.Role, "content": m.Content})
	}

	payload := map[string]interface{}{
		"model":             c.model,
		"input":             input,
		"store":             true,
		"max_output_tokens": 2048,
	}

	b, _ := json.Marshal(payload)
	return bytes.NewReader(b)
}

// parseResponse 解析豆包 /responses 返回
func (c *DoubaoClient) parseResponse(body []byte) (string, error) {
	var result struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
				Type string `json:"type"`
			} `json:"content"`
		} `json:"output"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("llm error: %s - %s", result.Error.Type, result.Error.Message)
	}
	if result.Status != "completed" {
		return "", fmt.Errorf("llm not completed, status: %s", result.Status)
	}

	for _, output := range result.Output {
		if output.Type == "message" && output.Role == "assistant" {
			for _, cnt := range output.Content {
				if cnt.Type == "output_text" && cnt.Text != "" {
					return cnt.Text, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no content in response")
}

func (c *DoubaoClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}
