package app

import (
	"log"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/monitor"
)

// LLMClientFactory 用于创建 LLM 客户端，可注入 mock 测试
type LLMClientFactory func(cfg config.LLMConfig) (llm.LLMClient, error)

// InitLLM 创建 LLM 客户端（Flash + Strong），并包裹监控层 + 成本追踪层
func InitLLM(cfg *config.Config, newClient, newStrongClient LLMClientFactory, costRecorder llmcost.Recorder) *LLMClients {
	llmClient, err := newClient(cfg.LLM)
	if err != nil {
		log.Fatal("create llm client:", err)
	}
	llmClient = monitor.WrapLLMClient(llmClient, "orchestrator/flash")
	llmClient = llmcost.WrapLLMClient(llmClient, costRecorder)

	strongClient, _ := newStrongClient(cfg.LLM)
	if strongClient != nil {
		log.Printf("[llm] strong model available: %s", cfg.LLM.StrongModel)
		strongClient = monitor.WrapLLMClient(strongClient, "orchestrator/pro")
		strongClient = llmcost.WrapLLMClient(strongClient, costRecorder)
	}

	return &LLMClients{Flash: llmClient, Strong: strongClient}
}
