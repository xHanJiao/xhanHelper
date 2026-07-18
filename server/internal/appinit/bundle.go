package appinit

import (
	"context"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/replay"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/tool"
)

// ToolsBundle 工具层初始化的产物
type ToolsBundle struct {
	Registry            *tool.Registry
	ReflectionReg       *tool.Registry
	MCPManager          *mcp.Manager
	MemExtractor        *agent.MemoryExtractor
	MissionEmbedder     *agent.MissionEmbedder
	ConceptEmbedService *agent.ConceptEmbedService
}

// AgentBundle Agent 层初始化的产物
type AgentBundle struct {
	Orch              *agent.Orchestrator
	CronRunner        *agent.CronRunner
	ReflectionOrch    *agent.ReflectionOrchestrator
	MissionRunner     *agent.MissionRunner
	MissionDiscoverer *agent.MissionDiscoverer
	ReplayRunner      *replay.Runner
	ClientBridge      *agent.ClientToolBridge
	BGCtx             context.Context
	BGCancel          context.CancelFunc
}
