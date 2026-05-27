package app

import (
	"log"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/monitor"
)

// LLMClientFactory 用于创建 LLM 客户端，可注入 mock 测试
type LLMClientFactory func(cfg config.LLMConfig) (llm.LLMClient, error)

// InitLLM 创建 LLM 客户端（Flash + Strong），并包裹监控层
func InitLLM(cfg *config.Config, newClient, newStrongClient LLMClientFactory) *LLMClients {
	llmClient, err := newClient(cfg.LLM)
	if err != nil {
		log.Fatal("create llm client:", err)
	}
	llmClient = monitor.WrapLLMClient(llmClient, "orchestrator/flash")

	strongClient, _ := newStrongClient(cfg.LLM)
	if strongClient != nil {
		log.Printf("[llm] strong model available: %s", cfg.LLM.StrongModel)
		strongClient = monitor.WrapLLMClient(strongClient, "orchestrator/pro")
	}

	return &LLMClients{Flash: llmClient, Strong: strongClient}
}
