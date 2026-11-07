# 聊天流「挂起 → 回前台」续传与对账

> 2026-11-07 设计。目标：PWA / 客户端切后台、Mac 睡眠、断网之后回到前台，
> 聊天状态与服务端的真实情况一致——不假死、不假错、不丢半截回复，能接着看逐字输出。
>
> 状态：已实施（2026-11-07，待真机验收）
>
> 估算（开工）：~13–26h（当时按「P0 2–4h + P1 8–14h + Swift 增量」叠加，面缝式相加）
> 估算（定稿）：5 面 / 5 缝（2 条需先建探针）/ 5 次真机往返 / 0 个待拍决定 · ~6–8h（不含真机操作时间，另 1–2h）

---

## 1. 背景

### 1.1 现象

iPhone 主屏 PWA 里发完消息就切屏，回来时看到：**完整回复 + 一条 `Load failed` + [重新加载]**。

### 1.2 根因链

1. iOS 挂起 PWA，正在跑的 `/api/chat` SSE 连接被掐，`reader.read()` reject 出 `TypeError: Load failed`（WebKit 的网络层文案；Chrome 是 `Failed to fetch`）。
2. `useChatSSE.js:122-130` 的 catch → `failStream(err.message)` → `chat.error = "Load failed"`（`ChatView.vue:1024` 的红块只来自这里；历史拉取失败走的是 toast，不会进红块）。
3. 服务端**照常跑完**：`runCtx` 独立于 `Request.Context`（`chat.go:327`、`:369-372`），完整回复正常落库。
4. 回前台 `visibilitychange` → `syncCurrentConversation()` → `loadHistory()` → `setMessages()`，完整回复贴回来；**但成功刷新没清 `chat.error`**（只有点「重新加载」的 `retryAfterError` 才清）。

所以红条是「早就过期的断流报错」，内容没丢。

### 1.3 这不是一个新问题

- 上一轮的「已知残留」：`docs/bugs/046-pwa-conversation-list-stale.md` 第 45 行——「服务端没有 SSE 心跳帧…误判的代价是看到『连接超时』+ 丢实时增量」。
- 现有三处兜底都只能「收场」，不能「接回」：120s 空闲看门狗（`useChatSSE.js:12`）、`isStreaming` 时跳过同步（`ChatView.vue:391`、`ContentView.swift:534`）、Swift 对 `NSURLErrorNetworkConnectionLost` 的静默收尾（`ChatViewModel.swift:1094-1099`）。

### 1.4 三个决定方案的事实

| # | 事实 | 位置 |
|---|------|------|
| 1 | assistant 消息是**一轮 LLM 调用结束才落库**，不是边流边写。挂起期间拉历史看不到正在生成的那段文字 | `agent/orchestrator.go:1621/1672` |
| 2 | SSE **没有 `id:`、没有心跳、没有重连**；nginx `proxy_read_timeout 300s` | `view/render.go:23`、`docs/front-end/plan.md:786` |
| 3 | 挂起超过 120s 回来，`setTimeout` 的看门狗会**立刻补触发**，先弹「连接超时」再 abort | `useChatSSE.js:67` |

推论：**实时文本只存在于那条 TCP 连接里，没有第二份。** 要「接回」必须让服务端把本轮事件留一份。

---

## 2. 目标与非目标

**目标**

1. 回前台后 UI 状态 = 服务端这一轮的真实状态：不假死、不假错。
2. 跑完了 → 立刻显示完整答案，旧的断流报错自动消失。
3. 还在跑 → 接着看逐字输出（不是干等）。
4. 半截内容不重复、不丢；输入框及时恢复可用。
5. 协议一次设计，Web / macOS / iOS 共用；客户端逻辑各写一遍。

**非目标**

- 跨进程 / 多实例：hub 在内存里，只在单进程成立（与现有 `chatStops` 同一前提）。
- 增量落库（让「另一端」看直播）：DB 写放大 + 要处理截断/回滚，收益与 SSE buffer 重叠。
- 被 iOS 直接杀进程后的恢复：冷启动走现有拉历史路径。
- 客户端工具在挂起期间的**执行**保证：只保证事件能补发、审批能接上（240s 上限不变）。

---

## 3. 设计原则

1. **服务端是唯一事实来源**：这一轮是否还在跑、已经产出到第几帧，只有服务端知道。客户端只做对账。
2. **断点用序号，不用时间**：`seq` 天然去重，重连不需要判断「老连接是不是还活着」——回前台**无条件重连**即可。
3. **协议客户端无关**：心跳、`id:`、`idle`/`reset` 是 SSE 标准形态，两端解析器都能吃，差别只在消费逻辑。
4. **降级路径明确**：buffer 不可用时退化成「拉历史 + 明确提示」，绝不留在灰态。

---

## 4. 协议

### 4.1 心跳

`/api/chat` 与 `/api/chat/stream` 在事件转发循环里加 15s ticker，空闲时写：

```
: ping

```

（只有注释行，无 `event`/`data`。两端解析器**已经**忽略注释行：`utils/sse.js:22`、`SSELineDecoder.swift:63`。）

作用：① 区分「静默但活着」（verify / 子任务 ChatSync）与「半死」；② 让看门狗阈值能从 120s 降到 60s；③ 给 nginx 与 URLSession 的 idle timeout 续命。

### 4.2 帧序号

`view.WriteSSE` 旁边新增：

```go
func WriteSSEID(w io.Writer, flusher http.Flusher, id int64, event, data string) {
    data = strings.ReplaceAll(data, "\r\n", "\n")
    data = strings.ReplaceAll(data, "\n", "\ndata: ")
    fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, data)
    flusher.Flush()
}
```

- `id` 从 1 开始，**每轮独立**（`seq` 属于这一轮，不是会话级）。
- 老客户端把 `id:` 当未知字段忽略，向后兼容。

### 4.3 续传接口

```
GET /api/chat/stream?conversation_id=<id>&from=<seq>
Authorization: Bearer <token>
Accept: text/event-stream
```

`from` = 客户端已收到的最大 seq（没收到任何帧传 0）。语义：

| 服务端情况 | 响应 |
|---|---|
| 本轮还在跑 | 先补发 `seq > from` 的帧，然后挂成订阅者继续推；15s 心跳 |
| 本轮已结束，buffer 还在 | 补发剩下的帧（含 `done`）后关闭 |
| 没有本轮（后端重启 / 被 stop / 早就结束） | 写一帧 `event: idle` 后关闭 |
| `from` 早于 buffer 里最老的帧 | 写一帧 `event: reset` 后关闭 |

- 归属校验复用 `requireConversationOwner`（`conversations.go:199`）。
- 客户端工具、`subagent`、`plan` 等所有事件一视同仁进 buffer，重放顺序即原始顺序。
- 非流式请求体（GET 无 body），鉴权走 Bearer（用 fetch / URLSession，不是 `EventSource`，可带 header）。

### 4.4 两个控制帧

| 事件 | 含义 | 客户端动作 |
|---|---|---|
| `idle` | 没有可续的轮次 | 拉历史 → `isStreaming=false` → 清错误 |
| `reset` | 连续性断了（buffer 被裁） | 丢弃半截占位 → 拉历史 → 给「连接中断，点重新加载」提示 |

---

## 5. 服务端实现

### 5.1 turn hub（新文件 `server/internal/controller/chat_stream.go`）

```go
type sseFrame struct{ seq int64; event, data string }

type turnStream struct {
    mu        sync.Mutex
    seq       int64
    frames    []sseFrame          // 环形裁剪：超过 maxBytes 从头部丢
    bytes     int
    oldestSeq int64               // 头部被裁后指向第一帧的 seq
    subs      map[chan sseFrame]struct{}
    done      bool
    touchedAt time.Time
}

var chatStreams = ... // map[convID]*turnStream + 全局锁
```

| 项 | 值 | 理由 |
|---|---|---|
| 单轮 buffer 上限 | 4 MiB | 实测单轮增量远小于此；工具结果已被 `MaxToolResultChars` 截断 |
| 订阅者通道深度 | 256 帧 | 满了就踢（关闭该通道），客户端重连按 seq 补齐 |
| TTL | 轮结束后 10 分钟（`touchedAt` 计） | 覆盖「挂起很久才回来」；下一轮开始时立即替换旧的 |
| 清理 | `appinit` 用 `bgCtx` 起 1 分钟 ticker 调 `Sweep()` | 只回收 `done` 的轮；**活轮永不回收**（心跳不算 Publish，静默十几分钟是常态） |
| 写超时 | 每帧写前 `SetWriteDeadline(30s)` | 死连接会把内核发送缓冲填满、让 Write 阻塞；没有写超时就无法判定 clientGone |

API：

```go
func (h *hub) Begin(convID string) *TurnStream   // 新轮：替换同会话旧轮（与 chatStops.Register 同语义）
func (h *hub) Publish(convID, event, data string) // 入 buffer + 广播
func (h *hub) Subscribe(convID string, from int64) (frames []sseFrame, ch <-chan sseFrame, cancel func(), state subState)
func (h *hub) Finish(convID string)              // 标记 done + 记 touchedAt
func (h *hub) Sweep()                            // TTL 回收
```

`subState` ∈ `{subOK, subIdle, subReset}`，直接对应 §4.3 表。

### 5.2 chat handler 改造（`controller/chat.go`）

现在是「消费 `events` → 直接写 `w`」。改成「消费 → `Publish`（入 buffer + 广播给续连的订阅者）→ 本连接**照旧直接写 `w`**」，原连接的写出路径不用重构（避免自己订阅自己导致重复帧）：

```go
hub.Begin(conv.ID)                  // 新一轮：替换同会话旧轮（seq=1 先发 meta）
defer turn.Finish()                 // 标记结束；buffer 留给迟到的重连

ticker := time.NewTicker(15 * time.Second)
for {
    select {
    case evt := <-events:
        frame := turn.Publish(event, data)      // done 的 JSON 只算一次
        if !clientGone { writeFrame(w, frame) } // 写失败 -> clientGone
    case <-requestDone:                          // 只触发一次，随后置 nil
        clientGone = true
    case <-ticker.C:
        if !clientGone { writePing(w) }
    }
}
```

要点：

- **`clientGone` 只停止写，不停止 `Publish`**——这正是续传的数据来源。
- **`meta` 帧也要写给原连接**：`Begin` 只负责入 buffer + 广播给续连的订阅者；原连接直接写，所以要在循环前手写一次（否则正常路径拿不到 `turn_id` / 懒建会话 id）。
- `done` 的 JSON（含 `mergeTitle` 结果）**只生成一次**，同时用于写连接和入 buffer，避免重放时标题/卡片不一致。
- `chatStops.Register` 保持不变（停止语义不变）。
- `hub.Begin` 复用「同会话新轮取消旧轮」的既有约定（`chatStops.Register` 已这么做）。

### 5.3 路由

```go
auth.GET("/chat/stream", chatStream(rc.ConvRepo))
```

### 5.4 兼容性

- 老客户端不传 `from` → 等价 `from=0`；它们根本不会调这个接口，无影响。
- 心跳只增注释行，零影响。

---

## 6. Web 客户端

### 6.1 解析器（`utils/sse.js`）

- `id:` 解析：`onEvent(event, data, id)`，`parser.lastId` 可读；`id` 不参与 dispatch 的 switch。
- 注释行已有忽略逻辑，不动。
- `createIdleWatchdog` 增加 `pause()` / `resume(graceMs)`。

### 6.2 `useChatSSE.js`

| 改动 | 说明 |
|---|---|
| `lastSeq` / `lastTurnId` | 每轮从 0 / 空开始；`sendMessage` 重置；`resumeStream` 沿用 |
| `resumeStream()` | **代次守卫**：先把 `abortController` 换成新的再 abort 旧的；旧流的失败回调一律丢弃（`isCurrent`）。abort 旧流 → `api.streamGet('/api/chat/stream?...&turn=&from=')` → 复用 `parseSSEStream`；返回终止原因 `'idle' \| 'reset' \| 'done' \| 'unsupported' \| 'failed'` |
| `dispatchEvent` | 新增 `meta`（记 turn id / 拿懒建会话 id）、`idle` / `reset`（只置标志，收尾由调用方拉历史） |
| `failStream` | 网络层错误（`TypeError`）文案换成中文；**`document.visibilityState === 'hidden'` 时不亮错误、不结束流式**（回前台走 resume） |
| 看门狗 | 阈值 60s；挂起 `pause()`、回前台 `resume(15s)`；迟到补触发的超时在 `suspended` 时忽略 |
| `clearNetworkError()` | 只清网络类错误；服务端 `error` 保留 |
| `finishStream()` | 收尾但不置 `needsRefetch`（续传 idle/reset 用它） |

### 6.3 `api/client.js`

```js
streamGet: (path, signal) => request(path, { signal, headers: { Accept: 'text/event-stream' } })
```

### 6.4 `ChatView.vue`

- `loadHistory` 全量刷新成功返回 `true`（分页分支不算），`syncCurrentConversation` 据此清 `chat.error`。
- `onVisibilityChange`：

```js
if (document.visibilityState === 'hidden') { sse.setSuspended(true); return }
sse.setSuspended(false)
if (chat.isStreaming) {
  const why = await sse.resumeStream()
  if (why !== 'done') { sse.finishStream(); await syncCurrentConversation({ force: true }) }
} else {
  await syncCurrentConversation()
}
```

- `syncCurrentConversation` 去掉 `isStreaming` 早退（该判断上移到调用点），保留 3s 节流，但 `force` 跳过节流。
- `retryAfterError` 保持（它已经会清错误 + 拉历史）。

### 6.5 语义统一

续传时**保留 `streamingMessage` 继续追加**；只有 `reset` / `idle` 才丢弃它并改走拉历史。
另外：`streamingMessage` 已经被 `failStream` 收尾（为空）时不得续传——否则会另起一条气泡、和半截重复；
这种情况直接按 `reset` 处理。

---

## 7. macOS / iOS 客户端

### 7.1 现状差异（为什么不是「免费兼容」）

| 项 | Web | Swift |
|---|---|---|
| 心跳 | 已忽略注释 | 已忽略注释（`SSELineDecoder.swift:63`） |
| `id:` | 需加解析 | 现在当未知字段丢弃 |
| 断流处理 | 红块报错 | `NSURLErrorNetworkConnectionLost` 静默收尾，无 `done`（`ChatViewModel.swift:1094-1099`） |
| 判死后手 | 120s 看门狗 | `timeoutIntervalForRequest = 300`（`SSEClient.swift:26`），更迟钝 |
| 回前台 | `visibilitychange` | `scenePhase` + `didBecomeActive`，同样 `guard !isStreaming` 早退 |
| 多 tab | 无 | `chatTabManager`，每个 tab 一个 VM/SSEClient |
| 客户端工具 | 无 | fs / shell / browser，挂起时 `client_tool_call` 丢失 → 服务端白等 240s |
| 占位消息 | `local-` 假 id + `failedLocalId` | 哨兵 id `__streaming__` + `detachStreamingMessage()` |

关键：`loadConversation(preserveTransientState:)` **无条件** `sseClient.disconnect()` + `messages = 拉回的消息`（`ChatViewModel.swift:807-849`）。所以续传路径**不能**走它，否则会杀掉流、冲掉占位；`preserveTransientState` 只保思考/子任务/计划 UI 态。

### 7.2 `SSELineDecoder.swift` / `SSEClient.swift`

- `SSEEventAssembler` 增加 `id:` 解析，`feed` 返回 `(event, data, id)`；`StreamEvent` 加 `let id: String? = nil`（memberwise 默认值，现有 `StreamEvent(type:data:)` 调用点不用改）。
- `SSEClient` 拆出 `private func start(_ req: URLRequest)`（现有的 task 建立 + 状态重置），`connect` 与新的 `resume` 共用：

```swift
func resume(baseURL: String, token: String, conversationId: String, from: Int64)
```

GET + `Accept: text/event-stream` + Bearer；复用同一 delegate 与 `currentTask` 守卫（旧任务回调仍会被拦）。

- `StreamEventType` 新增 `.idle` / `.reset`（否则会走 unknown 警告）。

### 7.3 `ChatViewModel`

| 改动 | 说明 |
|---|---|
| `lastEventSeq` | 每次 `handleEvent` 从 `event.id` 更新 |
| `currentTurnID` / `pendingConversationID` | meta 帧下发：前者用于 `/api/chat/stream` 的 `turn`，后者给懒创建会话留续传用的 id（**不动 `conversationId`**，空白 tab 的落盘判定依赖它） |
| `resumeStreaming()` | `guard isStreaming`；**不**调 `loadConversation`；拿不到 id 时自己收尾并返回 false（调用方改走拉历史） |
| `.meta` | 记 turn id / pending conv id；turn 不匹配按 reset 收尾 |
| `.idle` / `.reset` | `finishStreaming()` + `detachStreamingMessage()` + `loadConversation(...)`（失败也不留 `__streaming__` 哨兵） |
| `.done` | 不变（占位仍在 → `replaceStreamingMessageID` 正常换真实 id） |
| `handleClientToolCall` | 按 `call_id` 去重（`handledClientToolCallIDs`，每轮清空）。重放可能带来已处理过的指令；服务端 `OnResult` 对已消费 id 返回 404，但客户端不该二次弹审批 |
| `loadConversation` | 保持现状（调用方负责不在流式中调它） |

### 7.4 `ContentView`

`scenePhase == .active` 与 `didBecomeActive` 两处（`ContentView.swift:527/548`）：

```swift
if let vm = activeChatViewModel, vm.isStreaming {
    vm.resumeStreaming(baseURL: ..., token: ...)
    return   // 只跳过 syncActiveChat：续传自己会补 done
}
// 原逻辑：syncActiveChat + reminders + 会话列表
```

macOS 多 tab：遍历 `chatTabManager` 中 `isStreaming` 的 tab 逐个 `resumeStreaming`（buffer 按会话区分，不会串台）。iOS 单 VM，天然一条。

### 7.5 测试靶

`Package.swift` 新增小 target 收 `SSELineDecoder.swift`（纯逻辑、只依赖 Foundation），
`swift test` 覆盖：`id:` 解析 / 多行 `data:` / 注释行 / 截断在 UTF-8 中间。不碰 Xcode。

---

## 8. 状态机总表（回前台）

| 客户端状态 | 服务端这一轮 | 动作 | 最终 UI |
|---|---|---|---|
| 不在流式 | — | 拉历史 | 最新消息 + 清旧错误 |
| 在流式 | 还在跑 | 续传（补帧 + 跟流） | 继续逐字输出 |
| 在流式 | 已结束，buffer 在 | 续传（补到 `done`） | 完整消息 + 正常元数据 |
| 在流式 | 没有本轮 | `idle` → 拉历史 | 完整消息 / 半截消失，无假错 |
| 在流式 | buffer 被裁 | `reset` → 拉历史 + 提示 | 与服务端一致，可手动刷新 |

---

## 9. 边界与失败模式

| 场景 | 行为 |
|---|---|
| 服务端重启（本轮丢失） | `idle` → 拉历史；若用户消息没回复，就是现状（本轮答案确实丢了） |
| 多实例部署 | **失效**（内存 hub）。上多实例时与 `chatStops` 一起迁移到 Redis/共享层 |
| 挂起期间客户端工具待审批 | 重放 `client_tool_call` → 审批框重新出现，240s 内回传仍有效；按 `call_id` 去重 |
| 多端同会话 | 后发者取消前一轮（既有行为）；buffer 按会话，两端都能续 |
| buffer 超限 | 丢头部、`oldestSeq` 前移；重连方收到 `reset` |
| 长时间静默（verify/子任务） | 心跳保活；60s 看门狗不再误判 |
| 客户端断网（离线） | 续传请求失败 → 退回「拉历史」，不自动重试（避免空转） |
| 旧后端（无 `/api/chat/stream`） | 客户端把 404/501 当「不支持续传」→ 直接拉历史，不弹错 |
| 旧客户端 + 新后端 | `id:` / `meta` / 心跳被忽略（未知字段/注释行/未知事件各警告一次），行为与今天一致 |
| 本地后端（SQLite 单进程） | 同样成立 |
| 写超时触发 | 判为 clientGone：停止写、继续 Publish，客户端回前台按 seq 续上 |

---

## 10. 改动文件清单

**服务端**

| 文件 | 改动 |
|---|---|
| `controller/chat_stream.go` | 新增：turn hub（Begin/Publish/Subscribe/Finish/Sweep）+ `chatStream` handler |
| `controller/chat.go` | 事件循环接 hub + 15s 心跳 + 30s 写超时；`done` JSON 只算一次；`clientGone` 只停写不停 Publish |
| `controller/router.go` | `GET /api/chat/stream` |
| `view/render.go` | `WriteSSEID` / `WriteComment` / `SetWriteTimeout` |
| `appinit/agents.go` | `bgCtx` 起 1 分钟 `Sweep()` |
| `controller/chat_stream_test.go` / `view/render_test.go` | 补发 / 裁剪 / idle·reset / 轮次不匹配 / 旧句柄不串轮 / 活轮不回收 / 帧格式 |

**Web**

| 文件 | 改动 |
|---|---|
| `utils/sse.js` | `id:` 解析；看门狗 `pause/resume` |
| `composables/useChatSSE.js` | `lastSeq` / `resumeStream` / `idle`·`reset` 分支 / 挂起期静默 / 中文文案 / 60s 阈值 |
| `api/client.js` | `streamGet` |
| `views/ChatView.vue` | visibility 状态机；`loadHistory` 成功返回 bool；成功刷新清错误 |
| `utils/sse.test.js` | `id:` 解析、看门狗 pause/resume |

**Swift（Shared，两 target 共用）**

| 文件 | 改动 |
|---|---|
| `Services/SSELineDecoder.swift` | `id:` 解析 |
| `Services/SSEClient.swift` | 拆 `start(req)`；新增 `resume(...)` |
| `Models/StreamEvent.swift` | `id` 字段；`.idle` / `.reset` |
| `ViewModels/ChatViewModel.swift` | `lastEventSeq` / `resumeStreaming` / `idle`·`reset` 分支 |
| `ViewModels/ChatViewModel+ClientTools.swift` | `call_id` 去重 |
| `Views/ContentView.swift` | scenePhase / didBecomeActive 的「流式中改续传」（多 tab 逐个） |
| `Tests/SSELineDecoderCoreTests` + `Tests/ChatWireCoreTests` | `id:` 解析、事件名映射 |

---

## 11. 测试与验证

**机器可判（面）**

- Go：`seq` 单调、按 `from` 补发、裁剪后 `oldestSeq` 正确、`idle`/`reset` 判定、订阅者通道满被踢、`done` 只生成一次。
- Web：`npm test`（SSE 解析 `id:` / 看门狗暂停 / `resumeStream` 的三种终止）。
- Swift：`swift test`（`SSELineDecoder`）/ `swiftc -typecheck`（两个 target 各一批，带真实平台参数）。

**真机（缝，5 次往返）**

1. PWA：发消息 → 立刻切屏 2 分钟 → 回来：逐字继续，无红条，`done` 元数据齐（灵阅卡片 / 记忆）。
2. PWA：流式中切屏 5 秒回来（连接可能还活着）：不重复文字，不双份消息。
3. PWA：流式结束后才切屏 → 回来：只拉历史，无错误、无 spinner。
4. PWA：挂起 >10 分钟（buffer 过期）→ 回来：`idle` / `reset` 分支正常，无灰态。
5. macOS：流式中合盖睡眠再唤醒 / 切换网络 → 续传或正常收尾；多 tab 不串台；客户端工具审批能接回。

---

## 12. 实施顺序

同批实施、一次发布：**后端 → Web（nginx）→ Swift 客户端**。后端先上，因为新增的
`id:` / 心跳 / `meta` 对旧客户端全是「忽略即可」的增量；客户端对 404 已做降级。

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | 心跳 + 写超时 + 代次守卫 + 看门狗挂起暂停 + 成功刷新只清网络类错误 | ✅ 已实施 |
| P1-a | 服务端 hub + `/api/chat/stream` + `meta` / `id:` + Go 单测 | ✅ 已实施 |
| P1-b | Web 续传（`id:` 解析、`resumeStream`、`idle`/`reset` 收尾、404 降级） | ✅ 已实施 |
| P1-c | Swift 续传（`id:`、`SSEClient.resume`、`resumeStreaming`、client tool 去重、多 tab） | ✅ 已实施 |

**待拍决定：0 个**（Swift 同批已定）。剩真机验收：Web 4 条 + macOS 1 条。

---

## 13. 不做 / 后续

- 增量落库、跨端直播：不做（见 §2）。
- 多实例共享 buffer：等真上多实例时和 `chatStops` 一起做。
- 服务端主动重试 / 断流后自动重连（客户端侧只有「回前台触发」这一处重连，不做后台定时重连）。
- 失败重试的自动轮询：`reset` 后给按钮，不自动轮询。

---

## 后续变更

- **2026-11-07 实施**（分支 `feat/chat-stream-resume`）：按本方案落地，与原设计相比有 5 处修正——
  1. **旧流失败按代次丢弃**：`resumeStream` 先替换 `abortController` 再 abort 旧流，旧流的 catch 一律 `return`。没有这条，回前台「先起续流、旧 rejection 后到」会打掉新流的状态（`useChatSSE.js`）。
  2. **hub 按轮句柄发布**：`Begin` 返回 `*TurnStream`，`Publish/Finish` 是它的方法，不按 convID 查表——否则新轮 `Begin` 之后旧 handler 晚到的 `error`/`done` 会写进新轮 buffer（`chat_stream.go`）。
  3. **写超时**：每帧写前 `SetWriteDeadline(30s)`；死连接从「静默丢 delta」变成显式 `clientGone`（`chat.go`、`view/render.go`）。
  4. **`meta` 帧**（seq=1）带 `turn_id` + `conversation_id`：解决懒创建会话首轮无法续传；Swift 侧只存 `pendingConversationID`，不动 `conversationId`（空白 tab 的落产判定依赖它）。
  5. **`Sweep` 只回收已结束的轮**：心跳不算 Publish，静默十几分钟的活轮不能被 TTL 删掉。
- **2026-11-07 代码评审修正**：
  6. **挂起判定改用持久标记**（`hiddenSinceSend`）：iOS 的 rejection 要等恢复后才执行，那时 `visibilityState` 已是 visible，只查现态会漏掉「挂起期断流」这条分支。
  7. **续传窗口内的 404/501 不当成会话已销毁**（Swift `isResumeInFlight`）：旧后端没有 `/api/chat/stream`，否则回一次前台就会关掉用户的 tab。
  8. **`done` 的 ctx 用事实判断**（`clientGone || ctx.Err() != nil`）：`events` 与 `requestDone` 同时就绪时 select 随机，标志可能还没置上。
  9. `lastSeq` 移到代次守卫之后推进（被丢弃的帧不能成为续传断点）；两处错误分类抽成 `classifyError`；`setDeadline` 抽成 `newDeadlineSetter`；`WriteSSE` 返回 error 并由调用方记日志。
  影响文件见 §10；测试：`go test ./...`、`npm test`、`swift test`（371 项）全绿，两个 target `swiftc -typecheck` 通过。
