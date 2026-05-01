package llm

import (
	"fmt"

	"xhan-helper/internal/config"
)

// NewClient 根据配置创建对应的 LLM 客户端
func NewClient(cfg config.LLMConfig) (LLMClient, error) {
	switch cfg.Provider {
	case "doubao":
		return NewDoubao(cfg), nil
	case "deepseek":
		return NewDeepSeek(cfg), nil
	case "claude":
		return NewClaude(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported llm provider: %s", cfg.Provider)
	}
}
