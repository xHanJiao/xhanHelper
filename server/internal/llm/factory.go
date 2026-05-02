package llm

import (
	"fmt"

	"xhan-helper/internal/config"
)

// NewClient 根据配置创建对应的 LLM 弱模型客户端（Flash）
func NewClient(cfg config.LLMConfig) (LLMClient, error) {
	switch cfg.Provider {
	case "doubao":
		return NewDoubao(cfg), nil
	case "deepseek":
		return NewFlashClient(cfg), nil
	case "claude":
		return NewClaude(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported llm provider: %s", cfg.Provider)
	}
}

// NewStrongClient 创建强模型客户端（用于深度思考模式）
// 如果未配置 StrongModel，返回 nil
func NewStrongClient(cfg config.LLMConfig) (LLMClient, error) {
	switch cfg.Provider {
	case "deepseek":
		model := cfg.StrongModel
		if model == "" {
			model = cfg.Model // fallback
		}
		return NewProClient(cfg, model), nil
	case "doubao":
		return nil, nil // 豆包不支持思考模式
	default:
		return nil, nil
	}
}
