# Bug #005 — SSE 流式推送中途截断

> 发现日期: 2026-05-14 | 严重程度: P1（用户体验受损） | 状态: 已修复

---

## 问题表现

对话过程中，SSE 消息推送到前端会突然中断（loading 转圈或内容戛然而止），但刷新页面后消息完整出现。

## 根因分析

### 根因一：多处阻塞式 channel 发送导致死锁

事件通道 `events` 缓冲只有 64，而 `orchestrator.go`、`deepseek.go`、`doubao.go` 中共 15 处使用了阻塞式 `events <- evt` 直接发送。当消费端（SSE handler）因任何原因变慢时，64 缓冲迅速填满，一个阻塞发送卡住 → 上游 goroutine 卡住 → 下游也无法消费 → 全部死锁。

死锁链条：
```
浏览器/网络慢 → handler 消费慢 → channel 满
  → 某个 events <- evt 阻塞
  → 该 goroutine 无法继续消费自己的上游 channel
  → 上游 goroutine 也阻塞
  → 整个流水线死锁，流中断
```

### 根因二：`chat.go:135` 使用 `context.Background()` 导致断连后无法自愈

编排器 goroutine 使用 `context.Background()` 而非 `c.Request.Context()`，即使客户端断开，goroutine 也永远不会被取消。死锁发生后无法通过 context 取消来恢复，goroutine 永久泄漏。

**为什么 DB 有完整消息**：死锁可能发生在 ReAct 的某一轮工具调用后、`done` 事件发送前。消息已在 `saveAssistantMsg` 中入库，但 `done` 事件因 channel 满被卡住，SSE 流中断。刷新页面时从 DB 加载完整消息。

## 修复方案

### 1. 所有阻塞发送改为非阻塞 `sendEvent`

**`orchestrator.go`**（10 处）：
| 位置 | 事件类型 |
|------|----------|
| `runReActLoop` 初始化 | `status` |
| context 取消分支 | `error`, `done` |
| LLM 调用失败 | `error` |
| `handleNativeToolCalls` | `plan_node_start`, `tool_result`, `plan_node_done` |
| `handleFlashResponse` | `tool_result` |
| `executeConcurrently` | `tool_call` |
| `emitDone` | `done` |

**`deepseek.go`**（3 处，`readSSEStream`）：`text_delta`, `thought_delta`, `done`

**`doubao.go`**（2 处）：`text_delta`, `done`

### 2. 增大 channel 缓冲

| 文件 | 旧值 | 新值 |
|------|------|------|
| `orchestrator.go:121` | 64 | 512 |
| `deepseek.go:134` | 64 | 512 |
| `doubao.go:53` | 32 | 256 |

### 3. 保留 `context.Background()`

`chat.go:135` **不改**。原因是：如果换成 `c.Request.Context()`，客户端断开 → context 取消 → LLM HTTP 请求中断 → 消息接收不完整 → 无法存 DB。当前设计是"SSE 推送"和"消息接收+持久化"两条独立生命线，即使客户端断开，服务端也要收完 LLM 响应并存库。

## 消息丢弃风险评估

非阻塞 `sendEvent` 只在 channel 满时丢弃事件。正常运行时 512 缓冲足够，不会丢。异常情况下丢消息的影响：

| 事件类型 | 丢弃影响 | 风险 |
|----------|----------|------|
| `text_delta` | 前端少显示几个字，完整内容已存 DB | 低 |
| `thought_delta` | 同上 | 低 |
| `status` | 仅提示信息 | 极低 |
| `plan_node_*`, `plan` | Plan 图不完整 | 低 |
| `tool_call`, `tool_result` | 前端不显示工具调用过程 | 低 |
| `done` | 前端一直 loading。但发出时流程已收尾，channel 已排空，几乎不会丢 | 极低 |
| `error` | 错误提示丢失。同上，几乎不会丢 | 极低 |
