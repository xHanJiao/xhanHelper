# 文档词云多选 + 共现联动（macOS）

```
估算（开工）：~8 面 / 2 缝（0 需先建探针）/ 2 次真机往返 / 1 个待拍决定 · ~2–3h
估算（定稿）：  8 面 / 2 缝（0 需先建探针）/ 1 次真机往返 / 0 个待拍决定 · ~2h
```

> 定稿后待拍决定（联动范围）已由用户选定「① 直接共现」。

## 目标

macOS 文档页「词云」模式支持**多选标签**：

1. **共现联动高亮**：⌘+点击选中 A 时，与 A 同篇出现过的标签 B/C/D 高亮，其余压暗（图效果）。
2. **交集筛选（层层收敛）**：多选 A/C/D 后，只保留「**同时**命中全部标签」的文档，越选越少。

## 已确认的设计决策（勿改）

1. **单击 = 单标签筛选**（切列表，保持历史行为）；**⌘+单击 = 切换多选**（留在词云）。
2. **联动范围 = 直接共现（1 跳）交集**：只高亮与**全部**已选标签都共现的标签（保证再选还能继续收敛）。传递闭包 / 分级在 mockup 里给过候选，未采纳。
3. **高亮分层**：选中 = `appAccent` + 胶囊底；1 跳交集邻居 = `accentSecondary`；其余 = `primary` × 0.18。既有的本地搜索高亮（`cloudQuery`）叠加在每层上，未命中再压暗。
4. **筛选语义 = AND 交集**：`tags=A,C,D`，文档须同时含全部标签（初版误做成 OR 并集，已按「越选越少」改成交集）。
5. **后端零新增查库**：`/api/documents/tags` 在一次 `Pluck` 里同时算出频次与两两共现边，加法式返回 `edges`；交集总数复用现有 `limit=0`（只出 total）。
6. **不建索引、不规范化**（见下「性能结论」）。
7. **范围**：仅 macOS 词云 + 后端。Web 标签下拉不动（旧 `tag` 参数保留）。
8. **布局稳定**：高亮只改颜色/透明度（选中加胶囊底但不改 `weight`），**不触发 `BallCloud.place` 重排**，打字/选择时词云不跳动。

## 改动清单

| # | 文件 | 改动 |
|---|------|------|
| 1 | `server/internal/service/document_repo.go` | 新增 `TagEdge`；`tagGraph` 一次全扫同出频次+共现边，`TagGraph`/`ListTagCounts` 复用；新增 `ListByAllTags`（多标签 AND 交集），`List` 委托；删死代码 `tagMatchClause` |
| 2 | `server/internal/controller/document.go` | `parseDocTags` 解析 `tags=A,C,D`；`ListDocuments` 走 `ListByAllTags`；`/documents/tags` 返回 `{tags, edges}` |
| 3 | `server/internal/service/document_repo_test.go` | 新增交集 / 共现边两个单测 |
| 4 | `Shared/Services/APIClient.swift` | `listDocuments(tags:)`；`documentCount(tags:)`（limit=0）；`listDocumentTags()` 返回整个 response；新增 `TagEdge`，`DocumentTagListResponse` 加 `edges` |
| 5 | `Shared/ViewModels/DocumentViewModel.swift` | `activeTags` / `selectedTags` / `cooccurrence` / `resultCount`；`toggleSelect` / `filterBySelection` / `clearFilter` / `neighbors`（邻接交集） / `buildGraph`；load/loadMore 传 `tags` |
| 6 | `Shared/Views/DocumentListView.swift` | `TagCloudView` 分层高亮 + `NSEvent.modifierFlags` 区分单击/⌘；多选动作条 `selectionBar`；右下角键帽提示 `cloudHint`；列表筛选条改多 chip |
| 7 | `Shared/Views/BrowsePanelView.swift` | `onChange(of: mode)` 顺手清多选 |
| 8 | `docs/design/document-cloud-multiselect/mockup.html` | 决策用交互预览（含三种联动候选开关） |

## 关键点

- **命中样式不动布局**：只改 `foregroundColor` / `opacity`（胶囊底用 `.background`，不占布局），不改 `font`/`weight`。
- **单击 vs ⌘单击**：用 `NSEvent.modifierFlags.contains(.command)` 在点击回调里判定，而非叠两个手势（避免优先级/双重触发）。
- **共现图**：`cooccurrence[tag] = [邻居: 共现次数]`，由 `edges` 双向填充；`neighbors(of:)` 求**邻接交集**（与全部已选都共现）并剔除已选中。邻接交集是成对共现的近似，作候选提示够用，真实篇数以服务端交集查询为准。
- **替换语义**：`tagGraph` 计数保持旧语义（同篇重复按出现次数计，钉在旧测试上），仅**共现边**对同篇标签去重后两两计数。

## 性能结论（实测，勿再纠结索引）

本地 dev 库即 prod dump：单用户 163 篇 / 334 标签。

| 查询 | 实测（warm） |
|------|------|
| 单标签筛选（多标签同形，多几个 AND） | ~1.5ms |
| `/documents/tags` pluck | ~0.66ms |

- 线性外推 ~9µs/行：千篇 ~9ms、万篇 ~90ms。
- **`tags` 列上的索引对 `LIKE '%,A,%'` 前导通配无用**；`user_id` 单用户全命中，也不筛。唯一能用索引的是规范化 `document_tags` + `(user_id, tag)`，代价是迁移+写入路径同步，**到万篇量级再做**（代码里已用 `ponytail:` 注释标了天花板与升级路径）。

## 人工验收清单

1. 词云 ⌘+点击 A → B/C/D 变 `accentSecondary`、其余压暗；再 ⌘+点击 C → 邻居收敛为交集（A、C 都共现的才亮）；逐个取消 → 恢复常态。
2. 多选后底部出现动作条：已选 chips + `查看 N 篇`；N = 同时命中全部标签的文档数（与服务端 total 一致），**越选越小**。
3. 动作条点击 → 切列表模式，按交集筛选；列表顶部多 chip 筛选条，✕ → 回词云并清空。
4. 单击某标签 → 切列表，仅该标签筛选（历史行为不变）。
5. 右下角键帽提示常驻；切换列表/词云后多选与高亮被清掉。
6. 搜索高亮与多选叠加：选中态下未命中的词再压暗。
7. Web 文档页无变化；旧单 `tag` 参数仍生效。
8. 后端 `go build ./... && go test ./...` 绿；客户端双 target `swiftc -typecheck` 绿；真机跑一次词云交互。

## 已知接受的简化

- 联动只做 1 跳直接共现（交集），不做传递闭包/分级。
- `edges` 每次全量重算（个人量级够；万篇再规范化）。
- 交集篇数走一次额外轻量请求（`limit=0`），不在客户端建全量倒排索引。
