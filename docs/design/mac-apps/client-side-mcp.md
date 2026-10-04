# Mac 客户端本地 MCP 集成方案

> 2026-07-18 · v3（Go 侧方案按代码实况细化，与 v2 的差异见「二、Go 服务端改动」开头）

## 动机

当前 xhanHelper 的 MCP 集成跑在 Go 服务器端（`server/internal/mcp/`）。但：

- **服务器性能差**，跑不动 MCP 子进程（如 agent-browser 需要 Chrome 实例）
- **Mac 本地有资源**，Chrome 本身就运行在 Mac 上，agent-browser 可以直接接入
- **扩展需求**：以后想接更多本地 MCP server（文件系统、Apple Script、本地 AI 模型等）

因此，将 MCP 执行层从服务器**迁移到 Mac 客户端**。服务器只负责 LLM 推理编排，Mac 做本地工具执行的体力活。

---

## 核心思想

```
之前的架构：  MCP server 跑在 Go 服务器上（服务器性能不够）
新架构：      MCP server 跑在 Mac Swift 客户端上（Mac 资源充足）
沟通方式：    Go 服务器通过 SSE + HTTP 将工具调用中转到 Mac 客户端执行
```

LLM 看到的工具列表 = 服务器本地工具（记账/笔记/搜索等）+ Mac 上报的客户端工具（浏览器/文件等）

---

## 架构概览

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         Mac App (Swift)                                    │
│                                                                           │
│  ┌──────────────────────────┐   ┌───────────────────────────────────┐    │
│  │  MCPManager (actor)       │   │  ChatViewModel                     │    │
│  │                           │   │                                    │    │
│  │  • 管理多个 MCPServer      │   │  sendMessage() → POST /api/chat   │    │
│  │  • 懒启动 / 空闲回收        │   │    body: {message, client_tools}  │    │
│  │  • LRU 抢占 (max=2)       │   │                                    │    │
│  │  • toolCache 查询          │   │  handleSSE() → client_tool_call   │    │
│  │                           │   │    → MCPManager.callTool()         │    │
│  └───────────┬───────────────┘   │    → APIClient.submitResult()     │    │
│              │                    └──────────────┬────────────────────┘    │
│              │ 管理                                │                        │
│  ┌───────────▼───────────────┐                    │                        │
│  │  MCPServer                 │                   │                        │
│  │                            │                   │                        │
│  │  state: stopped/start../   │                   │                        │
│  │  Process(stdin/out Pipe)   │                   │                        │
│  │  JSON-RPC 2.0 over stdio   │                   │                        │
│  │  MCP handshake + callTool  │                   │                        │
│  └───────────┬───────────────┘                   │                        │
│              │ Process.spawn()                    │                        │
│  ┌───────────▼───────────────┐                   │                        │
│  │  agent-browser mcp         │                   │                        │
│  │  (MCP server 子进程)        │                   │                        │
│  └───────────────────────────┘                   │                        │
└──────────────────────────────────────────────────┼────────────────────────┘
                                                   │
        POST /api/chat           (携带 client_tools)│
        SSE stream               (含 client_tool_call 事件)
        POST /api/chat/tool-result (回传工具执行结果)
                                                   │
┌──────────────────────────────────────────────────┼────────────────────────┐
│                      Go 后端 (服务器)              │                        │
│                                                   │                        │
│  controller/chat.go                                │                        │
│    parseRequest() → 提取 client_tools               │                        │
│                                                    │                        │
│  agent/orchestrator.go                             │                        │
│    buildSystemPrompt() → mergeTools(server + client)│                       │
│    executeOneRound() → 判断 source:                 │                        │
│      "server" → 本地 tool.Registry.Execute()        │                        │
│      "client" → clientBridge.executeClientTool()   │                        │
│                                                    │                        │
│  agent/client_bridge.go (新增)                      │                        │
│    pending map[string]chan ClientToolResult          │                        │
│    executeClientTool() → SSE send + chan block      │                        │
│    onClientToolResult() → chan receive → continue   │                        │
│                                                    │                        │
│  view/render.go                                    │                        │
│    新增 SSE event: client_tool_call                 │                        │
└────────────────────────────────────────────────────┴────────────────────────┘

LLM 看到的工具列表 = server_tools ∪ client_tools
LLM 调用工具 → orchestrator 判断 source → 本地执行 或 SSE 中转
```

---

## 一、Swift 端组件

### 1.1 文件清单

```
client/xHelper/xHelper/Shared/
├── MCP/
│   ├── JSONRPC.swift          (~50行)  — JSON-RPC 2.0 数据结构
│   ├── MCPConfig.swift        (~30行)  — MCP server 配置模型
│   ├── MCPServer.swift        (~170行) — 单个 MCP 进程封装
│   └── MCPManager.swift       (~140行) — 多 server 生命周期管理
├── Services/
│   └── ToolResultClient.swift (~30行)  — APIClient 扩展: 回传工具结果
└── ViewModels/
    └── ChatViewModel+ClientTools.swift (~80行) — 发送 client_tools + 处理 client_tool_call SSE
```

总计约 500 行新 Swift 代码。

### 1.2 JSONRPC.swift — 数据结构

**设计决策：协议层用强类型 `Codable`，payload 层用 `JSONSerialization` 透传。**

MCP 协议中的 `inputSchema`、`arguments`、`result` 等字段是任意嵌套 JSON，Swift Foundation 没有 `AnyCodable`。业界有三种方案：

| 方案 | 适用场景 |
|------|----------|
| `JSONSerialization` 透传（**选这个**） | payload 不需要在 Swift 里强类型操作，只是 encode/decode 后转发 |
| `AnyCodable` 三方库 | 需要在 Swift 里对 payload 做类型判断和字段存取 |
| 手写 `JSONValue` 枚举 | 服务端框架（如 Vapor），需要完全的类型安全 |

我们选第一种：协议消息（initialize、tools/list 等）用 `Codable` 强类型定义；`params`、`inputSchema`、`arguments` 等 payload 用 `Data`/`[String: Any]` + `JSONSerialization` 当不透明 blob 透传。

```swift
// ── 强类型协议消息 ──

// JSON-RPC 2.0 请求（params 用 Data 存原始 JSON）
struct JSONRPCRequest: Codable {
    let jsonrpc: String      // "2.0"
    let id: Int64
    let method: String       // e.g. "initialize", "tools/list", "tools/call"
    var params: Data?        // 原始 JSON bytes，用 JSONSerialization 编解码
}

// JSON-RPC 2.0 响应
struct JSONRPCResponse: Codable {
    let jsonrpc: String
    let id: Int64?
    let result: Data?        // 原始 JSON bytes
    let error: JSONRPCError?
}

struct JSONRPCError: Codable {
    let code: Int
    let message: String
}

// JSON-RPC 2.0 通知（无 id，不需要响应）
struct JSONRPCNotification: Codable {
    let jsonrpc: String
    let method: String
    var params: Data?
}

// MCP tools/list 返回的工具定义 — 也是透传
struct MCPToolDef: Codable {
    let name: String
    let description: String
    let inputSchema: Data?   // 原始 JSON，转成 LLM function schema 时透传
}

// ── 编解码辅助 ──

extension JSONRPCRequest {
    // 便捷构造：params 从字典编码
    static func make(id: Int64, method: String, params: [String: Any]? = nil) -> JSONRPCRequest {
        var req = JSONRPCRequest(id: id, method: method)
        if let p = params, let data = try? JSONSerialization.data(withJSONObject: p) {
            req.params = data
        }
        return req
    }
    
    // 编码为 JSON 行（newline-delimited）
    func encode() throws -> Data {
        var data = try JSONEncoder().encode(self)
        data.append(0x0A) // \n
        return data
    }
}

extension JSONRPCResponse {
    // 从 JSON 行解码
    static func decode(from line: Data) throws -> JSONRPCResponse {
        return try JSONDecoder().decode(JSONRPCResponse.self, from: line)
    }
    
    // 解码 result 为字典
    func resultDict() -> [String: Any]? {
        guard let data = result else { return nil }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
    }
    
    // 解码 result 中的 tools 列表
    func toolsFromResult() -> [MCPToolDef]? {
        guard let dict = resultDict(),
              let tools = dict["tools"] as? [[String: Any]] else { return nil }
        return tools.compactMap { t in
            guard let name = t["name"] as? String,
                  let desc = t["description"] as? String else { return nil }
            let schema = t["inputSchema"] as? [String: Any]
            return MCPToolDef(
                name: name,
                description: desc,
                inputSchema: schema.flatMap { try? JSONSerialization.data(withJSONObject: $0) }
            )
        }
    }
}
```

### 1.3 MCPConfig.swift — 配置

```swift
struct MCPServerConfig: Codable {
    let name: String
    let command: String           // e.g. "agent-browser"
    let args: [String]            // e.g. ["mcp"]
    let env: [String: String]?
    let idleTimeoutSecs: Int      // 默认 300
    let description: String
}

struct MCPManagerConfig: Codable {
    let maxConcurrent: Int        // 默认 2
    let servers: [MCPServerConfig]
}
```

配置来源：Mac App 启动时从 Bundle 里的 JSON 文件或用户偏好读取。

### 1.4 MCPServer.swift — 单进程封装

**状态机：**

```swift
enum MCPServerState {
    case stopped
    case starting
    case ready
    case error
}
```

**核心方法：**

```
MCPServer
├── init(config)                     // 创建，state=stopped
├── start() async throws             // 启动进程 + MCP 握手 → state=ready
│   ├── Process.launchPath = config.command
│   ├── Process.arguments = config.args
│   ├── standardInput = Pipe()
│   ├── standardOutput = Pipe()
│   ├── Process.run()
│   ├── readLoop() 启动异步读取
│   ├── call("initialize", ...)       // MCP 握手
│   ├── sendNotification("initialized")
│   ├── call("tools/list") → 缓存 toolsCache
│   └── state = .ready
├── stop()                            // sync.Once 保护，幂等
│   ├── Process.terminate()           // SIGTERM
│   ├── wait 5s → Process.interrupt() // SIGKILL fallback
│   └── state = .stopped
├── callTool(name, args) async throws → Data  // 调用 MCP 工具
│   ├── inFlight += 1
│   ├── call("tools/call", {name, arguments})   // args: [String: Any]
│   ├── inFlight -= 1
│   └── 返回原始 result Data
├── readLoop()                        // 持续读 stdout
│   └── 解析 JSON-RPC response → 路由到 pending[id] 的 continuation
├── lastUsedAt: Date                  // 用于空闲回收
├── inFlight: Int                     // 正在执行的工具调用数
└── toolsCache: [MCPToolDef]          // tools/list 结果缓存
```

**readLoop 实现要点：**

```
FileHandle.readabilityHandler + 换行分割 (newline-delimited JSON)
每个响应带 id → 查找 pending[id] → resume continuation
如果 stdout 关闭 → Process 已死 → state = .error
```

**pending 路由实现：**

```
// Swift 里用 dictionary + checked continuation
private var pending: [Int64: CheckedContinuation<Data, Error>] = [:]

// 类似 Go 的 pending map + channel，Swift 用 continuation 更好
```

### 1.5 MCPManager.swift — 生命周期管理

**用 `actor` 保证线程安全**（等价于 Go 的 `sync.Mutex` 保护）：

```swift
actor MCPManager {
    private var config: MCPManagerConfig
    private var servers: [String: MCPServer] = [:]
    private var idleReaperTask: Task<Void, Never>?

    // --- 对外接口 ---
    func ensureRunning(name: String) async throws → MCPServer
    func callTool(server: String, tool: String, args: [String: Any]) async throws → Data
    func getToolsSnapshot(name: String) → [MCPToolDef]
    func getAllClientTools() → [(serverName: String, tools: [MCPToolDef])]
    func startIdleReaper()
    func shutdown()
}
```

**ensureRunning 流程：**

```
① 检查 servers[name] 状态:
    .ready → 直接返回
    .starting → throw error (正在启动)
    其他 → stop + 移除旧实例

② 检查并发上限 (maxConcurrent):
    activeCount >= max → LRU 抢占:
        找最老的 idle server (inFlight==0)
        → stop + 移除
    → 然后继续

③ 创建新 MCPServer → start()
   → 加入 servers[name]
   → 返回
```

**空闲回收：**

```
Timer.scheduledTimer(repeating: 30s):
  for server in servers:
    if state == .ready && inFlight == 0:
      idle = now - server.lastUsedAt
      if idle > server.config.idleTimeoutSecs:
        server.stop()
        servers.remove(name)
```

**LRU 抢占：**

```
findLRUVictim():
  for server in servers:
    if inFlight > 0: continue
    if oldest == nil || server.lastUsedAt < oldest.lastUsedAt:
      oldest = server
  return oldest
```

### 1.6 ChatViewModel+ClientTools.swift — 对接现有聊天流

**发送时：**

```swift
func sendMessage(_ text: String, ...) {
    // 原有逻辑...
    
    // 新增：收集客户端工具 schema
    let clientTools = await mcpManager.getAllClientTools()
    let clientToolsJSON = clientTools.toLLMFunctionSchema()
    
    // 传给 SSE 连接（在 body 中增加 client_tools 字段）
    sseClient.connect(..., clientTools: clientToolsJSON)
}
```

**SSE 事件处理（在 handleEvent 中新增）：**

```swift
case .clientToolCall:
    // event.data = {"call_id":"abc","tool_name":"agent_browser_open","arguments":{"url":"..."}}
    Task {
        let result = try await mcpManager.callTool(
            server: "browser",
            tool: toolName,
            args: arguments
        )
        await apiClient.submitToolResult(
            conversationId: cid,
            callId: callId,
            result: String(data: result, encoding: .utf8) ?? ""
        )
    }
```

### 1.7 ToolResultClient.swift — API 扩展

```swift
extension APIClient {
    func submitToolResult(conversationId: String, callId: String, 
                          result: String, error: String? = nil) async throws {
        let body: [String: Any] = [
            "conversation_id": conversationId,
            "call_id": callId,
            "result": result,
            "error": error as Any
        ]
        let _: APIResponse<Empty> = try await post("/api/chat/tool-result", body: body)
    }
}
```

---

## 二、Go 服务端改动（v3，按代码实况细化）

> **与 v2 的差异**：
> 1. **不存在 prompt 注入问题**：`use_native_fc: true` 是全局配置（config.yaml），flash 也走原生 FC，工具在 LLM 请求体里（`mkLlmChatReq` → `GetNativeFCTools()`）。promptbuilder 零改动；`use_native_fc=false` 时客户端工具忽略 + 日志。
> 2. **ClientToolBridge 去掉 `sendSSE` 回调**：orchestrator 没有 SSE writer，事件走 `events chan *llm.StreamEvent` 到 chat handler 再 `WriteSSE`。桥只管 pending 路由，拆成 Register/Unregister/OnResult 三步（先注册再发事件，消除回传竞态窗口）。
> 3. **`client_tool_call` 事件必须阻塞发送**：现有 `sendEvent` 是非阻塞丢弃式，该事件丢失 = 工具永不执行。用带 5s 超时的阻塞发送兜底（chat 的 orch.Run 用 context.Background()，断连不会取消 ctx）。
> 4. **不给 `tool.ToolCall` 加 Source 字段**：source 是请求级状态，orchestrator 用本次请求的 `clientIdx map[string]bool`（key=server_name）判断分发。
> 5. **tool-result 请求去掉 `conversation_id`**：路由只需 call_id，归属由 pending 中记录的 userID 校验。
> 6. 超时默认 240s（覆盖客户端 L3 审批队首 180s + 执行余量），配置项 `llm.client_tool_timeout_secs`。

### 2.0 命名与分发

原生 FC 扁平命名为 `tool__func`（deepseek.go），回程 `splitToolFuncName` 按最后一个 `__` 拆分。客户端工具复用：

- 客户端上报：`server_name`（如 `browser`）+ `name`（如 `browser_navigate`）
- 合并给 LLM：按 `server_name` 分组成 `llm.ToolDefinition{Name: serverName, Functions: [...]}` → LLM 看到 `browser__browser_navigate`
- 回程拆出 `ToolCall{Tool:"browser", Function:"browser_navigate"}`，`clientIdx[tc.Tool]` 命中 → 客户端路由
- `client_tool_call` 事件里 `tool_name` 用扁平名原样下发，Swift 端按前缀路由到对应 MCPServer，双方不重复实现拆名逻辑

合并时校验（不合格跳过 + log 警告）：
- `server_name` 与 registry 现有工具名冲突 → 跳过（防劫持）
- 工具名含 `__` → 跳过（破坏 LastIndex 拆分）
- 总数上限 50

### 2.1 新增文件: `agent/client_bridge.go`（~80 行）

```go
type ClientToolResult struct {
    Result json.RawMessage
    Error  string
}

type pendingCall struct {
    userID string // 鉴权：回传者必须是发起者
    ch     chan ClientToolResult
}

type ClientToolBridge struct {
    mu      sync.Mutex
    pending map[string]*pendingCall
}

func NewClientToolBridge() *ClientToolBridge
func (b *ClientToolBridge) Register(callID, userID string) <-chan ClientToolResult
func (b *ClientToolBridge) Unregister(callID string)                 // defer 调用，幂等
func (b *ClientToolBridge) OnResult(callID, userID string, r ClientToolResult) error
```

线程安全模型与 `mcp/client.go` 的 `pending map[int64]chan` 一致。

### 2.2 请求链路

**新类型**（agent 包）：
```go
type ClientTool struct {
    ServerName  string                 `json:"server_name"`
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    Parameters  map[string]interface{} `json:"parameters"` // JSON Schema 透传
}
```

- `controller/chat.go` 请求结构体加 `ClientTools []agent.ClientTool \`json:"client_tools"\``
- `RunRequest` 加 `ClientTools []ClientTool`，chat.go 构造处透传；replay 等其他构造点零值即可

### 2.3 工具合并: `mkLlmChatReq`

```go
var tools []llm.ToolDefinition
if o.useNativeFC {
    tools = o.registry.GetNativeFCTools()
    tools = append(tools, buildClientToolDefs(req.ClientTools, o.registry)...)
} else if len(req.ClientTools) > 0 {
    log.Printf("[orch] client_tools ignored: use_native_fc=false")
}
```

`buildClientToolDefs` 按 2.0 规则校验 + 分组。registry 新增 `Has(name string) bool`。

### 2.4 执行分发: `executeConcurrently` 改造

签名加参数 `clientIdx map[string]bool`（nil = 全 server，行为与现状一致）。内部保持 `results[i]` 与 `toolCalls[i]` 下标对齐，`collectToolResults` / `collectToolResultsNative` 零改动：

```
① 按 clientIdx[tc.Tool] 分成 server / client 两组
② server 组：现有逻辑不变（FillDisplay + tool_call 事件 + 信号量并发 + panic 恢复）
③ client 组：串行执行，对每个 call：
   a. 发 "tool_call" 事件（非阻塞，UI 展示）
   b. callID := uuid；ch := bridge.Register(callID, userID)；defer Unregister
   c. 发 "client_tool_call" 事件 —— 阻塞发送 + 5s 超时兜底：
      超时（客户端断连/channel 满）→ 该工具返回错误结果，继续下一个
   d. select 等 ch / clientToolTimeout / ctx.Done()
   e. 写 results[idx]
④ wg.Wait()（server 组）→ 返回
```

调用点（原生 FC 路径 + executePromptEngTools 文本兜底路径）都持有 `req`，均传入 clientIdx —— 文本 JSON 兜底撞上 client 工具时分发逻辑同样生效。

### 2.5 SSE 出口: `controller/chat.go`

事件 switch 加：
```go
case "client_tool_call":
    view.WriteSSE(w, flusher, "client_tool_call", evt.Content)
```

### 2.6 回传路由: `POST /api/chat/tool-result`

```go
func chatToolResult(bridge *agent.ClientToolBridge) gin.HandlerFunc {
    // 从 auth 中间件取 user_id
    // body: {call_id (required), result, error}
    // bridge.OnResult 失败（unknown call_id / user mismatch）统一 404，避免探测
}
```

注册在 auth 组内：`auth.POST("/chat/tool-result", chatToolResult(rc.ClientBridge))`。

### 2.7 接线

| 文件 | 改动 |
|------|------|
| `appinit/agents.go` | `bridge := agent.NewClientToolBridge()` → OrchestratorConfig |
| `agent/orchestrator.go` | OrchestratorConfig/Orchestrator 加 `ClientBridge` 字段（必要依赖，不做 nil 静默） |
| `appinit/bundle.go` | AgentBundle 加 `ClientBridge` |
| `controller/router.go` | RouterConfig 加 `ClientBridge` + 注册路由 |
| `appinit/router.go` | 透传 `agents.ClientBridge` |

### 2.8 配置

`llm.client_tool_timeout_secs`，默认 240。

### 2.9 边界情况

| 场景 | 行为 |
|------|------|
| 客户端断连后 LLM 才发起 client 调用 | client_tool_call 阻塞发送 5s 超时 → 错误结果，LLM 下轮应对 |
| 回传超时（>240s） | select 超时 → 错误结果 + Unregister；迟到回传 404 |
| 重复回传 | ch 缓冲 1，首个生效；之后已 Unregister → 404 |
| 跨用户伪造 call_id | OnResult 校验 userID → 404 |
| iOS（不发 client_tools） | clientIdx 为 nil → 与现状完全一致 |

### 2.10 测试

- 单测：bridge（正常/超时/mismatch/重复/并发）；buildClientToolDefs 校验规则；混合分发下标对齐
- `make test` 全绿
- 手工联调：curl 开 SSE 带假 client_tools + 另一终端 curl 回传，验证 LLM 拿到结果继续推理（不依赖 Swift 端）

---

## 三、通信协议

### 3.1 交互时序

```
Mac App                              Go Server                           agent-browser
  │                                    │                                    │
  │── POST /api/chat ─────────────────→│                                    │
  │   {message, client_tools}          │                                    │
  │   (SSE stream opened)              │                                    │
  │                                    │── LLM 推理 ──→ "需要打开百度"       │
  │                                    │                                    │
  │←── SSE: client_tool_call ──────── │                                    │
  │    {call_id, tool_name, args}      │                                    │
  │                                    │   [Go 侧: pending["abc"]←channel]  │
  │                                    │   [Go 侧: select 阻塞等待]          │
  │                                    │                                    │
  │── MCPManager.callTool() ──────────────────────────────────────────────→│
  │                                    │                                    │
  │←── tool result ─────────────────────────────────────────────────────── │
  │                                    │                                    │
  │── POST /api/chat/tool-result ────→│                                    │
  │    {call_id:"abc", result:"..."}   │                                    │
  │                                    │   [Go 侧: ch ← result]             │
  │                                    │   [Go 侧: select 解除阻塞]          │
  │                                    │── LLM 继续推理 ──→ 最终答复         │
  │←── SSE: text_delta, done ──────── │                                    │
```

### 3.2 client_tools 格式（Mac → Server）

```json
{
  "message": "帮我看看北京天气",
  "conversation_id": "...",
  "client_tools": [
    {
      "server_name": "browser",
      "name": "browser_navigate",
      "description": "Navigate to a URL",
      "parameters": {
        "type": "object",
        "properties": {
          "url": {"type": "string", "description": "The URL to navigate to"}
        },
        "required": ["url"]
      }
    }
  ]
}
```

> v3：去掉 `source` 字段（服务端按 server_name 分组即隐含 client 语义）；`name` 为 MCP 原始工具名。

### 3.3 client_tool_call SSE 事件（Server → Mac）

```
event: client_tool_call
data: {"call_id":"uuid-abc","tool_name":"browser__browser_navigate","arguments":{"url":"https://baidu.com"}}
```

> v3：`tool_name` 为扁平名 `serverName__toolName`，Swift 端按第一个 `__` 前缀路由到对应 MCPServer。

### 3.4 tool-result API（Mac → Server）

```
POST /api/chat/tool-result
Authorization: Bearer <token>
Content-Type: application/json

{
    "call_id": "uuid-abc",
    "result": "{\"pageTitle\":\"百度一下\"}",
    "error": null
}
```

> v3：去掉 `conversation_id`（路由只需 call_id，归属由 pending 中的 userID 校验）；unknown call_id / 用户不匹配统一返回 404。

---

## 四、进程生命周期管理

### 4.1 启动

- **懒启动**：首次 MCPManager.callTool() 时才启动 agent-browser 进程
- 不是 App 启动时就拉进程（节省资源）

### 4.2 保活/监控

- `readLoop` 持续读 stdout：如果 Pipe 关闭 → `state = .error`
- 下次调用时 Manager 检测到 error → stop + 重建

### 4.3 空闲回收

- `Timer.scheduledTimer(repeating: 30s)` 扫描
- 条件：`state == .ready && inFlight == 0 && idle > idleTimeoutSecs`
- 空闲回收不发通知——对话已结束

### 4.4 LRU 抢占

- 并发上限默认 2 个 MCP server
- 启动新 server 前检查：如果 `activeCount >= maxConcurrent`
- 找最老的 idle server → stop → 腾出位置

### 4.5 优雅关闭

- App 退出时：`NSApplication.willTerminateNotification` → `MCPManager.shutdown()`
- 每个 server：`Process.terminate()` (SIGTERM) → wait 5s → `Process.interrupt()` (SIGKILL)

### 4.6 MCP 握手流程

```
Swift → agent-browser:
  {"jsonrpc":"2.0","id":1,"method":"initialize","params":{
    "protocolVersion":"2024-11-05",
    "capabilities":{},
    "clientInfo":{"name":"xhanHelper","version":"0.1.0"}
  }}
  
agent-browser → Swift:
  {"jsonrpc":"2.0","id":1,"result":{...server capabilities...}}

Swift → agent-browser (notification):
  {"jsonrpc":"2.0","method":"notifications/initialized"}

Swift → agent-browser:
  {"jsonrpc":"2.0","id":2,"method":"tools/list"}

agent-browser → Swift:
  {"jsonrpc":"2.0","id":2,"result":{"tools":[
    {"name":"agent_browser_open","description":"...","inputSchema":{...}},
    {"name":"agent_browser_click","description":"...","inputSchema":{...}},
    ...
  ]}}
```

---

## 五、多个客户端工具调用处理

**设计决策：顺序执行，一次一个。**

LLM 在一轮中返回 N 个 client tool_calls 时，对每个 client tool_call 顺序执行：

1. 发 SSE client_tool_call 事件
2. ClientToolBridge 阻塞等待 HTTP 回调（pending map + channel）
3. 收到结果，收集到 results 数组

全部 N 个完成后，N 个 tool_result 一起发给 LLM 继续推理。

**为什么选顺序执行？**
- 实现最简单，无需在 Swift 端处理并发
- 浏览器操作天然有依赖关系（先打开页面 → 再执行 JS → 最后截图）
- ClientToolBridge 的 pending map + channel 模式天然线程安全，以后需要并发时不用改 bridge 层
- LLM 工具调用有并行语义时，Go 后端 maxConcurrency 控制即可（目前默认 2）

Swift 端 MCPManager.callTool 也按同步调用写，一次只执行一个 client tool call，不引入并发。

---

## 六、已决策的设计选择

| 问题 | 决策 | 理由 |
|------|------|------|
| AnyCodable | JSONSerialization 透传，强类型 Codable 只用于协议层 | 最小依赖，payload 不需要在 Swift 里强类型操作 |
| Mac App 位置 | 基于现有 xhanHelper 客户端，MCP 模块放 Shared/MCP/ | 复用现有 SSE/聊天基础设施 |
| 线程安全 | actor 保护 MCPManager | 等价 Go 的 sync.Mutex，天然安全 |
| 空闲回收超时 | 300s（和 Go 版一致） | Mac 资源充裕，后续可调 |
| agent-browser 模式 | agent-browser mcp（自带浏览器管理） | 标准模式，后续可选 --cdp |
| MCP 安装 | 假定用户已安装 | 分发方案另议 |
| 服务端旧 MCP | 保留 server/internal/mcp/ 不删 | 纯服务器端 MCP 仍可能有需求


## 七、实施顺序

| 阶段 | 内容 | 预计工作量 |
|------|------|-----------|
| P0 | `JSONRPC.swift` + `MCPConfig.swift` — 纯数据结构 | 小 |
| P1 | `MCPServer.swift` — 单进程 MCP 客户端 | 中 |
| P2 | `MCPManager.swift` — 多进程生命周期管理 | 中 |
| P3 | Go 服务端: `client_bridge.go` + orchestrator 改动 + SSE 事件 + HTTP 路由 | 中 |
| P4 | Swift 对接: `ChatViewModel+ClientTools.swift` + `ToolResultClient.swift` | 小 |
| P5 | 端到端联调：用 agent-browser 抓一条百度天气 | 小 |
| P6 | 稳定性：超时重试、进程崩溃恢复、并发安全 | 中 |
