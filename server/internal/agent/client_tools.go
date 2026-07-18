package agent

import (
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

// ClientTool — 客户端（Mac App）上报的本地 MCP 工具定义
type ClientTool struct {
	ServerName  string                 `json:"server_name"` // MCP server 分组名，如 "browser"
	Name        string                 `json:"name"`        // MCP 原始工具名，如 "browser_navigate"
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // JSON Schema，透传
}

// maxClientTools — 单次请求客户端工具数量上限，防 prompt 膨胀
const maxClientTools = 50

// buildClientToolDefs 校验客户端工具并按 server_name 分组为 llm.ToolDefinition。
// 校验规则（不合格跳过 + 日志）：
//   - server_name / name 为空
//   - server_name 与 registry 现有工具名冲突（防劫持服务端工具）
//   - server_name 或 name 含 "__"（破坏扁平名 LastIndex 拆分）
//   - 总数超过 maxClientTools 的部分丢弃
func buildClientToolDefs(clientTools []ClientTool, registry *tool.Registry) []llm.ToolDefinition {
	if len(clientTools) == 0 {
		return nil
	}
	if len(clientTools) > maxClientTools {
		log.Printf("[orch] client_tools truncated: %d > %d", len(clientTools), maxClientTools)
		clientTools = clientTools[:maxClientTools]
	}

	grouped := make(map[string][]llm.FuncDef)
	var order []string
	for _, ct := range clientTools {
		if ct.ServerName == "" || ct.Name == "" {
			log.Printf("[orch] client tool skipped: empty server_name or name (%q.%q)", ct.ServerName, ct.Name)
			continue
		}
		if strings.Contains(ct.ServerName, "__") || strings.Contains(ct.Name, "__") {
			log.Printf("[orch] client tool skipped: name contains reserved separator __ (%s.%s)", ct.ServerName, ct.Name)
			continue
		}
		if registry.Has(ct.ServerName) {
			log.Printf("[orch] client tool skipped: server_name conflicts with server tool (%s)", ct.ServerName)
			continue
		}
		if _, seen := grouped[ct.ServerName]; !seen {
			order = append(order, ct.ServerName)
		}
		grouped[ct.ServerName] = append(grouped[ct.ServerName], llm.FuncDef{
			Name:        ct.Name,
			Description: ct.Description,
			Parameters:  ct.Parameters,
		})
	}

	defs := make([]llm.ToolDefinition, 0, len(order))
	for _, name := range order {
		defs = append(defs, llm.ToolDefinition{
			Name:        name,
			Description: "Mac 客户端本地工具（" + name + "）",
			Functions:   grouped[name],
		})
	}
	return defs
}

// clientToolIndex 构建 server_name → true 的分发索引（复用 buildClientToolDefs 的校验口径）。
// 返回 nil 表示本次请求没有客户端工具。
func clientToolIndex(clientTools []ClientTool, registry *tool.Registry) map[string]bool {
	defs := buildClientToolDefs(clientTools, registry)
	if len(defs) == 0 {
		return nil
	}
	idx := make(map[string]bool, len(defs))
	for _, d := range defs {
		idx[d.Name] = true
	}
	return idx
}
