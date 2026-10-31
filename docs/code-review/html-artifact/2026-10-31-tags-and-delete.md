# 第六轮审查 · 灵阅标签（必填 / 词云 / 筛选）与工具侧删除

**审查日期**：2026-10-31
**审查范围**：分支 `feat/artifact-tags-and-delete` 未提交改动（18 文件，+1365 / −84）
**范围明细**：
- 服务端：`model/artifact.go`、`service/artifact_repo.go`、`service/document_repo.go`（抽 `buildTagGraph`）、`controller/artifact.go`、`controller/router.go`、`tool/html_artifact.go` + 两份测试
- 客户端：`Models/ArtifactRef.swift`、`Services/APIClient.swift`、`ViewModels/ArtifactViewModel.swift`、`Views/{ArtifactListView,BrowsePanelView,CardStyle,DocumentListView}.swift`
- 文档：`docs/design/html-artifact/{design.md,fine-grained-edit.md}`

**结论**：✅ **可以合并**。未发现 P0 / P1；3 条 P2 建议本轮或紧随一轮修掉（都不影响数据正确性，属于「出错时会给出误导性结果」的那类）。服务端 `go build ./... && go test ./...` 全绿。

---

## 一、总览

| 级别 | 数量 | 说明 |
|---|---|---|
| P0 | 0 | 无越权、无注入、无数据丢失路径 |
| P1 | 0 | — |
| P2 | 3 + 1 风险确认 | 参数解析吞错、两次请求绑一个 catch、正文与标签非原子写、不可逆批量删除的提示词风险 |
| P3 | 7 | 见 §四 表格 |

---

## 二、P2

### P2-1 `?limit=abc` 从「回默认 30」变成「只取 total」，返回 200 + 空列表

`server/internal/controller/artifact.go:129`

```go
limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
if limit < 0 || limit > 100 { limit = 30 }   // 0 现在是哨兵，不再被拦
```

`limit=0` 被赋予新语义（只出 total）之后，`Atoi` 的 error 被丢弃就出现了新坑：`?limit=abc`、`?limit=`（空值）都会得到 `limit == 0`，于是返回 `{"items": [], "total": N}` 的 200 —— 调用方看到的是「这个用户一页都没有」，而不是「参数非法」。注释里写的「非数字回默认值」已不成立。

另外一键：`limit=200` 仍然静默降到 30（不是钳到 100），与注释也不完全一致，属旧行为，可不改。

**建议**：显式判错，落 400（本项目「严禁静默吞错误」的直接适用点）：

```go
limit := 30
if raw := c.Query("limit"); raw != "" {
    n, err := strconv.Atoi(raw)
    if err != nil || n < 0 || n > 100 {
        view.JSONError(c, http.StatusBadRequest, "invalid limit")
        return
    }
    limit = n
}
```

`offset` 同理（现在 `?offset=abc` → 0，行为可接受，但同样在吞错）。

**补充测试**：`artifact_test.go` 里加一条 `?limit=abc` 期望非 200，把这条语义钉住。

### P2-2 `ArtifactViewModel.load()` 把列表与词云绑在同一个 `do/catch`：词云挂了，列表也显示「加载失败」

`client/xHelper/xHelper/Shared/ViewModels/ArtifactViewModel.swift:60-77`

`items` / `total` 已经赋值成功，随后 `listArtifactTags()` 一抛错就落进通用 catch，`errorMessage` 被写上 → `PageContainer` 显示「加载失败 + 重试」，用户看到的是整页失败，而列表其实是好的。

这违反项目既有口径：「不影响流程（可降级）→ 就地 `log` 说明原因后继续」。文档侧 `DocumentViewModel.load` 虽然不是同一个写法，但词云数据本来就不是列表的必要条件。

**建议**：拆一个 `private func loadTags() async`，单独 `do/catch`，失败只 `LogBuffer.shared.error` + 保留上一次的 `tags` / `cooccurrence`；列表与词云各自失败互不影响。

### P2-3 `patch` 同时改正文和标签 = 两条 SQL，失败时模型会掉进重试陷阱

`server/internal/tool/html_artifact.go:663-681`

先 `UpdateIfSourceUnchanged(...)`（正文，乐观锁），再 `SetTags(...)`。若第二条失败：

- 正文已经落库；
- 返回给模型的是 `保存标签失败`；
- 模型按提示重试同一个 patch → 这次 `source_html` 已变，命中「页面已变（另一处改过或已删除），请重新 html.get」；
- 模型拿到的信息与事实相反（没有别人改过，是它自己改的）。

概率低（需要 DB 两次写之间出错），但一旦发生，排查成本高（「我自己改的为什么说被别人改」）。

**建议**：让标签跟着同一条 UPDATE 走 —— `UpdateIfSourceUnchanged` 增加 `tags string` 参数，`Updates(map[string]any{"html":…, "source_html":…, "tags":…})`。这样保留乐观锁语义，且一次写。改动比保留两次写好写。

### P2-4（风险确认，非缺陷）模型可一次性不可逆删除 100 页

`tool/html_artifact.go` 的 `delete` 与 `controller` 的批量删除共用「软删、无回收站、删了不能恢复」的语义。UI 侧有确认弹窗，工具侧只有提示词约束（「只在用户明确说删掉时调用」）。

设计文档 §9.7 已明确这是有意的（灵阅无回收站），本轮不要求改。记录在这里是提醒：**用户能表达「删掉」的方式很多**（「这张不要了」「清一下旧的手账」），一旦模型理解偏差，损失是 100 页。可选的加固（任选其一，不急）：
- 把上限从 100 降到 20（一次会话内正常删除很难超过这个量）；
- 删除成功后在回复里强制点名被删的标题（`missing` 已有，缺的是「删了什么」的清单）；
- 或对 `delete` 增加「先 `list` 回显、等用户确认」的两步约定。

---

## 三、P3

| # | 位置 | 问题 | 建议 |
|---|---|---|---|
| 1 | `APIClient.swift:238-244` | 复用 `urlQueryAllowed` 拼 querystring：`&` 不转义（实测 `手账&旅行` → 两个参数）、`+` 不转义（`C++` → 服务端解成 `C  `，再被 `ReplaceAll(" ","")` 变成 `C`）。文档列表 `listDocuments` 是同一写法，属继承旧问题 | 换 `URLComponents` + `queryItems`，两个列表一起改（一处顺手，别再复制） |
| 2 | `Views/CardStyle.swift:9-21` | `extension String { var tagBadges }` 是全局命名空间扩张，语义又绑了 SwiftUI 的 `Color` | 可保留（与 `parseTags` 去重是收益）；后续新增类似解析时改成 `TagBadge.list(from:)` 之类的具名入口 |
| 3 | `controller/artifact.go:143` | `tags` 参数无数量/长度上限：一个请求可以塞几千个 `WHERE` 子句（自伤型 DoS，不影响他人） | 加 `if len(tags) > 10 { 400 }` 一行 |
| 4 | `service/artifact_repo.go:114` | `keyword` 直接拼 `%…%`，未转义 `%` / `_`：搜 `50%` 会匹配到任意以 50 开头的内容（`document_repo.keywordClause` 同款） | 与文档一起处理；单这一处改会两套口径不一致 |
| 5 | `tool/html_artifact.go:546` | `src == ""`（早于 `source_html` 列的老页）在标签逻辑之前就返回错误，导致这类页**永远无法只补标签**，只能整页重建 | 把 `tags` 分支提到 `src == ""` 检查之前；或在报错文案里补一句「这页可以先 `create` 整页重建并顺手打标签」 |
| 6 | `controller/artifact.go:46-52` | `GET /api/artifacts/:id` 未返回 `tags`，与列表/`html.get` 不一致（当前客户端只取 `.html`，无实际影响） | 顺手补上 `"tags": a.Tags`，避免下一个人以为详情没有标签 |
| 7 | `Views/BrowsePanelView.swift:469-477` | 用 segmented 切回「词云」时不清 `activeTags`，此时筛选条在词云态不可见，再切回列表仍是筛过的，容易以为「少了页面」 | 切到词云时一并清 `activeTags`（与 `clearFilter` 同口径），或词云态也显示筛选条 |

**文档小瑕疵**：`fine-grained-edit.md` 写「四类至少给一个（`ops` / `insert` / `remove` / `style`），另有可选的 `tags`」，而代码错误文案是「`ops` / `insert` / `remove` / `style` / `tags` 至少给一个」。建议改成「四类至少给一个；`tags` 也可单独给（只改标签）」，与实现对齐。

---

## 四、已核对、无需改动的项

- **越权隔离**：`ListByTags` / `TagGraph` / `ListTags` / `CountUntagged` / `SetTags` / `ExistingIDs` / `DeleteMany` 全部带 `user_id`，`SetTags` / `ExistingIDs` 的跨用户用例已覆盖（`artifact_repo_test.go`）。
- **注入**：`tagBoundaryClause` 只返回固定 SQL 片段，标签与关键词全走参数；`q.Where("("+clause+")", args...)` 拼的是常量。
- **列表不取正文**：`ListByTags` 仍只 `Select` 六列，`html` 不进 SQL / 不进 JSON。
- **GORM 复用**：`q.Count()` 之后复用 `q` 做 `Find` 没有条件/Select 泄漏（单测实测通过；`Count` 只为 SELECT 子句做 defer 清理，条件保留）。
- **空集合 / 边界**：`tags` 全空、重复、全角逗号、`limit=0`、`offset<0`、越界 id、重复 id、空 `artifact_ids`、`delete` 超 100 均有测试覆盖。
- **确定性**：`buildTagGraph` 先按频次降序再按名字升序，`edges` 按 a/b 排序 —— 同一数据两次调用顺序稳定（进上下文的工具结果不会来回抖）。
- **工具契约**：`ReadOnlyFuncs` 与 `Functions()` 一致（`list_tags` 只读、`delete` 写），`Catalog.Others` 有描述，Registry 的三个启动校验都不会 panic。
- **兼容性**：`ArtifactSummary.tags` 为可选且 `decodeIfPresent`，老服务端/老数据（`""` 或 `NULL`）都能解；`String.tagBadges` 替换两处 `parseTags` 后行为一致（空串 → 空数组）。
- **前端（Vue）**：未消费 `/api/artifacts`，加列不影响。
- **测试**：`go build ./... && go test ./...` 全绿（含新增 6 组用例）。

---

## 五、未覆盖

- **Swift 侧未编译**：按约定不在本地跑 `xcodebuild`，`.mode` / `Picker` / `#if os(macOS)` 分支需在 Xcode 里编一次确认；`ArtifactBrowseMode` 的 `Hashable`、`$artifactVM.mode` 绑定、iOS 分支走 `listContent` 这几处是靠静态阅读判断的。
- **MySQL 实测**：单测只用 SQLite。`tagBoundaryClause` 的 `REPLACE(...) LIKE` 在 MySQL 上会对 `tags` 列做全表表达式匹配（用不上索引），量级上百页可接受，但没有实测耗时。
- **词云真机交互**：⌘ 多选 → 交集篇数 → 切列表这条链没跑真机；`minTagCount` 的 Stepper 上限（`maxArtifactTagCount`）也没点过。
- **老库迁移**：只验了 SQLite 的 `ALTER TABLE ADD COLUMN`（NULL tags 计入 `untagged`）；MySQL 加列后老行也是 NULL，逻辑一致，但没在真库上跑。

---

## 六、下一步

建议顺序：
1. P2-1（`limit` 解析）—— 一行判断 + 一条测试，最划算；
2. P2-3（`UpdateIfSourceUnchanged` 带上 tags）—— 少一条 SQL，顺带消掉重试陷阱；
3. P2-2（`loadTags` 拆开）—— 用户体验向，随手改；
4. P3 里只挑 #1（URLComponents）和 #7（切词云清筛选），其余记账即可。

需要我按这个顺序直接改的话说一声。

---

## 七、处理回执（2026-10-31 · 同轮修完）

| 条目 | 处理 | 说明 |
|---|---|---|
| P2-1 `limit`/`offset` 吞错 | ✅ 已修 | 两个参数改为显式解析，非数字 / 越界 → 400；`limit=0` 仍保留「只出 total」语义。新增 5 条 400 用例（`abc` / `-1` / `200` / `offset=x` / 11 个标签）。 |
| P2-2 列表与词云共用一个 catch | ✅ 已修 | 拆出 `loadTags()`，单独 `do/catch`：词云失败只记日志、保留上一次标签，不再把列表拖进错误态。 |
| P2-3 正文与标签两次写 | ✅ 已修 | `UpdateIfSourceUnchanged` 增加 `tags` 参数，正文 + 标签同一条 UPDATE 落库；`tags`-only 仍走 `SetTags`（不重写正文）。repo 测试断言同一次写入带上标签。 |
| P2-4 不可逆批量删除 | 维持 | 设计已定为「软删、无回收站」，本轮不改；「回复里点名删了什么」记 TODO。 |
| P3 #1 URLComponents | 不改 | 与 `listDocuments` 同款问题，改要一起改，另开一轮（已在文档里记账）。 |
| P3 #2 `String.tagBadges` 命名 | 不改 | 收益是消掉两处重复；后续再新增解析时改成具名入口。 |
| P3 #3 `tags` 数量无上限 | ✅ 已修 | `> 10` → 400。 |
| P3 #4 LIKE 未转义 `%`/`_` | 不改 | 与 `document_repo.keywordClause` 同口径，单独改会造成两套语义；一并处理时再动。 |
| P3 #5 老页无法只补标签 | ✅ 已修 | `source_html` 为空时，只有改正文（ops/insert/remove/style）才拒；`patch(tags=…)` 可单独改。新增用例覆盖。 |
| P3 #6 详情不返回 tags | ✅ 已修 | `GET /api/artifacts/:id` 补 `tags`。 |
| P3 #7 切词云不清 `activeTags` | 不改 | 切回列表时筛选条与标签 chip 都还在，用户能看到自己处于筛选态；清掉反而会让「看一眼词云再回来」丢筛选。 |
| 文档小瑕疵 | ✅ 已改 | `fine-grained-edit.md` 改为「`ops`/`insert`/`remove`/`style` 至少给一个；`tags` 也可单独给」。 |

**复验**：`go build ./... && go test ./...` 全绿（新增：参数非法 400、老页只改标签、正文 + 标签一次写入）；`swiftc -typecheck` macOS 26.3 / iOS 26.0 双端无 error。仍未做真机 / `xcodebuild`（按约定归用户）。

---

## 八、第二轮复验（修后复审）

**结论**：6 项修复都落在点上，**无 P0 / P1，可合并**。但 P2-3 的修法带进来一个**新的 P2（并发下标签被静默回滚）**，另有 2 条 P3 和 1 处文档缺口。服务端 `go build ./... && go test ./...` 我这边跑过，全绿。

### 8.1 逐条复验（已修项）

| 条目 | 代码位置 | 复验结论 |
|---|---|---|
| P2-1 `limit`/`offset` | `controller/artifact.go:131-150` | ✅ 用 `c.Query` 判空后显式 `Atoi` + 范围检查，非法一律 400；`?limit=`（空值）现在等同未传，`limit=0` 仍是「只出 total」。5 条 400 用例覆盖到位 |
| P2-2 词云失败拖累列表 | `ArtifactViewModel.swift:78-89` | ✅ `loadTags()` 独立 `do/catch`，失败只记日志、保留旧标签。与项目「可降级就地 log」口径一致 |
| P2-3 正文 / 标签两次写 | `artifact_repo.go:247`、`tool/html_artifact.go:664` | ✅ 同一条 `Updates` 落库；repo 测试断言 `tags` 随正文一起写入。`tags`-only 仍走 `SetTags`、不重写正文 —— 重试陷阱消掉了（**但见 8.2**） |
| P3 #3 `tags` 数量 | `artifact.go:151-154` | ✅ `> 10` → 400，用例在 400 那组里 |
| P3 #5 老页只补标签 | `tool/html_artifact.go:546-549` | ✅ 拒的条件收窄成 `hasStructure \|\| style != nil`；`TestHTMLArtifactPatchTagsOnLegacyPage` 同时钉住「只改标签成功」「改内容仍拒」 |
| P3 #6 详情缺 tags | `artifact.go:46-51` | ✅ 手写 map 补上了；客户端 `ArtifactPayload` 不声明该字段，多一个键不影响解码 |
| 文档瑕疵 | `fine-grained-edit.md:211-212` | ✅ 改成「四类至少给一个；`tags` 也可单独给」，与代码错误文案一致 |

### 8.2 新增 P2：不带 `tags` 的正文 patch 会把并发改的标签回滚

`tool/html_artifact.go:664` 现在无条件把 `tagsOut` 传下去，而 `tagsOut` 在 `req.Tags == nil` 时就等于**调用开始前读到的** `a.Tags`：

```go
tagsOut := a.Tags              // 读于 GetForRead
...
if req.Tags != nil { ... }     // 没传就保持旧值
ok, err := t.repo.UpdateIfSourceUnchanged(..., tagsOut)  // 永远写 tags
```

于是：

1. patch A（改正文，不带 tags）读到 `tags = "手账"`、`source_html = S`；
2. patch B（只改标签）把它改成 `"旅行,日本"` —— 这一步**不碰 `source_html`**，所以 A 的乐观锁照常通过；
3. A 落库，`tags` 被写回 `"手账"`。

用户刚打的标签被静默抹掉。修之前 A 不写这一列，所以这是修 P2-3 带进来的新问题。窗口很窄（要同一页上「改正文」与「改标签」并发），但服务端工具本来就是并发执行的（`tool_max_concurrency`），同一轮里模型发两个 patch，或两个端同时操作，都够触发；而且失败形态正是「静默回滚」，最难查。

**建议**：让「不传」真的是不写。repo 参数改 `tags *string`，`nil` 时不进 map：

```go
m := map[string]any{"html": html, "source_html": sourceHTML}
if tags != nil { m["tags"] = *tags }
```

调用方只在 `req.Tags != nil` 时传指针。这样「正文 + 标签」同时给时仍是**一条 UPDATE**（8.1 的收益保留），不给时不碰这一列。加一条测试：先改标签，再用不带 tags 的 patch 改正文，断言 `tags` 未被回滚。

### 8.3 新增 P3

| # | 位置 | 问题 | 建议 |
|---|---|---|---|
| 1 | `ArtifactViewModel.swift:122`（`toggleSelect`） | 服务端 `tags > 10` → 400，客户端 ⌘ 多选没有对应上限：选到第 11 个后「查看结果」变成静默失败（`artifactCount` 401/400 只进日志、按钮退回「查看结果」），点下去列表报「加载失败」 | `toggleSelect` 里 `if selectedTags.count >= 10 { ToastCenter... ; return }`，与 `maxTagFilter = 10` 常量共用一处 |
| 2 | `ArtifactViewModel.swift:60,75` | `loadTags()` 仍串在 `isLoading` 里（`defer` 在函数末尾执行）：词云接口慢/挂时，**列表已经拿到数据却一直显示转圈**（`PageContainer` 的 `isLoading` 分支优先于内容），P2-2 的收益打了折 | 在 `await loadTags()` 之前把 `isLoading = false` 掉（或 `async let` 并发拉两者、只等列表） |
| 3 | `docs/design/html-artifact/design.md:374` | 参数非法 → 400、`tags` 上限 10 这两条新契约没进设计文档，下一个人读文档会以为 `limit` 仍是「非法回默认」 | §9.5 那行补半句：「`limit`/`offset` 非法 → 400；`tags` 最多 10 个」 |

### 8.4 对「不改」的 4 条的意见

| 条目 | 我的看法 |
|---|---|
| P3 #1 URLComponents | **同意**。和 `listDocuments` 同一处病，分开改只会留下两套写法；建议把「两个列表一起换 `URLComponents`」记进账本，别让它漂成永久 TODO |
| P3 #2 `String.tagBadges` | **同意**。收益明确（消掉两份 `parseTags`、行为已统一），命名问题等第三次复制再收 |
| P3 #4 LIKE 未转义 `%`/`_` | **同意**。和 `document_repo.keywordClause` 同口径，单独改反而制造两套语义 —— 和 #1 一样挂在「文档/灵阅共同收口」那笔账上 |
| P3 #7 切词云不清 `activeTags` | **你的理由成立，我撤回这条**。筛选条 + chip 在列表态始终可见，用户不会「以为丢了页面」；清掉反而丢了「瞟一眼词云再回来」的上下文 |
| P2-4 批量删除 | **同意维持**。TODO 里「回复点名被删标题」的性价比最高，比降上限更有用（用户看到删了什么才能立刻纠正） |

### 8.5 收尾建议

修完 8.2 那一条（约 10 行 + 1 个用例）就可以提交了；8.3 的三条可以同轮顺手带上（合计不超过 20 行），也可以直接落 TODO。提交信息建议按你写的那条，正文里点一句「标签与正文同一条 UPDATE（仅在显式传 tags 时）」。

---

## 九、第三轮复验（8.2 / 8.3 修后）

**结论**：8.2 与 8.3 三条**全部修对**，实现与文档一致。新增 **1 条 P3（回归用例不红）**，不阻塞合并。`go build ./... && go test ./...` 我这边重跑，全绿。

### 9.1 逐条复验

| 条目 | 代码位置 | 结论 |
|---|---|---|
| 8.2 不带 tags 的正文 patch 回滚标签 | `artifact_repo.go:247-261`、`tool/html_artifact.go:667-671` | ✅ 签名改 `tags *string`，`nil` 时 map 里没有 `tags` 键；调用方只在 `req.Tags != nil` 时传指针。注释也写清了「不要把读到的旧值写回去」。这条修法比我建议的还稳（`nil` 语义显式化，而不是靠调用方传对值） |
| 8.3-1 客户端 10 个上限 | `ArtifactViewModel.swift:47,130-133` | ✅ `maxSelectedTags = 10` + 超限 Toast，与 `controller/artifact.go:150` 对齐；错误路径从「静默失败」变成「当场拒绝并说原因」 |
| 8.3-2 `isLoading` 被词云串住 | `ArtifactViewModel.swift:76` | ✅ 列表请求结束就撤转圈，`await loadTags()` 放后面；原 `defer` 已删，函数体内无早返回，不会漏撤 |
| 8.3-3 设计文档缺 400 契约 | `design.md:374` | ✅ 补了「`limit`/`offset` 非数字或越界、`tags` 超过 10 个一律 400（参数错误不静默降级）」 |

### 9.2 新增 P3：8.2 的回归用例对旧代码是绿的（等于没钉住）

`TestHTMLArtifactPatchWithoutTagsKeepsCurrentTags`（`html_artifact_test.go:1851`）走的是**顺序**路径：先 `patch(tags)` 改标签，**再**发正文 patch。正文 patch 执行时 `GetForRead` 读到的 `a.Tags` 已经是 `"旅行,日本"`，所以就算按旧实现把 `tagsOut` 写回，写的也是同一个值 —— 断言照样通过。真正要复现的是「正文 patch 先读到旧标签，标签随后被改，正文 patch 再落库」，那一步在工具 HTTP 面里表达不出来。

`TestArtifactRepoUpdateIfSourceUnchanged` 也只传了 `&tags`，**没有任何一处断言 `tags == nil` 时不碰该列** —— 新加的分支其实没被覆盖到。

**建议**：repo 里加一条真正会红的用例（旧实现传 `&stale` 时必挂）：

```go
// tags=nil 不能碰 tags 列：模拟「正文 patch 读完之后，标签被另一处改过」
stale, fresh := "旧标签", "后来的"
repo.SetTags(ctx, "u1", "a1", fresh)
ok, err := repo.UpdateIfSourceUnchanged(ctx, "u1", "a1", "v2", "<html>v3</html>", "v3", nil)
// 断言 ok && got.Tags == fresh   ← 旧实现写回 stale 会挂
```

工具层那条可以留着当「正常路径不丢标签」的保障，但别在注释里写「并发不被回滚」—— 它证明不了。

### 9.3 一个边界说明（非缺陷）

`create` 带 `artifact_id` 改版时仍然是无条件写 `tags`（不传就写 `oldTags`），理论上也有同一个回滚窗口。但 `create` 的语义本来就是**整页覆盖**（`title` / `summary` 早有同样行为），并发「只改标签」对上一张整页重写属于用户自己没见过的时序，且与本次修复无关。**不建议改**，记在这里是免得下次有人拿 patch 的修法来质疑 `Update`。

### 9.4 结论

可以提交了。三条 P3（8.3）已修；唯一新发现是测试质量，不影响行为，跟你提的「真机 / MySQL 未实测」一起记在账上即可。

---

## 九、第二轮处理回执（2026-10-31）

| 条目 | 处理 | 说明 |
|---|---|---|
| 8.2 不带 tags 的正文 patch 回滚并发标签 | ✅ 已修 | `UpdateIfSourceUnchanged` 的 `tags` 改 `*string`：`nil` 时不进 `Updates` map；工具侧只在 `req.Tags != nil` 时传。新增 2 条用例：repo 层「先改标签、再不带 tags 写正文，标签不被回滚」+ 工具层同场景。 |
| 8.3 #1 客户端多选无上限 | ✅ 已修 | `ArtifactViewModel.maxSelectedTags = 10`，`toggleSelect` 超限 toast 并拒绝，与后端 400 口径对齐。 |
| 8.3 #2 `loadTags` 拖着 `isLoading` | ✅ 已修 | 列表 `do/catch` 结束即 `isLoading = false`，之后才 `await loadTags()`；词云慢不再让列表一直转圈。 |
| 8.3 #3 设计文档缺新契约 | ✅ 已补 | `design.md` §9.5 加「`limit`/`offset` 非法或 `tags` > 10 → 400」。 |
| 8.4 四条「不改」 | 维持共识 | #1 / #4 挂「文档 + 灵阅共同收口」账；#2 等第三次复制再收；#7 撤回；P2-4 维持，TODO 记「回复点名被删标题」。 |

**复验**：`go build ./... && go test ./...` 全绿；`swiftc -typecheck` macOS 26.3 / iOS 26.0 双端无 error。（真机 / `xcodebuild` 仍归用户。）

---

## 十、8.2 用例有效性复查（2026-10-31 · 同日）

**确认审查意见成立**：原来的 `TestHTMLArtifactPatchWithoutTagsKeepsCurrentTags` 钉不住回归。

- 它走的是「先改标签 → 再改正文」的顺序路径；正文 patch 里的 `GetForRead` 读到的已经是新标签，
  旧实现（无条件 `tagsParam = &tagsOut`）写回的是同一个值，照样通过。
- 仓库层的 `TestArtifactRepoUpdateIfSourceUnchangedKeepsTagsWhenNil` **确实传了 `nil` 并断言标签不被改写**，
  它钉住的是「nil 不写该列」这条分支；但真正的回归点在工具层调用处（传了旧值），仓库层复现不了。

**已改成可复现的用例**：测试里建一条 SQLite 触发器

```sql
CREATE TRIGGER bump_tags BEFORE UPDATE OF html ON artifacts
BEGIN UPDATE artifacts SET tags = '旅行,日本' WHERE id = NEW.id; END;
```

正文 UPDATE 之前把 tags 换成新值，稳定模拟「读到旧标签 → 别处改标签 → 正文落库」的窗口。

**有效性已实测**：临时把工具改回无条件 `tagsParam = &tagsOut`，该用例失败（`tags="手账"`，标签被回滚）；
恢复 `req.Tags != nil` 判断后通过。完整服务端测试与双端 `swiftc -typecheck` 复跑通过。
