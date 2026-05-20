package app

import "xhan-helper/internal/llm"

// LLMClients 封装 Flash 和 Pro 两个 LLM 客户端。
// Flash 用于日常对话快速响应，Strong 用于 DeepSeek 思考模式深度推理（可选）。
type LLMClients struct {
	Flash  llm.LLMClient
	Strong llm.LLMClient // 可选，Pro 模型
}
