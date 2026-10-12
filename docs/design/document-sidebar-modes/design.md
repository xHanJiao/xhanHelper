# 文档详情页侧栏 · 双模式（文档内导航 / 文档列表导航）

> 状态：**已实现**（2026-10-12）
> 范围：仅 macOS 客户端（`client/xHelper/xHelper/Shared/Views/DocumentListView.swift`）。服务端 **零改动**。
> 前序：`docs/design/document-highlight-annotation/design.md`（详情页三列布局 + 目录栏的由来）、`docs/design/document-annotation-single-view/design.md`（右侧批注面板同批收敛）。
> 唯一入口：macOS 客户端。iOS / Web / 分享页 **不做**。

---

## 一、背景与目标

文档详情页左侧栏（`DocumentFullView.tocSidebar`，固定 220 宽）此前**只有一种形态**：本文档的标题大纲（`tocItems`）。

2026-10-12 去掉了文档页的两条换篇路径（⌘←/⌘→ 快捷键、侧栏页脚的「上一篇 / 下一篇」按钮），**切文档只剩「返回文档列表 → 再点开」一条路**，在文档间来回对照时很笨。

**目标**：侧栏加一个模式切换 ——
- **本文目录**：文档内导航（现状）；
- **文档列表**：文档列表导航，且**必须是「进入详情页时那份筛选条件下的列表」**（用户明确要求：带着关键词 / 标签 / 读者层级进来，侧栏要看到同一批文档，而不是全量）。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| SM-1 | 数据从哪来 | ✅ 直接读 `DocumentViewModel.documents`。筛选状态（`keyword` / `searchFields` / `activeTags` / `audienceView`）本就存在 VM 上、进详情页不清空，所以「同筛选」是**天然成立**的，不需要任何额外传参或重新查询 |
| SM-2 | 侧栏显示门槛 | ✅ macOS 改为**常驻**（去掉 `!tocItems.isEmpty`）。否则没标题的文档连切换入口都没有。目录空时给空态提示「本文档没有标题」 |
| SM-3 | 切换器形态 | ✅ 侧栏头部**单行菜单**：label 显示当前节名（「本文目录」/「文档列表 · N 篇」）+ 8pt `chevron.down`，`.menuStyle(.borderlessButton)` + `.menuIndicator(.hidden)` + `.fixedSize()`。菜单里用**两个 `Toggle`**（选中项自带 ✓）。⚠️ **不要用 `Picker`**：空 label 的 `Picker` 放进 `Menu` 会自己多渲染一个空菜单项（灰色空框），2026-10-12 踩过，见文末后续变更 |
| SM-4 | 模式持久化 | ✅ `@AppStorage("xhan_doc_sidebar_mode")`。**必须持久化** —— 换篇会走外层 `.id(doc.id)` 重建，普通 `@State` 留不住，每点一次列表就弹回目录 |
| SM-5 | 当前文档不在列表里时 | ✅ 顶部**钉一行**标出当前文档，下接一条 Divider，再是列表。典型场景：聊天内链跳进来（`documents` 可能是空的或上一轮筛选的旧结果）、当前文档翻在第 2 页往后 |
| SM-6 | 分页 | ✅ 复用 `DocumentViewModel.loadMore()`：列表末行 `onAppear` 触发（与 `CardGridView` 同一套路）。`shortcut:` 只续拉一页，不做「跳到第 N 页」，个人用量够 |
| SM-7 | 行样式 | ✅ **两行**：标题（限 1 行，当前项 `semibold` + `appAccent` 勾）+ 次信息行「相对时间 · 前 3 个标签」（`formatConversationDate`）。行内边距 `h10/v6`、圆角 6 底色 —— 当前项 `appAccent.opacity(0.10)`、hover `Color.primary.opacity(0.06)`，取法与 `TopBarView.conversationMenuRow` 同源。**纯标题的字墙已废**（2026-10-12 用户嫌密，见文末后续变更） |

| SM-8 | 侧栏里再放一套筛选控件 | ❌ 不做。筛选仍由 `BrowsePanelView` 面板头管（详情页里那几个控件本来就被 `openDocument == nil` 条件藏起来了）；侧栏列表只是筛选结果的投影 |
| SM-9 | iOS | ❌ 不动。iOS 仍 `!tocItems.isEmpty` 才显示、只有目录模式、无分段控件（新增部分全部包 `#if os(macOS)`） |
| SM-10 | 服务端 / API / 模型 | ✅ **零改动**。不新增接口、不加字段 |

---

## 三、交互设计

```
┌── 侧栏（220） ──────────┬── 正文（WebView） ──┐
│ 本文目录 ⌄         ‹    │                      │
│ ─────────────────────── │                      │
│ ▸ 回归测试：带图文档示例 │   （正文本体）        │
│   ▸ 图片一：…           │                      │
│   ▸ 图片二：…           │                      │
└─────────────────────────┴──────────────────────┘
        ↑ 单行菜单（点开选「本文目录 / 文档列表 · N 篇」），右侧仍是收起 chevron

┌── 侧栏（列表模式） ─────┬── 正文（WebView） ──┐
│ 文档列表 · 26 篇 ⌄  ‹   │                      │
│ ─────────────────────── │                      │
│ 回归测试：带图文档…   ✓ │   （正文本体）        │
│ 3 天前 · 测试 回归测试  │                      │
│ 测试文档 B              │                      │
│ 8 天前 · 测试           │                      │
└─────────────────────────┴──────────────────────┘
```

列表模式的三种状态：

| 状态 | 表现 |
|---|---|
| 当前文档在 `documents` 里 | 该行 accent 底色 + 加粗 + 勾；进入时 `withAnimation(.easeInOut(0.2)) { scrollTo(anchor: .top) }` 把当前文档**带动画地**对齐到列表顶部（⚠️ **待真机确认**：`LazyVStack` 对未物化行 `scrollTo` 可能静默不生效，见 §五与后续变更） |
| 当前文档**不在** `documents` 里 | 顶部钉一行当前文档（同样式）+ Divider，下面才是列表 |
| `documents` 为空 | 钉行 + 空态「当前筛选下没有文档」 |

点任一行 → `viewModel.openDocument = doc` → 外层 `.id(doc.id)` 重建详情页 → `DocumentListView.onChange(of: openDocument)` 同步顶栏文档按钮组（既有链路，未改动）。

---

## 四、实现要点

`DocumentListView.swift` 单文件内完成：

1. `private enum DocSidebarMode: String { case toc, list }`。
2. `DocumentFullView` 加 `@ObservedObject var viewModel: DocumentViewModel`（`DocumentListView.body` 传入），拿 `documents` / `loadMore()` / `openDocument`。
3. macOS 属性区加 `@AppStorage("xhan_doc_sidebar_mode") private var sidebarMode: DocSidebarMode = .toc`。
4. `tocSidebar` 拆成「头部（节名菜单 + 收起按钮）+ 体（`tocOutline` / `documentNavList`）」。
5. 正文列渲染门槛：macOS `tocExpanded ? tocSidebar : tocCollapsedStrip` 无条件；iOS 原样包在 `if !tocItems.isEmpty` 里。

---

## 五、边界与已知取舍

- **切到无标题文档不再消失侧栏**，代价是这类文档的正文宽少 220（可手动收起，`xhan_doc_toc_collapsed` 已记忆）。
- **钉行只钉一条**：当前文档不在列表里时不会把它插回列表的正确位置（列表按服务端 `updated_at DESC` 排，位置还得靠拉页）。导航够用。
- **换篇必然把侧栏滚回顶部**：外层 `.id(doc.id)`（`DocumentListView.body`）重建整个详情子树，含侧栏 ScrollView —— 这是 `.id` 的既有语义（那句注释原话就是「滚动位置 / 查找栏 / 目录展开态归零」），不是本次引入。所以列表用 `scrollTo(anchor: .top)` 把当前项对齐到顶部（**不用 `.center`**：点哪条哪条往中间跑，手感像列表自己乱动）。真正的「点了不跳、列表也不回顶」需要**把左侧栏移出 `.id(doc.id)` 子树**（让侧栏滚动位置跨篇保留），代价是重新切分 `DocumentFullView` 里那堆 per-doc `@State`（reader / 查找栏 / 匹配计数 / 连线…）—— **独立重构，本次明确不做**，只记账。
- ⚠️ **滚动定位未真机验证**：`documentNavList` 的 `onAppear` 里 `proxy.scrollTo(document.id, anchor: .top)` 依赖该行已被 `LazyVStack` 物化。**可达的失败路径**：侧栏滚到底触发 `loadMore` 到若干页（`documents` 有上百条）→ 点靠底部的某篇 → 外层 `.id(doc.id)` 重建 → 列表从顶部重新渲染、当前行未物化 → `scrollTo` 静默不滚（用户以为"没定位到我"）。注意这与 SM-5 是两回事：SM-5 那种「不在 `documents` 里」走钉行、天然可见。**退化方案（确认不生效后择一）**：① 取消 `scrollTo`，当前文档一律顶部钉行（复用 SM-5 组件，净删代码，代价是当前行在列表里出现两次）；② 先滚到已加载末行再 `loadMore`（**不推荐**：循环 + 时序都脆）。`anchor` 改 `.top` 不改变这条判断。
- **列行次信息与 `parseTags` 同源**：`documentNavSubtitle` 复用 `DocumentFullView.parseTags(_:)` 取标签文本，不再手写第三份切分。注意两者口径有细微差别：`parseTags` 不丢「纯空白字段」（会产出 `text == ""` 的 TagBadge），手写版会丢 —— 服务端不存这种数据，实际无差。
- **列表页与侧栏列表是同一份数组**，所以详情页里对文档的增删（目前详情页没有删除入口）不影响一致性。
- 单行菜单在 220 宽（左右各 12 padding、右侧 24 收起按钮）下排得下（`.fixedSize()` 保证不被拉满，当前 ≈165pt；`listTotal` 到**万篇**级才会开始压）；窗口触发 `Layout.docTocHardFloor` 时会直接收成窄条，不会挤变形。

---

## 六、验证

- macOS + iOS 两批 `xcrun swiftc -typecheck`（不带 `xcodebuild`）。
- 真机由用户在 Xcode 复核：带筛选进文档 → 侧栏切「文档列表」看到同批文档；换篇后仍停在列表模式；聊天内链跳进来的文档 → 顶部有钉行。
- 服务端无改动，无需起后端。

---

## 后续变更

### 2026-10-12 · 切换器与列表行样式改版（用户拍板）

- **背景**：首版用分段控件「目录 \| 列表」+ 纯标题行；用户反馈「按钮的形式和下面的列表都有点丑，列表太密看不出是什么，目录/列表什么鬼不知道自己是什么」。
- **决定**：
  1. 分段控件 → **单行菜单**（label 显示当前节名 + `chevron.down`，点开是两个 `Toggle`）；SM-3 已就地更新。
  2. 命名 **目录 → 本文目录**、**列表 → 文档列表（· N 篇）**（N 取 `listTotal`，避免翻页时数字抖动）。
  3. 行 → **两行**（标题 + 「相对时间 · 前 3 个标签」）+ 圆角底色（当前项 accent 10%、hover `primary` 6%，取法同 `TopBarView.conversationMenuRow`）+ 当前项勾；SM-7 已就地更新。
- **影响文件**：`DocumentListView.swift`（`modeTitle(_:)` / `sidebarModeLabel`、`tocSidebar` 头部、`documentNavRow` / `documentNavSubtitle`、新增 `@State hoveredDocID`）。
- **不变**：`@AppStorage` 记忆、iOS 原样、服务端零改动。
- **测试**：本文档 §三 的两张示意图与三状态表已就地更新。

### 2026-10-12 · 修：`Menu` 里别放空 label 的 `Picker`

- **现象**：点开侧栏节名菜单，多出一个**空的灰色圆角框**（悬停时高亮），下面才是「本文目录 / ✓ 文档列表」。
- **根因**：头部原本写的是 `Menu { Picker("", selection:) { ... }.labelsHidden() }`。macOS 把**空 label 的 `Picker` 当成一个菜单项**渲染（那一格空框就是它的 label），Picker 的选项则成为该行展开的内容。**不是系统 bug，是 API 用错**。
- **修法**：菜单内容换成两个 `Toggle` + `Binding<Bool>`（`set` 里 `if $0` 保住单选语义），选中项由系统画 ✓。label 侧一字未动。
- **影响文件**：`DocumentListView.swift`（`tocSidebar` 头部 `Menu` 内容）。
- **教训**：`Menu` 里放选择器请用 `Toggle`；`Picker` 只用在有可见 label 或 `Form`/`Section` 语境下。

### 2026-10-12 · 评审订正（P2/P3）

- **P2-1 标签切分去重**：`documentNavSubtitle` 的内联 `split/trim/filter` 改为 `parseTags(doc.tags).prefix(3).map(\.text)`，不再添第三份实现（口径差异见 §五）。
- **P2-2 滚动定位**：§三 状态表与 §五 新增「⚠️ 待真机确认」标注与退化方案，不再把它当既定行为写死。
- **P3-4**：本文档后续变更条目里的「点开 `Picker`」订正为「两个 `Toggle`」。
- **P3-5**：`hoveredDocID` 移入 `#if os(macOS)`（`sidebarMode` 与同文件的 `annotOpen` / `tocCollapsed` 一样留在外面 —— 那 3 个 AppStorage 统一挪属独立小重构，不在本次）。
- **P3-6**：`.fixedSize()` 处补已知边界注释（万篇级才会压）。
- **非本次**：`DocumentSelectionCoordinator.openDocument(id:)` 里 8 处自标「定位后删」的 `[doclink]` 临时埋点已摘除。

### 2026-10-12 · 换篇后列表滚动：`.center` → `.top`（用户拍板）

- **背景**：用户在真机发现「点一篇，那一条就被拎到列表中部」，怀疑是 bug。
- **定位**：不是 bug，是两层叠加 —— ① 外层 `.id(doc.id)` 换篇必然把侧栏滚回顶部（`.id` 的既有语义）；② `scrollTo(anchor: .center)` 是我为「回顶后当前项可能不在视野」加的补偿。
- **决定**：`anchor` 改 **`.top`** —— 当前项贴列表顶部，上一条就留在眼皮上方，滚一下即回。理由：居中会让「点哪条哪条往中间跑」，像列表自己在乱动。
- **不变**：§五 的「`.id` 回顶是既有语义」「未物化行 `scrollTo` 待真机确认」两条判断都不受影响；「把左侧栏移出 `.id` 子树」仍只记账不做。
- **影响文件**：`DocumentListView.swift`（`documentNavList` 的 `onAppear`，一行 + 注释）。

### 2026-10-12 · 侧栏定位加动画（用户拍板）

- **背景**：改成 `.top` 之后，定位仍是「闪」过去的 —— 首帧画在顶部，等 `onAppear` 触发才瞬移到位，用户原话「闹鬼一样」。
- **决定**：包 `withAnimation(.easeInOut(duration: 0.2))`（与房子其它侧栏/面板动效同值）。
- **不变**：`anchor: .top`、`documentInOpenList` 守卫、未物化行那条待确认项，均不受影响。
- **影响文件**：`DocumentListView.swift`（同一处 `onAppear`）。
