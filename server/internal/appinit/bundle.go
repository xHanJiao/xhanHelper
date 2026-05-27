package appinit

import (
	"context"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/tool"
)

// ToolsBundle 工具层初始化的产物
type ToolsBundle struct {
	Registry        *tool.Registry
	ReflectionReg   *tool.Registry
	MCPManager      *mcp.Manager
	CommitmentTimer *agent.CommitmentTimer
	MemExtractor    *agent.MemoryExtractor
}

// AgentBundle Agent 层初始化的产物
type AgentBundle struct {
	Orch           *agent.Orchestrator
	CronRunner     *agent.CronRunner
	ReflectionOrch *agent.ReflectionOrchestrator
	BGCtx          context.Context
	BGCancel       context.CancelFunc
}
