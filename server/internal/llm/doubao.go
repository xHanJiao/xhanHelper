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

const defaultSystemPrompt = `你是 xhanHelper，一个个人 AI 助手。
1. 简洁与结构：用最少的词把事说清楚；第一句给结论，不写开场铺垫与收尾总结
2. 诚实：不知道就说不知道，不要编造
3. 追问：意图不明确时追问关键信息，一次只问最少必要的那几项`

// DoubaoClient — 豆包 API 客户端
type DoubaoClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

func (c *DoubaoClient) ModelName() string { return c.model }

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
	msg, err := c.callChatAPI(ctx, req)
	if err != nil {
		return nil, err
	}

	events := make(chan *StreamEvent, 256)
	go func() {
		defer close(events)
		for _, r := range msg.Content {
			select {
			case <-ctx.Done():
				return
			default:
			}
			sendStreamEvent(events, &StreamEvent{Type: "text_delta", Content: string(r)})
		}
		sendStreamEvent(events, &StreamEvent{
			Type:         "done",
			RequestJSON:  msg.RequestJSON,
			ResponseJSON: msg.ResponseJSON,
		})
	}()
	return events, nil
}

// ChatSync 非流式对话 — 直接返回完整回复
func (c *DoubaoClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	return c.callChatAPI(ctx, req)
}

func (c *DoubaoClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (Message, error) {
	return Message{}, fmt.Errorf("ChatWithRawJSON not implemented for doubao")
}

// callChatAPI 调用豆包 /chat/completions API (OpenAI 兼容)
func (c *DoubaoClient) callChatAPI(ctx context.Context, req *ChatRequest) (Message, error) {
	reqBytes, body := c.buildBody(req)

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return Message{}, err
	}
	c.setHeaders(httpReq)

	log.Printf("[llm] request: model=%s, msgs=%d, url=%s", c.model, len(req.Messages)+1, url)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("[llm] http error: %v", err)
		return Message{}, fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[llm] read body error: %v", err)
		return Message{}, err
	}

	if resp.StatusCode != 200 {
		log.Printf("[llm] status %d: %s", resp.StatusCode, string(respBytes))
		return Message{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, string(respBytes))
	}

	content, err := parseChatResponse(respBytes)
	if err != nil {
		log.Printf("[llm] parse error: %v, body=%s", err, string(respBytes))
		return Message{}, err
	}
	log.Printf("[llm] response: len=%d", len(content))

	reqJSONStr := string(reqBytes)
	respJSONStr := string(respBytes)
	return Message{
		Role:         "assistant",
		Content:      content,
		RequestJSON:  &reqJSONStr,
		ResponseJSON: &respJSONStr,
	}, nil
}

// buildBody 构造请求体
func (c *DoubaoClient) buildBody(req *ChatRequest) ([]byte, io.Reader) {
	messages := make([]map[string]string, 0)

	sp := req.SystemPrompt
	if sp == "" {
		sp = defaultSystemPrompt
	}
	messages = append(messages, map[string]string{"role": "system", "content": sp})

	for _, m := range req.Messages {
		if m.Content == "" && len(m.ToolCalls) == 0 && m.ToolCallID == "" {
			continue
		}
		role := m.Role
		content := m.Content
		if role == "tool" {
			role = "user"
		}
		messages = append(messages, map[string]string{"role": role, "content": content})
	}

	payload := map[string]interface{}{
		"model":    c.model,
		"messages": messages,
	}

	b, _ := json.Marshal(payload)
	return b, bytes.NewReader(b)
}

// BuildSystemPrompt 豆包不支持原生 function calling，需要 prompt engineering
func (c *DoubaoClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString(fmt.Sprintf("\n当前时间: %s\n", time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")))
	if len(tools) == 0 {
		return b.String()
	}
	b.WriteString("\n\n===== 工具使用 =====\n")
	for _, t := range tools {
		for _, fn := range t.Functions {
			b.WriteString(fmt.Sprintf("%s: %s\n", fn.Name, fn.Description))
			if props, ok := fn.Parameters["properties"].(map[string]interface{}); ok {
				required := make(map[string]bool)
				if reqs, ok := fn.Parameters["required"].([]interface{}); ok {
					for _, r := range reqs {
						required[r.(string)] = true
					}
				}
				for key, val := range props {
					prop := val.(map[string]interface{})
					typ, _ := prop["type"].(string)
					desc, _ := prop["description"].(string)
					req := ""
					if required[key] {
						req = " (必填)"
					}
					b.WriteString(fmt.Sprintf("  - %s (%s%s): %s\n", key, typ, req, desc))
					if enumVals, ok := prop["enum"].([]interface{}); ok {
						vals := make([]string, len(enumVals))
						for i, v := range enumVals {
							vals[i] = fmt.Sprint(v)
						}
						b.WriteString(fmt.Sprintf("    可选值: %s\n", strings.Join(vals, "/")))
					}
				}
			}
		}
	}
	b.WriteString("===== 输出格式 =====\n")
	b.WriteString("每次回复都必须输出 JSON 对象: {\"reply\":\"给用户的回复\",\"tool_calls\":[{\"tool\":\"<工具名>\",\"function\":\"<函数名>\",\"args\":{...}}]}。不需要工具时 tool_calls 为空数组 []。**严禁在 JSON 前后添加任何文字。**\n")
	b.WriteString("不确定分类时反问用户，不要瞎猜。\n\n")
	b.WriteString("===== 思考方式 =====\n")
	b.WriteString("面对复杂任务时，按以下步骤思考和行动：\n")
	b.WriteString("[思考] 分析用户意图，确定需要哪些信息\n")
	b.WriteString("[计划] 列出需要的操作步骤\n")
	b.WriteString("[行动] 调用函数获取信息\n")
	b.WriteString("[反思] 根据结果判断是否需要调整\n")
	b.WriteString("[回复] 给出自然语言回复\n")
	b.WriteString("简单的记账操作（一句话就能完成）不需要展示思考过程。\n")
	return b.String()
}

func (c *DoubaoClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}
