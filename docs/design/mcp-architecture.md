# MCP 集成架构

> 2026-05-13

## 核心类

```
MCPToolCaller (接口)
     │
MCPProxyTool      ← 唯一工具，实现 tool.Tool 接口
(name=server)
     │
  Manager         ← 生命周期管理
     │
  MCPServer       ← stdio 子进程，JSON-RPC 2.0
```

### MCPToolCaller — 接口

Manager 暴露给 Proxy 的最小接口，方便单测 mock。

```go
type MCPToolCaller interface {
    EnsureRunning(ctx, name string) (*MCPServer, error)
    CallTool(ctx, serverName, toolName string, args) (json.RawMessage, error)
    GetToolsSnapshot(name string) []ToolDef
}
```

### MCPProxyTool — 唯一的 MCP 工具适配层

- 1 个 MCP server = 1 个 Proxy，永久存在
- `Name()` = server 名（如 `hotnews`），供 tool_retriever 发现
- `Functions()` **动态化**：
  - 始终包含 `invoke`：通用入口
  - server 就绪后追加真实工具函数（如 `get_hot_news`），含真实参数 schema
- `Execute(funcName, args)`：
  - `funcName == "invoke"` → 转发到第一个真实工具
  - `funcName == 真实工具名` → 直调该工具
  - 首次调用触发 `EnsureRunning`（懒启动）

### Manager — 生命周期管理

- 懒启动：首次 `EnsureRunning` 时 `exec.Command` + MCP 握手（initialize → tools/list）
- 空闲回收：每 30s 扫描，`lastUsedAt > idleTimeout && inFlight == 0` → SIGTERM
- LRU 抢占：并发上限（默认 2）满时，关最久未用的腾位置
- `tools/list` 结果缓存在 `MCPServer.ToolsCache`，Proxy 通过 `GetToolsSnapshot` 读取

### MCPServer — stdio 子进程

- `exec.Cmd` + stdin/stdout pipe
- JSON-RPC 2.0：initialize → initialized → tools/list → tools/call
- 响应路由：`rpcID` 对应 `pending[id]` channel
- `Stop()`：sync.Once 保护，幂等可重入

## 初始化链路

```
main.go
  │
  ├── Manager ← YAML config
  │
  └── for each server config:
        proxy := NewMCPProxyTool(cfg, Manager)
        Registry.Register(proxy)        → tools["hotnews"] = proxy
```

## 运行时刻

```
LLM: tool_retriever.search("新闻")
  → Registry.SearchTools → 命中 "hotnews" (MCPProxyTool)
  → 返回 {name:"hotnews", functions:[invoke], description:"[MCP:hotnews] ..."}

LLM: hotnews.invoke({query:"热点"})
  → MCPProxyTool.Execute()
    ① EnsureRunning("hotnews")   ← 首次，懒启动，阻塞 1-3s
       → exec "npx -y @wopal/..."
       → initialize → tools/list → 状态 ready
    ② funcName == "invoke" → 转发到第一个真实工具 get_hot_news
    ③ CallTool("hotnews", "get_hot_news", {query:"热点"})
       → MCPServer.CallTool → JSON-RPC → 真实结果
    ④ parseMCPResult → 返回 LLM

LLM: hotnews.get_hot_news({sources:[1,2,3]})   ← 后续按真实 schema 调
  → MCPProxyTool.Execute()
    ① EnsureRunning → 已 ready，直接返回
    ② funcName == "get_hot_news" → 直调
    ③ CallTool("hotnews", "get_hot_news", {sources:[1,2,3]})
    ④ parseMCPResult → 返回 LLM
```

## Functions() 动态化

```
server 未启动:
  Functions() → [invoke(query)]

server 已启动:
  Functions() → [invoke(query), get_hot_news(sources, ...)]

LLM 每轮都能看到最新的函数列表，启动后自动获得真实调用方式。
```

## 关键设计决策

| 决策 | 原因 |
|------|------|
| 只保留 Proxy，删除 Adapter | Proxy 已能暴露真实 schema + 转发执行，Adapter 冗余 |
| Functions() 动态化 | server 就绪后 LLM 自动看到真实函数签名 |
| Proxy 不删 | server 名 ≠ tool 名是 MCP 固有特性，Proxy 作为永久适配层 |
| Proxy 转发而非 retry | 避免 activeNames 过期 / LLM 重试死循环 |
| stdio 而非 HTTP SSE | 子进程生命周期管理更简单，无需端口分配 |
| 懒启动 | 内存有限，不用的 server 不占资源 |
| JSON-RPC 2.0 | MCP 标准协议，所有 MCP server 兼容 |
