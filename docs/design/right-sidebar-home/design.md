# 右栏首页：今日条 + 插画入口卡 + 最近页脚

> 状态：已实现（2026-11-12）
> 样张：`mockup.html`（控制条可切 2/3 列、大/中/小、840/560/380 宽、深浅色、最近开关、卡片顶对齐/居中）

估算（开工）：4 面 / 2 缝 / 2 次真机往返 / 5 个待拍决定 → 5–8h
估算（定稿）：5 面 / 2 缝 / 2 次真机往返 / 0 个待拍决定 → 6–8h
（面：首页视图、状态加载器、设置三入口、6 张插图资产、设计文档；缝：设置窗→右栏的跨窗打开、380 窄宽降级）

## 1. 背景

右栏在「没有网页 tab、也没选面板」时显示 `BrowsePanelView.grid`：3 列 × 2 行的同权 icon 方块，用 `frame(minHeight:)` 撑满后垂直居中。

问题不在留白，而在信息为零：

- 九个入口视觉权重完全一致，没有主次；
- 每格是永不变的静态文案，看不到任何「量」；
- 居中让方块飘在大片空地里，像没加载完。

九个入口里有三个（图片 / 智能体 / 监控看板）并非日常高频，混在同一权重里既挤又误导。

## 2. 目标 / 非目标

**目标**

1. 首页变成「今天 + 六个常用入口 + 最近三件事」，每个入口带一行会变的数字；
2. 图片 / 智能体 / 监控看板移出首页，改从设置进入；
3. 不新增后端接口。

**非目标**：真看板、可排序/可自定义入口、后端聚合接口、iOS（右栏是 macOS 独有）。

## 3. 信息结构

```
下午好                                    11月11日 周二
待办 3 · 事件 6 · 本月支出 ¥1,284
┌───────────────┬───────────────┐
│ [插画] 打开浏览器 │ [插画] 账单      │
│        3 个标签  │        本月 ¥1,284│
│        上次停在… │        32 笔     │
├───────────────┼───────────────┤
│ [插画] 记忆      │ [插画] 灵阅      │
├───────────────┼───────────────┤
│ [插画] 文档      │ [插画] 事件      │
└───────────────┴───────────────┘
（中间留空，有意）
────────────────────────────────  ← 0.5pt 分隔线
最近
[记忆质量… ] [Q4 支出对比报告] [macOS 卡片… ]
[文档·昨天22:14] [灵阅·昨天18:02] [文档·11月9日]
```

三段：

| 段 | 内容 | 布局 |
|---|---|---|
| 问候行 | 时段问候 + 日期；第二行三个数字（待办 / 事件 / 本月支出） | 贴顶 |
| 六张卡 | 插画 + 标题 + 主数字 + 副文案 | 2 列 × 3 行，**顶对齐** |
| 最近页脚 | 3 条跨类型最近条目 | `margin-top: auto` 贴底，上方 0.5pt 分隔线 |

对齐口径：卡片顶对齐（用户拍板），中间留空是刻意的；不做垂直居中。

## 4. 视觉规格

全部取现有 token（`client/xHelper/xHelperMac/Assets.xcassets`），不自造色相。

| 元素 | 规格 |
|---|---|
| 页面内边距 | 上 18 / 左右 26 / 下 22 |
| 问候 | 19pt，weight 650；日期 12pt secondary |
| 今日行 | 12.5pt secondary，数字用 `assistantText` 600 加重；分隔符 `·` 35% 透明 |
| 卡片 | 高 132，圆角 14，间距 12；底 `Color.white 44%`（深色 7%）；描边 `.5pt` 白 66%（深色 10%） |
| 卡片悬停 | 底提亮到 66%（深色 13%），`translateY(-1.5)`，外阴影 `black 10% / 6pt` |
| 插画区 | **正方形，边长 = 卡片高**，右侧 0.5pt 极淡分隔；每卡一层 12–16% 的渐变底 |
| 标题 | 13.5pt / 600 |
| 主数字 | 12.5pt / 560，`appAccent`，等宽数字（tabular） |
| 副文案 | 11pt / tertiary，单行截断 |
| 右箭头 | 7pt 折角线，tertiary 45%，距右 14 |
| 最近页脚 | 顶部 0.5pt `assistantText 12%` 线 + 18pt 间距；条目高约 50，圆角 11，标题 12pt / 540，第二行 10.5pt tertiary |
| 最近列数 | 与卡片一致：2 列卡片 → 3 列条目；1 列卡片 → 1 列条目 |

断点：右栏内容宽 ≥ 620 → 2 列卡片；< 620 → 1 列卡片（卡片高降到 104）；整页可滚动（沿用现在的 `ScrollView` 兜底）。

## 5. 六张插画

`mockup.html` 里就是最终矢量源（140×120 viewBox，同一套色：appAccent `#2F7D63` / 薄荷 `#92C8B4` / 天青 `#41C9E2` / 米白 `#F7EEDD`），落实现时逐张导出成 SVG 放进 `Assets.xcassets`（single scale + preserve vector），SwiftUI 用 `Image("sidebar_<key>")`。

| 入口 | 画面 | 资产名 |
|---|---|---|
| 打开浏览器 | 微倾斜窗口 + 三颗灯点 + 地球经纬 + 右下光标 | `sidebar_browser` |
| 账单 | 撕口小票 + 条目行 + ¥ 圆徽 + 三枚硬币 | `sidebar_bills` |
| 记忆 | 三张错位卡片 + 节点连线 + 闪光 | `sidebar_memory` |
| 灵阅 | 三页扇形叠放，首页是三根柱状图 + 闪光 | `sidebar_artifact` |
| 文档 | 一页纸 + 折角书签 + 波浪下划线（呼应划线批注） | `sidebar_document` |
| 事件 | 虚线时间轴三节点 + 三层叠加卡 + 绿勾徽章 | `sidebar_events` |

注意：插画里纸张是白底，深色模式下会偏亮（样张里可见）。实现时先按同一份资产上深色，**真机截图确认**是否要单独出一版深色资产，不要凭空调色。

## 6. 数据

六张卡 + 今日行 + 最近页脚的数据**全部来自已有接口**，一次并发 6 个轻请求（`limit=1/3`，各 10–20ms）。缓存 60 秒，不重复拉。

| 用途 | 接口 | 取用字段 |
|---|---|---|
| 待办 | `GET /api/reminders?status=pending&limit=1` | `total` |
| 事件 | `GET /api/event-threads?limit=1` | `total`、首条 `updated_at` |
| 本月支出 | `GET /api/bills/stats?start_date=<当月1日>&end_date=<今天>` | `sum`、`count` |
| 记忆 | `GET /api/memories?limit=1&offset=0` | `total`、首条 `created_at` |
| 文档 | `GET /api/documents?limit=3&offset=0` | `total`、前 3 条（`updated_at` 倒序） |
| 灵阅 | `GET /api/artifacts?limit=3&offset=0` | `total`、前 3 条（`created_at` 倒序） |
| 浏览器标签 | 本地 `RightSidebarManager.tabs.count` | 零请求 |

文案口径（**不许编造接口拿不到的字段**）：

- 事件接口没有状态字段 → 写「N 个事件」，不写「进行中」；
- 记忆接口没有「本周新增」→ 写「最近 6 小时前」（取最新一条 `created_at`）；
- 账单写「本月 ¥1,284 / 32 笔」，金额千分位、不保留小数；≥ 100,000 显示 `12.8 万`；
- 浏览器卡第三行「上次停在 X」取 `HistoryStore.shared.entries.first`，为空就整行不渲染；
- 最近页脚：把文档前 3 条（`updated_at`）与灵阅前 3 条（`created_at`）合并按时间倒序取 3 条，第二行写「文档 / 灵阅 · 相对时间」。

加载与降级：

- 一个小 `SidebarHomeViewModel: ObservableObject`（`@MainActor`，右栏级持有），`async let` 并发六路；单飞（进行中不重入）；
- 失败**不打扰用户**：该数字显示 `—`，`LogBuffer.shared.diag` 记一次，其余照常显示；
- 刷新时机：首页出现（`browsePanelSelection == nil` 且右栏展开）、从其它面板返回首页、右栏重新展开；60 秒内不重复。

## 7. 三个面板搬进设置

`BrowsePanel` 枚举**保留全部 case**（`DocumentSelectionCoordinator`、顶栏 ⌘1/⌘2/⌘3 深链都依赖），只新增 `static let homePanels: [BrowsePanel]` 供首页渲染，图片 / 智能体 / 监控看板三项从首页移除。

设置窗（`MacSettingsView`）新增一节「内容与工具」，三行按钮：

```
内容与工具
  图片库      预览与清理已上传的图    打开 ›
  智能体      管理自定义智能体        打开 ›
  监控看板    成本与调用统计          打开 ›
```

动作：`BrowserToolProvider.shared.rightSidebar` 已是全局可取（`ContentView.onAppear` 注入，见 `Services/ArtifactOpenCoordinator.swift` 同款用法）→ `isOpen = true` + `showBrowsePanel()` + `browsePanelSelection = .images/.agents/.dashboard`，再 `AppDelegate.primaryWindow?.makeKeyAndOrderFront(nil)` 把主窗带到前面；设置窗不关。

顶栏的 ⌘3 监控按钮**保留**（它是深链入口，不是首页入口）。

## 8. 代码改动

| 文件 | 改动 |
|---|---|
| `client/xHelper/xHelper/Shared/Views/BrowsePanelView.swift` | `grid` → 三段式首页；删除 `adaptiveColumns`；新增问候行、`HomeEntryCard`、最近页脚；`homePanels` |
| `client/xHelper/xHelper/Shared/ViewModels/SidebarHomeViewModel.swift`（新） | 6 路并发拉取、60s 缓存、单飞、失败降级、取消不覆写旧值 |
| `client/xHelper/xHelper/Shared/Views/SidebarHomeFormat.swift`（新，纯逻辑） | 金额格式化、相对时间、「当月 1 日」计算、问候 |
| `client/xHelper/xHelper/Shared/Views/BookmarkPanelView.swift`、`HistoryPanelView.swift` | 删掉各自私有的 `relativeTime`，改用公共实现（避免第三份） |
| `client/xHelper/xHelperMac/Assets.xcassets/sidebar_*.imageset`（6 个） | 插画资产（SVG 单尺度矢量） |
| `client/xHelper/xHelperMac/App.swift` | `MacSettingsView` 增加「内容与工具」一节 + `openRightPanel` |
| `client/xHelper/xHelper/Shared/Views/ContentView.swift`、`RightSidebarView.swift` | 统一持有 `homeVM` 并逐层注入（缓存不随首页摘除而丢） |
| `client/xHelper/Package.swift` + `Tests/SidebarHomeCoreTests/` | 纯逻辑 target：格式化 / 当月起始 / 相对时间边界（今天、昨天、跨月） |

## 9. 测试与验收

纯逻辑（`swift test`）：

- 金额：`0`、`999`、`1284` → `¥1,284`、`128400` → `12.8 万`；
- 相对时间：刚刚 / N 分钟前 / N 小时前 / 昨天 / N 天前 / 跨年；
- 「当月 1 日」在 1 月、12 月、跨年边界的取值。

真机（Xcode 构建后，`swiftc -typecheck` 只算编译自检）：

1. 840 宽：2 列卡片 + 3 条最近页脚贴底，滚动条不出现；
2. 拖到 560 / 380 宽：降成 1 列，不横向溢出；
3. 深色：卡片描边/阴影、插画白纸亮度是否可接受；
4. 断网（关本地后端或改错 token）：六个数字显示 `—`，页面不报错、不弹窗；
5. 点「设置 → 图片库」：主窗前置并展开右栏到图片面板；
6. 从任意面板返回：首页数字刷新一次（60s 内不重复请求，看日志确认）。

## 10. 不做的事

- 不加 `/api/sidebar/summary` 之类的聚合接口（6 个已有接口够用，多一层契约不划算）；
- 不做卡片拖拽排序 / 自定义首页；
- 不做「最近对话」；
- 不在首页放真图表或事件进度条；
- iOS 不动（右栏为 macOS 独有）。

## 11. 后续变更

- 2026-11-12 实现落地。与定稿的三处偏离：
  1. 首页数字的 store 挂在 `ContentView`（`@StateObject`）→ `RightSidebarView` → `BrowsePanelView` 逐层注入，而不是首页自己持有 —— 首页在开浏览器 tab 时会被摘除，自持会让 60s 缓存随之销毁。影响：`ContentView.swift`、`RightSidebarView.swift`、`BrowsePanelView.swift`。
  2. `RecentItem` 的「文档 / 灵阅」用枚举而非展示文案判断点开链路（避免改文案后静默点错）。影响：`SidebarHomeViewModel.swift`、`BrowsePanelView.swift`。
  3. 相对时间的口径补了一条：6 小时内一律按小时（23:55 与 00:10 只差 15 分钟，说「昨天」更糊涂），超过 6 小时才按日历天说「昨天」。影响：`SidebarHomeFormat.swift`、`Tests/SidebarHomeCoreTests`。
- 深色插画暂用同一份资产（真机截图后再定是否出深色专用版）。
