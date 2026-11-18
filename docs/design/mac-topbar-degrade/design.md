# 顶栏会话下拉常驻 + 顶栏自适应降级

> 2026-10-11 设计。两件事同批做：① 顶栏会话下拉**常驻**（含聊天画中画 PiP，取代双栏设计里的 G4/D4）；
> ② 顶栏**自适应降级**——顶栏不再决定窗口最小宽，窄窗时把次要按钮收进 `⋯`，
> 把窗口底线从 ~900 还回两栏的 **778**（PiP 750）。2026-11-18 起右栏最小宽 424 → 底线 **822**，见 §7。
> 状态：**已实现，待真机验收**（§6）。来源：`../mac-two-column-layout/design.md` §10 的「未做」项 + `docs/bugs/033` §5 残余项。
>
> **估算（定稿）：4 面 / 2 缝（0 条需建探针）/ 2 次真机往返 / 3 个待拍决定（已拍）→ 约 4–5 小时**
> （AI 会话工时，区间；开工粗估为 4–6 小时）。

## 1. 目标

| # | 目标 |
|---|------|
| G1 | 顶栏左侧的会话下拉**常驻**：PiP（右栏最大化、中栏让位）时也显示，可切会话 |
| G2 | 顶栏不再把窗口顶宽：窗口底线 = 两栏公式（右栏开 822 / 无右栏与 PiP 750），顶栏在窄窗**自己降级** |
| G3 | 降级不能丢能力：被收进 `⋯` 的按钮仍可达；**键盘快捷键不因降级失效** |

## 2. 现状（已核实，含 file:line）

- **常驻落点**：`TopBarView.swift:48-52` 的 `if !rightSidebar.isPipChat { conversationSwitcher }` —— 即双栏设计 G4/D4 的实现。
- **底线怎么来的**：`ContentView.swift:106` `minContent = max(bottomRowMin, topBarMinWidth, minWindowContentWidth)`。
  `topBarMinWidth` 是顶栏隐藏探针（`TopBarView.intrinsicWidthProbe`，`fixedSize()` 量全线内在宽）上报的值；
  顶栏是 `VStack` 最宽子项 → **顶栏内在宽 = 窗口底线**。~900 是顶栏撑出来的，不是两栏算出来的。
- **执行器**：底线经 `WindowMinWidthStore` → 根视图 `.frame(minWidth:)` → AppKit `contentMinSize`（`docs/bugs/033`），本次不动这条链。

### 2.1 宽度账（右栏开、release、标签封顶 240；按钮全是 `Layout.topBarIconSize = 26` 的统一 icon，故可算）

| 顶栏状态 | L0 全量 | L2 收中心槽组 | L3 再收标签(240→160) |
|---|---|---|---|
| 中心槽空（右栏开非浏览器/文档面板） | 794 | — | 714 |
| 文档详情组（3 键） | 923 | 841 | 761 |
| 浏览器组（7 键） | **1063 ← 这就是 ~900 的来源** | 841 | 761 |
| dev 浏览器（另加 DEV 徽标 + 簇内间距 = +52） | 1115 | 893 | 813 |

- 无右栏 / PiP 目标底线 750，右栏开目标底线 **822**（`Layout.rightSidebarOpenMinContentWidth` = 中栏 380 + 缝 6 + 右栏最小 424 + 外扩 12）。
- 于是「窄窗必须收东西」是硬事实：中心槽组一显形（浏览器态 +269）就超底线。
- 单组宽 = `12 + 26n + 9(n−1)`。
  ⚠️ **9 不是 4**：组内按钮之间渲染的是 `Divider()`（`TopBarView.buttonRow`），而 `CapsuleGroup` 是
  `HStack(spacing: 4)` —— 相邻子项各吃一次间距，故每对按钮占 `4 + 线宽1 + 4 = 9`，与组间 Divider 同构。
  漏算这一项会系统性低估 `5(n−1)`（7 键浏览器组差 30pt），会让档位偏「不降级」、靠探针把窗口顶宽。
  对应常量：`Layout.topBarDividerWidth = topBarInGroupSpacing*2 + topBarDividerLineWidth`。
- 表里的数**按当前常量重算过（2026-10-12）**：比首版整表高 **6** —— `topBarTrafficClearance` 56 → 62
  （红绿灯右移 6，见 `../mac-apps/topbar-slot-system.md` §八）。`width(level:model:)` 是这些数的唯一来源。
- 表里「L2 收中心槽组」那列 = **中心槽一个都不留**时的宽（这正是 `width(level:model:)` 的口径：档位判据、
  卡片闸与探针都用它）。实际渲染时中心组会按剩余空间**留几个留几个**（§3.3 要点 5、`../overflow-collapse/design.md`）
  → 表里的数只会更保守，不会更乐观。

## 3. 方案

### 3.1 常驻（取代 G4/D4）

`TopBarView.topBarHStack` 去掉 `isPipChat` 守卫，下拉在任何形态都渲染。PiP 下它照样能开 `popover`
（锚在顶栏）；切会话后浮动面板取 `chatTabManager.currentTab?.viewModel`，天然跟随。

### 3.2 底线交还两栏（探针降级为安全网）

`updateWindowMinWidth` 的公式**不动**，但顶栏降级后探针量到的已是「降级后的顶栏」，
正常不超过窗口底线（822 / 750），于是底线由 `bottomRowMin` 决定：**822（右栏开）/ 750（无右栏、PiP）**。
探针保留作安全网：万一档位算乐观了，它把窗口顶开，而不是让顶栏在底线上溢出。

### 3.3 自适应降级（`TopBarLayout`，纯逻辑）

按**可用宽**（`ContentView.topBarAvailableWidth` = 真实窗口内容宽，取不到窗口时回退布局 geo）选「装得下的最不降级档」：

| 档 | 做什么 | 浏览器态内在宽 |
|---|---|---|
| `full` | 全量 | 1063（dev 1115） |
| `foldCenter` | 中心槽组（浏览器 / 文档详情）→ `⋯`；**能留几个留几个**（余量够就少收几个） | 841（dev 893；一个都不留时） |
| `compactSwitcher` | + 下拉标签上限 240 → 160 | 761（dev 813） |

选档阈值（含 `Layout.topBarDegradeMargin = 8` 余量）：

| 状态 | 全量 | 收中心槽组 | 收中心槽组 + 标签 160 |
|---|---|---|---|
| 浏览器态 | ≥1071 | 849–1070 | 769–848 |
| 文档态 | ≥931 | 849–930 | 769–848 |
| 无中心槽（右栏开别的面板） | ≥802 | — | 722–793 |

**dev 构建各档再 +52**（DEV 徽标常驻，2026-10-14 起；见 §7）：浏览器态 ≥1123 / 901–1122 / 821–900；
文档态 ≥983 / 901–982 / 821–900；无中心槽 ≥854 / — / 774–853。dev 在双栏底线 822 下基本都装得下
（浏览器态末档 813 ≤ 814；无中心槽收标签后 766）—— 例外是无痕会话（🎭 前缀 +16）：dev 浏览器态 829 > 814，
探针把窗口顶到 829。PiP 750 下无中心槽末档 766 > 742 → 探针把窗口顶到 766（dev 专属）。

→ 落点（导航组 6 颗、右栏最小 424 后重算）：**双栏 822** 时浏览器/文档态 = 「收中心槽组 + 标签 160」
（761 ≤ 814），无中心槽 = 全量（794 ≤ 814）；**PiP 750** 时一律「标签 160」（无中心槽 714 ≤ 742；
**浏览器态 761 > 742 → 探针把窗口顶到 761，Release 也一样** —— PiP 下 `isOpen` 仍为 true，网页 tab 的
浏览器中心组照常可见，这个组合真实可达）。
<769 走最深档（PiP 750 正落在这里，靠探针兜底）。

四条设计要点：

1. **不收导航组**（右侧栏首页入口：浏览器/账单/书架/灵阅/文档/事件）。`TopBarButton.keyEquivalent`（⌘1–6）是挂在
   **被渲染的按钮**上的（`keyboardShortcut`），整组收进 `⋯` 会连快捷键一起丢掉；收标题只是截断文字。
   故最末杠杆是标签而不是导航组 —— 与最初设想的「DEV → 中心槽 → 导航组」不同，理由在实现时才发现。
   （DEV 徽标自 2026-10-14 起不再参与降级，顺序只剩「中心槽组 → 标签收窄」，见 §7。）
2. **算而不是量**：按钮统一 26pt、内边距/间距全是常数 → 档位是纯函数，真实栏与隐藏探针必然同档（不会自激）。
3. **标签按上限吃满来算**：档位不随会话标题抖动（切会话不抖）；短标题时顶栏实际更窄，方向保守、不会溢出。
4. **中心槽组还得落进「卡片右侧那段」**（2026-10-12 加，见 `docs/bugs/034`）：右簇是**右对齐**的，
   它一显形就把整簇往左推 —— 右栏开着时中栏卡片右缘（`内容宽 − cardGap − seam − 右栏宽`）就是按钮的边界，
   越过就是「按钮压在卡片上」（`../mac-card-top-wrap`）。所以选档多一道闸：低于 `foldCenter` 的档还须满足
   `trailingWidth(level) ≤ ContentView.topBarTrailingLimit − margin`；`foldCenter` 及更深的档免闸（再降也缩不了右簇）。
   后果：右栏开着时中心槽组留在 `⋯` 里（标签不受牵连），右栏拖到 ≥646（浏览器）/ ≥506（文档）才够它回栏；
   右栏关着 / PiP（中栏不占位）不受此闸约束。右栏最小宽 2026-11-18 起为 **424** → 可用 **420** vs 折叠态右簇
   **412**，留 **8pt**（导航组 6 颗时曾是 376 vs 412 = 越界 36pt，靠抬最小宽解决，见 §7 同日条目）。
5. **中心槽「部分收」**（2026-10-12 加，见 `../overflow-collapse/design.md`）：到 `foldCenter` 及更深的档，
   中心组不再一刀切整组进 `⋯`，而是按**白给的空间**留前 k 个（组内注册顺序；`k=0` = 整组进 `⋯`，与旧版一致）。
   只吃余量 —— 不让 nav / util 组吐按钮（带快捷键的导航组不为一个中心按钮让位）。
   实测那一屏（窗口 995 / 右栏 600）→ 浏览器 7 键留 **3** 个（6 颗导航组比原来多吃 35pt）。本文件所有宽度数仍是「一个都不留」的口径（更保守）。
   `⋯` 长在**那一组的末尾**（同一颗胶囊、组内分隔线；占 26 + 9 = **35**）——只有 `k = 0` 时才退回右槽那颗
   独立 `⋯`（占 38 + 9 = **47**）。`⋯` 是**可选行为**：组自己声明 `TopBarGroup.autoOverflow`（默认关）；
   两个中心组开，右槽导航 / 工具组不开 → 那两颗胶囊永不变形。

### 3.4 `⋯` 溢出

原生 `Menu`（`CapsuleOverflowMenu`），行 = `Label(button.help(), systemImage: button.icon())`，
直接复用按钮自己的闭包；外观与 `CapsuleButton` 同款（26pt 单图标 + 悬浮圈）。
落位：右槽的**最左单元**（贴在 ⌘1–6 导航组左侧 —— 它替的就是原本在左边的中心槽组），只在真收了东西时渲染。

### 3.5 落点

- 新增 `Shared/Views/TopBarLayout.swift`：`TopBarLevel` / `TopBarWidthModel` / `TopBarPlan` / `TopBarLayout`（纯逻辑）。
- `Shared/Views/LayoutConstants.swift`：新增顶栏宽度常量（标签上限、chrome 宽、组距、Divider 宽、安全余量）。
- `Shared/Views/TopBarView.swift`：常驻、`plan`/`widthModel`、`topBarHStack(plan:)`、`renderRightSlot`、`⋯`、探针带档。
- `Shared/Views/ContentView.swift`：新增 `topBarAvailableWidth`（`realWindowContentWidth() ?? windowWidth`，与 033 的
  「geo 会失真」纪律一致）+ 传给 `TopBarView(availableWidth:)`。
- `Package.swift` + `Tests/TopBarLayoutCoreTests/`：纯逻辑镜像成 SwiftPM target + 9 条单测。

## 4. 决定（已拍）

- **D1**：顶栏会话下拉**常驻**（含 PiP）——取代 `mac-two-column-layout` 的 G4/D4。
- **D2**：顶栏不再决定窗口底线，底线 = 两栏公式（822 / 750）；顶栏探针降级为安全网。
- **D3**：降级顺序 = 中心槽组 → 标签收窄（240→160）；**不收导航组**（⌘1–6 挂在渲染的按钮上）、
  **不收 DEV 徽标**（2026-10-14 改，见 §7）。
- **D4**：`⋯` 用原生 `Menu`，落在右槽最左（⌘1–6 导航组左侧，替原位在左的中心槽组）。
- **D5（沿用）**：下拉标签上限 240（降级末档 160）。
- **D6**：中心槽组要显形还须落进「中栏卡片右侧那段」（`trailingWidth ≤ 卡片右侧可用宽 − margin`）；
  右栏关着 / PiP 不受此闸约束。2026-10-12 加，起因见 `docs/bugs/034`。
- **D7**：中心槽组「部分收」—— 到 `foldCenter` 及更深的档，能留几个留几个（组内注册顺序），
  `⋯` 不带数量；只吃白给的空间，不挤 nav / util 组；`⌘1–6` 与设置永不让位。
  2026-10-12 加，见 `../overflow-collapse/design.md`。

## 5. 影响文件

`client/xHelper/xHelper/Shared/Views/{TopBarLayout.swift(新),TopBarView.swift,LayoutConstants.swift,ContentView.swift}`、
`client/xHelper/Package.swift`、`client/xHelper/Tests/TopBarLayoutCoreTests/`（新）；
文档：本文件、`../mac-two-column-layout/design.md`、`../mac-apps/topbar-slot-system.md`、`docs/bugs/033`。

## 6. 验收

**自检（已完成）**：`swift test --filter TopBarLayoutCoreTests` 9/9 通过；macOS + iOS 双 target `swiftc -typecheck` 0 error。

**真机（待跑）**：

1. 双栏 + 右栏开浏览器 → 拖窄：窗口停在 **822**（不再 ~900）；浏览器组进 `⋯`，`⋯` 里 7 个键点了都生效。
   （此档带上标签收窄到 160 —— 见 §3.3 落点，属预期。）
2. 双栏 + 文档详情 → 同样停 822，文档组（下载/导出 PDF/分享）进 `⋯`。
3. 双栏 + 右栏开非浏览器/文档面板 → 停 822，**不降级**（`⋯` 不出现，标签保持 240）。
4. PiP（右栏最大化）→ 下拉**常驻可见**、可切会话（切完浮动面板正常）；拖窄停 **750**，标签收窄（浏览器/文档还收组）。
5. 拉宽（**右栏关着 / PiP**）：文档态 ≥931 / 浏览器态 ≥1071 → 中心槽组回栏（`⋯` 消失）。
   dev 构建（+52）：文档态 ≥983 / 浏览器态 ≥1123 —— 徽标一直在，只是中心组回栏。
6. 关窗 → Dock 重开 → 守卫仍在（`docs/bugs/033` 回归项）。
7. `⋯` 胶囊的外观与邻居一致（同宽同高、有悬浮圈）——`Menu` 的 label 内边距是离屏量不出来的估算项。
8. **右栏开着**（浏览器 / 文档详情面板）：窗口从最窄拖到很宽 —— 中心组**整组**不会显形，但会按剩余空间
   **部分露出来**（第 9 条）；`⋯` 左缘不压中栏卡片。宽窗下把右栏拖到 ≥646（浏览器）/ ≥506（文档）→
   中心组能整组显形、整簇落在卡片右侧。关右栏 / PiP → 第 5 条照旧。
9. **部分收**（2026-10-12 加，`../overflow-collapse/design.md`）：窗口 995 / 右栏 600 + 浏览器面板 →
   浏览器键露 **3** 个、`⋯` 里 4 个，`⋯` 里点了都生效；**露出的 3 个 + 末尾 `⋯` 在同一颗胶囊里**
   （不是旁边单开一颗）；右侧两组（浏览器~事件 / 最大化~设置）完全不变形；拖到 822 → 回到整组进独立 `⋯`；
   右栏停在 424（那段 420）→ 一个都不露（不越卡片右缘）；**窗口要能一路拖窄**（多露按钮不该把最小宽撑住）。

**校准（必跑，不是可选）**：§2.1 的账里**结构性宽度全是 `Layout` 精确常数**，剩下的估算项只有
「下拉箭头 11 / DEV 徽标 44 / 🎭 前缀 16 / `⋯` 的 `Menu` label 内边距 / Divider 线宽 1」。
真机跑第 1、4 条时顺便核对：把探针实测的内在宽（`TopBarMinWidthKey`，可临时打一行日志）与
`TopBarLayout.width(level:model:)` 逐档对照，**差 >8pt 就改 `LayoutConstants` 的顶栏常量**（机制不动）。
`⋯` 若确实比邻居胖/瘦几 pt，优先改 `topBarDividerWidth`/给 `CapsuleOverflowMenu` 补 `.frame(width:26,height:26)`。

## 7. 后续变更

### 2026-10-12 · 中心槽组不许压在中栏卡片上（选档加「右簇可用宽」第二道闸）

- **现象与根因**：见 `docs/bugs/034`。一句话：降级只按「窗口内容宽」选档，不知道按钮簇右对齐、
  只能待在**中栏卡片右侧那段** —— 窗口越宽卡片越宽，显形的中心槽组越容易落到卡片上。
- **决定**（用户拍 A）：右栏开且非 PiP 时，`foldCenter` 之前的档还须满足
  `trailingWidth(level) ≤ ContentView.topBarTrailingLimit − margin`；`foldCenter` 及更深免闸（见 §3.3 要点 4、D6）。
- **数字**：§2.1 与 §3.3 两张表按当前常量重算（旧值整表差 6 = `topBarTrafficClearance` 56 → 62）；
  闸门口径：右栏最小 380 → 可用 **376** vs 折叠态右簇 **377**（差 1pt，接受；导航组 6 颗后为 **412**，见 §7 2026-11-18）；
  中心槽组回栏需右栏 **≥611**（浏览器）/ **≥471**（文档）（6 颗后为 ≥646 / ≥506）。以上数字由 `TopBarLayout.width/trailingWidth` 现算核对过。
- **影响文件**：`Shared/Views/{TopBarLayout,TopBarView,ContentView}.swift`、
  `Tests/TopBarLayoutCoreTests/TopBarLayoutTests.swift`（+5 条）、本文件、`../mac-card-top-wrap/design.md`（§二 + §八）。

### 2026-10-12 · 中心槽「部分收」：放不下时别再整组进 `⋯`

- **需求**（用户原话）：「不要一整个胶囊全部收起，而是收起一部分」—— 实拍那一屏（窗口 ≈995 / 右栏 ≈600）
  明明还有 ~160 白给的空间，浏览器 7 键却一个都没了。
- **决定**：D1 组内注册顺序（不加新字段）／D2 `⋯` 不带数量／D3 缩略图行不迁移／D4 只吃白给的空间、
  不挤 nav / util 组。细节与"人话版"见 `../overflow-collapse/design.md`。
- **口径**：`width / trailingWidth / rightSlotWidth / showsOverflow` 的缺省口径仍是「中心组一个都不留」
  （§2.1 / §3.3 的表因此**依然有效**）；新增 `centerVisible:` 变体只服务"找可行的 k"。
- **现算核对**（`swift test` 272 全绿 + 独立脚本）：995 / 段落 596 → 留 4；1036 / 596 → 留 5；
  1400 / 596 → 留 5；段落 376（右栏 380）→ 一律留 0（不越卡片右缘）；778 → 留 0（与旧版一致）。
- **影响文件**：`Shared/Views/{TopBarLayout,TopBarView}.swift`、`Tests/TopBarLayoutCoreTests/`（1 改 + 4 新）、
  本文件、`../overflow-collapse/design.md`（新）。
- **形态与开关（同日再改，用户实拍反馈）**：`⋯` 并进那一组的**末尾**（同一颗胶囊；只有 `k = 0` 才退回右槽那颗
  独立胶囊，占宽 35 vs 47）；`⋯` 改为**可选**（`TopBarGroup.autoOverflow`，默认关；两个中心组开，
  右槽导航 / 工具组不开 → 永不变形）。模型按组带这个标志，算 k 时只动开着开关的组。
  改动文件：`Shared/Services/TopBarRegistry.swift`、`Tools/BrowserToolProvider.swift`、
  `Services/DocumentSelectionCoordinator.swift`、`Shared/Views/{TopBarLayout,TopBarView}.swift`
  （新增视图组件 `OverflowCapsuleGroup`）、`Tests/TopBarLayoutCoreTests/`（+4 条，合计 276 全绿）。

### 2026-10-14 · DEV 徽标改红底、降级不再收起

- **需求**（用户原话）：「这个 dev 环境的 macOS 下的标记，给我做成红色底，不能收起」。
- **决定**：① 徽标底色 `appAccent` 16% → **实心系统红 `Color.red`**、文字白（红底上绿字读不出来）；
  ② 删掉 `TopBarLevel.hideDevChip` 这一档，`foldCenter` / `compactSwitcher` 顺移 1；
  `TopBarPlan.showsDevChip` 从「dev 且档位浅于该档」改为 `model.isDev`，宽度账里徽标恒占位。
- **数字**（`TopBarLayout.width` 现算核对）：dev 各档内在宽 = release + **52**（徽标 44 + 多一个右簇子项带来的
  簇内间距 8）。浏览器态 dev 1080 / 858 / 778，阈值 ≥1088 / 866–1087 / 786–865（§2.1、§3.3 已同步）。
- **已知偏差（接受）**：PiP / 无右栏底线 750 下 dev 内在宽 778 > 742，探针把窗口最小宽顶到 778（+28，dev 专属）；
  双栏底线 778 下 dev「无中心槽」态也落到末档（标签 160）。避免它得再给 dev 加一档降级，不值。
- **影响文件**：`Shared/Views/{TopBarView,TopBarLayout,LayoutConstants}.swift`、
  `Tests/TopBarLayoutCoreTests/TopBarLayoutTests.swift`（`testDevChipIsShedFirst` → `testDevChipIsAlwaysShown`）、本文件。

### 2026-10-15 · `⌘1–5` 的隐含前提：**单窗口**（主场景 `WindowGroup` → `Window`）

- **现象**：`⌘1–5` 整体失灵、鼠标点顶栏正常。根因不在顶栏 —— app 同时活着两个主窗口（另一个在已断开的
  iPad 那块屏的空间里），两窗口各注册一份 `⌘1–5`，SwiftUI 把键解析到了看不见的那一份。见 `docs/bugs/038`。
- **决定**：§3.3 要点 1 / D3 里「`⌘1–5` 挂在**被渲染的按钮**上」这句话只保证了"按钮在栏上就有快捷键"，
  没保证"只有一份"。补上前提：主场景改单窗口 `Window("xhanHelper", id: "main")`
  （`xHelperMac/App.swift`），从此不会有第二份注册。
- **影响文件**：`xHelperMac/App.swift`、`Shared/Views/{ContentView,AppBackdrop}.swift`（注释）、`docs/bugs/038`、本文件。
- **不变**：降级顺序、宽度账、`⋯` 落位一律不动（本条只动"有几份顶栏"）。

### 2026-10-31 · `⌘2` 从账单换成灵阅

- **需求**（用户原话）：「把 cmd+2 从账单换成灵阅」。
- **决定**：导航组第二颗按钮 `open-bills`（账单）换成 `open-artifacts`（灵阅，图标 `doc.richtext`），
  `⌘2` 随之改为打开灵阅；账单降级到右栏首页网格（`BrowsePanel.allCases`）与设置「账单记录」，
  ~~顶栏不再有账单入口（`⌘1–5` 只有 5 个槽，属刻意取舍）。导航组仍是 5 颗按钮，降级/宽度账不变。~~
  2026-11-18 起导航组改为与右栏首页入口 1:1（6 颗，账单回到 `⌘2`），上面两句作废。
- **影响文件**：`Shared/Views/TopBarView.swift`、`xHelperMac/ShortcutGuideView.swift`、`Shared/Views/TopBarLayout.swift`（注释）、
  `docs/design/mac-apps/chat-pip-mode.md`、本文件。

### 2026-11-18 · 导航组改为「右栏首页入口」1:1（5 颗 → 6 颗，`⌘1–6`）

- **需求**（用户原话）：「它和右侧栏的内容不完全对应……希望它和右侧栏的内容对应上，顺序上和内容上，都一一对应。
  并且快捷键也是 cmd + 1~6；然后我们能否有某种机制保证未来切换右侧栏的内容时候一定能保证把上面的快捷按钮也切换了？」
- **决定**：导航组不再手写，改由 `SidebarQuickEntry.all` 生成（右栏首页网格 `SidebarHomeView` 同一份顺序）：
  `⌘1` 浏览器 / `⌘2` 账单 / `⌘3` 书架 / `⌘4` 灵阅 / `⌘5` 文档 / `⌘6` 事件。
  快捷说明页（`ShortcutGuideView`）同一份生成；注册时 `assert` 条目 ≤9、id 唯一。
  原 `⌘3` 监控、`⌘5` 记忆不再在顶栏（设置「内容与工具」仍可达，与首页网格的取舍一致）。
- **数字**（6 颗导航组，`TopBarLayout` 现算核对）：组宽 178 → **213**（+26 图标 +9 组内线）。
  末档（标签 160）：无中心槽 **714**（≤742）；浏览器/文档态收中心槽 + 独立 `⋯` = **761**。
  **当时（右栏最小宽仍是 380）的偏差，均已由同日「右栏最小宽 424」条目消掉**：
  ① 双栏底线 778 下「无中心槽」态落末档（794 > 770）→ 底线 822 后回到全量；
  ② dev 在 778 下浏览器态 813 > 770 需探针撑宽 → 822 下 813 ≤ 814 自然装下；
  ③ **PiP 750 + 浏览器态 761 > 742 → 探针把窗口顶到 761**（Release 也一样：PiP 下 `isOpen` 仍为 true，
  网页 tab 的浏览器中心组照常可见，这个组合真实可达，不是「到不了」）；
  ④ 右栏最窄 380 时折叠右簇 412 > 可用 376 → 最左那颗导航按钮压进中栏卡片 36pt → 抬到 424 后 412 ≤ 420。
- **不变**：降级顺序（中心槽 → 标签）、不收导航组、`⋯` 落位、`default-utility` 组一律不动。
- **影响文件**：`Shared/Views/{BrowsePanelView,SidebarHomeView,TopBarView,TopBarLayout,RightSidebarManager}.swift`、
  `xHelperMac/ShortcutGuideView.swift`、`Tests/TopBarLayoutCoreTests/TopBarLayoutTests.swift`、
  `docs/design/{mac-apps/topbar-slot-system,right-sidebar-home,right-sidebar-unified-tabs}`、本文件。

### 2026-11-18 · 右栏最小宽 380 → 424（消掉顶栏右簇压卡片）

- **需求**（用户原话）：「右栏被拖到比 424pt 还窄（默认 380 就命中）时……最左那颗导航按钮会压进中栏卡片
  右上角 36pt（旧 5 颗时是 1pt）这个的解法是什么？我觉得，可以限制最小宽度？」→ 拍板「只做方案 1」。
- **根因**：重叠量与窗口宽无关，只跟右栏宽有关 —— 右簇右缘锚在「窗口 − 16」，卡片右缘锚在
  「窗口 − 右栏宽 − 缝」，所以重叠 = 右簇宽 − (右栏宽 − 4)。6 颗导航组下折叠态右簇 412，右栏最小 380 → 越 36pt。
- **决定**：`Layout.rightSidebarMinWidth` 380 → **424**（= 412 + 选档余量 8 + 4）；窗口底线随之
  778 → **822**（中栏 380 + 缝 6 + 424 + 外扩 12）。**方案 2（窄时藏「最大化」按钮）不做** —— 424 后
  所有情形都装得下，那个开关会变成永不触发的死逻辑。`TopBarLayout` 降级算法、导航组不折叠、工具组不动。
- **数字**（现算核对）：右栏 424 → 卡片右侧那段 **420** vs 折叠态右簇 **412**，留 8pt；PiP 底线仍 750
  （`pipMinContentWidth` = 12 + 424 = 436 < 750，不受影响）；`artifactNavHardFloor` 600 → **644**（目录 220 + 424）。
- **已知偏差（接受）**：dev 构建折叠态右簇 464（DEV 徽标 +52）在右栏 424 下仍越界 44pt —— 要连 dev 也保证
  得把最小宽抬到 476、底线 874，不值，dev 自用。
- **影响文件**：`Shared/Views/LayoutConstants.swift`、`Shared/Views/{TopBarLayout,ContentView}.swift`（注释）、
  `Tests/TopBarLayoutCoreTests/TopBarLayoutTests.swift`（`twoColumnFloor` 随常量变 822、+1 条验收断言）、
  `docs/design/{mac-topbar-degrade,mac-two-column-layout,overflow-collapse}`、本文件。
