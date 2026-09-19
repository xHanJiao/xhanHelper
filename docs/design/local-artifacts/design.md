# 本地产物管线（LocalArtifact）

> 日期：2026-09-19
> 状态：✅ 已实现（客户端纯逻辑 `swift test` 绿；macOS/iOS `swiftc -typecheck` 0 error；集成由 Xcode 真机验证）
> 来源：文档内链需求（聊天里的「打开文档」卡片）倒逼出的一次通用化
> 前身：`chart` 客户端工具的本地产物落盘（`LocalToolResultStore` / `insertChartMessage`）

## 一、背景与问题

聊天里有些**工具结果**不止是回填给模型的文本，还需要**在会话流里做特殊展示**，并希望**切屏/重载后仍在**（本地缓存）：

- `chart` 客户端工具：图表要渲染成 `ChartBubble`，且重载后还在。
- `document` 工具（新增需求）：agent 建/改过的文档要渲染成可点卡片，点开右栏文档面板定位。

改造前，这套「特殊展示 + 本地缓存」是 chart 专属的一坨散装代码：

| 阶段 | 位置 | 问题 |
|---|---|---|
| 触发（客户端 action） | `ChatViewModel+ClientTools.handleClientAction` | 只认 chart |
| 触发（服务端 tool result） | `ChatViewModel` `.toolResult` | 无通用入口 |
| 构建本地消息 + 落盘 | `insertChartMessage` | 每类型要复制一份 |
| 暂存字段 | `Message.chartData` | 每类型要加一个字段 |
| 恢复 + 解码 + 重定位 | `mergeLocalToolResults` | 每类型加一个 if 分支 |
| 渲染 | `MessageListView` `if let cd = msg.chartData` | 每类型加一个 if 分支 |
| 持久化 | `LocalToolResultStore` | 已泛型，OK |
| 不可分享 | `MessageListView.isUnshareable` | 已由 `localToolResult != nil` 覆盖 |

再加 docRefs，就要在 5 处各复制一段。故抽成统一管线。

### 1.1 必须绕开的坑：assistant 消息 id 会变

SSE 流式期间 assistant 占位消息 id 是哨兵 `__streaming__`，工具结果（`tool_result` 事件）**先于**真实消息 id 到达，`.done` 才把 id 换成服务端真实 id。

所以**绝对不能把本地产物按 assistant 消息 id 绑定**——绑定时刻的 id 与之后的 id 不是同一个。chart 当年的解法是：本地产物**自成一个稳定 id 的本地消息** + `createdAt`，持久化后按时间重放、按「其后最近一条 assistant」重定位。本管线沿用这个解法，并把它抽成纯函数以便单测。

## 二、目标 / 非目标

**目标**
- 统一的「工具结果 → 特殊展示 + 本地缓存」管线；新增类型只写「enum case + 载荷 Codable + 渲染视图 + producer 分支」。
- 本地产物按会话持久化，切屏/重载后仍在，且**挨在产生它的那条回复下方**（不浮在最新消息下）。
- 管线纯逻辑可单测（`swift test`）。
- chart 迁移到同一管线，不保留双轨。

**非目标**
- 不做 producer 协议/注册表（1~2 种类型时一个 switch 更短更安全；第 4+ 种再升）。
- 不重写 `LocalToolResultStore`（已是泛型）。
- 不统一 clientAction 与 toolResult 两条触发源（来源不同，强行统一更绕）。
- 不做跨设备同步（本地缓存是设备本地的）。

## 三、设计

### 3.1 统一产物：`LocalArtifact`

`Shared/LocalArtifacts/LocalArtifact.swift`——闭集 enum，新类型加 case 后编译器强制补齐三处 switch：

```swift
enum LocalArtifact: Equatable {
    case chart(ChartData)
    case docRefs([DocumentRef])

    init?(type: LocalToolResultType, payload: Data)   // 解码
    var type: LocalToolResultType                      // 类型
    func payload() throws -> Data                      // 编码
}
```

`Message` 用一个瞬态字段 `localArtifact: LocalArtifact?` 承载解码结果（替换原 `chartData`）；持久化的是 `localToolResult.payload`（`LocalToolResultData { type, payload: Data }`），与 `Message` 解耦。

### 3.2 持久化：`LocalToolResultStore`（可测性改造）

`Shared/Services/LocalToolResultStore.swift`

- `init(baseURL: URL? = nil)`：默认 `Application Support/xhanHelper/local_results`；测试注入临时目录。
- `upsert(type:payload:id:createdAt:conversationId:)`：改收原语，去掉对 `Message` 的依赖 → 整个 actor 变成 Foundation-only，可进 SwiftPM。
- 所有 I/O 错误**向上抛出**，由 app 层调用方记录一次（不在此静默吞）。
- 每会话一个 `<convId>.json`，`Entry { id, createdAt, type, payload }`，按 `id` 去重覆盖。

### 3.3 插入器：`ChatViewModel.insertLocalArtifact(_:)`

```swift
func insertLocalArtifact(_ artifact: LocalArtifact) {
    // 1) artifact.payload() 编码（失败 → 记录并跳过）
    // 2) 建 role=local_tool_result 消息：本地稳定 id "local_<uuid>" + createdAt=now
    // 3) 设 localToolResult（持久化壳）与 localArtifact（解码值）
    // 4) append 到 messages；异步 upsert 落盘
}
```

chart 与文档卡片共用它，不再各写一份。

### 3.4 服务端 tool_result → 产物：producer（只 create/update）

`Shared/LocalArtifacts/DocumentArtifactProducer.swift`——纯函数 `patch(function:args:data:) -> DocumentRefPatch?`。

**为什么只认 `create_document` / `update_document`，不认 `get_document`**：卡片是「agent 操作过的文档」入口。若把读也算进来，同轮「先 get（读到改前旧值）→ 再 update（改后新值）」会让卡片刻在**过期快照**上（第一版 bug）。去掉 get 后，每次写都带出它当时的权威新状态（title/tags 在入参里）。只读场景的导航靠模型正文内链 + 右栏文档面板。`query`/`list` 是列表，逐条出卡片太吵，也不做。

返回的是**增量 patch**，字段用 Optional 区分「未提供 vs 显式清空」：

```swift
struct DocumentRefPatch {
    let id: String
    var title: String?     // nil = 未改标题
    var tags: [String]?    // nil = 未带 tags 参数（保留旧值）；[] = 传空串（显式清空）
    var preview: String?   // nil = 本次无摘要（保留旧值）
}
```

规则：
- `create_document`：`title = data.title ?? args.title`；`tags = parseTags(args.tags)`（新建无参数即空）；`preview = makePreview(args.content)`（全文）。
- `update_document`：`title = args.title`（可选）；`tags = args 出现 ? parseTags : nil`；`preview = makePreview(args.content)` —— **只用全文重写的 `content`**，`old_text/new_text` 是片段，不作摘要（否则会冲掉旧摘要）。
- `parseTags`：半角/全角逗号切分、去空格、去重（对齐服务端 `service.NormalizeTags`）。`makePreview`：折叠空白 + 截 60 字 + 空 → nil。

未来同类需求（tool_result JSON → 特殊展示）在这里加一个 producer 函数即可。

### 3.5 按轮合并：`DocumentRef.make/merged`（纯）

同一轮内同一篇可能被多次写（create→update、update→update）。`ChatViewModel` 用 `roundDocRefs: [docID: DocumentRef]` 累积本轮状态：

```swift
static func make(from patch:) -> DocumentRef   // 首触达：title ?? "文档"、tags ?? []
func merged(with patch:) -> DocumentRef         // 再触达：patch 非 nil 覆盖，nil 保留旧值
```

`roundDocRefMessageID: [docID: localMessageID]` 记本轮已落成的本地消息：再触达同一篇时**就地更新**该消息的 artifact/payload 并重新 upsert（位置不变），不新增卡片。两表在 `sendMessage` / `loadConversation` 清空。

效果：`create[a,b,c] → update[a]` 得 `[a]`；`update(无 tags)` 保留旧标签；`update(tags="")` 正确清空；`update(new_text 局部替换)` 不冲掉旧摘要。

### 3.6 重定位：`LocalArtifactMerge.reposition`（纯）

`Shared/LocalArtifacts/LocalArtifactMerge.swift`

```swift
static func reposition<T>(_ items: [T], isLocal: (T) -> Bool, isAssistant: (T) -> Bool) -> [T]
```

按 `createdAt` 排序后，把每条 `local_tool_result` 移到「其后最近一条 assistant」之后；其后没有 assistant 则保持原位。用泛型 + 谓词保持纯、可单测（不必依赖 `Message`）。

### 3.7 渲染分发

`MessageListView.localArtifactView(_:)`——switch 穷尽：

```swift
case .chart(let cd):   ChartBubble(chartData: cd)
case .docRefs(let refs): DocumentRefCard（仅 macOS，iOS 无右栏文档面板）
```

`DocumentRefCard`：图标 + 标题 + tag 胶囊 + preview 单行 + 「打开」，配色全用现有 token（`appAccent` / `surfaceInput` / `assistantText`）。

## 四、一次会话的时序

```
SSE tool_call      → parseToolCall（留存 tool/function/args）→ pendingToolCalls
SSE tool_result    → DocumentArtifactProducer.patch(args, data) → DocumentRefPatch
                   → upsertRoundDocumentRef: roundDocRefs 合并 → 就地更新已有本地消息
                     （或首触达 insertLocalArtifact）+ 异步 upsert 落盘   ← 卡片即刻出现
SSE done           → assistant id __streaming__ → 真实 id（本地产物不受影响）
...
loadConversation   → messages 全量替换
                   → mergeLocalToolResults: 按 [first.createdAt, last.createdAt] 从盘读回
                     → LocalArtifact(type:payload:) 解码 → append
                     → 按 createdAt 排序 → LocalArtifactMerge.reposition 归位
                   ⇒ 卡片回到对应回复下方
```

## 五、可测性边界

- **纯逻辑**（进 SwiftPM target `LocalArtifactsCore`，`swift test` 全绿）：artifact 编解码、producer（tags/preview/title 规则）、store（含注入临时目录的持久化）、重定位。
- **SwiftUI / VM glue**（`Message` 依赖 `PlatformImage`，进不了纯 target）：`insertLocalArtifact` 的接线、`localArtifactView` 渲染、`MessageListView` 布局。由 `swiftc -typecheck`（macOS/iOS）+ Xcode 真机覆盖。

## 六、测试覆盖

命令：`cd client/xHelper && swift test`（LocalArtifactsCore 共 41 例）。

| 文件 | 覆盖 |
|---|---|
| `LocalArtifactTests` | chart/docRefs 编解码往返；空 docRefs；Unicode/emoji/换行；坏 payload → nil；类型不符 → nil |
| `DocumentArtifactTests` | 只认 create/update（get/query/list/delete → nil）；create 无 tags = `[]`；update 无 tags = `nil`、空串 = `[]`；`new_text` 不作 preview；title 来源；缺 id/空 id → nil；parseTags 半/全角 + 去重 + 去空；preview 折叠 + 截断 + 空 → nil |
| `DocumentRefMergeTests` | `make` 兜底 title「文档」/tags `[]`；`merged` nil 保留、非 nil 覆盖；`create[a,b,c]→update[a]`= `[a]`；update 无 tags 保留；update `tags=""` 清空；片段更新不冲掉旧摘要 |
| `LocalToolResultStoreTests` | upsert→entries 往返；多会话文件隔离；同 id 覆盖不重复；范围过滤含边界；换新实例仍从盘读回；clearAll 清盘与内存 |
| `LocalArtifactMergeTests` | local 移到其后最近 assistant；多条 local 同落一条 assistant 后；末尾无 assistant 保持；已正确顺序不变；交错场景；空/单元素 noop |

## 七、涉及文件

新增：
- `client/xHelper/xHelper/Shared/LocalArtifacts/{LocalArtifact,DocumentRef,DocumentArtifactProducer,LocalArtifactMerge}.swift`（`DocumentRef.swift` 含 `DocumentRefPatch` 与 `make/merged`）
- `client/xHelper/Tests/LocalArtifactsCoreTests/{LocalArtifactTests,DocumentArtifactTests,DocumentRefMergeTests,LocalToolResultStoreTests,LocalArtifactMergeTests}.swift`

改造：
- `Models/LocalToolResultData.swift`（+`docRefs`）、`Models/Message.swift`（`chartData`→`localArtifact`）、`Models/ChartData.swift`（+`Equatable`）
- `Services/LocalToolResultStore.swift`（注入 baseURL + 原语 upsert + throwing）
- `ViewModels/ChatViewModel.swift`（`insertLocalArtifact` / `upsertRoundDocumentRef` / `roundDocRefs`+`roundDocRefMessageID` / merge 重写；删 `documentRefs`）
- `ViewModels/ChatViewModel+ClientTools.swift`（chart 走 `insertLocalArtifact(.chart)`）
- `Views/MessageListView.swift`（`localArtifactView` + `DocumentRefCard` 加 tags/preview；删 `documentRefs` 参数）
- `Views/ChatView.swift`（删 `documentRefs:` 实参）
- `xHelperMac/App.swift`（clearAll 改 try/catch 记录）
- `client/xHelper/Package.swift`（新增 `LocalArtifactsCore` + `LocalArtifactsCoreTests`）

服务端：**零改动**（数据本就在现有 tool_call 入参 / tool_result 返回值里，不碰前缀缓存）。

## 八、验收

- `cd client/xHelper && swift test` 全绿（含 LocalArtifactsCore 41 例）。
- `swiftc -typecheck` macOS + iOS 0 error。
- 真机：发消息触发建/改文档 → 卡片带 tags + preview；切屏/重载回来卡片仍在，且挨在对应回复下方。

## 九、风险 / 已知取舍

- **时间串格式**：本地 `ISO8601DateFormatter`（无小数秒）与服务端 RFC3339（`time.Time` JSON）可能不完全一致，范围过滤靠 `[first, last]` 够宽 + 重定位循环兜底；chart 一直如此运行。
- **设备本地**：缓存不跨设备，新设备/重装后历史轮次的本地产物不出现。要跨设备需回头解析历史 `tool_calls_json`，是另一个量级，先不做。
- **本地结果不可分享**：`isUnshareable` 覆盖所有 `localToolResult`，chart 同款，符合预期。

## 十、后续迭代（未做）

- 出现第 4+ 种本地产物、且希望「不改 chat loop 就能加类型」时，再引入 producer 注册表（`[ToolResultArtifactProducer.Type]`）。当前 1 种服务端 producer（document）直接用函数调用，不上注册表。
- 若本地产物需要跨设备，改走历史 `tool_calls_json` 解析（注意 native FC 与 prompt-eng 两种 JSON 形态）。
