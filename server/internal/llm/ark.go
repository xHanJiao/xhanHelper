package llm

import (
	"bufio"
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

// ArkClient — 火山方舟 OpenAI 兼容客户端（托管 DeepSeek 等模型）。
// 独立实现，不改动 deepseek.go：方舟与官方 DeepSeek 的差异集中在
// encrypted_content 回传、stream_options.include_usage、不支持 user_id。
type ArkClient struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewArkClient 创建方舟客户端。model 与 strong_model 一致时用强模型超时。
func NewArkClient(cfg config.LLMConfig, model string) *ArkClient {
	timeout := time.Duration(cfg.HTTPTimeoutSecs) * time.Second
	if model == cfg.StrongModel {
		timeout = time.Duration(cfg.StrongHTTPTimeoutSecs) * time.Second
	}
	return &ArkClient{
		apiKey:     cfg.APIKey,
		baseURL:    cfg.BaseURL,
		model:      model,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *ArkClient) ModelName() string { return c.model }

func (c *ArkClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// ——— 请求体构建 ———

// buildMessages 构建消息列表，含 encrypted_content / reasoning_content 回传。
func (c *ArkClient) buildMessages(req *ChatRequest) []map[string]interface{} {
	messages := make([]map[string]interface{}, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": req.SystemPrompt})
	}
	for _, m := range req.Messages {
		if m.Content == "" && len(m.ToolCalls) == 0 && m.ToolCallID == "" {
			continue
		}
		msg := map[string]interface{}{"role": m.Role}
		if m.Content != "" {
			msg["content"] = m.Content
		}
		if m.ReasoningContent != "" {
			msg["reasoning_content"] = m.ReasoningContent
		}
		// 方舟要求：思考模型必须回传 encrypted_content，否则影响推理效果
		if m.EncryptedContent != "" {
			msg["encrypted_content"] = m.EncryptedContent
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

// fillParams 注入公共参数。方舟不支持 user_id，不注入。
func (c *ArkClient) fillParams(payload map[string]interface{}, req *ChatRequest) {
	if req.Thinking != nil {
		t := "disabled"
		if *req.Thinking {
			t = "enabled"
		}
		payload["thinking"] = map[string]string{"type": t}
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		payload["max_tokens"] = *req.MaxTokens
	}
}

// fillTools 注入原生 function calling 的工具定义（OpenAI 格式）。
func (c *ArkClient) fillTools(payload map[string]interface{}, req *ChatRequest) {
	if len(req.Tools) == 0 {
		return
	}
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

func (c *ArkClient) buildStreamBody(req *ChatRequest) ([]byte, io.Reader) {
	payload := map[string]interface{}{
		"model":    c.model,
		"messages": c.buildMessages(req),
		"stream":   true,
		// 方舟流式默认不带 usage，需显式开启才能在 [DONE] 前拿到 token 用量（成本统计依赖）
		"stream_options": map[string]interface{}{"include_usage": true},
	}
	c.fillParams(payload, req)
	c.fillTools(payload, req)
	b, _ := json.Marshal(payload)
	return b, bytes.NewReader(b)
}

func (c *ArkClient) buildSyncBody(req *ChatRequest) ([]byte, io.Reader) {
	payload := map[string]interface{}{
		"model":    c.model,
		"messages": c.buildMessages(req),
		"stream":   false,
	}
	c.fillParams(payload, req)
	c.fillTools(payload, req)
	b, _ := json.Marshal(payload)
	return b, bytes.NewReader(b)
}

// ——— 流式 ———

// arkUsageDetails — 方舟 usage 结构（OpenAI 风格，缓存命中在 prompt_tokens_details.cached_tokens）。
type arkUsageDetails struct {
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
}

// toArkUsage 映射方舟 usage 到 llm.Usage。方舟不返回顶层缓存字段，
// 命中来自 prompt_tokens_details.cached_tokens，未命中 = prompt - 命中。
func toArkUsage(u *arkUsageDetails) *Usage {
	if u == nil {
		return nil
	}
	usage := &Usage{
		PromptTokens:          u.PromptTokens,
		CompletionTokens:      u.CompletionTokens,
		TotalTokens:           u.TotalTokens,
		PromptCacheHitTokens:  u.PromptCacheHitTokens,
		PromptCacheMissTokens: u.PromptCacheMissTokens,
	}
	if usage.PromptCacheHitTokens == 0 && u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		usage.PromptCacheHitTokens = u.PromptTokensDetails.CachedTokens
	}
	if usage.PromptCacheMissTokens == 0 {
		usage.PromptCacheMissTokens = usage.PromptTokens - usage.PromptCacheHitTokens
		if usage.PromptCacheMissTokens < 0 {
			usage.PromptCacheMissTokens = 0
		}
	}
	return usage
}

// arkStreamChunk — 方舟 SSE 单个 chunk（delta 含 encrypted_content）。
type arkStreamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			EncryptedContent string `json:"encrypted_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *arkUsageDetails `json:"usage"`
}

func (c *ArkClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	url := c.baseURL + "/chat/completions"
	reqBytes, reqBody := c.buildStreamBody(req)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, reqBody)
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	lastMsg := ""
	if len(req.Messages) > 0 {
		lm := req.Messages[len(req.Messages)-1]
		lastMsg = lm.Content[:min(500, len(lm.Content))]
	}
	log.Printf("[ark] stream request: model=%s msgs=%d tools=%d last_msg=%s",
		c.model, len(req.Messages)+1, len(req.Tools), lastMsg)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ark stream request: %w", err)
	}
	if resp.StatusCode != 200 {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("ark stream status %d (failed to read body: %v)", resp.StatusCode, err)
		}
		return nil, &StatusError{Code: resp.StatusCode, Body: string(body[:min(1000, len(body))])}
	}

	events := make(chan *StreamEvent, 512)
	go c.readSSEStream(ctx, resp.Body, events, string(reqBytes))
	return events, nil
}

func (c *ArkClient) readSSEStream(ctx context.Context, body io.ReadCloser, events chan<- *StreamEvent, reqJSONStr string) {
	defer close(events)
	defer body.Close()

	var respBuf bytes.Buffer
	tee := io.TeeReader(body, &respBuf)
	scanner := bufio.NewScanner(tee)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var (
		content    strings.Builder
		reasoning  strings.Builder
		encrypted  strings.Builder
		tcMap      = make(map[int]*ToolCall)
		usage      *Usage
		respChunks []string
	)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		respChunks = append(respChunks, line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk arkStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			usage = toArkUsage(chunk.Usage)
		}

		for _, choice := range chunk.Choices {
			d := choice.Delta
			if d.Content != "" {
				content.WriteString(d.Content)
				sendStreamEvent(events, &StreamEvent{Type: "text_delta", Content: d.Content})
			}
			if d.ReasoningContent != "" {
				reasoning.WriteString(d.ReasoningContent)
				sendStreamEvent(events, &StreamEvent{Type: "thought_delta", Content: d.ReasoningContent})
			}
			// 思考内容加密原文：独立 chunk（content/reasoning 为空），在正式应答前返回完整值
			if d.EncryptedContent != "" {
				encrypted.WriteString(d.EncryptedContent)
			}
			for _, tc := range d.ToolCalls {
				cur, ok := tcMap[tc.Index]
				if !ok {
					cur = &ToolCall{ID: tc.ID}
					tcMap[tc.Index] = cur
				}
				if tc.Function.Name != "" {
					cur.Function.Name = tc.Function.Name
				}
				cur.Function.Arguments += tc.Function.Arguments
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[ark] stream scanner error: %v", err)
	}

	toolCalls := make([]ToolCall, 0, len(tcMap))
	for i := 0; i < len(tcMap); i++ {
		if tc, ok := tcMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}
	log.Printf("[ark] stream finished: content=%d, reasoning=%d, encrypted=%d, tool_calls=%d",
		content.Len(), reasoning.Len(), encrypted.Len(), len(toolCalls))

	respJSONStr := strings.Join(respChunks, "\n")
	sendStreamEvent(events, &StreamEvent{
		Type:             "done",
		ToolCalls:        toolCalls,
		Usage:            usage,
		EncryptedContent: encrypted.String(),
		RequestJSON:      &reqJSONStr,
		ResponseJSON:     &respJSONStr,
	})
}

// ——— 非流式 ———

func (c *ArkClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	reqBytes, reqReader := c.buildSyncBody(req)
	reqJSONStr := string(reqBytes)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", reqReader)
	if err != nil {
		return Message{}, fmt.Errorf("ark sync request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Message{}, fmt.Errorf("ark sync request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("ark sync read body: %w", err)
	}
	if resp.StatusCode != 200 {
		return Message{}, &StatusError{Code: resp.StatusCode, Body: string(body[:min(1000, len(body))])}
	}

	respJSONStr := string(body)
	var result struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				EncryptedContent string `json:"encrypted_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *arkUsageDetails `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Message{}, fmt.Errorf("ark sync parse: %w", err)
	}

	msg := Message{
		Role:         "assistant",
		RequestJSON:  &reqJSONStr,
		ResponseJSON: &respJSONStr,
		Usage:        toArkUsage(result.Usage),
	}
	if len(result.Choices) > 0 {
		msg.Content = result.Choices[0].Message.Content
		msg.ReasoningContent = result.Choices[0].Message.ReasoningContent
		msg.EncryptedContent = result.Choices[0].Message.EncryptedContent
		for _, tc := range result.Choices[0].Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID: tc.ID,
				Function: ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}
	return msg, nil
}

// ——— 重放 ———

func (c *ArkClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (Message, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(requestBody, &payload); err != nil {
		return Message{}, fmt.Errorf("parse request_json: %w", err)
	}
	payload["stream"] = false
	bodyBytes, _ := json.Marshal(payload)
	bodyStr := string(bodyBytes)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return Message{}, fmt.Errorf("ark raw request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Message{}, fmt.Errorf("ark raw request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("ark raw read body: %w", err)
	}
	if resp.StatusCode != 200 {
		return Message{}, &StatusError{Code: resp.StatusCode, Body: string(body[:min(1000, len(body))])}
	}

	respStr := string(body)
	content, err := parseChatResponse(body)
	if err != nil {
		return Message{}, fmt.Errorf("ark raw parse: %w", err)
	}
	return Message{
		Role:         "assistant",
		Content:      content,
		RequestJSON:  &bodyStr,
		ResponseJSON: &respStr,
	}, nil
}

// ——— System Prompt（原生 function calling） ———

func (c *ArkClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	var b strings.Builder
	b.WriteString(base)
	if len(tools) == 0 {
		return b.String()
	}
	b.WriteString("\n\n你可以使用以下函数：\n")
	for _, t := range tools {
		for _, fn := range t.Functions {
			b.WriteString(fmt.Sprintf("- %s: %s\n", fn.Name, fn.Description))
		}
	}
	b.WriteString("\n只能使用上面列出的函数，调用时必须严格遵循对应参数定义。当用户请求涉及可以使用函数的操作时，可以使用对应函数。不确定时直接反问用户。\n")
	return b.String()
}
