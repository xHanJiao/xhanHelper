# 右栏首页：今日条 + 插画入口卡（最近页脚已于 2026-11-12 删除）

> 状态：已实现（2026-11-12）
> 样张：`mockup.html`（控制条可切 2/3 列、大/中/小、840/560/380 宽、深浅色、卡片顶对齐/居中）。样张里的「最近」区和它的开关已作废（该区 2026-11-12 删除），只作历史参考。

估算（开工）：4 面 / 2 缝 / 2 次真机往返 / 5 个待拍决定 → 5–8h
估算（定稿）：5 面 / 2 缝 / 2 次真机往返 / 0 个待拍决定 → 6–8h
（面：首页视图、状态加载器、设置三入口、6 张插图资产、设计文档；缝：设置窗→右栏的跨窗打开、380 窄宽降级）

## 1. 背景

右栏在「没有网页 tab、也没选面板」时显示 `BrowsePanelView.grid`：3 列 × 2 行的同权 icon 方块，用 `frame(minHeight:)` 撑满后垂直居中。

问题不在留白，而在信息为零：

- 九个入口视觉权重完全一致，没有主次；
- 每格是永不变的静态文案，看不到任何「量」；
- 居中让方块飘在大片空地里，像没加载完。

九个入口里有四个（记忆 / 图片 / 智能体 / 监控看板）并非日常高频，混在同一权重里既挤又误导。

## 2. 目标 / 非目标

**目标**

1. 首页变成「今天 + 六个常用入口 + 最近三件事」，每个入口带一行会变的数字；
2. 记忆 / 图片 / 智能体 / 监控看板移出首页，改从设置进入（记忆的位置换成只读的「备忘」）；
3. 不新增后端接口。

**非目标**：真看板、可排序/可自定义入口、后端聚合接口、iOS（右栏是 macOS 独有）。

## 3. 信息结构

```
下午好                                    11月11日 周二
备忘 3 · 事件 6 · 本月支出 ¥1,284
┌───────────────┬───────────────┐
│ [插画] 浏览器     │ [插画] 账单      │
│        3 个标签  │        本月 ¥1,284│
│        （信息行）│        32 笔     │
├───────────────┼───────────────┤
│ [插画] 备忘      │ [插画] 灵阅      │
├───────────────┼───────────────┤
│ [插画] 文档      │ [插画] 事件      │
└───────────────┴───────────────┘
（中间留空，有意）
```

> 底部「最近」区已于 2026-11-12 删除（宽卡信息行已列出最近对象），上图不再画它。

三段：

| 段 | 内容 | 布局 |
|---|---|---|
| 问候行 | 时段问候；第二行三个数字（备忘 / 事件 / 本月支出），整行可点进备忘（右上日期已删，2026-11-13） | 贴顶 |
| 六张卡 | 插画 + 标题 + 主数字 + 副文案（四档见 `design-cards-v2.md`；信息区有行时副文案不渲染） | 2 列 × 3 行，**顶对齐** |
| ~~最近页脚~~ | 已删除（2026-11-12）：宽卡信息行已给出最近对象 | — |

对齐口径：卡片顶对齐（用户拍板），中间留空是刻意的；不做垂直居中。

## 4. 视觉规格

> 卡片规格已被 `design-cards-v2.md`（四档信息密度 + 信息行）取代，本节仅保留问候行与配色口径。

全部取现有 token（`client/xHelper/xHelperMac/Assets.xcassets`），不自造色相。

| 元素 | 规格 |
|---|---|
| 页面内边距 | 上 18 / 左右 26 / 下 22 |
| 问候 | 23pt，weight 650；**不显示日期**（2026-11-13 删除：卡里每行都自带相对时间，右上再压一个绝对日期只是噪音） |
| 今日行 | 13pt secondary，数字用 `assistantText` 600 加重；分隔符 `·` 35% 透明；与问候行间距 8 |
| 卡片 | 高 164（信息·紧凑信息档）/ 132（三行档）/ `max(110, min(卡宽, 170))`（图标档），圆角 14，间距 12；底 `Color.white 44%`（深色 7%）；描边 `.5pt` 白 66%（深色 10%） |
| 卡片悬停 | 底提亮到 66%（深色 13%），`translateY(-1.5)`，外阴影 `black 10% / 6pt` |
| 插画区 | 信息档宽 116；紧凑信息档 `min(116, 0.42 × 卡宽)`；三行档宽 = `min(卡高, 0.42 × 卡宽)`（440 以上即正方形）；图标档填满列宽（高封顶 170）；右侧 0.5pt 极淡分隔；每卡一层 12–16% 的渐变底 |
| 标题 | 13.5pt / 600 |
| 主数字 | 12.5pt / 560，`appAccent`，等宽数字（tabular） |
| 副文案 | 11pt / tertiary，单行截断 |
| 右箭头 | 7pt 折角线，tertiary 45%，距右 14 |
| ~~最近页脚~~ | 已删除（2026-11-12），样式作废 |
| ~~最近列数~~ | 随页脚一同作废（2026-11-12） |

断点：**永远 2 列卡片**（2026-11-12 起，卡宽 =（内容宽 − 12）/ 2，窄了只降档位，不再退单列）；内容块封顶 1320 居中。档位见 `design-cards-v2.md` §3；整页可滚动（沿用现在的 `ScrollView` 兜底）。

## 5. 六张插画

`mockup.html` 里就是最终矢量源（140×120 viewBox，同一套色：appAccent `#2F7D63` / 薄荷 `#92C8B4` / 天青 `#41C9E2` / 米白 `#F7EEDD`），落实现时逐张导出成 SVG 放进 `Assets.xcassets`（single scale + preserve vector），SwiftUI 用 `Image("sidebar_<key>")`。

| 入口 | 画面 | 资产名 |
|---|---|---|
| 浏览器 | 微倾斜窗口 + 三颗灯点 + 地球经纬 + 右下光标 | `sidebar_browser` |
| 账单 | 撕口小票 + 条目行 + ¥ 圆徽 + 三枚硬币 | `sidebar_bills` |
| 备忘 | 横线本 + 勾选行 + 天青时钟角标 | `sidebar_reminder` |
| 记忆 | 三张错位卡片 + 节点连线 + 闪光（已移出首页，资产保留待回） | `sidebar_memory` |
| 灵阅 | 三页扇形叠放，首页是三根柱状图 + 闪光 | `sidebar_artifact` |
| 文档 | 一页纸 + 折角书签 + 波浪下划线（呼应划线批注） | `sidebar_document` |
| 事件 | 虚线时间轴三节点 + 三层叠加卡 + 绿勾徽章 | `sidebar_events` |

注意：插画里纸张是白底，深色模式下会偏亮（样张里可见）。实现时先按同一份资产上深色，**真机截图确认**是否要单独出一版深色资产，不要凭空调色。

## 6. 数据

六张卡 + 今日行的数据**全部来自已有接口**，一次并发 6 个轻请求（`limit=3/3/50`，各 10–20ms）。缓存 60 秒，不重复拉。

| 用途 | 接口 | 取用字段 |
|---|---|---|
| 备忘 | `GET /api/reminders?status=pending&limit=50` | `total`、全部 items（`deadline` 客户端升序） |
| 事件 | `GET /api/event-threads?limit=3` | `total`、首条 `updated_at`、前 3 条（信息行） |
| 本月支出 | `GET /api/bills/stats?start_date=<当月1日>&end_date=<今天>` | `sum`、`count` |
| 最近账单 | `GET /api/bills?limit=3` | 前 3 条（`item/amount/category1/date`，信息行） |
| 文档 | `GET /api/documents?limit=3&offset=0` | `total`、前 3 条（`updated_at` 倒序） |
| 灵阅 | `GET /api/artifacts?limit=3&offset=0` | `total`、前 3 条（`created_at` 倒序） |
| 浏览器标签 | 本地 `RightSidebarManager.tabs` | 零请求 |

备忘那一路**一次取 50 条，首页卡与备忘面板共用**：首页只用 `total` + 第一条未过期的 `deadline`，面板直接渲染同一份数组（不再单开一路、不新增接口）。

文案口径（**不许编造接口拿不到的字段**）：

- 事件接口没有状态字段 → 写「N 个事件」，不写「进行中」；
- 备忘卡写「N 条备忘 / 下一条 今天 18:00」；全都过期时副文案改「N 条已过期」；
- 备忘面板只读：标题 = 提醒内容、页脚 = 到期时间（`今天 18:00` / `明天 09:00` / `11月20日 09:00` / `昨天 05:43`），
  已过期的卡在左下角加橙色「已过期」胶囊。不提供完成/取消（`ack` 会往来源对话插触发消息，仍只走通知）；
- 账单写「本月 ¥1,284 / 32 笔」，金额千分位、不保留小数；≥ 100,000 显示 `12.8 万`；
- 浏览器卡的信息行：浏览器打开时 = 当前网页标签前 3 个（点行切标签），未打开时 = `HistoryStore.entries` 最新 3 条（点行新开标签）；「N 个标签」只数网页标签。卡头「上次停在 X」已于 2026-11-13 删除（见 `design-cards-v2.md` §4）；
- ~~最近页脚~~：已删除（2026-11-12）。文档/灵阅的前 3 条改为在宽卡的信息行里直接列出，不再单独拼一个「最近」区。

加载与降级：

- 一个小 `SidebarHomeViewModel: ObservableObject`（`@MainActor`，右栏级持有），`async let` 并发六路；单飞（进行中不重入）；
- 失败**不打扰用户**：该数字显示 `—`，`LogBuffer.shared.diag` 记一次，其余照常显示；
- 刷新时机：首页出现（`browsePanelSelection == nil` 且右栏展开）、从其它面板返回首页、右栏重新展开；60 秒内不重复。

## 7. 面板搬进设置

`BrowsePanel` 枚举**保留全部 case**（`DocumentSelectionCoordinator`、顶栏 ⌘1/⌘2/⌘3 深链都依赖），只新增 `static let homePanels: [BrowsePanel]` 供首页渲染，记忆 / 图片 / 智能体 / 监控看板四项从首页移除。

设置窗（`MacSettingsView`）新增一节「内容与工具」，四行按钮：

```
内容与工具
  记忆        长期记忆、主题词云与反馈  打开 ›
  图片库      预览与清理已上传的图      打开 ›
  智能体      管理自定义智能体          打开 ›
  监控看板    成本与调用统计            打开 ›
```

动作：`BrowserToolProvider.shared.rightSidebar` 已是全局可取（`ContentView.onAppear` 注入，见 `Services/ArtifactOpenCoordinator.swift` 同款用法）→ `isOpen = true` + `showBrowsePanel()` + `browsePanelSelection = .memories/.images/.agents/.dashboard`，再 `AppDelegate.primaryWindow?.makeKeyAndOrderFront(nil)` 把主窗带到前面；设置窗不关。

顶栏的 ⌘3 监控按钮**保留**（它是深链入口，不是首页入口）。

## 8. 代码改动

| 文件 | 改动 |
|---|---|
| `client/xHelper/xHelper/Shared/Views/BrowsePanelView.swift` | `grid` → 三段式首页；删除 `adaptiveColumns`；新增问候行、最近页脚；`homePanels`；今日行「备忘」可点；`panelContent(.reminders)` 接只读列表 |
| `client/xHelper/xHelper/Shared/Views/HomeEntryCard.swift`（新） | 首页入口卡从 `BrowsePanelView` 抽出成独立视图（预览靶可渲染） |
| `client/xHelper/xHelper/Shared/Views/ReminderPageView.swift`（新，纯展示） | 备忘面板：`PageContainer` + `CardGridView`（3 列 / 260），只读卡片，过期加橙色胶囊 |
| `client/xHelper/xHelper/Shared/Views/CardStyle.swift` | 新增 `.reminder` 样式（`minHeight` 104，比记忆卡矮一档） |
| `client/xHelper/xHelper/Shared/ViewModels/SidebarHomeViewModel.swift`（新） | 5 路并发拉取、60s 缓存、单飞、失败降级、取消不覆写旧值；换来的 `reminders` 数组供首页卡与备忘面板共用 |
| `client/xHelper/xHelper/Shared/Views/SidebarHomeFormat.swift`（新，纯逻辑） | 金额格式化、相对时间、「当月 1 日」计算、问候 |
| `client/xHelper/xHelper/Shared/Views/BookmarkPanelView.swift`、`HistoryPanelView.swift` | 删掉各自私有的 `relativeTime`，改用公共实现（避免第三份） |
| `client/xHelper/xHelperMac/Assets.xcassets/sidebar_*.imageset`（7 个） | 插画资产（SVG 单尺度矢量），含备忘的 `sidebar_reminder` |
| `client/xHelper/xHelperMac/App.swift` | `MacSettingsView` 增加「内容与工具」一节 + `openRightPanel` |
| `client/xHelper/xHelper/Shared/Views/ContentView.swift`、`RightSidebarView.swift` | 统一持有 `homeVM` 并逐层注入（缓存不随首页摘除而丢） |
| `client/xHelper/Package.swift` + `Tests/SidebarHomeCoreTests/` | 纯逻辑 target：格式化 / 当月起始 / 相对时间边界 / 到期文案与过期判定 |

## 9. 测试与验收

纯逻辑（`swift test`）：

- 金额：`0`、`999`、`1284` → `¥1,284`、`128400` → `12.8 万`；
- 相对时间：刚刚 / N 分钟前 / N 小时前 / 昨天 / N 天前 / 跨年；
- 「当月 1 日」在 1 月、12 月、跨年边界的取值；
- 到期文案：今天/明天/后天/昨天跨天、跨年带年份、非法输入返回 nil；`isOverdue` 边界与非法输入不误报。

真机（Xcode 构建后，`swiftc -typecheck` 只算编译自检）：

1. 840 宽：2 列卡片、无底部「最近」区，滚动条不出现；
2. 拖到 560 / 380 宽：降成 1 列，不横向溢出；
3. 深色：卡片描边/阴影、插画白纸亮度是否可接受；
4. 断网（关本地后端或改错 token）：各数字显示 `—`，页面不报错、不弹窗；备忘面板显示「加载失败，点刷新重试」而不是空列表；
5. 点「设置 → 图片库」：主窗前置并展开右栏到图片面板；
6. 从任意面板返回：首页数字刷新一次（60s 内不重复请求，看日志确认）。

## 10. 不做的事

- 不加 `/api/sidebar/summary` 之类的聚合接口（6 个已有接口够用，多一层契约不划算）；
- 不做卡片拖拽排序 / 自定义首页；
- 不做「最近对话」；
- 备忘不接 `ack` / 取消：面板纯只读，提醒的闭环仍走系统通知；
- 不在首页放真图表或事件进度条；
- iOS 不动（右栏为 macOS 独有）。

## 11. 后续变更

- 2026-11-12 实现落地。与定稿的三处偏离：
  1. 首页数字的 store 挂在 `ContentView`（`@StateObject`）→ `RightSidebarView` → `BrowsePanelView` 逐层注入，而不是首页自己持有 —— 首页在开浏览器 tab 时会被摘除，自持会让 60s 缓存随之销毁。影响：`ContentView.swift`、`RightSidebarView.swift`、`BrowsePanelView.swift`。
  2. `RecentItem` 的「文档 / 灵阅」用枚举而非展示文案判断点开链路（避免改文案后静默点错）。影响：`SidebarHomeViewModel.swift`、`BrowsePanelView.swift`。
  3. 相对时间的口径补了一条：6 小时内一律按小时（23:55 与 00:10 只差 15 分钟，说「昨天」更糊涂），超过 6 小时才按日历天说「昨天」。影响：`SidebarHomeFormat.swift`、`Tests/SidebarHomeCoreTests`。
- 深色插画暂用同一份资产（真机截图后再定是否出深色专用版）。
- 2026-11-12 第二轮改版（用户拍板）：
  1. 首页「记忆」卡换成「备忘」卡（新 `BrowsePanel.reminders`），记忆入口移到设置「内容与工具」第一行；`.memories` case 与顶栏 ⌘ 深链保留。
  2. 新增只读「备忘」面板：卡片式（与记忆同组件），数据与首页卡共用 `SidebarHomeViewModel.reminders`（`limit=50`，`deadline` 升序），不接 `ack`/取消。
  3. 今日行「待办」改名「备忘」并整行可点（直接进备忘面板），消掉「上面有数字、底下没入口」的割裂。
  4. 备忘卡比记忆卡矮一档（`CardStyle.reminder`，`minHeight` 104）；过期靠橙色「已过期」胶囊 + 页脚时间，不靠文案前缀。
  5. 首页请求从 6 路降到 5 路（去掉 memories）。
  影响：`BrowsePanelView.swift`、`SidebarHomeViewModel.swift`、`SidebarHomeFormat.swift`、`CardStyle.swift`、`ReminderPageView.swift`(新)、`HomeEntryCard.swift`(新，从首页视图抽出)、`xHelperMac/App.swift`、`Assets.xcassets/sidebar_reminder`、`Tests/SidebarHomeCoreTests`。
  6. `mockup.html` 仍是第一版（记忆卡 / 「待办」口径）的样张，只作历史参考；以实现与本文 §3/§5 为准。
- 备忘的 `limit=50` 不分页：pending 提醒量小；超过 50 条时按到期排序后仍是最该看的那批。真有量再加分页。
- 2026-11-12 第三轮改版（入口卡 v2，正本见 `design-cards-v2.md`）：
  1. 卡片改四档信息密度（≥560 信息 / 440–560 单行 / 340–440 三行 / <340 纯图标），首页内容块封顶 1320 居中；两列门槛从 620 提到 692（两列都够 340 才分栏，避免 621 宽掉成图标档）。
  2. 信息档直接列出各域最近 3 条可点对象：浏览器标签切标签、文档/灵阅直达、账单/备忘/事件进面板；信息档整卡不再是按钮。
  3. 图标只落在已有字段：浏览器真 favicon（失败 → host 首字）、账单 `category1` 12 项映射、备忘按过期状态、无类型字段的域用标题首字。
  4. 数据从 5 路变更 6 路：新增 `GET /api/bills?limit=3`，事件 `limit=1 → 3`；备忘、文档、灵阅沿用原请求。
  影响：`HomeEntryCard.swift`、`BrowsePanelView.swift`、`SidebarHomeViewModel.swift`、`SidebarHomeFormat.swift`、`Tests/SidebarHomeCoreTests`、`design-cards-v2.md`(新)。
- 2026-11-12 第四轮改版（用户拍板）：
  1. 删除底部「最近」页脚（任何宽度都不再显示）：宽卡信息行已经列出最近对象，非宽档也不再需要它兜底。`SidebarHomeViewModel` 的 `RecentItem` / `Stats.recent` / `mergeRecent` 一并删除（页脚是唯一消费者），`recentDocuments` / `recentArtifacts` 保留给信息行。
  2. 单行档的细节行改成左右结构：标题在左（单行 tail 截断）、时间/金额在右（不参与压缩），不再拼成一串 `标题 · 时间`。
  3. 备忘行右侧时间改用相对口径 `SidebarHomeFormat.dueRelative`（今天 / 明天 / 后天 / N 天后 / 昨天 / N 天前），与文档卡的「3 天前」一致；卡头「下一条」仍用绝对时间 `dueText`。已过期靠行首橙色图标区分，时间文本不改色。
  影响：`BrowsePanelView.swift`、`SidebarHomeViewModel.swift`、`HomeEntryCard.swift`、`SidebarHomeFormat.swift`、`Tests/SidebarHomeCoreTests`。
- 2026-11-12 第五轮（用户拍板）：入口卡标题「打开浏览器」改为「浏览器」；单行档细节行标题限宽 200pt（≈18 字，超出 tail 省略号），右侧时间贴右。影响 `BrowsePanelView.swift`、`HomeEntryCard.swift`。
- 2026-11-12：问候行加 `TimelineView` 每 30 秒重算，问候语与日期不再等到重进右栏才变（原来只靠视图重绘，跨整点/跨天不刷新）。影响 `BrowsePanelView.swift`。
- 2026-11-12：取消「内容宽 < 692 退单列」，首页**永远两列**，档位只看卡宽；单列专属的 104 卡高移除。影响 `SidebarHomeFormat.swift`（`homeMetrics`）+ 单测。
- 2026-11-12：图标档高度封顶 `min(卡宽, 170)`，卡片填满列宽（原来是边长 = 列宽的巨型方块）。
- 2026-11-12：首页档位加高度约束（`homeMetrics(contentWidth:availableHeight:)`）：窗口矮时信息档降单行档，132 是底线、放不下就滚动；底部 `Spacer` 改 0 起。
- 2026-11-12：图标档门槛 340 → 300（卡宽 315 已放得下三行文案，退图标会显得又宽又空）。
- 2026-11-12：reviewer 复核后修正三处——三行档的副文案恢复恒显示（`showsSecondary` 改看 `layout`）、图标档高度加下限 110、问候行 `TimelineView` 改 `.everyMinute`。
- 2026-11-12：图标档门槛 300 → 200，300–200 段改成三行档；插画带宽度改为 `min(卡高, 0.42 × 卡宽)`，窄卡让出文字区。
- 2026-11-12：三行档卡宽 < 300 只显示「标题 / 主数字」两行（`homeSecondaryMinCardWidth`）；卡宽 < 200 退回纯图标卡（文字区不足 50pt，数字会被截断）。
- 2026-11-13：浏览器卡信息行回填（打开时列标签、未打开列历史最新 3 条），「N 个标签」只数网页标签，卡头「上次停在 X」删除。
- 2026-11-13：卡片档位改为 信息（≥560）/ 紧凑信息（340–560，头部一行 + 3 条可点行）/ 三行（200–340）/ 图标（<200），单行灰字档整档删除；信息行标题改 `tail` 截断（留头）。详见 `design-cards-v2.md` §3/§4。
- 2026-11-13：卡高不再随窗口高变 —— 信息档与紧凑信息档统一 164 / 3 行，窗口矮就整页滚动（`homeMetrics` 去掉 `availableHeight` / `homeChromeHeight`）。
- 2026-11-13：问候区放大到 23pt / 13pt / 间距 8（原 19 / 12.5 / 6），日期固定 12pt；问候区抽成 `HomeGreetingView.swift`（尺寸参数化、默认值即线上值），出图脚本渲真视图。影响 `HomeGreetingView.swift`（新）、`BrowsePanelView.swift`、`scripts/snapshot-homecards.swift`。
- 2026-11-13：删掉问候区右上角的日期（`11月13日 周五`）——卡里每行都自带相对时间，右上再压一个绝对日期是冗余；问候语因此独立成一行。
- 2026-11-18：面板数据加 30s 缓存（与 `right-sidebar-unified-tabs` 的面板挂载闸门配套）：`ArtifactViewModel` /
  `BillViewModel` 加 `loadIfStale()`、`DocumentViewModel` 加 `loadCurrentMode(force:)`，面板入口 30s 内重进吃上一次结果；
  时间戳只在请求成功且未被取消时写入，跳过条件带 `errorMessage == nil`（失败/取消不会被缓存 30 秒）。
  页头刷新一律回源（文档 `force: true`，灵阅/账单直走 `load()`），筛选/搜索/翻页也不受缓存影响。
  缓存期内的面板进面板时不等 210ms 挂载闸门（`isFresh`），没有请求要拦就不该白等。
  代价：聊天里新建/修改的账单、文档、灵阅页最多 30s 后才会出现在面板列表里（点页头刷新可立即回源）；
  聊天写入后主动失效缓存暂不做（客户端拿不到「其它会话写了一笔」的信号，除非加推送）。
  首页的 60s 缓存（`SidebarHomeViewModel`）保持原样。
  影响：`ArtifactViewModel.swift`、`BillViewModel.swift`、`DocumentViewModel.swift`、`ArtifactListView.swift`、`BillListView.swift`、`BrowsePanelView.swift`。
