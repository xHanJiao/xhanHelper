# 灵阅详情页 · 左侧换篇列表

> 状态：**已实现**（2026-10-30）
> 范围：仅 macOS 客户端（`client/xHelper/xHelper/Shared/Views/`）。服务端 **零改动**，API / 模型零改动。
> 前序：`docs/design/html-artifact/design.md`（灵阅 = 右栏 tab + WKWebView，§9.1 / §9.5）、
> `docs/design/document-sidebar-modes/design.md`（文档详情的双模式侧栏，本次的样式与交互母本）。
> 唯一入口：macOS 客户端。iOS（sheet 整屏）保持现状。

---

## 一、背景与目标

灵阅详情是一个**右栏浏览器 tab**（`RightSidebarManager.artifactTabPrefix = "artifact:<id>"`，内容由
`ArtifactOpenCoordinator` 换成 `HTMLArtifactView`）。文档详情则走浏览面板内页，左侧有一栏 220 宽的
「本文目录 / 文档列表」侧栏，可以在文档间直接换篇。

灵阅攒成库之后，同一件事变笨了：看一篇 → 想对照另一篇 → 只能关 tab / 回灵阅面板 → 再点开，还要重新找位置。

**目标**：灵阅详情页左侧加一栏「灵阅列表」，点行直接换篇，交互与文档侧栏对齐（当前项高亮、hover、
滚到底续拉、可收起、窄栏自动收），能复用组件的就复用。

**明确不做**：

- **不做「本文目录」档**。灵阅是任意 HTML 且 `javaScriptEnabled = false`：拿不到标题树，也没有
  「滚到某标题」的通道。侧栏只有「列表」一种形态，头部因此**不挂模式菜单**。
- **不做面板内打开**。`html-artifact` §9.1 / §9.5 已拍：产物统一走 tab，聊天卡片与灵阅面板共用同一入口，
  不为灵阅单开一条「面板内详情」。
- **不做原地替换 tab**（用户 2026-10-30 拍板）：点行 = 打开/切到对应 tab（与今天点灵阅卡片完全一致）。
  因此不给 `RightSidebarManager` 加「换 tab 身份」的 API，也不碰 active / 分屏 / 保活那堆状态。
- **iOS 不动**：sheet 是手机宽度，侧栏放不下；与文档侧栏 SM-9 同口径。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| AN-1 | 侧栏长在哪 | ✅ 长在 **tab 内容里**（`ArtifactDetailView` 包住 `HTMLArtifactView`）。文档那种「面板内页 + 侧栏」不适用，理由见 §一 |
| AN-2 | 换篇语义 | ✅ 行点击 → `ArtifactOpenCoordinator.open(id:title:)`：已开着就切过去（同 id 不重建），没开就新开。**tab 会累积**，这是选定的语义（可用于左右对照），也避免新增 tab 身份迁移逻辑 |
| AN-3 | 列表数据源 | ✅ 复用 `ContentView` 里那份 **`ArtifactViewModel`**（照 `DocumentSelectionCoordinator.shared.documentVM` 注入 `ArtifactOpenCoordinator.shared.artifactVM`）。灵阅没有筛选条件，所以不存在文档 SM-1 那种「同筛选结果」的额外要求 |
| AN-4 | 列表没加载过 | ✅ 侧栏 `.task` 里 `if items.isEmpty && !isLoading { await load() }`（`!isLoading` 避免与面板并发双拉）。从聊天卡片直接打开 tab 时列表可能是空的（面板从没进过），补这一刀；已有数据不重拉（不重置面板翻的页）。首次加载完成后补一次定位（见 AN-13） |
| AN-5 | 行内容 | ✅ 两行：标题（空则「未命名页面」，限 1 行）+ 「相对时间 · 摘要」（`formatConversationDate` + 摘要压空白后 `lineLimit(1)`）。用户 2026-10-30 拍板 |
| AN-6 | 当前项不在列表里 | ✅ 顶部**钉一行**（副信息「当前页面」）+ Divider，下接列表。钉行可点，点了等于切回本 tab（幂等，不是空 action 的假按钮）。典型：从聊天打开一篇不在首页的产物 |
| AN-7 | 分页 | ✅ 末行 `onAppear` → `viewModel.loadMore()`（与 `CardGridView` / 文档侧栏同套路）。`shortcut:` 只续拉，不做跳页 |
| AN-8 | 收放 | ✅ `@AppStorage("xhan_artifact_nav_collapsed")` 手动记忆 + 硬地板：详情视图自身宽度 < `Layout.artifactNavHardFloor`（600）时强制收成 32 宽窄条。**手动收起会粘住**；被硬地板强制收起**不写回**存储，所以拖宽即恢复展开（与 `DocumentFullView.tocExpanded` 同口径） |
| AN-9 | 加载中的侧栏 | ✅ 骨架 tab 直接用 `ArtifactDetailView(html: nil)`：侧栏立即可见、可连续点，页面区显示 `ArtifactLoadingView`。拿不到正文时（失败）由 coordinator 照旧关掉 tab |
| AN-10 | 组件复用 | ✅ 从 `DocumentListView` 抽出 `SidebarNavRow`（行）与 `SidebarCollapsedStrip`（窄条）成公共组件，两处共用。模式菜单 / 目录大纲 / 批注面板**不抽**（灵阅无对应物） |
| AN-11 | 刷新 | ✅ 侧栏跟共享 VM 走：浏览面板页头的「刷新」`artifactVM.load()` 会一起更新（同时把列表回到第一页，与文档侧栏同代价） |
| AN-12 | 服务端 | ✅ 零改动。不新增接口、不加字段 |
| AN-13 | 首次定位时机 | ✅ 定位只做一次（`didAutoScroll`）：`onAppear` 时列表已有数据就直接滚；否则等 `items.count` 从 **0 → N** 那一次补上。**不能只挂在 `onAppear`** —— 从聊天卡片首次打开时那一刻列表还是空的，当前行永远不定位（第二轮审查 N1）。续拉 / 面板刷新不触发定位，别抢用户的手动滚动 |
| AN-14 | `artifactVM` 为 nil | ✅ 视为接线 bug，`ArtifactOpenCoordinator.tabContent` 直接 `fatalError`（它与 `rightSidebar` 同一处注入）。不做「没有侧栏的页面」这种半边降级 —— 用户会当成正常、没人会报（第二轮审查 N2） |
| AN-15 | `.task` 被取消 ≠ 加载失败 | ✅ `ArtifactViewModel.load` / `loadMore` 识别 `CancellationError` / `URLError.cancelled`：不写 `errorMessage`、不 toast，`isLoading` 走 `defer` 复位。切 tab / 关面板会取消 `.task`，而这份 VM 与灵阅面板共享 —— 原先取消信息会直接把面板打成「加载失败 + 重试」（第三轮审查 T3） |

---

## 三、交互设计

```
┌── 侧栏（220） ──────────┬── 页面（WKWebView） ──┐
│ 灵阅列表 · 26 篇    ‹   │                      │
│ ─────────────────────── │                      │
│ 今天的手账              │   （HTML 产物）       │
│ 昨天 21:30 · 这一天…  ✓ │                      │
│ 产品对比报告            │                      │
│ 3 天前 · 三款产品…      │                      │
└─────────────────────────┴──────────────────────┘
        ↑ 收起后变 32 宽窄条（chevron.right），点回展开
```

| 状态 | 表现 |
|---|---|
| 当前页在 `items` 里 | 该行 accent 底色 + 加粗 + 勾；进入时 `withAnimation(.easeInOut(0.2))` 把当前项滚到视口上方 ~12%（与文档侧栏同值） |
| 当前页**不在** `items` 里 | 顶部钉一行（同样式；点了切回本 tab，无副作用）+ Divider，下面才是列表 |
| `items` 为空 | 空态「暂无内容」 |
| 点行 | `ArtifactOpenCoordinator.open(id:title:)` → 已开该 tab 则切过去；否则新开 tab（骨架带侧栏） |
| 点「当前页」那一行 | 同一 id → 只是切回本 tab，无副作用 |
| 详情视图宽 < 600 | 强制收成窄条（不写回 `@AppStorage`），拖宽恢复展开；手动收起则拖宽也仍收起 |

---

## 四、实现要点

1. **`Shared/Views/SidebarNavRow.swift`（新）**：从 `DocumentListView.documentNavRow` / `tocCollapsedStrip`
   抽出 `SidebarNavRow`（title / subtitle / isCurrent / isHovered / action / onHover）与
   `SidebarCollapsedStrip`（help / action，宽取 `Layout.docPanelStripWidth`）。整文件 `#if os(macOS)`。
   `DocumentListView` 改调这两个，`hoveredDocID` / `documentNavSubtitle` 留在原处；`tocSidebar` 的裸值
   `220` 换成 `Layout.docTocExpandedWidth`。
2. **`Shared/Views/ArtifactDetailView.swift`（新）**：`HStack { 侧栏 | Divider | 页面区 }`；`html: String?`
   为 nil 时页面区给 `ArtifactLoadingView`。`viewModel` 是**非可选** `@ObservedObject`，由
   `ArtifactOpenCoordinator.tabContent` 传入必需依赖（缺失直接 `fatalError`，见 AN-14，**没有降级路径**）。
   侧栏列铺 `.regularMaterial` 纸底（右栏本身没有卡片底；用材质而不是 `Color.white`，
   深色模式下 `.primary` 文字才读得出来）；`.ignoresSafeArea()` 只加在**页面列**上，别把侧栏也拽出安全区。
3. **`ArtifactOpenCoordinator`**：加 `weak var artifactVM: ArtifactViewModel?`；骨架与最终内容都走
   `tabContent` → `ArtifactDetailView`，VM 缺失直接 `fatalError`（必需依赖，见 AN-14）。iOS 分支、
   失败关 tab、分享 / 下载 / 导出 PDF 全部不动。
4. **`ContentView.onAppear`**：`ArtifactOpenCoordinator.shared.artifactVM = artifactVM` 一行。
5. **`ArtifactViewModel`**：`total` 由 `private` 改 `private(set)`（「· N 篇」用）。
6. **`Layout`**：加 `artifactNavHardFloor = docTocExpandedWidth + rightSidebarMinWidth`（右栏最小宽 424 后 = **644**；
   与右栏最小宽同源，别写死数字 —— 2026-11-18 右栏最小宽从 380 抬到 424，见 `../mac-topbar-degrade/design.md`）。
7. **`HTMLArtifactView` / `ArtifactLoadingView`**：删掉各自的 `.ignoresSafeArea()`，改由调用方负责
   （macOS 在页面列、iOS 在 `ChatView` 的 sheet 补一个），行为不变。

---

## 五、边界与已知取舍

- **每篇一个 tab，tab 内各有一份侧栏**：连点几篇会堆 tab（用户选定），且每个 tab 的页面都少 220 宽（可收）。
- **共享 VM 的代价**：浏览面板页头「刷新」/ 面板重新进入时的 `load()` 会把侧栏列表重置回第一页 —— 与文档
  侧栏共用 `DocumentViewModel.documents` 是同一个代价，不额外处理。
- **换 tab 后侧栏滚动位置**：每个 tab 是独立视图实例，重新显示时会再次 `scrollTo(当前项, y: 0.12)`；
  `LazyVStack` 对未物化行的 `scrollTo` 可能静默不生效 —— 与 `document-sidebar-modes` §五那条**同一个老问题**，
  退化方案同样是「钉行」，本次不预置。
- **改版（`html.create` 带 `artifact_id`）**：coordinator 的 `reloadIfOpen` 换掉 content 后，`ArtifactDetailView`
  在同一个 `.id(tab.id)` 下原地更新，侧栏状态（收起、hover）不丢；正文换了 `HTMLArtifactView` 自己重载。
- **窄栏下页面可读性**：`injectingBaseline` 的正文列是 `min(760px, 100%)`，620 宽内正常收缩，无需另调。
- **深色模式的并排观感**：侧栏是 `.regularMaterial`（深色下偏暗），紧邻固定纯白页面，比改前整幅白更"跳"；文字可读，真机看一眼即可。**不要为此改 `HTMLArtifactView` 的白底** —— 产物 HTML 按浅色排版，白底是既定取舍（`docs/bugs/030` 那条"别让页面压在照片底图上"）。若真觉得跳，最小改法是把侧栏也切成固定浅色（`Color.white` + `environment(\.colorScheme, .light)`），代价是深色下侧栏不再跟随系统。

---

## 六、验证

- `xcrun swiftc -typecheck` 两批（macOS / iOS；iOS 也要过 —— coordinator 与 `HTMLArtifactView` 在共享 target）。
- 真机（用户 Xcode 复核）：
  - 灵阅面板点卡 → 右栏 tab 左侧出现侧栏，当前项高亮并停在上方约 12%；
  - 点另一篇 → 新开 tab、新侧栏高亮；再点回第一篇 → 切回旧 tab（不重复开）；
  - 从聊天卡片直接打开 **一篇不在首页、且靠后**的产物 → 侧栏列表加载完成后，当前行被定位到视口（不留在顶部）；
  - 首次打开一篇（列表还没加载完）就立刻切到别的 tab → 灵阅面板**不应**出现「加载失败 + 重试」（取消不算失败）；
  - 右栏拖窄到 < 600 → 自动收成窄条；拖宽恢复展开（硬地板收起不写回存储）；手动收起后拖宽仍收起；窄条点击展开、重启后收起态保留；
  - 滚到底自动加载第 31 条；当前页不在首页时顶部有钉行；
  - 顶栏出现「下载 / 分享」胶囊（2026-10-30 起；原来的右上角浮钮已撤，见 `artifact-topbar-actions`），页面白底、无横向滚动；让 agent 改版同一页 → 内容换新版、侧栏不重置。
- 服务端无改动，无需起后端。

---

## 后续变更

- **2026-10-30 详情页分享改走窗口顶栏**：右上角浮动分享钮撤掉，详情页顶栏同位置出现「下载 / 分享」胶囊组（`ArtifactOpenCoordinator.ensureRegisteredGroup`）；撤销分享迁到灵阅卡片 `⋯`。详见 `docs/design/artifact-topbar-actions/design.md`。影响文件：`Services/ArtifactOpenCoordinator.swift`、`Views/HTMLArtifactView.swift`、`Views/ArtifactListView.swift`、`Views/ContentView.swift`。
