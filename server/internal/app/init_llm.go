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

// InitLLM 创建 LLM 客户端（Flash + Strong），并包裹监控层 + 成本追踪层。
// llm_bg.model 配置后，额外创建后台客户端对（BgFlash/BgStrong），
// 供后台任务使用；未配置时 Bg 回退为主客户端。
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

	// 后台客户端对：llm_bg.model 为空 → 复用主客户端（零配置环境行为不变）
	bgFlash, bgStrong := llmClient, strongClient
	if cfg.LLMBg.Model != "" {
		bgClient, err := newClient(cfg.LLMBg)
		if err != nil {
			log.Fatal("create bg llm client:", err)
		}
		bgFlash = monitor.WrapLLMClient(bgClient, "background/flash")
		bgFlash = llmcost.WrapLLMClient(bgFlash, costRecorder)
		bgFlash = llm.WrapRetry(bgFlash)

		if bgStrongClient, _ := newStrongClient(cfg.LLMBg); bgStrongClient != nil {
			bgStrong = monitor.WrapLLMClient(bgStrongClient, "background/pro")
			bgStrong = llmcost.WrapLLMClient(bgStrong, costRecorder)
			bgStrong = llm.WrapRetry(bgStrong)
		}
	}

	return &LLMClients{Flash: llmClient, Strong: strongClient, BgFlash: bgFlash, BgStrong: bgStrong}
}
