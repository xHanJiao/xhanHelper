# 代码审查报告 · 灵阅正文按 ETag 落本地缓存

**审查日期**：2026-10-30
**审查分支**：`feat/artifact-html-cache`（未提交）
**审查范围**：10 个修改文件（客户端 6 + 服务端 3 + 文档 1）+ 1 个新增（`client/.../Services/ArtifactHTMLCache.swift`），共 +273 / −48
**需求文档**：`docs/design/html-artifact/design.md`（§4.1 正文出口、§9.8 加载态 + 本轮新增的 ETag 缓存段、§9.6 分享路由的 `no-store` 取舍、§十一 无痕生命周期）
**整体评估**：⚠️ **REQUEST_CHANGES（1 个 P1 回归必须修，P2 建议同轮收）** —— 缓存主链路（服务端 ETag / 304 / 客户端秒开 / 注入基线只做一次）设计与实现一致，服务端契约也补了测试；问题集中在「已开着的 tab 被漏掉激活」这一处行为回归，以及「无痕内容落盘」这个需要拍板的取舍。

> **修复状态（2026-10-30）**：P1 + P2 + P3 已全部处理（§3.1 按「接受并写进文档」拍板）。以下保留原始审查意见。

**本次自检**：

- `go build ./...` ✔
- `go test ./internal/controller -run Artifact -count=1` ✔（新增 `TestServeArtifactHTML_ETag` 全绿）
- `swiftc -typecheck`（macOS `arm64-apple-macos26.3` + iOS `arm64-apple-ios26.0-simulator`，带 `-default-isolation MainActor`，项目真实设置）两目标 **0 error**，仅存量警告 ✔
- 主线程 8MB 读写实测：写 5ms / 读 2ms（M 系列 + APFS）→ 「大页面文件 IO 卡 UI」这条不成立，**不要再拿它当理由改异步**
- 未跑：真机观感（8MB 带图页面的秒开/白闪）、断网回退链路 —— 两条都要构建后手点

---

## 一、改动概览

| 层 | 改动 | 评价 |
|---|---|---|
| 服务端 | 新增 `GET /api/artifacts/:id/html`：裸 HTML + `ETag`（正文 sha256 前 16 字节）+ `If-None-Match` → 304；`Cache-Control: private, no-store`、`nosniff` | ✅ 不改表、用正文哈希当版本，正面解决「原地改版 id 不变」 |
| 服务端测试 | 首次 200 / 命中 304 空响应 / 改版后 ETag 必变 / 越权 404 | ✅ 契约钉得准 |
| 客户端缓存 | `ArtifactHTMLCache`：`Caches/com.xhan.xhanHelper/artifacts[-dev]/<id>.{html,etag}`，512MB 上限、按 mtime 淘汰 | ✅ 目录与 `LocalPaths.appLogDir` 同一套约定 |
| 客户端打开流程 | 有缓存先秒开 → 条件请求确认：304 不重建 WebView、200 才替换、失败继续看缓存、404 清缓存 | ✅ 方向对，见 §二 / §三 的 3 处收口 |
| 视图 | `HTMLArtifactView` 改为接收**已注入基线**的字符串，注入统一在 `ArtifactOpenCoordinator.show` 做一次 | ✅ 修掉了 `updateNSView` 每 tick 复制 8MB 的真实浪费（原注释也写了「否则首次 update 必判变了再重载一次」） |

---

## 二、P1（必须修）

### 2.1 已开着的产物 tab 不再被切到前台，点卡片「毫无反应」

**位置**：`client/xHelper/xHelper/Shared/Services/ArtifactOpenCoordinator.swift:52-53`

```swift
// 同一个 id 已开着：只切过去，不重建（重建会把已渲染的页面扔掉再加载一遍）
guard !rs.tabs.contains(where: { $0.id == tabID }) else { return }
```

注释写的是「只切过去」，代码却是直接 `return`。改动前是**先 `rs.openTab(...)` 再 `guard !alreadyOpen`**，而 `RightSidebarManager.openTab` 在 tab 已存在时**不换 content，但会 `activeTabId = id` + `isOpen = true`**（`RightSidebarManager.swift:195-211`）。order 一颠倒，这段激活被整块跳过了。

**用户可见症状**：打开产物 A → 切到别的 tab（或收起右栏）→ 再点聊天里 A 的卡片 → 没有切换、没有展开右栏、也没有重载，看起来像点了没反应。这正是本轮改动之前专门注释要避免的那类反馈。

**修法**（恢复旧语义，仍然只跳过重复下载）：

```swift
if rs.tabs.contains(where: { $0.id == tabID }) {
    rs.isOpen = true
    rs.activateTab(id: tabID, inRightPane: rs.splitTabIds.contains(tabID))
    return
}
```

（`activateTab(id:inRightPane:)` 已经会把另一窗格的当前显示记下来，是右栏 tab 条点击走的同一条路径；`splitTabIds` 是 `@Published`，可见。）

---

## 三、P2（建议同轮收）

### 3.1 无痕 / 已删内容在本机 Caches 留副本，与文档承诺冲突（需拍板）

**位置**：`ArtifactHTMLCache.swift` 全文；冲突表述在 `docs/design/html-artifact/design.md:378`「无痕里生成的作品不会漏进灵阅」。

服务端是无痕会话 `BeginPurge` + Stage B 物理删。现在正文落 `Caches/com.xhan.xhanHelper/artifacts/`，而清缓存只有两条路：

- 用户自己删产物（`ArtifactViewModel.removeFromList` → `ArtifactHTMLCache.remove`）；
- 下次重开时撞 404（`load` 的 404 分支）。

无痕会话被销毁时（本机退出 / 另一台设备销毁 / 「会话已消失」自动关 tab）客户端完全不知道 → 那一页的完整字节会一直待在磁盘上，直到 512MB 淘汰。

利用面不宽（Caches 是应用私有目录，和 `LogBuffer` 落盘同级），但它和 §9.6 里「公开分享路由必须 `no-store`，允许缓存就等于撤销不彻底」是同一个取舍 —— 那边选择了不留副本，这边留下了。**需要先定调**：

- **接受** → 在 §9.8 / §十一 明确写「正文会落本机 Caches，无痕只保证服务端不残留」，别让文档继续停在「不漏」；
- **不接受** → 缓存侧车文件带上 `conversation_id`（`open` 目前没有该参数，要一路传进来），在 `ChatTabManager.handleConversationGone` 里按 conversation 清。

### 3.2 `If-None-Match` 用子串匹配，误判 304 会让用户永久停在旧页

**位置**：`server/internal/controller/artifact.go:78`

```go
if inm := c.GetHeader("If-None-Match"); inm != "" && (inm == "*" || strings.Contains(inm, etag)) {
```

现在能工作（etag 带引号，误命中概率低），但这是协议层判断，而 304 的后果不轻：客户端收到 304 就保留本地缓存、不重建 WebView，等于那页永远停在旧版本，用户得手动清缓存才能看到新版。建议按 `,` 拆分 + `TrimSpace` + 去 `W/` 前缀后逐项比较，顺便把弱比较的意图写出来。

### 3.3 iOS 上「已删除」只 toast、不关 sheet，删掉的页面还留在屏幕上

**位置**：`ArtifactOpenCoordinator.swift:108-116`

404 分支只在 macOS 关 tab，iOS 的 `presented` 不动。带缓存后这个矛盾被放大：sheet 先秒出缓存正文 → 条件请求 404 → 缓存被删了 → 屏幕上的旧内容还在，用户能继续读一个「已删除」的页。加一行 `#else presented = nil #endif`，与 macOS 语义（骨架/内容一起收掉）对齐。

### 3.4 304 不刷新 mtime，`pruneIfNeeded` 会把最常用的页先淘汰

**位置**：`ArtifactHTMLCache.swift:92`

淘汰按 mtime 从旧到新删，而命中 304 时不写任何文件 → 天天打开、从不改版的页 mtime 一直停在上次完整下载那天，最容易被清掉，然后下次又是整页重下（正是本轮想省掉的开销）。命中时 `FileManager.setAttributes([.modificationDate: Date()], ofItemAtPath:)` touch 一下即可（顺带让淘汰真正接近 LRU）。

### 3.5 缓存没有任何测试，而 `dir` 注入参数注明「只给测试注入」

**位置**：`ArtifactHTMLCache.swift:23`

`pruneIfNeeded`（约 40 行删除逻辑）、`remove`、etag 损坏回退都是有分支的新逻辑，而唯独它没被 SwiftPM 覆盖（现有 `Package.swift` 全是纯逻辑 target，加一个同款很容易）。二选一：

- 补一个 `ArtifactHTMLCacheTests`（注入临时目录）：超限删最旧、删 html 时 etag 一起没、坏 etag 当 `nil`、`remove` 幂等；
- 或者把 `dir` 参数删掉（既然不测，就不该留注入口）。

前者更值 —— 删错文件这种事靠人眼看不出来。

---

## 四、P3（挑着做）

| # | 位置 | 问题 |
|---|---|---|
| 1 | `ArtifactOpenCoordinator.swift:119-122` | 「网络失败但有缓存」分支又调了一次 `show(...)`：打开时缓存已经画上去了，这里再 `injectingBaseline` 一遍（8MB 字符串再拷一份）并替换一次 `tabs[i].content`。直接 `return` 更准（`updateNSView` 那层 `loadedHTML` 判等会挡住重载，所以只是白费，不出错） |
| 2 | `artifact.go:82`、`:259` | 两处 `c.Data(..., []byte(a.HTML))` 都会多拷一份 8MB；`c.Writer.WriteString(a.HTML)` + 手写 Content-Type 可省掉 |
| 3 | `artifact.go:60`（`GetByID`） | 选 `*` 把 `source_html` 也读出来了；这条已是热路径，仓库里已有 `GetForRead` / `GetMeta` 的「只取需要的列」先例，加个 `GetHTML`（`Select("id","title","html")`）更合既有约定 |
| 4 | `ArtifactHTMLCache.swift:26-31` | Caches 目录拼法在 `LocalPaths.swift:71`（`appLogDir`）已有一份，建议提成 `LocalPaths.artifactsCacheDir`，别让「Caches 在哪」有两处答案（顺带统一 `logs-dev` 与 `artifacts-dev` 两种后缀写法） |
| 5 | `design.md:327` | 「**不要全局缓存 HTML 正文**……别做『最近 N 篇』的 map」与新增 §9.8 直接矛盾（那句原本讲的是**内存常驻**）。同一次改动里补一句「磁盘缓存见 §9.8，说的是内存」，否则下一个人会拿它当现状 |
| 6 | `ArtifactHTMLCache.swift:87` | 文件名直接拼 id。当前 id 是服务端 uuid（`size:36`），安全；但这个值来自卡片数据，来源一旦变化就是路径穿越。一行 `guard !id.contains("/")` 成本为零 |
| 7 | `artifact.go:74-81` | 新端点没加 `Content-Security-Policy: sandbox`（公开分享路由有）。当前靠 Bearer 头 + 入库清洗，浏览器裸导航到不了，风险低；与既有路由对齐更省心 |

---

## 五、竞品对照：这里做对的三件事（别在后续迭代里改坏）

1. **版本用正文哈希而不是 id**：改版是原地覆盖（§4.2），id 当版本会让客户端永远拿旧缓存。ETag 跟着正文走，改版自然失效。
2. **注入基线只做一次**：原来 `makeNSView` + `updateNSView` + `makeCoordinator` 三处各注入一次，`updateNSView` 每次 SwiftUI 更新都跑 → 每个 tick 复制整页。现在视图只收最终串，基线注入集中在 `show`。
3. **`private, no-store` + 客户端自管缓存**：不让 URLSession / 中间代理插手，304 语义才是确定的（否则 URLSession 可能把 304 静默还原成它自己的缓存 200，客户端永远看不到 `notModified`）。

---

## 六、未覆盖与残留风险

- **真机观感未验**：8MB 带图页面的「秒开」到底多快、200 替换时有没有可见白闪（理论上同一 HTML 串不会重载，但 SwiftUI `AnyView` 的 identity 复用只在代码层推过）。
- **断网回退未验**：`load` 的「网络失败 → 显示缓存」只做了代码走查，没有真机切飞行模式。
- **无痕落盘（§3.1）**：定调前这是本分支唯一的产品级残留风险。
- **缓存目录无上限以外的治理**：512MB 上限靠 `store` 时触发，长期不写新页就不会回收；当前可接受（与日志治理同级别），不与本轮混。

---

## 七、修复清单

- [x] **P1 `ArtifactOpenCoordinator.swift:52-53`**：已开着 → `isOpen = true` + `activateTab(...)`，别直接 `return`
- [x] P2 `ArtifactOpenCoordinator.swift:108-116`：iOS 404 分支 `presented = nil`
- [x] P2 `ArtifactHTMLCache.swift:92`：304 命中时 touch mtime
- [x] P2 `artifact.go:78`：`If-None-Match` 按 `,` 拆分 + 去 `W/` 前缀比较
- [x] P2 `ArtifactHTMLCache`：补 SwiftPM 测试（5 条，见 §八）
- [x] **P2 §3.1 拍板**：接受并写进 §9.8「无痕边界」/ §十一（见 §八）
- [x] P3 挑做：#1/#2/#3/#4/#5/#6/#7 全部处理（见 §八）
- [ ] 真机跑一次：首次打开（miss）/ 二次打开（hit 304）/ 改版后重开（200 替换）/ 删产物后点旧卡片（404 关 tab）/ 断网重开（继续看缓存）

---

## 八、修复状态（2026-10-30，同分支）

| # | 项 | 状态 | 落点 / 说明 |
|---|---|---|---|
| P1 | 已开着的 tab 不激活 | ✅ 已修 | `ArtifactOpenCoordinator.open`：`rs.isOpen = true` + `rs.activateTab(id:inRightPane:)` |
| P2 §3.1 | 无痕副本残留 | ✅ 已拍板：接受 | §9.8「无痕边界」+ §十一「客户端正文缓存与无痕」+ 变更记录；彻底隔离需按 conversation 清，未做 |
| P2 §3.2 | `If-None-Match` 子串匹配 | ✅ 已修 | `etagMatches`：逗号拆分 + `TrimSpace` + 去 `W/`；`TestETagMatches` 覆盖 `"xx"` / 弱标签 / 列表 |
| P2 §3.3 | iOS 404 不关 sheet | ✅ 已修 | 404 分支 `if presented?.id == id { presented = nil }` |
| P2 §3.4 | 304 不刷新 mtime | ✅ 已修 | `ArtifactHTMLCache.touch(id:)`（html + etag），304 命中时调用 |
| P2 §3.5 | 缓存无测试 | ✅ 已修 | 新增 `ArtifactHTMLCacheCore` target + 5 条测试（存取删 / 无 etag 清旧 / 超限淘汰 / touch / 非法 id） |
| P3 #1 | 网络失败分支重复 `show` | ✅ 已修 | 缓存已画在屏幕上，改为只记日志 + `return` |
| P3 #2 | `c.Data([]byte(...))` 拷贝 | ✅ 已修 | 新增 `writeHTML`：`c.Writer.WriteString` + 手写 Content-Length，裸 HTML 与公开分享两处都换 |
| P3 #3 | `GetHTML` 只取需要的列 | ✅ 已修 | `ArtifactRepo.GetHTML` 只 Select `id/title/html`，不读 `source_html` |
| P3 #4 | Caches 路径两处答案 | ✅ 已修 | `LocalPaths.appCachesRoot` / `artifactsCacheDir`，`appLogDir` 复用 |
| P3 #5 | §9.2 与 §9.8 矛盾 | ✅ 已修 | 补注「这条说的是内存常驻；磁盘缓存见 §9.8」 |
| P3 #6 | 文件名直接拼 id | ✅ 已修 | `fileURL` 校验空 / `.` / `..` / `/` / `\`；测试覆盖 |
| P3 #7 | 新端点没加 CSP sandbox | ✅ 已修 | 抽出 `artifactCSP`，裸 HTML 与公开分享两个出口共用 |

验证（主工作区）：`go test ./internal/controller ./internal/service` ✔、`swift test --filter ArtifactHTMLCacheTests` 5/5 ✔、macOS/iOS `swiftc -typecheck` 0 error ✔。真机链路仍待验证（见 §七最后一条）。

---

## 九、第二轮 · 改动挪回主工作区后的复检（2026-10-30）

范围：主工作区 `feat/artifact-html-cache` 全部未提交改动（14 改 + 4 新，含 `Package.swift` / `project.pbxproj` / `LocalPaths` / 缓存核心拆分 / `Tests/ArtifactHTMLCacheCoreTests`）。

**在 main HEAD 上重跑**：

- `go build ./...` ✔、`go test ./...`（全包）全绿 ✔
- `swift test --filter ArtifactHTMLCacheCoreTests` 5/5 ✔（`ArtifactHTMLCache.swift` 不依赖 SwiftUI / LocalPaths / LogBuffer，SwiftPM 里按非 MainActor 编过，说明拆分干净）
- macOS / iOS 两批 `swiftc -typecheck` 0 error，且改动文件 0 warning ✔
- 分环境目录、`fileURL` 校验、`etagMatches` 逐项比较、`touch`、iOS 404 关 sheet、`open` 激活已开 tab —— 均已在代码里确认（不是只看 diff 的「已改」）

**§二~§四 的结论全部核对关闭**（逐条见 §八），本轮只剩下面几条，均 P3 且不阻塞：

1. **`pruneIfNeeded` 的淘汰粒度是「文件」不是「id」**（`ArtifactHTMLCache.swift:107-137`）。html 与 etag 是两个文件，排序按 mtime 升序、**同 mtime 的相对顺序不保证**（Swift `sorted` 非稳定），所以最后一条被淘汰的记录常常只删掉 `.html`（8MB）、留下 3 字节的 `.etag` 孤儿。后果极轻：孤儿占预算但会在后续某次 prune 被清掉；反过来的「删 tag 留 html」需要越限 ≤3 字节才会发生。`testPruneDeletesOldest` 实际就跑在这个分支上（断言仍成立，因为 html 没了 `load` 就返回 nil）。要收就按 id 分组、成对删（约 10 行），不收也行。
2. **`load` 里「304 但缓存刚好被系统清掉」的退避分支不可达**（`ArtifactOpenCoordinator.swift:94-104`）。304 的前提是发过 `If-None-Match`，而发 etag 的前提是 `cached != nil` → 走进 `else` 时 `cached` 必然为 nil 且 304 不可能发生。保留它当保险没坏处，但注释描述的场景不成立，建议改成「服务端行为异常时兜底」或直接删掉（省 6 行 + 一次嵌套请求）。
3. **`load` 会把空正文当命中**（`ArtifactHTMLCache.swift:28-39`）：`String(contentsOf:)` 读到 0 字节也返回 `Entry`，于是先画一版只有基线的空白页、随后 304 → 永远空白。用户可见概率接近 0（`write(atomically:)` 不会留半截文件，服务端也不允许空正文），一行 `guard !html.isEmpty else { return nil }` 可彻底堵死。
4. **`fetchArtifactHTML` 复制了 `doRequest` 的 401 / 非 200 映射**（`APIClient.swift:207-231`）。裸 HTML 不能套统一包装，所以确实要单写一段；但可以抽一个 `rawGet` 只做「状态码 → 错误」再复用，省 15 行重复。当前写法与既有 `doRequestStatusOnly` 同构，不改也站得住。
5. **提交卫生**：`client/.../Services/ArtifactHTMLCache.swift`、`ArtifactHTMLCache+Shared.swift`、`Tests/ArtifactHTMLCacheCoreTests/`、本报告仍是 `??`；`project.pbxproj` 的 `INFOPLIST_KEY_...` 字母序换位与本功能无关（工程用 `PBXFileSystemSynchronizedRootGroup`，新文件自动进 target，**不需要**改 pbxproj），应单独提交或还原。

**还不必重开的新观察**：`ArtifactDetailView`（上一分支）把 `html: String?` 直接透给 `HTMLArtifactView`，nil → 非 nil 是换成另一种视图类型，天然只 `makeNSView` 一次，没有「首次 update 必重载」的老坑；`docs/design/html-artifact/design.md` 的 §4.1 / §9.2 / §9.8 / §十一 / 变更记录已与本实现同口径，无残留旧值。

**本轮判定：可以合并（P0/P1/P2 = 0/0/0）**，前提是真机跑一遍 §七最后那条链路（miss / 304 / 200 替换 / 404 关 tab / 断网看缓存）。上面 5 条都是「顺手收」级别，建议至少做 #2、#5。
