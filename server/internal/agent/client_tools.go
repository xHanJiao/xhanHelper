package agent

import (
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

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

// —— 客户端工具结果预算链（单一来源，勿在别处再定义大小魔数）——
//
//	DB 硬底线:  messages.content TEXT（MySQL 64KB 字节）
//	  ↓ 留 3x 余量（JSON 转义膨胀 + 多字节字符 + 消息信封）
//	注入/落库预算: clientToolInjectBudgetBytes = 20000 字节
//	  ↓ 随 client_tool_call 事件下发（result_budget_bytes 字段）
//	客户端分页:  每页序列化后 ≤ 预算，超大结果走 result_read_more 续读
//	  ↓ 传输护栏（独立维度，防恶意/失控 payload）
//	HTTP 上限:  controller.maxClientToolResultBytes = 256KB
//
// 正常流程下客户端按下发预算分页，服务端截断兜底永不触发；
// 触发即说明客户端未按协议（旧版本客户端），normalizeClientToolData 会打告警日志。
const clientToolInjectBudgetBytes = 20000

// maxClientToolErrorChars — 客户端回传 error 字段的截断上限（字节）。
// 仅作为防御性上限，正常客户端错误信息不应超此长度。
const maxClientToolErrorChars = 2000

// clientToolHeartbeatInterval — 等待客户端执行期间的 SSE 心跳间隔，防止长时间静默被代理掐断
const clientToolHeartbeatInterval = 15 * time.Second

// buildClientToolDefs 校验客户端工具并按 server_name 分组为 llm.ToolDefinition。
// 校验规则（不合格跳过 + 日志）：
//   - server_name / name 为空
//   - server_name 与 registry 现有工具名冲突（防劫持服务端工具）
//   - server_name 或 name 含 "__"（破坏扁平名 LastIndex 拆分）
//   - server_name+name 重复（防扁平名重复导致 LLM API 报错）
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
	seen := make(map[string]bool)
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
		flatName := ct.ServerName + "__" + ct.Name
		if seen[flatName] {
			log.Printf("[orch] client tool skipped: duplicate name (%s.%s)", ct.ServerName, ct.Name)
			continue
		}
		seen[flatName] = true
		if _, ok := grouped[ct.ServerName]; !ok {
			order = append(order, ct.ServerName)
		}
		grouped[ct.ServerName] = append(grouped[ct.ServerName], llm.FuncDef{
			Name:        ct.Name,
			Description: ct.Description,
			Parameters:  ct.Parameters,
		})
	}

	// 按 server_name 排序 + 组内函数按名排序，保证同集合请求的 tools 数组字节稳定
	// （客户端上报顺序可能随连接状态变化，排序后与缓存前缀对齐）
	sort.Strings(order)
	defs := make([]llm.ToolDefinition, 0, len(order))
	for _, name := range order {
		funcs := grouped[name]
		sort.Slice(funcs, func(i, j int) bool { return funcs[i].Name < funcs[j].Name })
		defs = append(defs, llm.ToolDefinition{
			Name:        name,
			Description: "Mac 客户端本地工具（" + name + "）",
			Functions:   funcs,
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

// normalizeClientToolData 保证客户端回传数据可安全序列化并控制体积。
// 正常流程下客户端按下发预算分页，此兜底应永不触发；
// 触发截断时打告警日志（说明客户端未按协议或为旧版本）。
//   - 超过 clientToolInjectBudgetBytes：截断后包装为 JSON 字符串（截断会破坏 JSON 结构，必须整体转义）
//   - 非法 JSON（客户端回传纯文本）：包装为 JSON 字符串
//   - 其余原样透传
func normalizeClientToolData(data json.RawMessage) json.RawMessage {
	if len(data) > clientToolInjectBudgetBytes {
		log.Printf("[orch] client tool result exceeded injection budget (%d > %d bytes) — fallback truncation triggered. The client should paginate via result_read_more.", len(data), clientToolInjectBudgetBytes)
		// 字节截断可能切在多字节字符中间，清理边界上的非法 UTF-8 再转义
		truncated := strings.ToValidUTF8(truncateToolResult(string(data), clientToolInjectBudgetBytes), "")
		wrapped, _ := json.Marshal(truncated)
		return wrapped
	}
	if !json.Valid(data) {
		wrapped, _ := json.Marshal(string(data))
		return wrapped
	}
	return data
}
