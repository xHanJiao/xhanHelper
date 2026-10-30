# 灵阅详情接入顶栏胶囊组（下载 / 分享）

> 2026-10-30 · 状态：已定稿

## 1. 背景

文档详情在窗口顶栏有一组 `document-actions` 胶囊（下载 `.md` / 导出 PDF / 分享链接），由 `DocumentSelectionCoordinator` 注册、`visible` 判「右栏正显示文档详情」。

灵阅详情没有顶栏组：分享只能点页面右上角一颗浮动的分享圆钮（菜单里带「撤销分享」），下载完全没有入口。两个详情页的顶栏形态不一致，下载/分享在灵阅里不好找。

## 2. 目标

1. 灵阅详情在**同一位置**出现胶囊组：**下载** + **分享**（明确不要导出 PDF）。
2. 撤掉灵阅详情右上角的浮动分享圆钮，避免同一动作两个入口。
3. 「撤销分享」不丢，挪到灵阅卡片的 `⋯` 菜单。
4. 文档的「导出 PDF」两个入口（顶栏胶囊、卡片 `⋯` 的 dev 专属项）一起下架；实现保留，恢复时只需加回按钮。

## 3. 方案

### 3.1 灵阅顶栏组

在 `ArtifactOpenCoordinator` 里加 `ensureRegisteredGroup()`，照文档组同一套契约（`TopBarRegistry`「注册一次 + visible 闭包」，状态由顶栏已观察的 `RightSidebarManager` 驱动，不需要手动 `refresh()`）：

| 项 | 取值 |
|----|------|
| 组 id / 槽 / 优先级 | `artifact-actions` / `.center` / `8`（与文档组同级，二者 `visible` 互斥） |
| `autoOverflow` | `true`（放不下时留一部分 + `⋯`，与文档组同口径） |
| 下载 | `arrow.down.circle` → `downloadHTML(id:title:)`（`help`：下载页面 (.html)） |
| 分享 | `link` → `copyShareLink(id:)`（图标与文档组一致） |
| `visible` | 右栏打开且 `activeTabId` 带 `artifact:` 前缀 |
| id / 标题来源 | 当前 active tab：id = 去掉 `artifact:` 前缀，标题 = `tab.title`（正文加载后已由 `updateTabTitle` 写成真实标题） |

注册点是 `ContentView.onAppear`，紧跟 `ArtifactOpenCoordinator.shared.configure(...)`（与文档组注册同一处）。整个方法用 `#if os(macOS)`（顶栏只在 macOS）。

**必须同时修浏览器组**：灵阅 tab 和网页 tab 共用 `activeTabId`，而 `browser-actions`（priority 10）的 `visible` 只判 `activeTabId != nil`，会以更高优先级顶掉灵阅组（priority 8），新按钮根本不会显示。给浏览器组的 `visible` 加上 `!RightSidebarManager.isArtifactTab(activeTabId)` —— 与该文件自己的注释「visible = 右栏正显示浏览器 tab」一致，顺带修掉产物页上误显示浏览器工具组的老问题。

`copyShareLink` 原有的重入保护在视图里（`isSharing`），浮钮删除后把保护移进 coordinator —— 改成按 id 的 `sharingIDs` 集合，不同条目的分享互不阻塞（全局一个 Bool 会让「分享 A 未返回时点 B」静默无响应）。

### 3.2 撤销分享

- 新增 `ArtifactOpenCoordinator.revokeShare(id:)`：调 `unshare` 并把「已撤销 / 本来没分享 / 失败」三种结果 toast 出来（原来散在视图菜单里的 switch 收进来）；同样按 id 判重，连点不会发两次 DELETE。
- `ArtifactListView` 卡片 `⋯` 在「分享链接」后加「撤销分享」（`xmark.circle`，`role: .normal` —— 卡片菜单只在第一颗 destructive 前插分隔线，删除才是那颗；`link.badge.minus` 在 SF Symbols 里不存在）。
- 这条推翻了 `card-more-menu` 早先「撤销分享不进卡片菜单」的决定，理由：浮钮撤掉后卡片菜单是它唯一的家，且比留在详情页更顺手。

### 3.3 撤掉浮球

`HTMLArtifactView` 删除 `isSharing` / `shareButton` / `copyShareLink` / 右上角 overlay；`id` / `title` 失去唯一读取方后一并从参数里删掉（3 处调用点同步）。页面回归纯 WKWebView。

### 3.4 文档导出 PDF 下架

- `DocumentSelectionCoordinator` 的组里删掉 `document-export-pdf` 按钮。
- `DocumentListView.documentMenuActions` 删掉 `if LocalPaths.isDev` 的「导出 PDF」分支。
- `exportPDF(_:)`（coordinator）与 `DocumentPDFExporter.export(_:to:)` 保留不删：这是「先下掉」不是「不要了」，恢复时只加回按钮；`DocumentPDFExporter` 的 `exportHTML` 仍被灵阅导出 PDF 使用（卡片 `⋯` 里 dev 专属，未动）。

## 4. 不做什么

- 不给灵阅顶栏加导出 PDF。
- 不改 iOS：顶栏是 macOS-only，浮钮原本也是 `#if os(macOS)`，iOS 灵阅 sheet 行为不变。
- 不做分屏特殊判断：按「引擎 active tab」判可见（与浏览器组同口径）。两个方向都有已知缺口：① 灵阅在左窗格、右窗格是别的 tab → 顶栏不显示这组；② 灵阅被拖到右窗格并成为 active、左窗格是网页 → 浏览器组被排除，屏上有网页却没有浏览器工具组（工具在面板里另有入口）。要精确到窗格另开需求。

## 5. 影响文件

- `client/xHelper/xHelper/Shared/Services/ArtifactOpenCoordinator.swift`（+`ensureRegisteredGroup` / `currentArtifact` / `isArtifactDetailVisible` / `revokeShare`，`copyShareLink` 加重入保护）
- `client/xHelper/xHelper/Shared/Views/ContentView.swift`（+ 注册调用）
- `client/xHelper/xHelper/Shared/Services/DocumentSelectionCoordinator.swift`（−导出 PDF 按钮）
- `client/xHelper/xHelper/Shared/Views/DocumentListView.swift`（− dev 专属导出 PDF 菜单项）
- `client/xHelper/xHelper/Shared/Views/ArtifactListView.swift`（+撤销分享菜单项）
- `client/xHelper/xHelper/Shared/Views/HTMLArtifactView.swift`（−浮动分享钮，−死参数 `id` / `title`）
- `client/xHelper/xHelper/Shared/Views/ChatView.swift`、`Views/ArtifactDetailView.swift`（调用点同步）
- `client/xHelper/xHelper/Shared/Tools/BrowserToolProvider.swift`（浏览器组 `visible` 排除灵阅 tab，否则 priority 10 会顶掉灵阅组）

文档同步：`docs/design/html-artifact/design.md`、`docs/design/card-more-menu/design.md`、`docs/design/document-highlight-annotation/design.md`（D20）、`docs/design/mac-two-column-layout/design.md`（已注册组表）、`docs/design/artifact-nav-sidebar/design.md`（自查项）。

## 6. 验收

- `xcrun swiftc -typecheck` macOS 26.3 + iOS 26.0 双 target 0 error。
- 打开灵阅详情 → 顶栏出现「下载 / 分享」（**不是**浏览器工具组）；下载落 `.html`（Safari 打开观感与右栏一致）；分享复制链接。
- 切到网页 tab → 恢复浏览器工具组；切回灵阅 tab → 又变回下载/分享。
- 切到文档详情 → 组变成「下载 / 分享」两颗（没有 PDF）。
- 切到聊天 / 浏览器 tab / 收起右栏 → 组消失。
- 窄窗 → 组能部分收进 `⋯`。
- 灵阅卡片 `⋯` →「撤销分享」三种结果提示正确；撤销后旧链接 404。
- 灵阅详情右上角不再有浮球，页面内容不被遮挡。
