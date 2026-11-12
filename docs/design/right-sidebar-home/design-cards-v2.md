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
| **信息** | ≥ 560 | 164 | 插画带 116 ｜ 信息区 244（3 条可点行）｜ 文字区（标题/主数字/副文案）+ 箭头 |
| **单行** | 440–560 | 132 | 插画 ｜ 单行 `标题 · 主数字 ⟶ 副文案` + 至多 2 行文本细节 |
| **三行** | 340–440 | 132 | 插画 ｜ 标题 / 主数字 / 副文案 三行 |
| **图标** | < 340 | `min(卡宽, 170)` | 2 列 × 3 张纯插画卡（填满列宽、高度封顶 170），无文字，hover 出名字 |

- 卡高只跟档位/列数走，不因文本行数抖动。
- **永远两列**（用户 2026-11-12 拍板）：卡宽 =（内容宽 − 12）/ 2，窄了只降档位（信息 → 单行 → 三行 → 图标），不再退回单列——单列时一行只放一张卡，看着像列表而不是入口宫格。代价：内容宽 < 692（卡 < 340）时整页是两列图标卡；卡片填满列宽但**高度封顶 170**，所以是「宽卡 + 居中插画」而不是越拖越大的巨型方块。
- **高度也参与档位**（2026-11-12）：可用高 `H` 时每张卡最多 `(H − 123) / 3`（123 = 问候行 + 卡片上间距 + 行间距 + 上下 padding 的固定开销 `homeChromeHeight`）。信息档 164 放不下就降成单行档 132；**132 是底线**，再矮就允许整页滚动（宁可滚也不把卡压到字糊）。底部 `Spacer` 从 28 起改为 0 起，先让留白让位。图标档高度也吃这个上限（`min(卡宽, 170, maxCardHeight)`）。
- **内容块封顶 1320**（= 654 × 2 + 12 间距）。窗口再宽时卡片不再变宽，整块居中、两侧留白；窗口更窄时封顶不生效。
- 图标档固定两列（328 宽里放一张空卡比两个方块更难看）；问候行保留。
- **底部「最近」页脚已删除**（2026-11-12）：宽卡信息行已给出最近对象，任何宽度都不再显示。

## 4. 信息行（信息档）

- 规格：行高 28、行间 6、圆角 9、单行 11.5pt（`middle` 截断）、右侧细节 10pt。
- 常态：无底色、无描边（2026-11-12 改）；hover：`appAccent 12%` 底（深色 18%）+ 0.5pt `appAccent 45%` 描边。
- 最多 3 条，按各域「最近」顺序。
- 行容器**常态无底色、无描边**（只剩图标 chip + 文字）：卡片本身已是一层浮起的白卡，行再套一层白底 + 白描边会并排三张小卡，压过标题。hover 才浮出淡绿底（`appAccent` 12%/18%）+ 0.5pt 绿描边，可点感交给 hover。
- 右侧细节：备忘 = 相对到期（`今天 / 明天 / 后天 / N 天后 / 昨天 / N 天前`，过期的行首图标是橙色 `exclamationmark.circle`），文档/灵阅/事件 = 相对时间，账单 = 金额，浏览器 = host。
- 单行档的细节行没有行首图标，所以过期与否不额外标注（时间口径与其它卡一致——用户 2026-11-12 定，不加「已过期」前缀）。要看过期状态用信息档或备忘面板。
- 卡头第三行（`secondary`：「上次停在 X」/「41 笔」/「下一条 11月15日 20:00」/「最近编辑 3 天前」）**只在既没有细节行也没有信息行时渲染**（`details.isEmpty && infoRows.isEmpty`）。有信息区时它和行内的数据重复，还会并排出现两套时间口径（卡头「下一条 11月15日」对行内「23 天前」），因此藏掉。数据侧照算不额外请求；断网把信息区拉空时它会回来兜底。
- 细节行标题限宽 `200pt`（≈18 个汉字，超出 tail 省略号），右侧时间/金额贴右，中间自然留白。是**限宽**不是限字数，窄卡到宽卡一致。
- **图标来源（不猜、不新造字段）**：

| 行 | 图标 | 依据 |
|---|---|---|
| 浏览器标签 | 真 favicon（`faviconStore.image(forHost:)`），失败 → host 首字母色块 | `host` |
| 账单 | `category1` 的 12 项固定枚举映射（餐饮 fork.knife / 服装 tshirt / 交通 tram / 居住 house / 旅游 airplane / 美容 scissors / 人情 gift / 日用 basket / 数码 laptopcomputer / 学习 book / 医疗 cross.case / 娱乐 gamecontroller）；未知 → 首字母色块 | `category1` |
| 备忘 | 未过期 `checkmark.circle`（accent）／已过期 `exclamationmark.circle`（橙） | `status` + `deadline` |
| 文档 / 灵阅 / 事件 | **标题首字色块**（无类型字段，靠首字区分行） | 标题 |

- 首字色块：`appAccent 14%` 底 + `appAccent` 文字，18pt 圆角 5（不用哈希色相，避免新造色）。
- 映射与兜底写成纯函数（12 项全覆盖 + 兜底），配单测。

## 5. 点击语义

| 目标 | 行为 |
|---|---|
| 浏览器标签行 | 切到该标签（`manager.activeTabId` + `BrowserToolProvider.selectTab`） |
| 文档行 | 直接打开该文档（`DocumentSelectionCoordinator.openDocument`） |
| 灵阅行 | 直接打开该产物（`ArtifactOpenCoordinator.open`） |
| 备忘 / 账单 / 事件行 | 进对应面板（只读，无法定位单条） |
| 文字区 + 右箭头 | 打开对应面板 |

- **信息档的卡片不再是整块按钮**（信息行本身就是按钮，按钮套按钮会丢点击）；单行 / 三行 / 图标档仍是整卡按钮。
- 所有行与卡片保留 `help` 文案。

## 6. 数据

沿用 `design.md` §6 的六路请求，只调整两处：账单加 `GET /api/bills?limit=3`（现有 stats 只有 count/sum）、事件 `limit=1 → 3`。无新接口。

| 行数据 | 来源（`SidebarHomeViewModel`） |
|---|---|
| 浏览器标签 | `RightSidebarManager.tabs`（本地状态，零请求） |
| 最近账单 | `recentBills`（3 笔） |
| 最近备忘 | `reminders`（pending 50 条，取前 3） |
| 最近灵阅 / 文档 / 事件 | `recentArtifacts` / `recentDocuments` / `recentEvents`（各 3 条） |

## 7. 改动清单

| 文件 | 改动 |
|---|---|
| `xHelper/Shared/Views/HomeEntryCard.swift` | 四档渲染、信息行视图、图标档；`InfoRow` / `InfoIcon` / `DetailLine` 与显式 `init`（`layout/details/infoRows`） |
| `xHelper/Shared/Views/BrowsePanelView.swift` | 按内容宽取 `homeMetrics`、1320 封顶居中、`infoRows(for:)` / `detailLines(_:)`、去掉信息档的整卡按钮；删除「最近」页脚 |
| `xHelper/Shared/ViewModels/SidebarHomeViewModel.swift` | `recentBills/recentDocuments/recentArtifacts/recentEvents`；账单 list 与事件 limit=3；删除 `RecentItem` / `Stats.recent` / `mergeRecent` |
| `xHelper/Shared/Views/SidebarHomeFormat.swift` | `homeMetrics` / `HomeCardLayout` / `HomeCardMetrics`、`dueRelative`、分类 → SF Symbol 映射、标题首字（纯函数，进 SwiftPM 纯逻辑靶） |
| `xHelper/Package.swift` + `Tests/SidebarHomeCoreTests/` | 档位四档 + 692 门槛 + 1320 封顶 + 映射表 12 项全覆盖 + 未知分类兜底 + 首字截取的单测 |
| `docs/design/right-sidebar-home/design.md` | §4 指向本文；§11 追加变更记录 |

## 8. 验收

1. 拖宽拖窄：档位按 560 / 440 / 340 切换，不跳动、不横向溢出；1900 时卡宽锁 654、内容块居中。
2. 1900：六张卡都有 3 条信息行；点标签切到该标签、点文档打开文档、点备忘行进备忘面板。
3. 840 两列：三行文字（与现状一致）；500：单行 + 细节；380：2×3 图标，hover 显示名字。
4. 断网：数字显示 `—`，信息区为空但卡片不塌（不留半张空壳）。
5. 任何宽度都没有底部「最近」区；单行档细节行右侧时间始终可见（长标题只截标题）。
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
