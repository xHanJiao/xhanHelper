package app

import "xhan-helper/internal/llm"

// LLMClients 封装 Flash 和 Pro 两个 LLM 客户端。
// Flash 用于日常对话快速响应，Strong 用于 DeepSeek 思考模式深度推理（可选）。
// BgFlash / BgStrong 供后台任务使用；llm_bg 未配置时与 Flash / Strong 是同一个实例。
type LLMClients struct {
	Flash    llm.LLMClient
	Strong   llm.LLMClient // 可选，Pro 模型
	BgFlash  llm.LLMClient // 后台任务 Flash
	BgStrong llm.LLMClient // 后台任务 Pro
}
