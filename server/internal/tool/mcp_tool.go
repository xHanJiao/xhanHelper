package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/mcp"
)

// MCPToolCaller — MCP Manager 的最小接口，方便测试
type MCPToolCaller interface {
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (json.RawMessage, error)
	EnsureRunning(ctx context.Context, name string) (*mcp.MCPServer, error)
	GetToolsSnapshot(name string) []mcp.ToolDef
	GetAllSnapshots() map[string][]mcp.ToolDef
	GetConfiguredServers() []mcp.ServerConfig
}

// MCPToolAdapter — 将 MCP server 的一个工具适配为 Tool 接口
type MCPToolAdapter struct {
	serverName  string
	toolName    string
	description string
	functions   []FuncSchema
	caller      MCPToolCaller
}

// MCPProxyTool — MCP server 占位工具（未启动时注册，触发懒启动）
type MCPProxyTool struct {
	serverName  string
	description string
	caller      MCPToolCaller
}

// NewMCPProxyTool 创建占位工具，server 未启动前用于 tool_retriever 发现
func NewMCPProxyTool(cfg mcp.ServerConfig, caller MCPToolCaller) *MCPProxyTool {
	return &MCPProxyTool{
		serverName:  cfg.Name,
		description: fmt.Sprintf("[MCP:%s] %s（待启动）", cfg.Name, cfg.Description),
		caller:      caller,
	}
}

func (t *MCPProxyTool) Name() string        { return t.serverName }
func (t *MCPProxyTool) Description() string { return t.description }
func (t *MCPProxyTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "invoke",
		Description: "调用 " + t.serverName + " 服务（首次使用会自动启动）",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string", "description": "查询内容"}}},
	}}
}

func (t *MCPProxyTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	log.Printf("[mcp-proxy] %s: triggering lazy start", t.serverName)
	_, err := t.caller.EnsureRunning(ec.Ctx, t.serverName)
	if err != nil {
		return &Result{Success: false, Error: "mcp_pending_retry: " + err.Error()}
	}
	// server 就绪，下轮 LLM 重试时会用真正的 MCPToolAdapter
	return &Result{Success: false, Error: "mcp_pending_retry: " + t.serverName + " 服务已就绪，请重试"}
}

// NewMCPToolAdapter 创建适配器
func NewMCPToolAdapter(serverName string, def mcp.ToolDef, caller MCPToolCaller) *MCPToolAdapter {
	funcs := []FuncSchema{
		{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.InputSchema,
		},
	}
	return &MCPToolAdapter{
		serverName:  serverName,
		toolName:    def.Name,
		description: fmt.Sprintf("[MCP:%s] %s", serverName, def.Description),
		functions:   funcs,
		caller:      caller,
	}
}

func (t *MCPToolAdapter) Name() string            { return t.toolName }
func (t *MCPToolAdapter) Description() string     { return t.description }
func (t *MCPToolAdapter) Functions() []FuncSchema { return t.functions }

func (t *MCPToolAdapter) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var argMap map[string]interface{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argMap); err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("invalid args: %v", err)}
		}
	}

	result, err := t.caller.CallTool(ec.Ctx, t.serverName, t.toolName, argMap)
	if err != nil {
		log.Printf("[mcp-tool] %s.%s call error: %v", t.serverName, t.toolName, err)
		return &Result{Success: false, Error: "mcp_pending_retry: " + err.Error()}
	}

	// 解析 MCP 返回的 content 数组格式
	var mcpResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &mcpResult); err != nil {
		// 返回原始结果
		return &Result{Success: true, Data: result}
	}

	if mcpResult.IsError {
		msg := "unknown error"
		if len(mcpResult.Content) > 0 {
			msg = mcpResult.Content[0].Text
		}
		return &Result{Success: false, Error: msg}
	}

	// 提取文本内容
	if len(mcpResult.Content) == 1 && mcpResult.Content[0].Type == "text" {
		return &Result{Success: true, Data: json.RawMessage(fmt.Sprintf(`"%s"`, mcpResult.Content[0].Text))}
	}

	data, _ := json.Marshal(mcpResult.Content)
	return &Result{Success: true, Data: data}
}
