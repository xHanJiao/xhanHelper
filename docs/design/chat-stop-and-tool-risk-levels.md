# 计划：macOS 停止按钮 + 客户端工具分级（L1/L2/L3）

> 日期：2026-08-02
> 分支：`feature/chat-stop-tool-risks`
> 状态：已确认，待实施

## 一、背景

1. **macOS 无停止能力**：InputBar 停止按钮仅 iOS 分支存在；现有 `stopStreaming()` 只断 SSE——服务端 `orch.Run(context.Background())` 不受影响，ReAct 循环继续跑完（浪费 token、工具继续执行）。
2. **工具无分级**：客户端工具（LocalToolRegistry 链）全部自动执行，无风险控制。

## 二、服务端：显式终止机制（channel 方案）

### 2.1 运行注册表（新文件 `server/internal/controller/chat_stop.go`）

```go
type ChatStopRegistry struct {
    mu    sync.Mutex
    stops map[string]chan struct{}   // conversationID → stop signal
}
var chatStops = &ChatStopRegistry{stops: map[string]chan struct{}{}}

// Register 开始 chat 时注册；Unregister defer 回收
func (r *ChatStopRegistry) Register(convID string) chan struct{}
func (r *ChatStopRegistry) Unregister(convID string)
// Stop 幂等关闭 channel 发信号；无运行中的 chat 返回 false
func (r *ChatStopRegistry) Stop(convID string) bool
```

### 2.2 chat handler 集成（chat.go）

- 注册 + 回收：`stopCh := chatStops.Register(conv.ID); defer chatStops.Unregister(conv.ID)`
- **独立运行 ctx**（不依赖 Request.Context——网络波动不中断正常执行）：
  ```go
  runCtx, cancel := context.WithCancel(context.Background())
  defer cancel()
  go func() {
      select {
      case <-stopCh: cancel()        // 显式终止：channel 关闭 → 取消 ReAct
      case <-runCtx.Done():
      }
  }()
  events, err := orch.Run(runCtx, &agent.RunRequest{...})
  ```
- SSE 循环保留 `<-c.Request.Context().Done()` 检查：网络断开只停转发，**不取消运行**

### 2.3 终止接口

```
POST /api/chat/stop   body: {"conversation_id": "..."}   需 Bearer 认证
```
- 登录态：middleware 已注入 user_id
- 归属校验：复用 `requireConversationOwner`（非 owner 403 / 不存在 404）
- 响应：`{"stopped": true}` 或 `{"stopped": false, "reason": "no running chat"}`

### 2.4 半成品消息

取消路径（ctx 取消）下：已保存的 user 消息保留；**半成品 assistant 消息不保存不展示**。

## 三、客户端：停止按钮 + 显式终止调用

### 3.1 APIClient

```swift
func stopChat(conversationId: String) async throws   // POST /api/chat/stop
```

### 3.2 ChatViewModel.stopStreaming 改造

```swift
func stopStreaming() {
    Task {
        if let cid = conversationId, !cid.isEmpty {
            try? await apiClient.stopChat(conversationId: cid)
        }
        sseClient.disconnect()
        finishStreaming()
    }
}
```

### 3.3 macOS 停止按钮（InputBar.swift）

macOS 分支 TextEditor 右侧：流式时显示红色 `stop.circle.fill`（同 iOS 样式），点击调 `onStop`。

## 四、客户端工具分级（L1/L2/L3）

### 4.1 Registry 集中表（LocalToolRegistry.swift）

```swift
private let riskLevels: [String: Int] = [
    // L1 只读
    "browser__agent_browser_snapshot": 1, "browser__agent_browser_read": 1,
    "browser__agent_browser_get_text": 1, "browser__agent_browser_get_value": 1,
    "browser__agent_browser_get_url": 1, "browser__agent_browser_get_title": 1,
    "browser__agent_browser_list_tabs": 1,
    "browser__agent_browser_wait_for_load": 1, "browser__agent_browser_wait_for_selector": 1,
    // L3 高风险写（需用户确认）
    "browser__agent_browser_cgclick": 3, "browser__agent_browser_cgdrag": 3,
]
func riskLevel(for flatName: String) -> Int { riskLevels[flatName] ?? 2 }   // 未登记默认 L2
```
- 其余 browser 工具 + chart + MCP 动态工具 → 默认 L2
- **不传给 LLM**（clientToolsPayload 不打包级别，纯客户端执行控制）

### 4.2 执行拦截（ChatViewModel+ClientTools.swift handleClientToolCall）

```swift
let level = LocalToolRegistry.shared.riskLevel(for: toolName)
if level >= 3 {
    let confirmed = await viewModel.pendingToolConfirmation(toolName: toolName, arguments: args)
    guard confirmed else {
        try await api.submitToolResult(callId: callID, resultJSON: nil, error: "用户拒绝执行")
        return
    }
}
// 原执行逻辑不变
```
- 超时（队首 180s）→ 自动拒绝，提交"等待用户确认超时"

### 4.3 确认 UI（一行字，消息气泡下方）

- `ChatViewModel`：`@Published var pendingToolConfirmation: ToolConfirmation?`（{toolName, argsSummary, continuation}）
- MessageListView 最后一条消息下方渲染一行（左对齐、小字号）：
  ```
  ⚙️ 工具 cgclick 请求执行：ref=@e5   [允许] [拒绝]
  ```
- **自动滚动**：确认 UI 出现时若不在可视区，滚动到其位置
- 为未来"工具调用展示"预留状态位

## 五、改动文件清单

| 文件 | 改动 |
|------|------|
| `server/internal/controller/chat_stop.go` | 新增：ChatStopRegistry |
| `server/internal/controller/chat.go` | 注册/回收/独立 runCtx/goroutine 桥接 |
| `server/internal/controller/router.go` | 新增 POST /api/chat/stop 路由 |
| `server/internal/controller/conversations.go` | 新增 stopChat handler（复用 requireConversationOwner） |
| `client/.../APIClient.swift` | stopChat 方法 |
| `client/.../ChatViewModel.swift` | stopStreaming 改造 + pendingToolConfirmation |
| `client/.../ChatViewModel+ClientTools.swift` | 分级拦截 + 确认等待 |
| `client/.../LocalToolRegistry.swift` | riskLevels 表 + riskLevel(for:) |
| `client/.../InputBar.swift` | macOS 停止按钮 |
| `client/.../MessageListView.swift` | 确认一行字渲染 + 自动滚动 |

## 六、验证样例

1. macOS 流式中点停止 → 服务端日志确认 ReAct 中断、无半成品 assistant 入库
2. 网络波动 SSE 断 → ReAct 继续跑完（不误终止）
3. `POST /api/chat/stop` 未登录 401 / 非 owner 403 / 无运行 chat → stopped:false
4. cgclick/cgdrag 调用 → 一行字确认出现 + 自动滚动；允许执行 / 拒绝提交 / 队首 180s 超时提交
5. L1/L2 工具自动执行无确认；close/open 等默认 L2
6. 停止按钮仅流式时显示，点击后消失

---

## 后续变更

- **2026-10-04**：确认从单槽改队列，只显示队首；排队的并发审批不再互相覆盖。超时由 60s 调为 180s（只给队首计时），服务端 `llm.client_tool_timeout_secs` 120→240 配套。详见 `docs/bugs/027-macos-tool-confirmation-single-slot-overwrite.md`。影响 `ChatViewModel.swift`（`pendingToolConfirmations` 队列 + `resolveToolConfirmation(id:allow:)`）、`ChatView.swift`、`DiffPreviewView.swift`、`ContentView.swift`、`xHelperMac/App.swift`、`server/conf/config.yaml`。
