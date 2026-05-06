package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"xhan-helper/internal/config"
)

// ——— base client ———

type baseClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func (c *baseClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// buildMessages 将内部 Message 转为 API 格式
func buildMessages(req *ChatRequest) []map[string]interface{} {
	messages := make([]map[string]interface{}, 0)

	if req.SystemPrompt != "" {
		messages = append(messages, map[string]interface{}{
			"role": "system", "content": req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		msg := map[string]interface{}{"role": m.Role}
		if m.Content != "" {
			msg["content"] = m.Content
		}
		if m.ReasoningContent != "" {
			msg["reasoning_content"] = m.ReasoningContent
		}
		if len(m.ToolCalls) > 0 {
			tcs := make([]map[string]interface{}, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				tcs[i] = map[string]interface{}{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				}
			}
			msg["tool_calls"] = tcs
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		messages = append(messages, msg)
	}
	return messages
}

// buildRequest 构造 API 请求体
func buildRequest(req *ChatRequest, model string, thinking bool) io.Reader {
	messages := buildMessages(req)

	payload := map[string]interface{}{
		"model":    model,
		"messages": messages,
	}

	if thinking {
		payload["reasoning_effort"] = "high"
		payload["thinking"] = map[string]string{"type": "enabled"}
	}

	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, 0)
		for _, t := range req.Tools {
			for _, fn := range t.Functions {
				tools = append(tools, map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        t.Name + "__" + fn.Name,
						"description": fn.Description,
						"parameters":  fn.Parameters,
					},
				})
			}
		}
		payload["tools"] = tools
	}

	b, _ := json.Marshal(payload)
	return bytes.NewReader(b)
}

// ——— FlashClient (非思考模式) ———

// FlashClient — DeepSeek 弱模型客户端（非思考模式，快速便宜）
type FlashClient struct {
	*baseClient
	model string
}

func (c *FlashClient) ModelName() string { return c.model }

// NewFlashClient 创建 FlashClient
func NewFlashClient(cfg config.LLMConfig) *FlashClient {
	return &FlashClient{
		baseClient: &baseClient{
			apiKey:     cfg.APIKey,
			baseURL:    cfg.BaseURL,
			httpClient: &http.Client{Timeout: 120 * time.Second},
		},
		model: cfg.Model,
	}
}

func (c *FlashClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString(fmt.Sprintf("\n当前时间: %s\n", time.Now().Format("2006-01-02 15:04:05 Monday")))
	if len(tools) == 0 {
		return b.String()
	}
	b.WriteString("\n\n你可以使用以下函数：\n")
	for _, t := range tools {
		for _, fn := range t.Functions {
			b.WriteString(fmt.Sprintf("- %s: %s\n", fn.Name, fn.Description))
		}
	}
	b.WriteString("\n当用户请求涉及可以使用函数的操作时，可以使用对应函数。不确定时直接反问用户。\n")
	return b.String()
}

func (c *FlashClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	msg, err := c.ChatSync(ctx, req)
	if err != nil {
		return nil, err
	}
	events := make(chan *StreamEvent, 32)
	go func() {
		defer close(events)
		for _, r := range msg.Content {
			select {
			case events <- &StreamEvent{Type: "text_delta", Content: string(r)}:
			case <-ctx.Done():
				log.Printf("[flash:%s] Chat: ctx cancelled during streaming", c.model)
				return
			}
		}
		select {
		case events <- &StreamEvent{Type: "done"}:
		case <-ctx.Done():
			log.Printf("[flash:%s] Chat: ctx cancelled before done event", c.model)
		}
	}()
	return events, nil
}

func (c *FlashClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	return c.callAPI(ctx, req, false)
}

func (c *FlashClient) callAPI(ctx context.Context, req *ChatRequest, _ bool) (Message, error) {
	url := c.baseURL + "/chat/completions"
	reqBytes, _ := io.ReadAll(buildRequest(req, c.model, false))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBytes))
	if err != nil {
		return Message{}, err
	}
	c.setHeaders(httpReq)

	lastMsg := ""
	if len(req.Messages) > 0 {
		lm := req.Messages[len(req.Messages)-1]
		lastMsg = lm.Content[:min(150, len(lm.Content))]
	}
	log.Printf("[flash:%s] request: msgs=%d, tools=%d, last_msg=%s",
		c.model, len(req.Messages)+1, len(req.Tools), lastMsg)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Message{}, fmt.Errorf("flash request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return Message{}, fmt.Errorf("flash status %d: %s", resp.StatusCode, string(respBody[:min(300, len(respBody))]))
	}

	msg, err := parseDeepSeekResponse(respBody)
	if err != nil {
		return msg, err
	}
	reqJSON := string(reqBytes)
	respJSON := string(respBody)
	msg.RequestJSON = &reqJSON
	msg.ResponseJSON = &respJSON
	return msg, nil
}

// ——— ProClient (思考模式) ———

// ProClient — DeepSeek 强模型客户端（思考模式，支持思维链 + 工具调用）
type ProClient struct {
	*baseClient
	model string
}

func (c *ProClient) ModelName() string { return c.model }

// NewProClient 创建 ProClient
func NewProClient(cfg config.LLMConfig, model string) *ProClient {
	return &ProClient{
		baseClient: &baseClient{
			apiKey:     cfg.APIKey,
			baseURL:    cfg.BaseURL,
			httpClient: &http.Client{Timeout: 600 * time.Second},
		},
		model: model,
	}
}

func (c *ProClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	var b strings.Builder
	b.WriteString(base)
	if len(tools) == 0 {
		return b.String()
	}
	b.WriteString("\n\n你可以使用以下工具（仅在必要时调用）：\n")
	for _, t := range tools {
		for _, fn := range t.Functions {
			b.WriteString(fmt.Sprintf("- %s: %s\n", fn.Name, fn.Description))
		}
	}
	return b.String()
}

func (c *ProClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	msg, err := c.ChatSync(ctx, req)
	if err != nil {
		return nil, err
	}
	events := make(chan *StreamEvent, 64)
	go func() {
		defer close(events)
		if msg.ReasoningContent != "" {
			select {
			case events <- &StreamEvent{Type: "thought_delta", Content: msg.ReasoningContent}:
			case <-ctx.Done():
				log.Printf("[pro:%s] Chat: ctx cancelled during streaming", c.model)
				return
			}
		}
		for _, r := range msg.Content {
			select {
			case events <- &StreamEvent{Type: "text_delta", Content: string(r)}:
			case <-ctx.Done():
				log.Printf("[pro:%s] Chat: ctx cancelled during streaming", c.model)
				return
			}
		}
		select {
		case events <- &StreamEvent{Type: "done"}:
		case <-ctx.Done():
			log.Printf("[pro:%s] Chat: ctx cancelled before done event", c.model)
		}
	}()
	return events, nil
}

func (c *ProClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	return c.callAPI(ctx, req, true)
}

func (c *ProClient) callAPI(ctx context.Context, req *ChatRequest, thinking bool) (Message, error) {
	url := c.baseURL + "/chat/completions"
	reqBytes, _ := io.ReadAll(buildRequest(req, c.model, true))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBytes))
	if err != nil {
		return Message{}, err
	}
	c.setHeaders(httpReq)

	log.Printf("[pro:%s] request: msgs=%d, tools=%d, thinking=true",
		c.model, len(req.Messages)+1, len(req.Tools))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Message{}, fmt.Errorf("pro request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return Message{}, fmt.Errorf("pro status %d: %s", resp.StatusCode, string(respBody[:min(300, len(respBody))]))
	}

	msg, err := parseDeepSeekResponse(respBody)
	if err != nil {
		return msg, err
	}
	reqJSON := string(reqBytes)
	respJSON := string(respBody)
	msg.RequestJSON = &reqJSON
	msg.ResponseJSON = &respJSON
	return msg, nil
}

// ——— shared parsing ———

// parseDeepSeekResponse 解析 API 响应，返回 Message（含 ReasoningContent + ToolCalls）
func parseDeepSeekResponse(body []byte) (Message, error) {
	var result struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Message{}, fmt.Errorf("parse response: %w", err)
	}
	if result.Error != nil {
		return Message{}, fmt.Errorf("api error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return Message{}, fmt.Errorf("no choices")
	}

	msg := result.Choices[0].Message
	reasoning := msg.ReasoningContent

	var toolCalls []ToolCall
	if len(msg.ToolCalls) > 0 {
		toolCalls = make([]ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			toolCalls[i] = ToolCall{
				ID: tc.ID,
				Function: ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}

	if result.Usage != nil {
		log.Printf("[deepseek] usage: prompt=%d, completion=%d, total=%d",
			result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
	}
	if len(toolCalls) > 0 {
		toolCallsJSON, _ := json.Marshal(toolCalls)
		tcCount := len(toolCalls)
		log.Printf("[deepseek] response: content=%d, reasoning=%d, tool_calls=%d -> %s",
			len(msg.Content), len(reasoning), tcCount, string(toolCallsJSON)[:min(200, len(toolCallsJSON))])
	} else {
		log.Printf("[deepseek] response: content=%d, reasoning=%d, tool_calls=0, text=%s",
			len(msg.Content), len(reasoning), msg.Content[:min(200, len(msg.Content))])
	}

	return Message{
		Role:             "assistant",
		Content:          msg.Content,
		ReasoningContent: reasoning,
		ToolCalls:        toolCalls,
	}, nil
}

// parseChatResponse 解析纯文本返回 (OpenAI 格式，doubao 共用)
func parseChatResponse(body []byte) (string, error) {
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
		return "", fmt.Errorf("api error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}
