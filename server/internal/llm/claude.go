package llm

import (
	"context"
	"fmt"

	"xhan-helper/internal/config"
)

// ClaudeClient — Claude API 客户端（占位，后续实现）
type ClaudeClient struct {
	apiKey  string
	model   string
	baseURL string
}

// NewClaude 创建 Claude 客户端
func NewClaude(cfg config.LLMConfig) *ClaudeClient {
	return &ClaudeClient{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		baseURL: cfg.BaseURL,
	}
}

// Chat 流式对话（占位）
func (c *ClaudeClient) Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error) {
	return nil, fmt.Errorf("claude client not implemented yet")
}

// ChatSync 非流式对话（占位）
func (c *ClaudeClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error) {
	return Message{}, fmt.Errorf("claude client not implemented yet")
}

func (c *ClaudeClient) BuildSystemPrompt(base string, tools []ToolDefinition) string {
	return base
}
