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
		if m.Content == "" && len(m.ToolCalls) == 0 && m.ToolCallID == "" && len(m.Images) == 0 {
			continue
		}
		msg := map[string]interface{}{"role": m.Role}
		if len(m.Images) > 0 {
			// 带图消息：content 必须是 content block 数组（图片只能出现在 user 消息）
			msg["content"] = buildContentBlocks(m.Content, m.Images)
		} else if m.Content != "" {
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

// buildContentBlocks 组装带图消息的 content 数组。
// 形式：[{type:text,text:...}, {type:file,file_id:file-api-xxx}]
// 图片用 `file` 块承载（不是 image_url）——官方文档：Files API 的 file_id 走 file 块，
// 且此时 detail 字段被忽略。文本为空时不产出空的 text block。
func buildContentBlocks(text string, images []ImageRef) []map[string]interface{} {
	blocks := make([]map[string]interface{}, 0, len(images)+1)
	if text != "" {
		blocks = append(blocks, map[string]interface{}{"type": "text", "text": text})
	}
	for _, img := range images {
		if img.FileID == "" {
			continue
		}
		blocks = append(blocks, map[string]interface{}{"type": "file", "file_id": img.FileID})
	}
	return blocks
}

// streamChunk — SSE 流式响应的单个 chunk
type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
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
	Usage *struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		TotalTokens           int `json:"total_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
}

// callStreamAPI — 真正的流式 API 调用，读取 SSE chunk 实时推送
func (c *baseClient) callStreamAPI(ctx context.Context, req *ChatRequest, model string) (<-chan *StreamEvent, error) {
	url := c.baseURL + "/chat/completions"
	reqBytes, reqBody := buildStreamBody(req, model)

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
	var params []string
	if req.Temperature != nil {
		params = append(params, fmt.Sprintf("temp=%.1f", *req.Temperature))
	}
	if req.MaxTokens != nil {
		params = append(params, fmt.Sprintf("max_tok=%d", *req.MaxTokens))
	}
	if req.Thinking != nil {
		params = append(params, fmt.Sprintf("thinking=%v", *req.Thinking))
	}
	paramsStr := ""
	if len(params) > 0 {
		paramsStr = " " + strings.Join(params, " ")
	}
	log.Printf("[%s] stream request: msgs=%d tools=%d%s last_msg=%s",
		model, len(req.Messages)+1, len(req.Tools), paramsStr, lastMsg)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("stream request: %w", err)
	}
	if resp.StatusCode != 200 {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("stream status %d (failed to read body: %v)", resp.StatusCode, err)
		}
		return nil, &StatusError{Code: resp.StatusCode, Body: string(body[:min(1000, len(body))])}
	}

	events := make(chan *StreamEvent, 512)
	go c.readSSEStream(ctx, resp.Body, events, model, string(reqBytes))
	return events, nil
}

func (c *baseClient) readSSEStream(ctx context.Context, body io.ReadCloser, events chan<- *StreamEvent, model, reqJSONStr string) {
	defer close(events)
	defer body.Close()

	// 先读取并缓存整个响应体，同时 tee 到 scanner
	var respBuf bytes.Buffer
	tee := io.TeeReader(body, &respBuf)

	scanner := bufio.NewScanner(tee)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var (
		content    strings.Builder
		reasoning  strings.Builder
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

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			usage = &Usage{
				PromptTokens:          chunk.Usage.PromptTokens,
				CompletionTokens:      chunk.Usage.CompletionTokens,
				TotalTokens:           chunk.Usage.TotalTokens,
				PromptCacheHitTokens:  chunk.Usage.PromptCacheHitTokens,
				PromptCacheMissTokens: chunk.Usage.PromptCacheMissTokens,
			}
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
		log.Printf("[%s] stream scanner error: %v", model, err)
	}

	toolCalls := make([]ToolCall, 0, len(tcMap))
	for i := 0; i < len(tcMap); i++ {
		if tc, ok := tcMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	if len(toolCalls) > 0 {
		tcJSON, _ := json.Marshal(toolCalls)
		log.Printf("[%s] stream finished: content=%d, reasoning=%d, tool_calls=%d -> %s",
			model, content.Len(), reasoning.Len(), len(toolCalls),
			string(tcJSON)[:min(500, len(tcJSON))])
	} else {
		text := content.String()
		log.Printf("[%s] stream finished: content=%d, reasoning=%d, tool_calls=0, text=%s",
			model, content.Len(), reasoning.Len(), text[:min(500, len(text))])
	}
	if usage != nil {
		log.Printf("[%s] usage: prompt=%d, completion=%d, total=%d",
			model, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}

	// 构建完整响应 JSON（用数组包含所有 chunks）
	respJSONStr := strings.Join(respChunks, "\n")

	sendStreamEvent(events, &StreamEvent{
		Type:         "done",
		ToolCalls:    toolCalls,
		Usage:        usage,
		RequestJSON:  &reqJSONStr,
		ResponseJSON: &respJSONStr,
	})
}

func buildStreamBody(req *ChatRequest, model string) ([]byte, io.Reader) {
	messages := buildMessages(req)

	payload := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}

	if req.Thinking != nil {
		if *req.Thinking {
			payload["thinking"] = map[string]string{"type": "enabled"}
			if req.Effort != "" {
				payload["reasoning_effort"] = req.Effort
			}
		} else {
			payload["thinking"] = map[string]string{"type": "disabled"}
		}
	}

	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		payload["max_tokens"] = *req.MaxTokens
	}
	if req.UserID != "" {
		payload["user_id"] = req.UserID
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
	return b, bytes.NewReader(b)
}

// collectStream — 消费流式 channel，汇总成完整 Message（供 ChatSync 使用）
func collectStream(events <-chan *StreamEvent) Message {
	var content, reasoning strings.Builder
	var toolCalls []ToolCall
	var reqJSON, respJSON *string
	for evt := range events {
		switch evt.Type {
		case "text_delta":
			content.WriteString(evt.Content)
		case "thought_delta":
			reasoning.WriteString(evt.Content)
		case "done":
			toolCalls = evt.ToolCalls
			reqJSON = evt.RequestJSON
			respJSON = evt.ResponseJSON
		}
	}
	return Message{
		Role:             "assistant",
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		ToolCalls:        toolCalls,
		RequestJSON:      reqJSON,
		ResponseJSON:     respJSON,
	}
}

// buildRequest 构造非流式 API 请求体（保留供 doubao 等共用 parseChatResponse 的场景）
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

// callSyncAPI 非流式 API 调用（stream: false），返回单个完整 Message
func (c *baseClient) callSyncAPI(ctx context.Context, req *ChatRequest, model string) (Message, error) {
	reqBytes, reqReader := buildSyncBody(req, model)
	reqJSONStr := string(reqBytes)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", reqReader)
	if err != nil {
		return Message{}, fmt.Errorf("sync request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Message{}, fmt.Errorf("sync request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("sync read body: %w", err)
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
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens          int `json:"prompt_tokens"`
			CompletionTokens      int `json:"completion_tokens"`
			TotalTokens           int `json:"total_tokens"`
			PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return Message{}, fmt.Errorf("sync parse: %w", err)
	}

	msg := Message{
		Role:         "assistant",
		RequestJSON:  &reqJSONStr,
		ResponseJSON: &respJSONStr,
		Usage: &Usage{
			PromptTokens:          result.Usage.PromptTokens,
			CompletionTokens:      result.Usage.CompletionTokens,
			TotalTokens:           result.Usage.TotalTokens,
			PromptCacheHitTokens:  result.Usage.PromptCacheHitTokens,
			PromptCacheMissTokens: result.Usage.PromptCacheMissTokens,
		},
	}

	if len(result.Choices) > 0 {
		msg.Content = result.Choices[0].Message.Content
		msg.ReasoningContent = result.Choices[0].Message.ReasoningContent
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

// chatWithRawJSON 直接发送原始 LLM 请求 JSON（用于重放对比）
func (c *baseClient) chatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (Message, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(requestBody, &payload); err != nil {
		return Message{}, fmt.Errorf("parse request_json: %w", err)
	}
	payload["stream"] = false

	bodyBytes, _ := json.Marshal(payload)
	bodyStr := string(bodyBytes)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return Message{}, fmt.Errorf("raw request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Message{}, fmt.Errorf("raw request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("raw read body: %w", err)
	}
	if resp.StatusCode != 200 {
		return Message{}, &StatusError{Code: resp.StatusCode, Body: string(body[:min(1000, len(body))])}
	}

	respStr := string(body)
	content, err := parseChatResponse(body)
	if err != nil {
		return Message{}, fmt.Errorf("raw parse: %w", err)
	}

	return Message{
		Role:         "assistant",
		Content:      content,
		RequestJSON:  &bodyStr,
		ResponseJSON: &respStr,
	}, nil
}

// buildSyncBody 构造非流式请求体（stream: false）
func buildSyncBody(req *ChatRequest, model string) ([]byte, io.Reader) {
	messages := buildMessages(req)
	payload := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}
	if req.Thinking != nil {
		if *req.Thinking {
			payload["thinking"] = map[string]string{"type": "enabled"}
			if req.Effort != "" {
				payload["reasoning_effort"] = req.Effort
			}
		} else {
			payload["thinking"] = map[string]string{"type": "disabled"}
		}
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		payload["max_tokens"] = *req.MaxTokens
	}
	if req.UserID != "" {
		payload["user_id"] = req.UserID
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
	return b, bytes.NewReader(b)
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
			httpClient: &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSecs) * time.Second},
		},
		model: cfg.Model,
	}
}

func (c *FlashClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
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

func (c *FlashClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	return c.callStreamAPI(ctx, req, c.model)
}

func (c *FlashClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	return c.callSyncAPI(ctx, req, c.model)
}

func (c *FlashClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (Message, error) {
	return c.chatWithRawJSON(ctx, requestBody)
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
			httpClient: &http.Client{Timeout: time.Duration(cfg.StrongHTTPTimeoutSecs) * time.Second},
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
	b.WriteString("\n只能使用上面列出的函数，调用时必须严格遵循对应参数定义。\n")
	return b.String()
}

func (c *ProClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	if req.Thinking == nil {
		t := true
		req.Thinking = &t
	}
	return c.callStreamAPI(ctx, req, c.model)
}

func (c *ProClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	return c.callSyncAPI(ctx, req, c.model)
}

func (c *ProClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (Message, error) {
	return c.chatWithRawJSON(ctx, requestBody)
}

// sendStreamEvent 非阻塞发送流事件，避免客户端断连后 goroutine 卡住
func sendStreamEvent(events chan<- *StreamEvent, evt *StreamEvent) {
	select {
	case events <- evt:
	default:
	}
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
