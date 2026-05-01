package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// Chat 流式对话 — 底层调非流式 /chat/completions API，拿到完整回复后模拟流式输出
func (c *DoubaoClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	fullText, err := c.callChatAPI(ctx, req)
	if err != nil {
		return nil, err
	}

	events := make(chan *StreamEvent, 32)
	go func() {
		defer close(events)
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
	return c.callChatAPI(ctx, req)
}

// callChatAPI 调用豆包 /chat/completions API (OpenAI 兼容)
func (c *DoubaoClient) callChatAPI(ctx context.Context, req *ChatRequest) (string, error) {
	body := c.buildBody(req)

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return "", err
	}
	c.setHeaders(httpReq)

	log.Printf("[llm] request: model=%s, msgs=%d, url=%s", c.model, len(req.Messages)+1, url)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("[llm] http error: %v", err)
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[llm] read body error: %v", err)
		return "", err
	}

	if resp.StatusCode != 200 {
		log.Printf("[llm] status %d: %s", resp.StatusCode, string(respBody[:min(300, len(respBody))]))
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, string(respBody[:min(300, len(respBody))]))
	}

	content, err := c.parseResponse(respBody)
	if err != nil {
		log.Printf("[llm] parse error: %v, body=%s", err, string(respBody[:min(200, len(respBody))]))
		return "", err
	}
	log.Printf("[llm] response: len=%d", len(content))
	return content, nil
}

// buildBody 构造请求体
func (c *DoubaoClient) buildBody(req *ChatRequest) io.Reader {
	messages := make([]map[string]string, 0)

	sp := req.SystemPrompt
	if sp == "" {
		sp = defaultSystemPrompt
	}
	messages = append(messages, map[string]string{"role": "system", "content": sp})

	for _, m := range req.Messages {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}

	payload := map[string]interface{}{
		"model":    c.model,
		"messages": messages,
	}

	b, _ := json.Marshal(payload)
	return bytes.NewReader(b)
}

// parseResponse 解析 /chat/completions 返回
func (c *DoubaoClient) parseResponse(body []byte) (string, error) {
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("llm error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

func (c *DoubaoClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
