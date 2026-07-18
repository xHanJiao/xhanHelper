# 客户端工具结果预算链统一治理

> 2026-07-18 · 对应 Review 中 P1.1「截断口径分散」的整改

## 动机

`client-side-mcp.md` 落地后，客户端工具结果从 MCP server → Swift 客户端 → HTTP 回传 → Go 服务端 → LLM 注入 → DB 落库，链路中出现 **6 道彼此独立的限制**：

| # | 位置 | 限制 | 单位 | 问题 |
|---|------|------|------|------|
| 1 | 客户端分页触发 `MCPManager` | 200KB | 字节 | 拍脑袋对齐 #3，不是业务需求 |
| 2 | 客户端分块 `chunkChars` | 12,000 | Swift 字符 | 中文 12K 字符 ≈ 36KB 字节，超 #4 的 20KB 上限 |
| 3 | HTTP 护栏 `chat.go` | 256KB | 字节 | 传输维度，独立合理 |
| 4 | 注入/落库 `client_tools.go` | 20,000 | 字节（名却叫 Chars） | 与 #2 口径不一致 |
| 5 | DB `messages.content` TEXT | 64KB (MySQL) | 字节 | 硬底线，不可越过 |
| 6 | 跨轮历史压缩 `BuildMessagesForFlash` | 2000 | 字节 | 有意设计（旧轮压缩），不动 |

**痛点**：#1/#2/#4 三套口径互相打架。客户端分页开了 read_more 口子，但 chunk 大小用的是字符数→中文场景首页就被服务端二次截断，且只有 >200KB 才分页→20KB–200KB 区间的超限数据静默丢失，read_more 对此区间完全无效。

## 治理原则

1. **单一来源**：预算只在 Go 服务端定义一次，写在 `client_tools.go` 常量块里，随 `client_tool_call` 事件下发给客户端
2. **服务端兜底退化为纯防御**：正常流程下永不触发截断；触发 = 客户端未按协议（旧版本），打告警日志
3. **客户端不再硬编码任何大小魔数**，全部从服务端下发字段读取

## 方案概览

```
服务端 (single source of truth)
  client_tools.go:
    clientToolInjectBudgetBytes = 20000  ← 预算唯一定义
       │
       ▼
  orchestrator.go executeClientTool:
    往 client_tool_call 事件新增两个字段:
      "result_budget_bytes": 20000
      "timeout_secs": 60
       │
       ▼  SSE 下发 ──────────────────────────────┐
                                                  │
客户端 (consumer)                                  │
  ChatViewModel+ClientTools:                      │
    解析 event.payload 取 budgetBytes / timeout   │
       │                                          │
       ▼                                          │
  MCPManager.callTool(budgetBytes, deadlineSecs): │
    • 分页触发: data.count > budgetBytes           │
    • 分块大小: budgetBytes × 80%（信封预留 20%）  │
    • 切页单位: UTF-8 字节（Character 边界安全）   │
    • page_token: 首页返回，续读必填，防读到覆盖   │
    • 自愈时间预算: 首次超时 = deadlineSecs        │
                    剩余 < 15s → 跳过重试抛错      │
                    重试超时 = remaining            │
    • shutdown 清缓存                              │
       │                                          │
       ▼  POST /api/chat/tool-result ────────────┘
       
服务端 normalizeClientToolData (fallback only):
  正常: 数据已在预算内，原样透传
  触发: len > clientToolInjectBudgetBytes → log.Printf 告警 + 截断兜底
```

## 预算链推导

```
DB 硬底线:         messages.content TEXT (MySQL 64KB 字节)
  ↓ 留 3x 余量 (JSON 转义膨胀 + 多字节字符 + 消息信封)
注入/落库预算:     clientToolInjectBudgetBytes = 20,000 字节
  ↓ 随 client_tool_call 下发 (result_budget_bytes)
客户端分页:        每页序列化后 ≤ 预算，内容配额 = 预算 × 80%
  ↓
HTTP 护栏(独立):   controller.maxClientToolResultBytes = 256KB
```

80% 配额理由：分页 JSON 信封（chunk/truncated/token 等元数据）约 400–600 字节，CJK 无转义膨胀，ASCII HTML 中引号转义膨胀约 10%。最坏情况下 (80% + 信封) 约 16,600 字节 < 20,000，安全。

## 分页 cache 身份绑定（page_token）

```
首页 JSON: {"truncated":true, "page_token":"abc123", "chunk":"1/5", "content":"..."}
续读参数: cursor=2, page_token="abc123"

校验规则:
  • token 缺失 → 参数错误
  • token 不匹配 → "缓存已失效（被新工具结果覆盖），请重新执行原工具"
  • server_name 不匹配 → 同上
  • cursor 越界 → 范围错误
```

`shutdown()` 同时清空分页缓存（`paged = nil`）。

## 文本提取策略（per-server 配置）

`MCPServerConfig.pageTextSource` 枚举：

| 值 | 行为 | 适用 server |
|----|------|-------------|
| `.rawJSON`（默认） | 全文以原始 JSON 字符串分页 | 通用，不做假设 |
| `.mcpContentText` | 取 `content[].text` 拼接为分页文本 | agent-browser（stdout/structuredContent 与 text 重复） |

不在通用路径假设 MCP 结果结构，由 server 维度配置。

## 兼容性

| 场景 | 行为 |
|------|------|
| 新服务端 × 旧客户端 | 多余字段被忽略，行为同现状；#4 兜底截断仍生效 |
| 新客户端 × 旧服务端 | `result_budget_bytes`/`timeout_secs` 缺失 → fallback 默认 20000/54s |
| 新客户端 × 新服务端 | 完全按预算链运作，服务端截断永不触发 |

## 测试

- 服务端 `go test ./internal/agent/ -run "TestNormalize" -v` — 截断兜底触发时打告警日志
- 服务端 `go test ./...` 全绿
- 客户端按约定由 Xcode 编译验证（不执行 xcodebuild）
- `server/scripts/client-tool-e2e.sh` — payload 加字段不影响脚本（只解析 call_id）

## 设计决策

| 问题 | 决策 | 理由 |
|------|------|------|
| 预算下发方式 | 随每个 `client_tool_call` 事件携带 | 免新的 config 端点；每次调用都是最新值 |
| timeout 也下发 | 是（`timeout_secs`） | 时间预算同理，统一治理，客户端用 90% (=deadline) |
| 分块内容配额 | 预算 × 80% | 足够保守（信封+JSON 转义 <20% 预算） |
| 极端保护 | 300 页硬上限 | 防止失控的 MCP tool 返回海量数据撑爆内存 |
| page_token 载体 | 短随机 base36 串（~7 字符） | token 开销极小，不占内容预算 |
