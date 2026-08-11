# 009 — macOS 客户端 verify 开关化 + 工具调用可视化

## 目标

1. verify 从全局配置转为客户端可控开关（per-conversation，双向覆盖，默认关）
2. macOS 客户端接入 `tool_call`/`tool_result`/`verify` 事件，在最新助手气泡下显示工具执行反馈（消除"卡住"感知）
3. 修 statusMessage 残留 bug + done.error 提示

## SSE 事件现状（调研摘要）

### 后端发送的事件（`controller/chat.go:259-284` + `orchestrator.go`）

| 事件 | Payload | 时机 |
|------|---------|------|
| `message` | `{"delta":str}` | 流式文本 token（含 prompt-eng 的 streamTextChunks 重播） |
| `thought_delta` | 原始文本 | DeepSeek 思考流 |
| `thought_done` | 空 | 思考块结束 |
| `status` | 纯文本 | 解析重试提示 / 等客户端工具心跳 |
| `tool_call` | `{tool,function,args,display,arg_labels}` | 每个工具执行前（`executeConcurrently:1357`，顺序发送） |
| `tool_result` | `{success,data,error}` | 每个工具执行完（`collectToolResults:1276/1295`，全部执行完后批量发送） |
| `client_tool_call` | `{call_id,tool_name,arguments,...}` | 转发本地 MCP 工具给 Mac |
| `verify` | `{status:checking\|done\|retry, retry:int, reason:string}` | 答案校验循环（`verifyAndMaybeRetry:1137`） |
| `reminders_changed` | `{}` | 提醒创建后 |
| `done` | `{conversation_id,message_id,user_message_id,memory_ids?,note_ids?,error?,title?}` | 流结束 |
| `error` | 纯文本 | 失败 |

### macOS 客户端接入现状（`ChatViewModel.swift:582-678`）

| 事件 | 处理 |
|------|------|
| `message` / `thought_delta` / `thought_done` | ✅ 已接入 |
| `status` | ✅ 已接入（`statusMessage`） |
| `client_tool_call` | ✅ 已接入（本地 MCP 执行 + 确认栏） |
| `done` / `error` | ✅ 已接入（缺少 done.error 处理） |
| **`tool_call`** / **`tool_result`** | ❌ 空 break（`:624-628`） |
| `verify` | ❌ SSEClient 层丢弃（`:113` default return） |

### Verify 现状

- 全局配置：`config.yaml → llm.verification.enabled`（`config.go:117-121`）→ `appinit/agents.go:166` → `orchestrator.verifyEnabled`（构造时固化，不可运行时切换）
- 判定点唯一：`orchestrator.go:929-930` — `o.verifyEnabled && len(state.allToolCalls) > 0 && !req.SkipRetrieval && !state.finalRound`
- 当前**关闭**，`verify` 事件虽已转发但无客户端消费

### 客户端现有开关机制（参考 deepMode）

- deepMode：`ChatViewModel.swift:255-268` toggle + per-conv UserDefaults `xhan_conv_deep_mode_<id>`
- 发送：`SSEClient.connect()` body `deep_mode: true`（`:29`）
- UI：`ChatView.swift:150` ModelPill（`#if os(macOS)` 内）
- 会话切换恢复：`loadConversation:389`
- 会话创建预存：`handleEvent .done:645`

## 时序分析

### 工具执行时序（`executeConcurrently:1331-1407`）

```
tool_call(工具A) → tool_call(工具B)  // 顺序发送，goroutine 并发执行
→ [等待 wg.Wait()]
→ tool_result(A✅) → tool_result(B✅) // 批量发送（collectToolResults 顺序出）
```

客户端看到：tool_call 瞬间来多个（running 状态）→ 静默等待 → tool_result 瞬间来多个（done/failed）。UI 必须在"静默等待"段展示工具名 + 加载态，否则用户感知卡住。

### Verify 时序（`handleOneRoundResponse:841-919`）

**Prompt-eng 路径**（`useNativeFC=false`）：

```
LLM JSON(no tools) → shouldVerify? → verify:checking → [LLM verify call 1-2s]
  → verify:done → streamTextChunks(reply 文本) → done
  → verify:retry → [新 LLM round: tool_call → tool_result → ...] → verify 重试
```

文本流在 verify 之后才出现。

**Native FC 路径**（`useNativeFC=true`）：

```
LLM 流式文本 + tool_calls → 工具执行 → LLM 新一轮
→ 无 tool_calls → 文本已流完 → shouldVerify? → verify:checking → verify:done → done
```

文本先到，verify 后到。

### 客户端状态清理

Vue 在首个 `message` delta 清所有 toast（`useChatSSE.js:126` + `ToolCallToast.vue:185-200`）。macOS 当前只在 `done` 清 `statusMessage`（`:639`）→ 残留 bug。

## 实施步骤

### 一、后端（5 处改动）

#### 1. `controller/chat.go:58-70` — request 加字段

```go
var req struct {
    ConversationID *string                `json:"conversation_id"`
    Message        string                 `json:"message"`
    DeepMode       bool                   `json:"deep_mode"`
    AgentID        *string                `json:"agent_id"`
    Memory         retrieval.SourceConfig `json:"memory"`
    InitMode       string                 `json:"init_mode"`
    Temperature    *float64               `json:"temperature"`
    MaxTokens      *int                   `json:"max_tokens"`
    Thinking       *bool                  `json:"thinking"`
    ClientTools    []agent.ClientTool     `json:"client_tools"`
    VerifyEnabled  *bool                  `json:"verify_enabled"` // 新增
}
```

语义：nil = 跟随服务端全局配置，true/false = 强制覆盖。

#### 2. `agent/orchestrator.go:52` — RunRequest 加字段

```go
type RunRequest struct {
    // ...现有字段...
    VerifyEnabled *bool // 新增：per-request verify 覆盖
}
```

#### 3. `controller/chat.go:220` — 传入

```go
events, err := orch.Run(runCtx, &agent.RunRequest{
    // ...现有字段...
    VerifyEnabled: req.VerifyEnabled,
})
```

#### 4. `agent/orchestrator.go:929-930` — 判定改写

```go
func (o *Orchestrator) shouldVerify(req *RunRequest, state *reactState) bool {
    enabled := o.verifyEnabled
    if req.VerifyEnabled != nil {
        enabled = *req.VerifyEnabled
    }
    return enabled && len(state.allToolCalls) > 0 && !req.SkipRetrieval && !state.finalRound
}
```

#### 5. `agent/orchestrator_verify_test.go` — 新增用例

- 显式 `VerifyEnabled: ptr(true)` — 配置 off 仍开
- 显式 `VerifyEnabled: ptr(false)` — 配置 on 仍关
- `VerifyEnabled: nil` — 跟随配置行为不变

### 二、客户端

#### 6. `Shared/Models/StreamEvent.swift` — 加 case

```swift
enum StreamEventType: String {
    case plan
    case planNodeStart
    case planNodeDone
    case planDone
    case thought
    case thoughtDelta
    case thoughtDone
    case status
    case verify        // 新增
    case message
    case toolCall
    case toolResult
    case clientToolCall
    case remindersChanged
    case done
    case error
}
```

#### 7. `Shared/Services/SSEClient.swift`

**L96-114 dispatch** — 加：
```swift
case "verify": type = .verify
```

**L20 connect** — 签名加 `verifyEnabled: Bool? = nil`，body 加：
```swift
if let ve = verifyEnabled { body["verify_enabled"] = ve }
```

#### 8. `Shared/ViewModels/ChatViewModel.swift`

**新模型**（文件顶部或末尾）：

```swift
struct ToolCallDisplay {
    let display: String
    var status: Status
    var summary: String?
    enum Status { case running, done, failed }
}

struct VerifyStatus {
    let status: String  // checking | done | retry
    let retry: Int
}
```

**新 @Published 字段**（`:42-50` 区域）：

```swift
@Published var verifyEnabled = false
@Published var toolQueue: [ToolCallDisplay] = []
@Published var verifyStatus: VerifyStatus? = nil
```

**verifyEnabled Toggle**（`:255-268` 旁）：

```swift
func toggleVerify() {
    verifyEnabled.toggle()
    UserDefaults.standard.set(verifyEnabled, forKey: verifyEnabledDefaultsKey)
    ToastCenter.shared.show(verifyEnabled ? "已开启：回复校验" : "已关闭：回复校验")
}

private var verifyEnabledDefaultsKey: String {
    "xhan_conv_verify_enabled_\(conversationId ?? "")"
}
```

**handleEvent 改动**（`:582-678` switch）：

| Event | 行号 | 改动 |
|-------|------|------|
| `.toolCall` | `:624-625` break → | `parseToolCall(data)` → `toolQueue.append(ToolCallDisplay(display: ..., status: .running))` |
| `.toolResult` | `:627-628` break → | `parseToolResult(data)` → 匹配队列最后 running → 置 `.done`/`.failed` + summary |
| `.verify` | 新 case | `parseVerify(data)` → `verifyStatus = VerifyStatus(status:..., retry:...)` |
| `.message` | `:608` | 加 `statusMessage = nil`（修残留 bug） |
| `.done` | `:637` | 加 `parseDoneError(data)` 非空 toast；`verifyStatus = nil`；`toolQueue = []` |
| `.error` | `:670` | 加 `toolQueue = []`；`verifyStatus = nil` |

**其他方法改动**：

| 方法 | 改动 |
|------|------|
| `sendMessage` | 加 `toolQueue = []`；`verifyStatus = nil` |
| `finishStreaming` | 加 `toolQueue = []`；`verifyStatus = nil` |
| `loadConversation` | `deepMode` 加载下行加 `verifyEnabled = UserDefaults.standard.bool(...)` |
| `handleEvent .done` | `deepMode` 保存下行加 verify 预存 |
| `setupSSE` | `connect()` 调用加 `verifyEnabled: verifyEnabled` |

**解析辅助函数**：

```swift
private func parseToolCall(_ raw: String) -> (display: String, args: [String: Any]?) {
    guard let d = raw.data(using: .utf8),
          let tc = try? JSONSerialization.jsonObject(with: d) as? [String: Any],
          let display = tc["display"] as? String else { return (tc?["function"] as? String ?? "工具", nil) }
    return (display, tc["arg_labels"] as? [String: Any])
}

private func parseToolResult(_ raw: String) -> (success: Bool, error: String?) {
    guard let d = raw.data(using: .utf8),
          let tr = try? JSONSerialization.jsonObject(with: d) as? [String: Any],
          let success = tr["success"] as? Bool else { return (false, "解析失败") }
    return (success, tr["error"] as? String)
}

private func buildToolSummary(display: String, success: Bool, error: String?) -> String {
    if success { return "✅ \(display) 已完成" }
    return "❌ \(display): \(error ?? "失败")"
}

private func parseVerify(_ raw: String) -> (status: String, retry: Int)? {
    guard let d = raw.data(using: .utf8),
          let v = try? JSONSerialization.jsonObject(with: d) as? [String: Any],
          let status = v["status"] as? String,
          let retry = v["retry"] as? Int else { return nil }
    return (status, retry)
}

private func parseDoneError(_ raw: String) -> String? {
    guard let d = raw.data(using: .utf8),
          let done = try? JSONSerialization.jsonObject(with: d) as? [String: Any],
          let err = done["error"] as? String, !err.isEmpty else { return nil }
    return err
}
```

#### 9. 新文件 `Shared/Views/ToolActivityToast.swift`

```swift
struct ToolActivityToast: View {
    let toolQueue: [ToolCallDisplay]
    let verifyStatus: VerifyStatus?

    var body: some View {
        HStack(spacing: 8) {
            if let current = toolQueue.first {
                toolPill(current)
                    .transition(.move(edge: .trailing).combined(with: .opacity))
            } else if let vs = verifyStatus {
                verifyPill(vs)
                    .transition(.move(edge: .trailing).combined(with: .opacity))
            }
        }
        .animation(.easeInOut(duration: 0.25), value: toolQueue.first?.status)
        .animation(.easeInOut(duration: 0.25), value: verifyStatus?.status)
    }

    @ViewBuilder
    func toolPill(_ tc: ToolCallDisplay) -> some View {
        HStack(spacing: 6) {
            Image(systemName: iconFor(tc.status))
                .font(.caption)
                .foregroundColor(iconColorFor(tc.status))
            Text(tc.status == .running ? tc.display : (tc.summary ?? tc.display))
                .font(.caption)
                .foregroundColor(.secondary)
        }
        .padding(.horizontal, 12).padding(.vertical, 6)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(.white.opacity(0.35), lineWidth: 0.5))
    }

    @ViewBuilder
    func verifyPill(_ vs: VerifyStatus) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "text.magnifyingglass")
                .font(.caption)
                .foregroundColor(.accentColor)
            Text(verifyLabel(for: vs))
                .font(.caption)
                .foregroundColor(.secondary)
        }
        .padding(.horizontal, 12).padding(.vertical, 6)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(.white.opacity(0.35), lineWidth: 0.5))
    }

    func iconFor(_ s: ToolCallDisplay.Status) -> String {
        switch s {
        case .running: return "gearshape"
        case .done: return "checkmark.circle.fill"
        case .failed: return "xmark.circle.fill"
        }
    }

    func iconColorFor(_ s: ToolCallDisplay.Status) -> Color {
        switch s {
        case .running: return .accentColor
        case .done: return .green
        case .failed: return .red
        }
    }

    func verifyLabel(for vs: VerifyStatus) -> String {
        switch vs.status {
        case "checking": return "结果检查中...（第\(vs.retry)次）"
        case "done": return "✅ 结果检查通过"
        case "retry": return "检查未通过，正在完善...（第\(vs.retry)次）"
        default: return "检验中..."
        }
    }
}
```

**设计原则**：
- 单条排队：`toolQueue.first` 展示当前，结果到即切（不固定停留），失败多停 2s
- 工具优先于 verify（时序不冲突：工具先于文本，verify 在文本后）
- 文本开始流（`message` delta）→ toolQueue 被清 → pill 消失
- 毛玻璃 pill 样式，仿 ThinkingBubble

#### 10. `Shared/Views/MessageListView.swift`

**构造函数加参数**：

```swift
struct MessageListView: View {
    // ...现有参数...
    let toolQueue: [ToolCallDisplay]   // 新增
    let verifyStatus: VerifyStatus?    // 新增
```

**messageRowContent 加 ToolActivityToast**（`:309-328` VStack 内）：

```swift
VStack(spacing: 12) {
    // 现有：ThinkingBubble (L310)
    // 现有：ChatBubble (L316-328)

    // 新增：工具/校验 toast，仅在 streaming 气泡下
    if msg.id == "__streaming__" && (!toolQueue.isEmpty || verifyStatus != nil) {
        ToolActivityToast(toolQueue: toolQueue, verifyStatus: verifyStatus)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, Layout.chatHorizontalPadding)
    }
}
```

**调用方传递**：需在实例化 `MessageListView` 的地方（`ChatView.swift` 或 `FloatingChatPanel.swift`）传入 `viewModel.toolQueue` / `viewModel.verifyStatus`。

#### 11. `Shared/Views/ChatView.swift` — 开关 UI

`:150` ModelPill 旁（`#if os(macOS)` 内）：

```swift
ModelPill(context: ctx, isDeep: viewModel.deepMode) {
    viewModel.toggleDeepMode()
}
VerifyPill(context: ctx, isOn: viewModel.verifyEnabled) {
    viewModel.toggleVerify()
}
```

`VerifyPill` 与 `ModelPill` 同风格：毛玻璃胶囊 + accent 高亮 + SF Symbol `text.magnifyingglass` + tooltip "回复质量校验（每轮多一次 LLM 调用）"。若 ModelPill 为内联 View 无需抽取，则直接复用其代码块。

**iOS 端无开关**：`#if os(macOS)` 保护，iOS 不渲染 pill。

#### 12. 清 statusMessage 残留（`ChatViewModel.swift`）

`.message` case 第一帧（`:609` 附近）：

```swift
case .message:
    if streamingContent.isEmpty { statusMessage = nil } // 首个 delta 清状态
    if let delta = parseMessageDelta(event.data) {
        streamingContent += delta
        throttleFlush()
    }
```

## 开关默认值

首次安装 / 升级：`verifyEnabled = false`（`UserDefaults.bool` 缺省 false）。与当前行为一致（服务端配置 off + 客户端不传 → verify 不触发）。

## 验证清单

| # | 测试场景 | 期望 |
|---|---------|------|
| 1 | 默认关 + 工具类消息 | 无 verify 事件，无新 pill，tool pill 正常 ⚙️→✅ |
| 2 | 开校验 + "帮我记账15块" | tool pill → ✅ → 文本流 → verify pill 🔍 → done |
| 3 | 开校验 + verify 未通过（retry） | verify:retry → pill 变"检查未通过…" → 新 LLM → done |
| 4 | 多工具并发 | N 个 tool_call running → tool_result×N done → 逐个展示 |
| 5 | 停流 | pill + verify all clear |
| 6 | 切换会话 | 新会话 verifyEnabled 独立恢复 |
| 7 | statusMessage 残留 | 文本流第一帧清除 |
| 8 | done.error（max iterations） | toast 提示 |
| 9 | 后端 test | `go test ./...` verify 3 新用例 pass |

## 不改

- Vue 前端（不改）
- iOS UI（共享 VM 逻辑编译但无 toggle，verify 事件达 VM 但默认关 → 无可见变化）
- 死代码（PlanModal 等）
- 后端 `verify` 事件转发已存在（`chat.go:278`），无需改

## 参考文献

- SSE 事件调研：本代码库 `agent/orchestrator.go`、`controller/chat.go`、`view/render.go`
- Vue 参考实现：`front-end/src/components/ToolCallToast.vue`、`front-end/src/composables/useChatSSE.js:16-22`
- macOS 现有参考：`ChatViewModel.swift` deepMode + `ChatView.swift` ModelPill
