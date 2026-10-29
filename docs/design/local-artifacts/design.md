# 本地产物管线（LocalArtifact）

> 日期：2026-09-19
> 状态：✅ 已实现（客户端纯逻辑 `swift test` 绿；macOS/iOS `swiftc -typecheck` 0 error；服务端 `go test` 绿；集成由 Xcode 真机验证）
> 追加（同日）：卡片观感修复——服务端 `update_document` 回填权威 `title`/`tags`/`preview`（消除占位「文档」）；卡片按 `card-mockup.html` 定稿重排（**只留标题 + 摘要(≤3 行)**、整卡可点、无外框、行距 6；图标/标签/按钮已裁掉）；本地产物（chart + 文档卡片）**移到回复的思考 + 正文上方**
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

所以**绝对不能把本地产物按 assistant 消息 id 绑定**——绑定时刻的 id 与之后的 id 不是同一个。chart 当年的解法是：本地产物**自成一个稳定 id 的本地消息** + `createdAt`，持久化后按时间重放、按「最近 assistant（位置距离）」重定位。本管线沿用这个解法，并把它抽成纯函数以便单测。

## 二、目标 / 非目标

**目标**
- 统一的「工具结果 → 特殊展示 + 本地缓存」管线；新增类型只写「enum case + 载荷 Codable + 渲染视图 + producer 分支」。
- 本地产物按会话持久化，切屏/重载后仍在，且**落在产生它的那条回复的思考 + 正文上方**（不浮在最新消息下）。
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
    // 4) 插到当前 __streaming__ assistant 之前（找不到才 append）；异步 upsert 落盘
}
```

chart 与文档卡片共用它，不再各写一份。

### 3.4 服务端 tool_result → 产物：producer（只 create/update）

`Shared/LocalArtifacts/DocumentArtifactProducer.swift`——纯函数 `patch(function:args:data:) -> DocumentRefPatch?`。

**为什么只认 `create_document` / `update_document`，不认 `get_document`**：卡片是「agent 操作过的文档」入口。若把读也算进来，同轮「先 get（读到改前旧值）→ 再 update（改后新值）」会让卡片刻在**过期快照**上（第一版 bug）。去掉 get 后，每次写都带出它当时的权威新状态（create 用入参；update 用**服务端写后回填**的 title/tags/preview，见 3.4.1）。只读场景的导航靠模型正文内链 + 右栏文档面板。`query`/`list` 是列表，逐条出卡片太吵，也不做。

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
- `update_document`：**data 优先、args 兜底**——`title = data.title ?? args.title`；`tags = data.tags.map(parseTags) ?? (args 出现 ? parseTags : nil)`；`preview = makePreview(data.preview) ?? makePreview(args.content)`。服务端回填的是写后权威值（见 3.4.1），故 data 优先；旧服务端无这些字段时回落到 args 的旧行为。
- `parseTags`：半角/全角逗号切分、去空格、去重（对齐服务端 `service.NormalizeTags`）。`makePreview`：折叠空白 + 安全上限 200 字（真触发才补 `...`，正常不触发）+ 空 → nil；**展示截断交给卡片 `lineLimit(3)`**（按实际宽度截并补 `…`），数据层不按固定字数砍（服务端 100 字预览原样透传）。

未来同类需求（tool_result JSON → 特殊展示）在这里加一个 producer 函数即可。

#### 3.4.1 服务端回填权威 title / tags / preview（2026-09-19 补）

**问题**：update-only 轮（模型只改正文/只调标签、没传 `title`）时，producer 从入参取不到 title → `DocumentRef.make` 兜底成占位「文档」，卡片显示不出真标题；preview 也无从取（只有片段 `new_text`，不能当摘要）。

**做法**：`server/internal/tool/document.go::updateDocument` 在结果里回填写后权威值（该函数本就 `GetByID` 拿到 `current`，零额外查询）：
- `title = updates["title"] ?? current.Title`
- `tags = updates["tags"] ?? current.Tags`（`Document.Tags` 是逗号串）
- `preview = util.TruncateContent(updates["content"] ?? current.Content, docPreviewLen=100)`

`nothing to update` 早退路径保持原样（no-op 不出卡片）。这改的是**工具结果内容**、不是工具定义，不破坏 DeepSeek 前缀缓存（动态内容本就不进请求体模板）。`document_test.go` 的 `TestDocumentUpdateReturnsAuthoritativeFields` 钉住。

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

按创建时间排序后，把每条 `local_tool_result` 移到**与其位置距离最近的 assistant 之前**（等距取更靠前者）；列表里没有 assistant 则保持原位。这样卡片落在产生它的那条回复的**思考 + 正文上方**（chart 与 docRefs 统一）。用泛型 + 谓词保持纯、可单测（不必依赖 `Message`）。

「最近」用**位置距离**而非时间比较：本地 `createdAt` 与服务端时间串格式不同（无/有小数秒），同秒 tie 时字符串排序不稳定——距离判定对「tie 排在 assistant 前/后」两种情况都稳定，也避免「有下一轮 assistant 时卡片被搬去下一轮」。

### 3.7 渲染分发

`MessageListView.localArtifactView(_:)`——switch 穷尽：

```swift
case .chart(let cd):   ChartBubble(chartData: cd)
case .docRefs(let refs): DocumentRefCard（仅 macOS，iOS 无右栏文档面板）
```

`DocumentRefCard`（按 `card-mockup.html` 定稿）：**整卡可点**（`Button` 包住内容，`buttonStyle(.plain)`），只展示**标题 13.5 semibold + 摘要 11.5 muted（最多 3 行）**，`spacing 6`；`surfaceInput` 底、r11、无外框、maxWidth 240、内边距 12×10。图标/标签/「打开」按钮已按观感迭代裁掉（点整卡即可打开文档面板定位）。

**多张卡片类产物进横向卡片条**（2026-10-16 起，见文末「后续变更」）：相邻的卡片类产物由 `LocalArtifactGrouping.rows` 并成一行，交给 `ArtifactCardStrip` 排布——收起态按宽度自适应露完整卡（**至少露一张**）+ 尾部窄「＋N」格，点它展开成横向可滑的一排（**没有「收起」控件**，展开后一段时间自动收起）；**单张仍走上面这条原路**。`LocalArtifact.isCardArtifact` 区分卡片类（`.docRefs`）与非卡片类（`.chart` 全宽图表，不进条子、且天然打断分组）。

> 标签展示曾用 `ViewThatFits(in: .horizontal)` 宽度自适应收起（`+N`），现标签整体不展示，相关代码已移除；若日后要恢复，参照 `card-mockup.html` 历史或 git 记录。

## 四、一次会话的时序

```
SSE tool_call      → parseToolCall（留存 tool/function/args）→ pendingToolCalls
SSE tool_result    → DocumentArtifactProducer.patch(args, data) → DocumentRefPatch
                   → upsertRoundDocumentRef: roundDocRefs 合并 → 就地更新已有本地消息
                     （或首触达 insertLocalArtifact：插到 __streaming__ assistant 之前）
                     + 异步 upsert 落盘   ← 卡片即刻出现在该回复的思考 + 正文上方
SSE done           → assistant id __streaming__ → 真实 id（本地产物不受影响）
...
loadConversation   → messages 全量替换
                   → mergeLocalToolResults: 读全会话产物（有锚点的按 user 消息 id 插回，
                     无锚点的旧数据按下界 + 时间排序 + 位置归位）
                     → LocalArtifact(type:payload:) 解码 → append
                     → 按 createdAt 排序 → LocalArtifactMerge.reposition 归位
                   ⇒ 卡片回到对应回复的思考 + 正文上方
```

## 五、可测性边界

- **纯逻辑**（进 SwiftPM target `LocalArtifactsCore`，`swift test` 全绿）：artifact 编解码、producer（tags/preview/title 规则）、store（含注入临时目录的持久化）、重定位。
- **SwiftUI / VM glue**（`Message` 依赖 `PlatformImage`，进不了纯 target）：`insertLocalArtifact` 的接线、`localArtifactView` 渲染、`MessageListView` 布局。由 `swiftc -typecheck`（macOS/iOS）+ Xcode 真机覆盖。

## 六、测试覆盖

命令：`cd client/xHelper && swift test`（LocalArtifactsCore 共 48 例）。

| 文件 | 覆盖 |
|---|---|
| `LocalArtifactTests` | chart/docRefs 编解码往返；空 docRefs；Unicode/emoji/换行；坏 payload → nil；类型不符 → nil |
| `DocumentArtifactTests` | 只认 create/update（get/query/list/delete → nil）；create 无 tags = `[]`；update 无 tags = `nil`、空串 = `[]`；update **data 优先于 args**（title/tags/preview）、data 有 title 而 args 无、data 回填空 tags = `[]`；`new_text` 不作 preview；title 来源；缺 id/空 id → nil；parseTags 半/全角 + 去重 + 去空；preview 折叠 + 安全上限截断补 `...` + 空 → nil |
| `DocumentRefMergeTests` | `make` 兜底 title「文档」/tags `[]`；`merged` nil 保留、非 nil 覆盖；`create[a,b,c]→update[a]`= `[a]`；update 无 tags 保留；update `tags=""` 清空；片段更新不冲掉旧摘要 |
| `LocalToolResultStoreTests` | upsert→allEntries 往返；锚点往返 + `reattachAnchor` 只改匹配项；旧 JSON 无锚点键仍能解码；读取不被服务端时钟上界过滤；多会话文件隔离；同 id 覆盖不重复；换新实例仍从盘读回；clearAll 清盘与内存 |
| `LocalArtifactMergeTests` | local 移到最近 assistant **之前**；tie 排在 assistant 前/后都稳定；多条 local 注入同一 assistant 前且保序；距下一轮更近则归下一轮；不被下一轮抢走；等距取靠前者；无 assistant 保持；空/单元素 noop |

## 七、涉及文件

新增：
- `client/xHelper/xHelper/Shared/LocalArtifacts/{LocalArtifact,DocumentRef,DocumentArtifactProducer,LocalArtifactMerge}.swift`（`DocumentRef.swift` 含 `DocumentRefPatch` 与 `make/merged`）
- `client/xHelper/Tests/LocalArtifactsCoreTests/{LocalArtifactTests,DocumentArtifactTests,DocumentRefMergeTests,LocalToolResultStoreTests,LocalArtifactMergeTests}.swift`
- `docs/design/local-artifacts/card-mockup.html`（卡片细节交互稿）

改造：
- `Models/LocalToolResultData.swift`（+`docRefs`）、`Models/Message.swift`（`chartData`→`localArtifact`）、`Models/ChartData.swift`（+`Equatable`）
- `Services/LocalToolResultStore.swift`（注入 baseURL + 原语 upsert + throwing）
- `ViewModels/ChatViewModel.swift`（`insertLocalArtifact` / `upsertRoundDocumentRef` / `roundDocRefs`+`roundDocRefMessageID` / merge 重写；删 `documentRefs`）
- `ViewModels/ChatViewModel+ClientTools.swift`（chart 走 `insertLocalArtifact(.chart)`）
- `Views/MessageListView.swift`（`localArtifactView` + `DocumentRefCard`：只留标题 + 摘要（≤3 行）、整卡可点；删 `documentRefs` 参数）
- `Views/ChatView.swift`（删 `documentRefs:` 实参）
- `xHelperMac/App.swift`（clearAll 改 try/catch 记录）
- `client/xHelper/Package.swift`（新增 `LocalArtifactsCore` + `LocalArtifactsCoreTests`）

服务端（2026-09-19 补）：
- `internal/tool/document.go`（`update_document` 结果回填权威 `title`/`tags`/`preview`）
- `internal/tool/document_test.go`（`TestDocumentUpdateReturnsAuthoritativeFields`）

## 八、验收

- `cd client/xHelper && swift test` 全绿（含 LocalArtifactsCore 44 例）。
- `swiftc -typecheck` macOS + iOS 0 error。
- 服务端 `go build ./... && go test ./...` 全绿（含 `TestDocumentUpdateReturnsAuthoritativeFields`）。
- 真机：发消息触发建/改文档 → 卡片显示真标题 + 摘要（≤3 行）、整卡可点、无外框；切屏/重载回来卡片仍在，且落在对应回复的思考 + 正文**上方**。

## 九、风险 / 已知取舍

- **时间串格式**：本地 `ISO8601DateFormatter`（UTC `Z`，无小数秒）与服务端 RFC3339 可能**时区偏移都不同**——本地 SQLite 后端下发东八区 `…+08:00`，云端 MySQL 下发 `Z`。**绝不能按字符串比较**（会差 8 小时）。统一走 `LocalArtifactTime.parse/isOrdered/isOnOrAfter` 解析成 `Date` 再比（解析失败才回退字符串）。重定位本身按**位置**判定（不比较时间串）。
  - **2026-10-29 更正**：光解析成 `Date` 还不够——**卡片是本机时钟、消息是服务端时钟**（实测差 ~44s），跨时钟域比较上界会把卡片误剔。现行做法：读取不过滤上界，归位按 `Entry.anchorUserID`（位置锚点）而非时间；`isWithin` 已换成只判下界的 `isOnOrAfter`。见 `docs/bugs/040`。
- **设备本地**：缓存不跨设备，新设备/重装后历史轮次的本地产物不出现。要跨设备需回头解析历史 `tool_calls_json`，是另一个量级，先不做。
- **本地结果不可分享**：`isUnshareable` 覆盖所有 `localToolResult`，chart 同款，符合预期。
- **update 结果多带一个 preview**：服务端每次 `update_document` 多返回 ~100 字正文摘要进模型上下文（为让 update-only 卡片有摘要）。量小、接受；若日后在意 token，把 `preview` 从结果里去掉、卡片改无摘要态即可。

## 十、后续迭代（未做）

- 出现第 4+ 种本地产物、且希望「不改 chat loop 就能加类型」时，再引入 producer 注册表（`[ToolResultArtifactProducer.Type]`）。当前 1 种服务端 producer（document）直接用函数调用，不上注册表。
- 若本地产物需要跨设备，改走历史 `tool_calls_json` 解析（注意 native FC 与 prompt-eng 两种 JSON 形态）。

## 后续变更

- 2026-10-04 · 修「刷新即消失 / 卡片飞到上一条消息」（见 `docs/bugs/028-document-card-not-persisted.md`）：
  - 时间比较一律解析成 `Date`（新增 `LocalArtifacts/LocalArtifactTime.swift`）：`LocalToolResultStore.entries` 范围过滤 + `ChatViewModel.mergeLocalToolResults` 排序都改用它。§九「时间串格式」更正为**时区偏移不一致**（本地 SQLite 后端 `+08:00` vs 云端 `Z`），字符串比较会差 8 小时。
  - 新会话首轮：`insertLocalArtifact` 在会话 id 为空时不落盘（只留内存）；`done` 拿到真实 id 后 `persistLocalArtifactsUnderConversation` 补写并清理空键文件 `local_results/.json`。此前会写进空文件名、重载读不到。
  - `LocalArtifactMerge.reposition` 增加 `isUser` 谓词：产物改锚定「前一条 user 消息」之后，无 user（隐藏轮）才回退「最近 assistant」——防该轮 assistant 无正文被过滤时卡片被吸到上一轮。
  - 影响文件：`LocalArtifactTime.swift`(新) / `LocalArtifactMerge.swift` / `LocalToolResultStore.swift` / `ChatViewModel.swift` + 对应测试。
- 2026-10-16 · 聊天里的卡片类产物改成**横向卡片条**（多张不再竖向叠）：
  - **决定（现行）**：相邻的卡片类产物并成一行；收起态＝按可用宽度自适应，塞得下几张完整卡露几张、剩下的收成尾部一个**窄「＋N」格**（只写数字、不带单位——窄格里容不下「张」；淡底 + 虚描边）；点它展开成**横向可滑**的一排完整卡，**没有「收起」控件**（展开内容必定比可视宽更宽，尾部放什么都得滑到底才够得着），改为**展开后 8s 自动收起**。窗口变宽不再溢出时自动回收起态。**单张、或只有一条分组时完全走原路**（不经条子），与改造前一致。「收起」控件的三次变化见下条。
  - **「收起」控件的三次变化，最后删掉**：① 钉在行首（横向 `ScrollView` 之外，滑多远都点得到）→ 观感被否；② 挪到行尾（箭头改 `chevron.left`——横向展开就往左收回）→ 发现**永远够不着**（见下条推导）；③ **删掉**，改为展开后自动收起。
  - **推导：行尾的「收起」格必定在可视区外**。能展开 ⟺ 卡片放不下，即 `count*(cell+spacing) > available + spacing`；展开内容宽 = `count*(cell+spacing) + tileWidth` > `available + spacing + tileWidth`。所以它至少比右边缘多出 `spacing + tileWidth`（82pt），**只能滑到底才看得到**——这也是「自动收起」的由来。
  - **展开后自动收起**：`ArtifactCardMetrics.autoCollapseDelay`（8s），`showExpanded` 变真时起算、变假/再展开都重新起算（`onChange` 里先取消旧任务）。收起态不计时。常量放 `ArtifactCardMetrics` 是因为**泛型类型里不能有 static 存储属性**（第一版这里编译不过）。
  - **必须至少露一张卡**（真机反馈踩到）：卡片 240pt 比缩略图大得多，`ThumbRowLayout.collapsed` 原来允许「塞不下就 0 张」→ 窄窗口（可用宽 < 240+10+72 = 322）会退化成**只有一个「＋N」格、一张卡都没有**。改法：放不下时先保一张卡，**尾格吃剩下的宽度、可以被压窄**（连尾格都放不下就是 0 宽、不渲染它）。恒不溢出的下界因此是 `available ≥ cell + spacing`（比一张卡还窄就无从谈排布）。
  - **分组的唯一事实来源**：`LocalArtifactGrouping.rows`（纯函数，泛型 + 谓词）。按「相邻」而不是按轮次——`LocalArtifactMerge.reposition` 已把同一轮产物排到一起且与其它消息隔开，非卡片类产物（chart）会打断分组，所以相邻 ≡ 同一轮。
  - **分享选择模式不分组**（逐条渲染），否则每张卡要各配一个选择指示器，风险不值当。
  - **走过弯路（记下来免得回头重做）**：试过「＋N 格里叠 3 层空白卡背」和「＋N 格里叠**真卡**（阶梯，每张往右错 peek 露出左侧一截标题）」两版。结论：错位只有 8pt 时**露出来的那道边放真卡还是空白卡都一样看不见内容**（「没意义」的根因）；要让后卡读得出标题，peek 得大到 60–90pt，等于把横向宽度吃掉，反而更差。用户拍板回到**窄格 ＋N**。
  - **两个渲染坑（已在代码里注释钉住）**：① 尾格宽度由 `ThumbRowLayout.collapsed` 的返回值给出（`trailing`），视图与测试都不自己再算一遍；② 卡片宽度常量集中在 `ArtifactCardMetrics`——条子默认值、`MessageListView.artifactCard`、`DocumentRefCard` 的单卡 `maxWidth` 三处共用，不再各写一个 240。
  - **展开态按组记**：`MessageListView.expandedCardStrips: Set<messageID>`（键＝该组第一条消息 id），切会话时清空。
  - 影响文件：`Views/ArtifactCardStrip.swift`(新) · `Views/DocumentRefCard.swift`(新，从 `MessageListView` 抽出并加 `width`/`height`) · `Views/MessageListView.swift`（`DisplayRow` 分组 + `messageRowWithGestures`/`cardStripRow`/`artifactCard` + `expandedCardStrips`） · `Views/ThumbRowLayout.swift`（+ 尾格可不同宽的 `collapsed` 重载） · `LocalArtifacts/LocalArtifact.swift`（+`isCardArtifact`） · `LocalArtifacts/LocalArtifactGrouping.swift`(新) · `scripts/snapshot-cardstrip.swift` + `snapshot-cardstrip.sh`(新，离屏渲染自检靶)。
  - **iOS 不分组**：卡片类产物目前只有文档卡片，而它只在 macOS 出（iOS 无右栏文档面板，`DocumentSelectionCoordinator.openDocument` 见 `rightSidebar == nil` 直接 no-op）。iOS 上 `displayRows` 走「逐条」那条路，维持改造前行为——否则会出现「1 张不显示、≥2 张却显示一摞点不动的卡片」（两处平台判断不一致，review 抓到）。
  - **一个产物 = 一张卡**（前提，写进了 `LocalArtifact.docRefs` 的文档注释与 `artifactCard`）：`upsertRoundDocumentRef` 每篇文档各落一条 `[merged]`，`docRefs` 恒为单元素，所以条子的「＋N」按 `items.count` 算就是卡片数。将来真要一条装多张，得先把条子改成按卡片数展平。
  - **卡片定高 92 的依据**：正文（标题 1 行 + 摘要 ≤3 行 + 间距 + 内边距）定宽 240 实测 **84pt**，字号是 `.system(size:)` 固定值不随 Dynamic Type 变，所以留 8pt 余量够用（review 提到「只剩 ~3pt」是按行高估算的，实测不成立）。改正文结构要同步 `ArtifactCardMetrics.height`。
  - **尾格宽度的算法只有一处**：`ThumbRowLayout.collapsed` 把实际尾格宽度作为返回值给出（`trailing`），视图与测试都用它——不让两边各复刻一遍那个钳制（review 指出那样「改了实现忘了改测试，两边一起错也能绿」）。
  - **自检靶必须能失败**：`snapshot-cardstrip` 的 mode 写错直接 exit 2，任一镜头渲不出来就 exit 1（渲不出图也返回 0 的话，自检就是假绿）。
  - **展开态的键仍是「组首消息 id」**：组首一变（重排、往组前插卡）展开态就丢——认了，它本就只活 8s，不值得再加一层稳定 id。
  - 测试：`LocalArtifactsCoreTests/LocalArtifactGroupingTests.swift`(新) · `ThumbRowLayoutTests/CardStripLayoutTests.swift`(新) · `LocalArtifactTests`(+`isCardArtifact`)。
  - **渲染自检靶**（`scripts/snapshot-cardstrip.sh`）：把真视图渲成 PNG。两个坑：`ImageRenderer` **不渲染 `ScrollView`**（展开态会渲成空白）→ 改用 `NSHostingView` + `cacheDisplay`；裸可执行文件的 `Bundle.main` 找不到 `Assets.car`，token 色会解析成**透明** → 要包一个最小 `.app` 壳。
- 2026-10-29 · 卡片偶发消失：**时钟域混用**（见 `docs/bugs/040-document-card-dropped-by-clock-skew.md`）：
  - **根因**：卡片时间戳来自本机时钟，消息时间来自服务端时钟（实测本机快 ~44s），加载时却用服务端时间做**闭区间**过滤 → 卡片比页尾消息晚就被当「越界」丢掉；下一轮再发消息页尾变大，卡片又回来（表现为「有时候消失」）。停止/出错轮（无 assistant 落库）同样中招。
  - **决定（用户拍板 A+B）**：① `entries(for:from:to:)` → `allEntries(for:)`，读取**不再有上界**，只剩「不早于页首」的下界（`LocalArtifactTime.isOnOrAfter`）；② `Entry` 增 `anchorUserID`（该卡所属轮的 user 消息 id），加载时按锚点**位置**插回、不参与时间排序；done 里 user 消息 id 从本地 UUID 换成服务端 id 时 `reattachAnchor` 跟着改写。
  - **行为**：新卡片永不因时钟被剔、也不会被时钟偏移带到下一轮；旧数据（无锚点）与停止轮（锚点是本地 id，永远匹配不上）回退「下界过滤 + 时间排序 + `reposition`」，仍然可见。
  - **已知取舍**：客户端时钟超前时，上一页最后一轮的卡片理论上可能漏进当前页（需与页首 user 消息相差 <时钟差，7 轮边界），最坏落位偏一格、不会消失。
  - **落盘时序修正**：白板首轮补落盘（`persistLocalArtifactsUnderConversation`）从 done 的「拿到会话 id」处挪到「user 消息 id 也替换完」之后，落下的锚点直接是服务端 id（否则会与 `reattachAnchor` 抢写）。
  - 影响文件：`Services/LocalToolResultStore.swift` · `LocalArtifacts/LocalArtifactTime.swift` · `LocalArtifacts/LocalArtifactMerge.swift` · `ViewModels/ChatViewModel.swift` + 对应测试。
