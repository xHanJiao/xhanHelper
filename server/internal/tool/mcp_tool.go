package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/mcp"
)

// MCPToolCaller — MCP Manager 暴露给 Proxy 的最小接口
type MCPToolCaller interface {
	EnsureRunning(ctx context.Context, name string) (*mcp.MCPServer, error)
	CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (json.RawMessage, error)
	GetToolsSnapshot(name string) []mcp.ToolDef
}

// MCPProxyTool — MCP server 的永久代理工具
//   - 未启动时：暴露 invoke()，触发懒启动后转发到真实工具
//   - 已启动时：动态暴露真实工具函数，LLM 可直接按真实 schema 调用
type MCPProxyTool struct {
	serverName  string
	description string
	caller      MCPToolCaller
}

func NewMCPProxyTool(cfg mcp.ServerConfig, caller MCPToolCaller) *MCPProxyTool {
	return &MCPProxyTool{
		serverName:  cfg.Name,
		description: fmt.Sprintf("[MCP:%s] %s", cfg.Name, cfg.Description),
		caller:      caller,
	}
}

func (t *MCPProxyTool) Name() string        { return t.serverName }
func (t *MCPProxyTool) Description() string { return t.description }

// Functions 动态化：如果 server 已就绪，暴露真实工具函数；否则只暴露 invoke
func (t *MCPProxyTool) Functions() []FuncSchema {
	funcs := []FuncSchema{{
		Name:        "invoke",
		Description: "调用 " + t.serverName + " 服务（首次使用会自动启动）",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "查询内容"},
			},
		},
	}}

	tools := t.caller.GetToolsSnapshot(t.serverName)
	for _, td := range tools {
		funcs = append(funcs, FuncSchema{
			Name:        td.Name,
			Description: td.Description,
			Parameters:  td.InputSchema,
		})
	}
	return funcs
}

func (t *MCPProxyTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var argMap map[string]interface{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argMap); err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("invalid args: %v", err)}
		}
	}

	// 懒启动
	_, err := t.caller.EnsureRunning(ec.Ctx, t.serverName)
	if err != nil {
		return &Result{Success: false, Error: "mcp_pending_retry: " + err.Error()}
	}

	tools := t.caller.GetToolsSnapshot(t.serverName)
	if len(tools) == 0 {
		return &Result{Success: false, Error: "server ready but no tools available"}
	}

	// 如果是 invoke，转发到第一个真实工具；否则直接调指定函数
	realName := funcName
	if funcName == "invoke" {
		realName = tools[0].Name
	}

	log.Printf("[mcp-proxy] %s: %s(%v) → %s", t.serverName, funcName, argMap, realName)
	result, err := t.caller.CallTool(ec.Ctx, t.serverName, realName, argMap)
	if err != nil {
		log.Printf("[mcp-proxy] %s call error: %v", t.serverName, err)
		return &Result{Success: false, Error: err.Error()}
	}

	return parseMCPResult(result)
}

// parseMCPResult 解析 MCP tools/call 返回的 content 数组格式
func parseMCPResult(result json.RawMessage) *Result {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		raw := string(result)
		if len(raw) > 300 {
			raw = raw[:300]
		}
		log.Printf("[mcp] parse result: not content format, returning raw (err=%v, raw=%s)", err, raw)
		return &Result{Success: true, Data: result}
	}

	if r.IsError {
		msg := "unknown error"
		if len(r.Content) > 0 {
			msg = r.Content[0].Text
		}
		return &Result{Success: false, Error: msg}
	}

	if len(r.Content) == 0 {
		raw := string(result)
		if len(raw) > 300 {
			raw = raw[:300]
		}
		log.Printf("[mcp] parse result: empty content array, returning raw (raw=%s)", raw)
		return &Result{Success: true, Data: result}
	}

	if len(r.Content) == 1 && r.Content[0].Type == "text" {
		data, _ := json.Marshal(r.Content[0].Text)
		return &Result{Success: true, Data: data}
	}

	data, _ := json.Marshal(r.Content)
	return &Result{Success: true, Data: data}
}
