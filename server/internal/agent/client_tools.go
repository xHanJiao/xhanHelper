package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

// ClientTool — 客户端（Mac App）上报的本地 MCP 工具定义
type ClientTool struct {
	ServerName  string                 `json:"server_name"` // MCP server 分组名，如 "browser"
	Name        string                 `json:"name"`        // MCP 原始工具名，如 "browser_navigate"
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`   // JSON Schema，透传
	IsReadOnly  bool                   `json:"is_read_only"` // 只读标记。
	// 注（2026-08-14）：plan 模式已收紧为全量禁用客户端工具，此字段不再参与 plan 拦截，
	// 仅保留供未来"只读浏览模式"复用。客户端工具在客户端本地执行，服务端无法拦截执行本身，
	// 但分派前（executeConcurrently）会按会话模式拦断。agent_browser 因操作难以精确界定读写，
	// 整体标记只读（历史决策特例：调研场景需要 + 风险由客户端 riskLevel 兜底）。
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

// clientToolGroupMap 构建 server_name → 函数名集合 索引（makeExposeFn 校验用）。
// 用原始上报数据即可：buildClientToolDefs 会拒绝的非法条目（__ 分隔符/与 registry 冲突），
// 即使被暴露，filterExposedFuncDefs 也匹配不到定义，自然不注入——无害发散，不额外校验。
func clientToolGroupMap(infos []tool.ClientToolInfo) map[string]map[string]bool {
	if len(infos) == 0 {
		return nil
	}
	m := make(map[string]map[string]bool)
	for _, ct := range infos {
		if ct.ServerName == "" || ct.Name == "" {
			continue
		}
		if m[ct.ServerName] == nil {
			m[ct.ServerName] = make(map[string]bool)
		}
		m[ct.ServerName][ct.Name] = true
	}
	return m
}

// clientToolCatalog 生成客户端工具目录文本（组名+描述+函数名，无 schema）。
// 复用 buildClientToolDefs 的校验/排序，保证目录与可注入定义一致、字节稳定。
// 客户端工具不再无条件下发后，这是 LLM 发现客户端工具的唯一渠道。defs 为空返回 ""。
func clientToolCatalog(clientTools []ClientTool, registry *tool.Registry) string {
	defs := buildClientToolDefs(clientTools, registry)
	if len(defs) == 0 {
		return ""
	}
	var buf bytes.Buffer
	buf.WriteString("客户端工具组（Mac 客户端在线时可用，同样按需启用：先调用 expose_tools.enable，启用后下一轮即可直接调用）：\n")
	for _, d := range defs {
		buf.WriteString(fmt.Sprintf("- %s: %s\n", d.Name, d.Description))
		names := make([]string, len(d.Functions))
		for i, f := range d.Functions {
			names[i] = f.Name
		}
		buf.WriteString(fmt.Sprintf("  函数: %s\n", strings.Join(names, ", ")))
	}
	return buf.String()
}

// buildAskUserDef 构造 ui server 的 ask_user 工具定义（子循环注入用）。
// ask_user 是"用户介入"特殊客户端工具：仅 subAgent 可见，主循环 defs 不含它——
// 由 RunSubAgent 注入 SubAgentExt.ClientTools，客户端 handleClientToolCall 识别
// ui__ask_user 走 AskUserBar 分支（弹条 + 系统通知 + 等待用户 ok/deny）。
func buildAskUserDef() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "ui",
		Description: "Mac 客户端 UI 工具",
		Functions: []llm.FuncDef{
			{
				Name:        "ask_user",
				Description: "必须用户介入时调用：请求用户处理浏览器页面上的问题（如验证码、登录墙）。用户确认后继续当前操作；用户跳过或超时后必须换方案（换来源/跳过该页），不得重复请求同一动作。一次只请求一个动作，message 必须指明哪个页面/tab。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"message": map[string]interface{}{
							"type":        "string",
							"description": "展示给用户的话术，须指明页面/tab（如『XX 网站的验证码，请处理』）",
						},
					},
					"required": []interface{}{"message"},
				},
			},
		},
	}
}

// appendAskUserTool 客户端在线（ClientTools 非空）时注入 ui 工具（ask_user）。
// 同时返回更新后的 tools（LLM 可见 def）与 clientTools（MiniReAct clientIdx 分发索引）——
// 两者缺一不可：只加 def 会让 LLM 能调用但路由不到客户端（"未找到工具 'ui'"）。
func appendAskUserTool(tools []llm.ToolDefinition, clientTools []ClientTool) ([]llm.ToolDefinition, []ClientTool) {
	if len(clientTools) == 0 {
		return tools, clientTools
	}
	tools = append(tools, buildAskUserDef())
	clientTools = append(clientTools, ClientTool{
		ServerName:  "ui",
		Name:        "ask_user",
		Description: "请求用户介入（验证码/登录墙等）",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message": map[string]interface{}{
					"type":        "string",
					"description": "展示给用户的话术",
				},
			},
			"required": []interface{}{"message"},
		},
		IsReadOnly: true,
	})
	return tools, clientTools
}

// toToolClientInfos 将 agent.ClientTool 转换为 tool.ClientToolInfo（ExecuteContext 注入用）。
// 普通工具无需使用；task 工具通过 ExecuteContext.ClientTools 透传给子循环。
func toToolClientInfos(clientTools []ClientTool) []tool.ClientToolInfo {
	if len(clientTools) == 0 {
		return nil
	}
	infos := make([]tool.ClientToolInfo, len(clientTools))
	for i, ct := range clientTools {
		infos[i] = tool.ClientToolInfo{
			ServerName:  ct.ServerName,
			Name:        ct.Name,
			Description: ct.Description,
			Parameters:  ct.Parameters,
			IsReadOnly:  ct.IsReadOnly,
		}
	}
	return infos
}

// clientToolReadOnlyIndex 构建 flatName("server__func") → 是否只读 的索引，
// 供 plan 模式下客户端工具分派前拦截（executeClientTool）。查不到 = 非只读（plan 拦截）。
func clientToolReadOnlyIndex(clientTools []ClientTool) map[string]bool {
	if len(clientTools) == 0 {
		return nil
	}
	idx := make(map[string]bool, len(clientTools))
	for _, ct := range clientTools {
		if ct.ServerName == "" || ct.Name == "" {
			continue
		}
		idx[ct.ServerName+"__"+ct.Name] = ct.IsReadOnly
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

// clientToolCaller — 客户端工具执行器（orchestrator / MiniReAct 共用，消除重复实现）。
type clientToolCaller struct {
	bridge  *ClientToolBridge
	timeout time.Duration
}

// Call 分派一次客户端工具调用，阻塞等待客户端回传。
// mustSend：指令类事件（client_tool_call）必须送达，由调用方保证阻塞语义（5s 兜底）；
// 返回 false 表示未送达（发送超时/ctx 取消），本方法据此判定客户端不可用。
// bestEffort：展示/心跳类事件，非阻塞丢失只影响展示。
// 返回 (result, unavailable)：unavailable=true 仅表示**送达失败（真断连）**，
// 调用方据此置位 fast-fail 标志；执行超时（事件已送达客户端但未回传）返回 unavailable=false——
// 客户端可能只是慢/排队，不应锁死整个任务。
func (cc *clientToolCaller) Call(
	ctx context.Context,
	mustSend func(*llm.StreamEvent) bool,
	bestEffort func(*llm.StreamEvent),
	userID string,
	call tool.ToolCall,
	mode string,
	owner string, // 标签归属："main"（主循环）或 "subagent:<convID>"。客户端据此做标签隔离（list_tabs 标注 / close_tab 保护）
) (*tool.Result, bool) {
	if cc.bridge == nil {
		// 接线 bug：请求携带了 client_tools 却没有注入桥，炸在明处
		panic("[clientToolCaller] bridge is nil but client tool call dispatched")
	}
	display := call.Tool + "." + call.Function

	// UI 展示事件（非阻塞，丢失只影响展示）
	call.Display = display
	tcJSON, _ := json.Marshal(call)
	bestEffort(&llm.StreamEvent{Type: "tool_call", Content: string(tcJSON)})

	callID := uuid.New().String()
	ch := cc.bridge.Register(callID, userID)
	defer cc.bridge.Unregister(callID)

	payload, err := json.Marshal(map[string]interface{}{
		"call_id":             callID,
		"tool_name":           call.Tool + "__" + call.Function,
		"arguments":           call.Args,
		"result_budget_bytes": clientToolInjectBudgetBytes,
		"timeout_secs":        int(cc.timeout.Seconds()),
		"mode":                mode, // 会话模式，客户端可按需做二次拦截/展示
		"owner":               owner,
	})
	if err != nil {
		log.Printf("[clientToolCaller] client tool payload marshal error: %v", err)
		return &tool.Result{Success: false, Error: "客户端工具调用序列化失败"}, false
	}

	// 指令事件必须送达：阻塞发送 + 超时兜底（5s）
	if !mustSend(&llm.StreamEvent{Type: "client_tool_call", Content: string(payload)}) {
		if ctx.Err() != nil {
			return &tool.Result{Success: false, Error: "请求已取消"}, false
		}
		log.Printf("[clientToolCaller] client_tool_call send timeout: %s (client disconnected?)", display)
		// 送达失败 = 真断连（客户端收不到事件）：置位 unavailable（后续快速失败）
		return &tool.Result{Success: false, Error: "客户端未连接，无法执行本地工具 " + display + "。请不要再调用客户端本地工具，直接告知用户即可。"}, true
	}

	// 等待客户端回传：期间定期发心跳，防止 SSE 长时间静默被代理/客户端掐断
	timeout := time.NewTimer(cc.timeout)
	defer timeout.Stop()
	heartbeat := time.NewTicker(clientToolHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case r := <-ch:
			if r.Error != "" {
				errMsg := r.Error
				if len(errMsg) > maxClientToolErrorChars {
					errMsg = truncateToolResult(errMsg, maxClientToolErrorChars)
				}
				log.Printf("[clientToolCaller] client tool exec: %s error=%s", display, errMsg)
				return &tool.Result{Success: false, Error: errMsg}, false
			}
			log.Printf("[clientToolCaller] client tool exec: %s success, result=%d bytes", display, len(r.Result))
			return &tool.Result{Success: true, Data: normalizeClientToolData(r.Result)}, false
		case <-heartbeat.C:
			bestEffort(&llm.StreamEvent{Type: "status", Content: "正在等待客户端执行 " + display + "…"})
		case <-timeout.C:
			log.Printf("[clientToolCaller] client tool timeout after %v: %s", cc.timeout, display)
			// 执行超时 ≠ 客户端断连（事件已送达、客户端可能只是慢/排队）：不置位 unavailable，
			// 仅本次调用失败，LLM 可重试或换方案——避免一次慢调用锁死整个子任务。
			return &tool.Result{Success: false, Error: fmt.Sprintf("客户端工具执行超时（%v）：%s。可稍后重试或换方案。", cc.timeout, display)}, false
		case <-ctx.Done():
			return &tool.Result{Success: false, Error: "请求已取消"}, false
		}
	}
}
