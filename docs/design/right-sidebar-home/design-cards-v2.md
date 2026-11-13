# 右栏首页入口卡 v2 · 信息密度四档

> 状态：设计定稿，待实现（2026-11-12）。分支 `feat/right-sidebar-home`。
> 本文取代 `design.md` §4「视觉规格」里的卡片部分；`design.md` 的 §6 数据口径、§7 设置入口仍然有效。

## 1. 要解决的问题

1. **宽屏留白**：卡片按面板宽度两等分，1900 宽时每张 918pt，但卡内内容只有 ~650pt，右侧整块是空的。
2. **与数据脱节**：卡片只有「标题 / 主数字 / 副文案」，想打开最近某篇文档、某个标签，还得先点进面板再找。

## 2. 目标 / 非目标

**目标**

1. 卡宽决定信息密度：越宽信息越多，最窄退成图标。
2. 宽卡里直接列出最近对象，**行可点**（标签切换、文档/灵阅直达，其余进面板）。
3. 首页内容块封顶 1320 并居中；不新增后端接口。

**非目标**：三列及以上、卡片排序/自定义、iOS（右栏 macOS 独有）、深色专用插画、备忘的完成/取消操作。

## 3. 四档（按卡宽，随窗口平滑降级；**永远两列**）

| 档 | 卡宽 | 卡高 | 组成 |
|---|---|---|---|
| **信息** | ≥ 560 | 164 | 插画带 116 ｜ 信息区 244–320（3 条可点行，宽度随卡宽弹性，见 §4）｜ 文字区（标题/主数字；副文案仅信息区为空时渲染）+ 箭头 |
| **紧凑信息** | 340–560 | 164 | 插画带 `min(116, 0.42 卡宽)` ｜ 头部一行 `标题 · 主数字` + **3 条可点行**（行吃满剩余宽度）+ 箭头 |
| **三行** | 200–340 | 132 | 插画 ｜ 标题 / 主数字（卡宽 ≥ 300 才加第三行副文案：`homeSecondaryMinCardWidth`） |
| **图标** | < 200 | `max(110, min(卡宽, 170))` | 2 列 × 3 张纯插画卡（填满列宽、高度封顶 170），无文字，hover 出名字 |

- 卡高只跟档位走，不因文本行数抖动；**信息档与紧凑信息档同为 164 / 3 行**（2026-11-13 拍板），只换排布。
- **插画带宽度 = `min(卡高, 0.42 × 卡宽)`**（`SidebarHomeFormat.homeBandWidth`）：440 以上恒为卡高（老口径），卡 300 → 126、卡 200 → 84，横向空间让给文字。信息档恒为 116；紧凑信息档取 `min(116, homeBandWidth)`（卡 340 → 116，卡 200 → 84），省出来的宽度全给行。
- **永远两列**（用户 2026-11-12 拍板）：卡宽 =（内容宽 − 12）/ 2，窄了只降档位（信息 → 紧凑信息 → 三行 → 图标），不再退回单列——单列时一行只放一张卡，看着像列表而不是入口宫格。代价：内容宽 < 412（卡 < 200）时整页是两列图标卡；卡片填满列宽但**高度封顶 170**，所以是「宽卡 + 居中插画」而不是越拖越大的巨型方块。
- **卡高不跟可用高走**（2026-11-13 拍板 A，取代 2026-11-12 的「高度参与档位」）：信息档与紧凑信息档恒 164 / 3 行，窗口矮就整页滚动 —— 宁可滚，也不要同一台机器上信息区忽 2 行忽 3 行。`homeMetrics` 因此只有 `contentWidth` 一个入参，`availableHeight` / `homeChromeHeight` 已删除。图标档高度仍是 `max(110, min(卡宽, 170))`（不把插画压成扁带，也不变巨型方块）。
- **内容块封顶 1320**（= 654 × 2 + 12 间距）。窗口再宽时卡片不再变宽，整块居中、两侧留白；窗口更窄时封顶不生效。
- 图标档固定两列（328 宽里放一张空卡比两个方块更难看）；问候行保留（23pt 标题 / 13pt 今日行 / 间距 8，见 `design.md` §4）。
- **底部「最近」页脚已删除**（2026-11-12）：宽卡信息行已给出最近对象，任何宽度都不再显示。

## 4. 信息行（信息档 / 紧凑信息档）

- **行区宽度随卡宽弹性**（2026-11-13，`SidebarHomeFormat.homeInfoRowsWidth`）：卡 560 → 244（文字区保住 129pt，主数字不截），卡 ≥636 → 320 封顶（再多标题与右上细节之间就是一条大留白）。行宽富余时优先给行，而不是让文字区空着。
- 规格：行高 28、行间 6、圆角 9、单行 11.5pt（**`tail` 截断，留头**——用户 2026-11-13：标题多是「对象 + 补充」，头几个字才区分得开）、右侧细节 10pt。
- **紧凑信息档的排布**（2026-11-13）：标题不再单占一列，而是压成头部一行 `标题 · 主数字`，行在它下面横排；行区宽度 = 卡宽 − 插画带 − 箭头 − 左右 14，卡 440 时 ≈273pt，比信息档固定的 244 还宽。行数固定 3（`infoRows.prefix(3)`），与信息档同数：164 高里「头部 ~17 + 3×28 + 4 个 6pt 间距（含卡底 Spacer 前那道）」= 125，加上下各 14 共 153，余 11pt 由卡底 Spacer 吃掉；顶对齐，行数不足（如浏览器只开 1 个标签）时空位留在卡底，头部与第一条行跟邻居对齐。
- 常态：无底色、无描边（2026-11-12 改）；hover：`appAccent 12%` 底（深色 18%）+ 0.5pt `appAccent 45%` 描边。
- 最多 3 条，按各域「最近」顺序。
- 行容器**常态无底色、无描边**（只剩图标 chip + 文字）：卡片本身已是一层浮起的白卡，行再套一层白底 + 白描边会并排三张小卡，压过标题。hover 才浮出淡绿底（`appAccent` 12%/18%）+ 0.5pt 绿描边，可点感交给 hover。
- 右侧细节：备忘 = 相对到期（`今天 / 明天 / 后天 / N 天后 / 昨天 / N 天前`，过期的行首图标是橙色 `exclamationmark.circle`），文档/灵阅/事件/浏览器历史 = 相对时间，账单 = 金额；**浏览器标签行不显示 host**（2026-11-13：favicon 已经表达站点，标签也没有可比的时间字段）。
- 卡头第三行（`secondary`：「41 笔」/「下一条 11月15日 20:00」/「最近编辑 3 天前」）**只在没有信息行时渲染**（`infoRows.isEmpty`）。有信息区时它和行内的数据重复，还会并排出现两套时间口径（卡头「下一条 11月15日」对行内「23 天前」），因此藏掉。数据侧照算不额外请求；断网把信息区拉空时它会回来兜底。
- **图标来源（不猜、不新造字段）**：

| 行 | 图标 | 依据 |
|---|---|---|
| 浏览器标签 / 历史 | 真 favicon（`faviconStore.image(forHost:)`），失败 → host 首字母色块 | `host` |
| 账单 | `category1` 的 12 项固定枚举映射（餐饮 fork.knife / 服装 tshirt / 交通 tram / 居住 house / 旅游 airplane / 美容 scissors / 人情 gift / 日用 basket / 数码 laptopcomputer / 学习 book / 医疗 cross.case / 娱乐 gamecontroller）；未知 → 首字母色块 | `category1` |
| 备忘 | 未过期 `checkmark.circle`（accent）／已过期 `exclamationmark.circle`（橙） | `status` + `deadline` |
| 文档 / 灵阅 / 事件 | **标题首字色块**（无类型字段，靠首字区分行） | 标题 |

- 首字色块：`appAccent 14%` 底 + `appAccent` 文字，18pt 圆角 5（不用哈希色相，避免新造色）。
- 映射与兜底写成纯函数（12 项全覆盖 + 兜底），配单测。

## 5. 点击语义

| 目标 | 行为 |
|---|---|
| 浏览器标签行 | 切到该标签（`manager.activeTabId` + `BrowserToolProvider.selectTab`） |
| 浏览器历史行 | 新开标签打开该 URL（`BrowserToolProvider.openURL`），仅在浏览器未打开时出现 |
| 文档行 | 直接打开该文档（`DocumentSelectionCoordinator.openDocument`） |
| 灵阅行 | 直接打开该产物（`ArtifactOpenCoordinator.open`） |
| 备忘 / 账单 / 事件行 | 进对应面板（只读，无法定位单条） |
| 文字区 + 右箭头 | 打开对应面板 |

- **信息档 / 紧凑信息档的卡片不再是整块按钮**（信息行本身就是按钮，按钮套按钮会丢点击）：信息档只有文字区可点；紧凑信息档是「插画带 + 头部一行 + 行下方空位 + 右箭头」可点（只有上下 14pt padding、左右 14pt 与行间 6pt 是死区）；三行 / 图标档仍是整卡按钮。
- 所有行与卡片保留 `help` 文案。

## 6. 数据

沿用 `design.md` §6 的六路请求，只调整两处：账单加 `GET /api/bills?limit=3`（现有 stats 只有 count/sum）、事件 `limit=1 → 3`。无新接口。

| 行数据 | 来源（`SidebarHomeViewModel`） |
|---|---|
| 浏览器标签 | `RightSidebarManager.tabs` 里排除特殊 tab（收藏夹/历史/配置/本页资源）与灵阅产物后的网页标签；「N 个标签」同口径（本地状态，零请求） |
| 浏览器历史 | 没有网页标签时退化成 `HistoryStore.entries` 最新 3 条（本地持久化，零请求） |
| 最近账单 | `recentBills`（3 笔） |
| 最近备忘 | `reminders`（pending 50 条，取前 3） |
| 最近灵阅 / 文档 / 事件 | `recentArtifacts` / `recentDocuments` / `recentEvents`（各 3 条） |

## 7. 改动清单

| 文件 | 改动 |
|---|---|
| `xHelper/Shared/Views/HomeEntryCard.swift` | 四档渲染（`infoCard` / `compactCard` / `textCard` / `iconTile`）、信息行视图；`InfoRow` / `InfoIcon` 与显式 `init`（`layout/infoRows`） |
| `xHelper/Shared/Views/BrowsePanelView.swift` | 按内容宽取 `homeMetrics`、1320 封顶居中、`infoRows(for:)`、去掉信息档的整卡按钮；删除「最近」页脚 |
| `xHelper/Shared/ViewModels/SidebarHomeViewModel.swift` | `recentBills/recentDocuments/recentArtifacts/recentEvents`；账单 list 与事件 limit=3；删除 `RecentItem` / `Stats.recent` / `mergeRecent` |
| `xHelper/Shared/Views/SidebarHomeFormat.swift` | `homeMetrics` / `HomeCardLayout` / `HomeCardMetrics`、`dueRelative`、分类 → SF Symbol 映射、标题首字（纯函数，进 SwiftPM 纯逻辑靶） |
| `xHelper/Package.swift` + `Tests/SidebarHomeCoreTests/` | 档位四档 + 692 门槛 + 1320 封顶 + 映射表 12 项全覆盖 + 未知分类兜底 + 首字截取的单测 |
| `docs/design/right-sidebar-home/design.md` | §4 指向本文；§11 追加变更记录 |

## 8. 验收

1. 拖宽拖窄：档位按**卡宽** 560 / 340 切换（= 侧栏约 1184 / 744），不跳动、不横向溢出；1900 时卡宽锁 654、内容块居中。
2. 1900：六张卡都有 3 条信息行；点标签切到该标签、点文档打开文档、点备忘行进备忘面板。
3. 侧栏 840（卡 388）：紧凑信息档（头部一行 + 3 条可点行）；侧栏 500（卡 218）：三行文字档；侧栏 380（卡 158）：2×3 图标卡，hover 显示名字。
4. 断网：数字显示 `—`，信息区为空但卡片不塌（不留半张空壳）。
5. 任何宽度都没有底部「最近」区；行右侧细节始终可见（长标题只截标题，留头）。
6. `swift test --filter SidebarHomeCoreTests` 全绿；macOS / iOS `swiftc -typecheck` 无 error。

## 9. 后续变更

- **2026-11-12 · 实现落地**：两列门槛由 620 改为 692（见 §3），其余按本稿实现；纯逻辑（档位/图标/首字）落在 `SidebarHomeFormat.swift` 以便 `swift test` 覆盖。
- **2026-11-12 · 删除「最近」页脚 + 细节行改左右结构**：页脚任何宽度都不再显示（宽卡信息行已覆盖）；单行档细节行 = 标题左 / 时间右；备忘行右侧改 `dueRelative` 相对口径。影响 `BrowsePanelView.swift`、`SidebarHomeViewModel.swift`、`HomeEntryCard.swift`、`SidebarHomeFormat.swift`、`Tests/SidebarHomeCoreTests`。
- **2026-11-12 · 细节行标题限宽 200pt**：长标题（备忘内容）不再吃满整行，最多 200pt、其余留白，右侧时间始终贴右。同时入口卡标题「打开浏览器」改为「浏览器」。影响 `BrowsePanelView.swift`、`HomeEntryCard.swift`、`design.md`。
- **2026-11-12 · 卡头副文案按信息区隐藏**：`showsSecondary = details.isEmpty && infoRows.isEmpty`，避免与信息行/细节行重复、以及两套时间口径并排（排序不动）。
- **2026-11-12 · 信息行常态去底去描边**：三行白底 + 白描边在浅色卡上过重，改为只 hover 显形（淡绿底 + 绿描边）。影响 `HomeEntryCard.swift`。
- **2026-11-12 · 问候行跟时间自走**：`greeting` 外包 `TimelineView(.periodic(from:by:30))`，问候语整点换档、右上日期跨零点换；不用 `Timer`，视图摘除自动停。影响 `BrowsePanelView.swift`。
- **2026-11-12 · 永远两列**：取消「内容宽 < 692 退单列」，卡宽恒为（内容宽 − 12）/ 2，档位边界全部改看卡宽；单列专用的 104 卡高一并移除（统一 132/164）。`SidebarHomeFormat.homeMetrics` + 单测同步。
- **2026-11-12 · 图标档高度封顶 170**：两列恒定的副作用是窄窗口里卡片变成 300pt 见方的巨型方块，改为填满列宽 + 高度 `min(卡宽, 170)`。影响 `SidebarHomeFormat.homeMetrics`、`HomeEntryCard.iconTile`、单测。
- **2026-11-12 · 高度参与档位（保一屏）**：`homeMetrics` 新增 `availableHeight`（默认可不传，老调用行为不变），`BrowsePanelView` 传入 `geo.size.height`。信息档放不下降到单行档；132 为底线、极端情况整页滚动。新增 `homeChromeHeight = 123`。影响 `SidebarHomeFormat.swift`、`BrowsePanelView.swift`、单测（22 条）。
- **2026-11-12 · 图标档门槛 340 → 300**：340 时卡宽 315 已经有足够空间放「标题 / 主数字」，退成图标反而是一张又宽又空的插画卡。300 = 132 插画 + 内距 + 箭头后还剩 ~117，够最短一档文案。
- **2026-11-12 · reviewer 复核后修正**：① `showsSecondary` 之前只看 `details`/`infoRows` 是否为空，但 `details` 是按数据恒传的，导致**三行档**也把副文案藏了（132 高只填两行、「41 笔 / 下一条 11月15日」看不到）；改为只看 `layout`：信息档/单行档且有行时才藏，三行档恒显示。② 图标档高度加下限 110。③ 问候行从 `.periodic(from: Date(), by: 30)` 改 `.everyMinute`（body 重算会换起点，tick 被一直推后）。④ 卡宽 300–320 的三行档，副文案「下一条 11月15日 20:00」会尾截——已知取舍，门槛保持 300。⑤ 单测期望值写死字面量（125.67 / 边界 615、614 / 下限 110），不再用 `homeChromeHeight` 自证。
- **2026-11-12 · 图标档门槛 300 → 200 + 插画带随卡宽缩**：300–200 之间原本只有插画、认不出是什么域；改为走三行档（插画 + 标题 + 主数字），插画带按 `min(卡高, 0.42 × 卡宽)` 收窄让出文字区。只有卡宽 < 200（插画带缩到 84 以下、文字区不足 70pt）才保留纯插画卡。影响 `SidebarHomeFormat`（`homeBandWidth` 新增、门槛）、`HomeEntryCard`（新增 `width` 入参）、`BrowsePanelView`（传 `metrics.cardWidth`）、单测（23 条）。
- **2026-11-12 · 窄卡只留两行 + 最窄退回图标**：三行档在卡宽 < 300 时不再画第三行副文案（200 出头的卡里它只会被截成「上次停在 Dee…」）；卡宽 < 200 退回纯图标卡——这一档文字区不足 50pt，「本月 ¥2,963」会被截成「本月 ¥…」，不如只给插画（用户 2026-11-12 拍板）。
- **2026-11-13 · 浏览器卡信息区回填 + 删除「上次停在 X」**：浏览器打开时信息行 = 当前网页标签前 3 个（点行切标签），没打开时 = 历史最新 3 条（点行新开标签，右侧细节用相对时间）。「N 个标签」改为只数网页标签（此前把收藏夹/历史/配置/本页资源也算进去）。卡头「上次停在 X」整条删除——有历史时信息行已表达，全新环境历史为空时它也没内容可说。影响 `BrowsePanelView.swift`、本文档 §4/§5/§6。
- **2026-11-13 · 信息区下探到卡宽 340（新增紧凑信息档，删掉单行灰字档）**：并排三列要吃掉 431pt（插画 116 + 信息区 264 + 内距 28 + 箭头 23），所以信息档只能在卡宽 ≥ 560 出现，再窄就掉成「灰字细节行」（没图标、不可点）。改为把标题压成头部一行、行在下面横排（行区 = 卡宽 − 插画带 − 箭头 − 内距，卡 440 时 ≈273pt，比信息档固定的 244 还宽），信息区门槛降到 **卡宽 340**（≈侧栏 744，此前要 ≈1184），行数按卡高 132 → 2 行。旧 `.singleLine` 档整档删除（`DetailLine` / `details` / `detailLines()` 一并删除）。信息行标题截断由 `middle` 改 `tail`（留头）。影响 `SidebarHomeFormat.swift`（`homeCompactMinCardWidth` / `tier` / 高度降档改 `.compactInfo`）、`HomeEntryCard.swift`（`compactCard`）、`BrowsePanelView.swift`、单测（档位边界 340 / 440 / 559）。
- **2026-11-13 · 紧凑档三条细节拍定**：① 浏览器标签行不再显示 host（favicon 已表达站点）；② 只有 1 条行的卡内容顶部对齐，头部/第一条行与 2 行卡的邻居同栅格，空位留卡底（此前整段垂直居中，同排卡的信息区错位）；③ 插画带与行下方空位补成可点区（这一档此前是整卡按钮，点插画没反应会被当成卡片坏了）。影响 `BrowsePanelView.swift`、`HomeEntryCard.swift`。
- **2026-11-13 · 新增离屏出图脚本**：`scripts/snapshot-homecards.sh` 用真视图（`HomeEntryCard`）按卡宽 340/380/440/520/560/654 渲成 PNG，定档位/截断方式靠它，不靠 HTML 样张猜像素。
- **2026-11-13 · 信息档行区宽度改弹性（244 → 244–320）**：卡窄时行区仍是 244（文字区 129pt 不截主数字），卡 ≥636 给到 320 —— 留头截断下宽卡的「睡眠台账 v2（2026-10-25 起）」「采集上海 9 月份脉冲能否持续」这类标题能整条放下。纯函数 `SidebarHomeFormat.homeInfoRowsWidth` + 单测 5 条断言。影响 `SidebarHomeFormat.swift`、`HomeEntryCard.swift`、单测（24 条）。
- **2026-11-13 · 两档统一三行 / 删掉高度降档（拍板 A）**：紧凑信息档从 132 / 2 行抬到 **164 / 3 行**（垂直 padding 18 → 14），信息档不动；`homeMetrics` 删掉 `availableHeight` 入参与「164 放不下就降档」分支，`homeChromeHeight` 常量一并删除 —— 卡高不再随窗口高变，矮窗口整页滚动。动机：同一台机器拖宽窄时信息区 2/3 行来回变（网友反馈「为什么有时候两行有时候三行」），且六卡块只占 37% 屏高、底部空一大块。影响 `SidebarHomeFormat.swift`、`HomeEntryCard.swift`、`BrowsePanelView.swift`、单测（22 条，删掉 2 条高度相关用例）。
- **2026-11-13 · 问候区放大（19→23 / 12.5→13 / 间距 6→8）**：六张卡都是 164 高之后，19pt 的问候语读起来像页眉而不是首页标题，头部被卡片压住。问候区同时从 `BrowsePanelView` 抽成独立视图 `HomeGreetingView.swift`（字号/间距是带默认值的参数，默认值即线上取值），出图脚本直接渲真视图对比档位；日期固定 12pt，不跟着标题放大。影响 `HomeGreetingView.swift`（新）、`BrowsePanelView.swift`、`scripts/snapshot-homecards.swift`、`design.md` §4。
- **2026-11-13 · 问候区右上日期删除**：`11月13日 周五` 与卡里每行的相对时间重复，且把问候语挤成一个「标题 + 右侧小字」的常规页眉；去掉后问候语独占一行。影响 `HomeGreetingView.swift`、`design.md` §4。
