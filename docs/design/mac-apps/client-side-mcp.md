# Mac 客户端本地 MCP 集成方案

> 2026-07-17 · 讨论稿 v2

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

## 二、Go 服务端改动

### 2.1 新增文件: `agent/client_bridge.go`

```go
// ClientToolBridge — 客户端工具调用的 pending 管理
// 和 mcp/client.go:86 的 pending map + channel 模式完全一致

type ClientToolResult struct {
    Result json.RawMessage
    Error  string
}

type ClientToolBridge struct {
    mu      sync.Mutex
    pending map[string]chan ClientToolResult
}

func NewClientToolBridge() *ClientToolBridge {
    return &ClientToolBridge{
        pending: make(map[string]chan ClientToolResult),
    }
}

// ExecuteAndWait 发送 SSE 并阻塞等待客户端结果
func (b *ClientToolBridge) ExecuteAndWait(
    ctx context.Context,
    callID string,
    toolName string,
    args json.RawMessage,
    sendSSE func(event, data string),
    timeout time.Duration,
) (json.RawMessage, error) {
    ch := make(chan ClientToolResult, 1)
    b.mu.Lock()
    b.pending[callID] = ch
    b.mu.Unlock()
    
    defer func() {
        b.mu.Lock()
        delete(b.pending, callID)
        b.mu.Unlock()
    }()
    
    // 发送 SSE 事件给客户端
    data, _ := json.Marshal(map[string]interface{}{
        "call_id":   callID,
        "tool_name": toolName,
        "arguments": json.RawMessage(args),
    })
    sendSSE("client_tool_call", string(data))
    
    // 阻塞等待（带超时）
    select {
    case result := <-ch:
        if result.Error != "" {
            return nil, fmt.Errorf("client tool error: %s", result.Error)
        }
        return result.Result, nil
    case <-time.After(timeout):
        return nil, fmt.Errorf("client tool timeout after %v", timeout)
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

// OnResult 收到 HTTP 回调时，唤醒对应的阻塞等待
func (b *ClientToolBridge) OnResult(callID string, result ClientToolResult) error {
    b.mu.Lock()
    ch, ok := b.pending[callID]
    b.mu.Unlock()
    if !ok {
        return fmt.Errorf("unknown call_id: %s", callID)
    }
    ch <- result
    return nil
}
```

~60 行。线程安全模型直接复用 `mcp/client.go` 里的 `pending map[int64]chan` 模式。

### 2.2 改动: `agent/orchestrator.go`

**新增字段：**
```go
type Orchestrator struct {
    // ... 现有字段 ...
    clientBridge *ClientToolBridge   // 新增
}
```

**工具合并（buildSystemPrompt 附近）：**
```go
func (o *Orchestrator) buildToolsForLLM(clientTools []ToolSchema) []ToolDefinition {
    serverTools := o.registry.GetAllToolSchemas()  // 现有的
    
    // 标记 source
    for i := range serverTools {
        serverTools[i].Source = "server"
    }
    for i := range clientTools {
        clientTools[i].Source = "client"
    }
    
    return append(serverTools, clientTools...)
}
```

**工具执行分支（executeOneRound 中）：**
```go
func (o *Orchestrator) executeToolCall(call tool.ToolCall, source string) (*tool.Result, error) {
    if source == "server" {
        // 原有逻辑：本地 Registry 执行
        return o.registry.Execute(...)
    }
    
    // 客户端工具：桥接执行
    callID := uuid.New().String()
    result, err := o.clientBridge.ExecuteAndWait(
        ctx, callID, call.Name, call.Args,
        o.view.SendSSE,  // SSE 发送函数
        30*time.Second,  // 超时
    )
    if err != nil {
        return &tool.Result{Success: false, Error: err.Error()}, nil
    }
    return &tool.Result{Success: true, Data: result}, nil
}
```

### 2.3 改动: `controller/chat.go`

**请求体新增字段：**
```go
type ChatRequest struct {
    // ... 现有字段 ...
    ClientTools []ToolSchema `json:"client_tools"` // 新增
}
```

解析 `client_tools` 后传给 orchestrator。

### 2.4 改动: `view/render.go`

**新增 SSE 事件类型：**
```go
case "client_tool_call":
    // SSE: event: client_tool_call
    //      data: {"call_id":"...","tool_name":"...","arguments":{...}}
```

### 2.5 新增路由: `POST /api/chat/tool-result`

```go
// controller/chat.go 或新文件
func (c *ChatController) HandleToolResult(ctx *gin.Context) {
    var req struct {
        ConversationID string `json:"conversation_id"`
        CallID         string `json:"call_id"`
        Result         string `json:"result"`
        Error          string `json:"error"`
    }
    if err := ctx.ShouldBindJSON(&req); err != nil {
        view.JSONError(ctx, 400, "invalid request")
        return
    }
    
    bridge := getClientBridge()  // 从 app 容器获取
    err := bridge.OnResult(req.CallID, ClientToolResult{
        Result: json.RawMessage(req.Result),
        Error:  req.Error,
    })
    if err != nil {
        view.JSONError(ctx, 404, err.Error())
        return
    }
    view.JSONOK(ctx, map[string]string{"status": "ok"})
}
```

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
      "name": "agent_browser_open",
      "description": "Navigate to a URL",
      "parameters": {
        "type": "object",
        "properties": {
          "url": {"type": "string", "description": "The URL to navigate to"}
        },
        "required": ["url"]
      },
      "source": "client",
      "server_name": "browser"
    }
  ]
}
```

### 3.3 client_tool_call SSE 事件（Server → Mac）

```
event: client_tool_call
data: {"call_id":"uuid-abc","tool_name":"agent_browser_open","arguments":{"url":"https://baidu.com"}}
```

### 3.4 tool-result API（Mac → Server）

```
POST /api/chat/tool-result
Content-Type: application/json

{
    "conversation_id": "xxx",
    "call_id": "uuid-abc",
    "result": "{\"pageTitle\":\"百度一下\"}",
    "error": null
}
```

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
