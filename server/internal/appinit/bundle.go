package appinit

import (
	"context"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/replay"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// ToolsBundle 工具层初始化的产物
type ToolsBundle struct {
	Registry            *tool.Registry
	ReflectionReg       *tool.Registry
	MCPManager          *mcp.Manager
	MemExtractor        *agent.MemoryExtractor
	ConceptEmbedService *agent.ConceptEmbedService
	EntityPurger        *service.EntityPurger
}

// AgentBundle Agent 层初始化的产物
type AgentBundle struct {
	Orch           *agent.Orchestrator
	CronRunner     *agent.CronRunner
	ReflectionOrch *agent.ReflectionOrchestrator
	ReplayRunner   *replay.Runner
	ClientBridge   *agent.ClientToolBridge
	Destroyer      *agent.EphemeralDestroyer
	BGCtx          context.Context
	BGCancel       context.CancelFunc
}
