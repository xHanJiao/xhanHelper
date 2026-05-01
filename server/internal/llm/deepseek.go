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

// DeepSeekClient — DeepSeek API 客户端 (OpenAI 兼容，原生 function calling)
type DeepSeekClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// BuildSystemPrompt DeepSeek 支持原生 function calling，简要提示即可
func (c *DeepSeekClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	if len(tools) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n你可以使用以下函数。当用户请求涉及记账操作（记录、查询、修改、删除账单）时，必须调用对应函数，不要用纯文本模拟执行结果。不确定分类时反问用户。\n")
	for _, t := range tools {
		for _, fn := range t.Functions {
			b.WriteString(fmt.Sprintf("- %s: %s\n", fn.Name, fn.Description))
		}
	}
	return b.String()
}

func NewDeepSeek(cfg config.LLMConfig) *DeepSeekClient {
	return &DeepSeekClient{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *DeepSeekClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	fullText, err := c.callAPI(ctx, req)
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

func (c *DeepSeekClient) ChatSync(ctx context.Context, req *ChatRequest) (string, error) {
	return c.callAPI(ctx, req)
}

func (c *DeepSeekClient) callAPI(ctx context.Context, req *ChatRequest) (string, error) {
	body := c.buildBody(req)
	url := c.baseURL + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return "", err
	}
	c.setHeaders(httpReq)

	log.Printf("[deepseek] request: model=%s, msgs=%d, tools=%d, last_msg=%.100s", c.model, len(req.Messages)+1, len(req.Tools), req.Messages[len(req.Messages)-1].Content)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("[deepseek] http error: %v", err)
		return "", fmt.Errorf("deepseek request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		log.Printf("[deepseek] status %d: %s", resp.StatusCode, string(respBody[:min(300, len(respBody))]))
		return "", fmt.Errorf("deepseek status %d: %s", resp.StatusCode, string(respBody[:min(300, len(respBody))]))
	}

	result, err := parseDeepSeekResponse(respBody)
	if err != nil {
		log.Printf("[deepseek] parse error: %v", err)
		return "", err
	}
	tcCount := strings.Count(result, "tool_calls")
	log.Printf("[deepseek] response: len=%d, tool_calls=%d, preview=%s", len(result), tcCount, result[:min(200, len(result))])
	return result, nil
}

func (c *DeepSeekClient) buildBody(req *ChatRequest) io.Reader {
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

	// 原生 function calling: 把 Function 定义转为 OpenAI tools 格式
	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, 0)
		for _, t := range req.Tools {
			for _, fn := range t.Functions {
				tools = append(tools, map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        t.Name + "__" + fn.Name, // "bookkeeping__create_bill"
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

func (c *DeepSeekClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// parseChatResponse 解析纯文本返回 (OpenAI 格式)
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

// parseDeepSeekResponse 解析原生 function calling 返回
// 如果 LLM 返回 tool_calls，转成 {"reply":"","tool_calls":[...]} JSON 格式
// 如果 LLM 返回纯文本，直接返回文本
func parseDeepSeekResponse(body []byte) (string, error) {
	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
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
		return "", fmt.Errorf("no choices")
	}

	msg := result.Choices[0].Message

	// 如果有原生 tool_calls，转成 orchestrator 期望的 JSON 格式
	if len(msg.ToolCalls) > 0 {
		toolCalls := make([]map[string]interface{}, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			// 从 "bookkeeping__create_bill" 拆分出 tool 和 function
			toolName := tc.Function.Name
			funcName := tc.Function.Name
			if idx := strings.LastIndex(tc.Function.Name, "__"); idx > 0 {
				toolName = tc.Function.Name[:idx]
				funcName = tc.Function.Name[idx+2:]
			}

			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]interface{}{}
			}
			toolCalls[i] = map[string]interface{}{
				"tool":     toolName,
				"function": funcName,
				"args":     args,
			}
		}

		reply := msg.Content
		if reply == "" {
			reply = "正在处理..."
		}

		output := map[string]interface{}{
			"reply":      reply,
			"tool_calls": toolCalls,
		}
		b, _ := json.Marshal(output)
		return string(b), nil
	}

	// 纯文本回复
	return msg.Content, nil
}
