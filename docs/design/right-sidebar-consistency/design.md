# 右栏列表页统一：多选入口一律进页头、智能体卡片动作进「⋯」

> 日期：2026-10-30
> 估算（开工）：5 面 / 1 缝（0 条需先建探针）/ 1 次真机往返 / 3 个待拍决定 · ~2–4h
> 估算（定稿）：3 面 / 1 缝（0 条需先建探针）/ 1 次真机往返 / 0 个待拍决定 · ~1.5–2.5h

## 1. 人话版

右栏三个列表页都有多选，但长成了三样：

| | 入口 | 选中态 |
|---|---|---|
| 文档 | 页头 `checkmark.circle` 图标 | 页头整条换成「✕取消 ｜ 已选 N 篇 ｜ 删除 + 下载」 |
| 图片 | 页头 `checkmark.circle` 图标 | 页头整条换成「✕取消 ｜ 已选 N 张 ｜ 全选 + 删除」 |
| 灵阅 | **内容区顶部一行文字「选择」** | **不换页头**，内容区再多一行纯文字（全选/已选/删除所选/取消） |

灵阅那一行还少了 Esc、少了 ✕ 图标，且它跟文档/图片不共用 `SelectionToolbar`，所以「取消」在两处的写法都不一样。

根因很简单：**灵阅的多选状态是 `ArtifactListView` 里的 `@State`，而页头在 `BrowsePanelView`**，页头读不到，只能退到内容区自己画一行。

智能体是另一类不一致：它的编辑/删除还留在**卡片页脚**（两颗文字按钮），是清单里唯一没跟上 `docs/design/card-more-menu` 的卡片列表；且删除**没有二次确认**，点一下直接删掉设定。

改法两件：

1. **灵阅多选归位**：状态搬进 `ArtifactViewModel`，入口改成页头同款图标；选中态由 `BrowsePanelView` 用同一款 `SelectionToolbar` 画。灵阅那行自绘工具条整体删掉。
2. **智能体卡片接 `⋯`**：编辑/删除从页脚搬进右上角菜单，删除补一次确认。

## 2. 决策记录

| # | 决定 | 说明 |
|---|---|---|
| RS-1 | 多选入口统一到**页头图标** | 页头图标位是文档/图片既有惯例；反过来把文档/图片挪到内容区会更挤、也要多改两个页 |
| RS-2 | 灵阅多选状态放 **ViewModel** | 页头要读它；同时让「离开面板即退出多选」和文档/图片走同一条 `onChange` |
| RS-3 | 灵阅多选**跨端保留** | 文档/图片的多选入口在 `#if os(macOS)` 里，灵阅那行工具条原本两端都有。判断放在平台分支之外，iOS 不倒退 |
| RS-4 | 批量删除确认放 `BrowsePanelView` | 与文档（batch）/图片同一处；灵阅单条删除的 alert 仍留在 `ArtifactListView` |
| RS-5 | 智能体删除**加二次确认** | 设定不可恢复；文档/灵阅/图片都有确认，只有它没有 |
| RS-6 | 勾选圈样式、图片角 ✕、回收站「恢复」、文档「全选」**本次不动** | 用户 2026-10-30 拍板只做前两项，其余留以后 |

## 3. 技术方案

| 文件 | 改动 |
|---|---|
| `Shared/ViewModels/ArtifactViewModel.swift` | `isSelecting` / `selectedIDs` / `enterSelection` / `exitSelection` / `toggleSelection` / `selectAll` / `deleteSelected`（照 `ImageViewModel` + `DocumentViewModel.deleteSelected`）；`load()` 末尾把勾选与现有条目取交集（原在视图 `.task` 里） |
| `Shared/Views/BrowsePanelView.swift` | `body` 里灵阅选中态的判断提到 `#if os(macOS)` 之前；`panelHeader` 公共段加灵阅入口图标；新增 `artifactSelectionHeader`（复用 `SelectionToolbar`）+ `isConfirmingArtifactBatchDelete`；`onChange(of: browsePanelSelection)` 补 `artifactVM.exitSelection()`；`ArtifactPage` 子树挂批量删除确认框 |
| `Shared/Views/ArtifactListView.swift` | 删掉本地 `@State selecting/selected`、`toolbar`、`toggle`、`toggleAll`、`exitSelection`、`PendingDeletion.many`；卡片改读 VM；单条删除 alert 保留 |
| `Shared/Views/AgentListView.swift` | `actions` → `menuActions: [编辑, 删除]`；新增 `pendingDelete` + `confirmationDialog` |

工具条动作：`取消 | 已选 N 页 | 全选 + 删除`（`删除` 红字、空选或删除在飞时置灰）。批量删除文案：「选中的 N 页将被删除，不能恢复；已分享的链接会立即失效。」成功后 toast「已删除 N 页」（与文档/图片一致）。入口图标补了 `.accessibilityLabel`：iOS 不显示 `.help`，纯图标按钮没有无障碍名。

删除失败语义不变：分批删里前面的分块已经删掉了 → 从勾选里剔掉，留在选择态可直接重试（这段逻辑从视图搬进 `deleteSelected`）。

## 4. 影响面

- 主链路？否。纯客户端展示层，**后端零改动**；接口（`batch-delete` / `deleteAgent`）都已在用。
- iOS：灵阅多选**保留**且位置与 macOS 一致；文档/图片维持原状（它们本来就只有 macOS 有多选）。
- 卡片 `⋯` 是 `CardConfig.menuActions` 的第三个调用点，组件本身零改动。

## 5. 验证

1. `xcrun swiftc -typecheck`：macOS（`Shared/**` + `xHelperMac/*.swift`）与 iOS 各一次，带项目真实设置。
2. 真机（macOS + iOS 各一次）：macOS 上灵阅入口在页头、Esc 退出、全选/取消全选/删除/取消四态、删完对应右栏 tab 一并关闭、翻页后勾选不留幽灵 id；iOS 上确认灵阅入口图标够显眼（`.help` 不显示，只有 `.accessibilityLabel`）、多选点卡切换与删除可用。智能体 `⋯` 两项、删除有确认、取消不删、编辑弹窗照旧（两端）。
3. 深浅色各看一次 `⋯` 与勾选圈。

## 6. 明确不做

- 勾选圈/描边三套样式合一（文档 18pt 无底、图片 18pt 白圆底、灵阅 17pt 材质圆底）。
- 图片瓦片右上角 ✕ 收进 `⋯`（瓦片不是 `CardView`，要另做一套菜单）。
- 回收站「恢复」收进 `⋯`（页脚只有一颗按钮，收进去反而多点一次）。
- 文档工具条补「全选」。
- 智能体页的「新建」入口：`showCreateSheet` 目前没有任何地方置 `true`，弹窗打不开——单独开票。
