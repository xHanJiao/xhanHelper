# macOS 双栏布局改造（去左栏 + 顶栏会话下拉 + 输入栏无痕 pill）

> 2026-09-30 设计。参考 today.ai 的双栏形态：去掉 macOS 左侧栏，会话切换/新建收进顶栏左侧的下拉；
> 把「无痕」从侧栏右键菜单挪到输入栏，做成和「模式 / 信任」同款的会话级 pill。
> 状态：**设计定稿**，9 个决定（D1–D9）已拍板，按 §8 分期施工。

## 1. 目标

| # | 目标 |
|---|------|
| G1 | 删掉 macOS 左侧栏（含它和中栏之间的 seam），窗口只剩「中间对话栏 + 右侧可展开功能区」两栏 |
| G2 | 会话切换与新建收进顶栏**左侧区域**：一个下拉 trigger 显示当前会话名，其右侧一个常驻 `＋` 新建会话（普通新建） |
| G3 | 无痕能力从侧栏右键菜单 → 输入栏一个 pill；**点击即把当前会话标记为无痕**，可再点取消 |
| G4 | 顶栏下拉**仅在中间对话区可见时出现**；右侧栏最大化（PiP）时隐藏 |

---

## 2. 现状（已核实，含 file:line）

### 2.1 三栏布局与左 seam（**左栏是 4 件独立的元素，不止一张卡片**）

`ContentView.swift:426` 的 macOS 分支：`VStack { TopBarView; GeometryReader { HStack { … } } }`，三栏都是手搭卡片列。HStack（`spacing:0`）从左到右：

| # | 元素 | 位置 | 说明 |
|---|------|------|------|
| 0 | `HSpace(Layout.cardGap)` | `:441` | 窗口左缘 6px 外边距（**保留**） |
| 1 | `SidebarView` 卡片 | `:443-455` | 左栏本体（`if !isSidebarHidden`） |
| 2 | **左 seam**：`HSpace(seamHalfGap)+SeamMarker($leftSeamX)+HSpace(seamHalfGap)` | `:456-458` | 2.5+1+2.5=6px，缝线本体（**独立元素**） |
| 3 | 左 `ResizableDivider(id:"left")` | `:502-514`（overlay） | 拖拽手柄，浮在 seam 上、靠 `leftSeamX` 定位 |
| 4 | `HoverRevealButton` | `:493-497`（overlay） | 收窄后左缘 hover 展开（露出 ⌘B 的唯一可见控件） |

- 收窄后 `isSidebarHidden`（`sidebarWidth <= sidebarMinWidth(38)`，`:71-73`）时中栏已在 `HSpace(cardGap)` 后、离窗口左缘 6px —— 即今天的「隐藏态」。
- ⚠️ `SeamMarker` / `ResizableDivider` / `ResizeGhostState` / `ResizeDividerSpace`（`ResizableDivider.swift`）是**右侧 seam 与右栏分屏共用的**（`RightSidebarView.swift:338-349`），**只删左侧那两次调用，不能删 struct**。

### 2.2 顶栏槽位系统（`TopBarView.swift` + `Services/TopBarRegistry.swift`）

- 渲染：`HStack { Spacer().frame(width: navLeadingOffset); renderSlot(.left); Spacer(); devChip; renderSlot(.center); renderSlot(.right) }`，`.padding(.horizontal,16)`+`.padding(.vertical,6)`，`.ignoresSafeArea(.top)`。顶栏高 = `toolbarVGap*2 + topBarCapsuleHeight` = **48pt**（`LayoutConstants.swift:25,34`）。
- 取显规则（`TopBarRegistry.swift:103-108`）：每槽取**最高优先级的可见组**，同优先级共存；组 `visible()` 每次重渲染求值；`TopBarView.visibleGroups:187-190` 再滤掉「按钮全不可见」的空组。
- 已注册组（**`.left` 槽当前无任何注册者 → 永远为空**）：

  | 组 | 槽 | 优先级 | 注册处 | 可见条件 |
  |----|----|--------|--------|----------|
  | `default-right` | right | 5 | `TopBarView.swift:91-124` | 恒真（按钮各自 gated） |
  | `default-utility` | right | 5 | `:133-160`（macOS） | 恒真 |
  | `browser-actions` | **center** | 10 | `BrowserToolProvider.swift:2185-2225` | `activeTabId != nil && tabs 非空 && isOpen` |
  | `document-actions` | center | 8 | `DocumentSelectionCoordinator.swift:93-118` | 文档面板选中某篇时 |

- **`TopBarButton` 只支持图标**（`TopBarRegistry.swift:11-36`，`icon: () -> String`），无文字、无 popover 内容；`CapsuleButton/CapsuleGroup/CapsuleIcon` 是 `TopBarView.swift` 内 **private**（同文件内可复用）。
- **`navLeadingOffset` 是死代码**：`ContentView.swift:429` 硬编码传 `0`；`ContentView.swift:592/659` 的注释仍称它随左栏宽变（**已过期**）；`docs/bugs/014` 的宽度分析同样基于旧值。
- ⚠️ **顶栏宽度会反噬窗口最小宽**：`ContentView.swift:426` 是 `VStack`，宽度 = 子项最大值（= bug 014 机制）。若 trigger 显示长会话标题，顶栏 intrinsic 宽变大 → 窗口最小宽变大。**trigger 必须固定 `maxWidth` + 截断**。
- **过期文档**：`docs/design/mac-apps/topbar-slot-system.md` 称浏览器组在 `.left` 槽（实际已挪 `.center`，`:2187`）。
- 顺手修复：`TopBarView.swift:10` 原观察 `BookmarkStore`（无按钮读它），真正读的 `BookmarksBarState` 未观察 → 收藏栏按钮 help 文案不刷新。已改观察对象为 `BookmarksBarState`。

### 2.3 左栏功能与入口（`SidebarView.swift`）

- 结构：`expandedContent:64` / `conversationListSection:103` / `conversationRow:160`。
- 新建会话：`chatTabManager.newChat()`（`ChatTabManager.swift:83`）；切会话：`chatTabManager.openConversation(...)`（`:134`）。
- **无痕入口仅 2 处**：侧栏右键菜单 `SidebarView.swift:309/339` → `chatTabManager.newEphemeralChat()`（`ChatTabManager.swift:119`，先 `POST /conversations {ephemeral:true}` 再打开）；macOS 快捷键 Cmd+Shift+N（`App.swift:119-125`）。
- **会话列表的初始加载挂在 SidebarView**：`SidebarView.swift:144-149` 的 `.task { if !hasLoadedOnce { await load() } }`。
- 列表数据：`ConversationListViewModel`（分页 20 / 60s 前台轮询 / `load/reload/loadMore/rename/setPinned/delete`，`ViewModels/ConversationListViewModel.swift`）。

### 2.4 数据模型（无痕）

- `Conversation.Extra` JSON 的 `ConversationExtra.Ephemeral bool`，**当前只允许建会话时设置（设计上「属性不可变」）**，无修改端点。
- 销毁链路：chat 结束 → `OnChatEnd`（`appinit/agents.go:241-246`）→ `CronRunner.NotifySessionEnd`（`cron_runner.go:273`）排 session_end 延迟任务 → 到点后 `post_chat_group.go:89-94` 判 `IsEphemeral` 命中即 `EphemeralDestroyer.BeginDestroy`（Stage A 同步隐藏，不可逆）→ 后台分批物理删（Stage B）。静默间隔 `Chat.SessionGapMinutes`（`config.go:968`，**默认 30 分钟**）。
- ⚠️ **`post_chat_group.go:89-94` 的 `IsEphemeral` 判定早于 `lastMsgID` 的使用**：无痕分支不碰 `lastMsgID`，所以补挂任务时 `userMsgID` 只影响去重记账/日志。
- 边界：**空无痕会话（建了没发消息）没有 session_end 任务，不会自动销毁**，只能手动删（`ephemeral-conversation.md:94`）。

### 2.5 快捷键与菜单

全在 macOS 的 `NSEvent` monitor（`xHelperMac/App.swift`），经 `AppShortcutBridge`（`Shared/Services/AppShortcutBridge.swift`，`#if os(macOS)`）分发；**无任何 `.commands` / `CommandGroup` / 菜单项**。

| 键 | 回调 | App 绑定 | ContentView 注册 | 本次 |
|----|------|----------|------------------|------|
| ⌘B | `onToggleLeftSidebar` | `App.swift:112-117` | `:338-340` | **删**（D1） |
| ⌘N | `onNewChat` | `:127-133` | `:341-343` | 保留（= 顶栏 `＋`） |
| ⌘⇧N | `onNewEphemeralChat` | `:119-125` | `:344-346` | **删**（D8） |
| ⌘⇧↑/↓ | `onSwitchConversation` | `:157-164` | `:347-349` | 保留（与下拉切换一致） |
| ⌘V | `onPasteImages` | `:175-184` | `:350-352` | 不动 |

- `ShortcutGuideView.swift`（macOS 设置页，手工维护，`:15` 自标注必须同步）：行 **23**（⌘B）与行 **22**（⌘⇧N）需删；行 21/25/26/27 保留。
- `AppShortcutBridge` 头注释 `:7-12` 漏列 ⌘V（顺手补；⌘⇧N 即将删除不必再列）。

### 2.6 窗口最小宽 / 自动宽度链（**左栏收窄不是纯视觉，是"降窗口最小宽好继续缩"**）

```
sidebarWidth ─┬─→ baseMinWidth / pipMinContentWidth   (ContentView.swift:125-140)
              │        └─→ updateWindowMinWidth() :144-152
              │                └─→ AppDelegate.requiredMinContentWidth (App.swift:246)
              │                        └─→ windowWillResize clamp (App.swift:313-324)
              ├─→ 自动收窄 :571-573 / 自动恢复 :574-577（sidebarAutoCollapsed :38）
              ├─→ PiP 缩窗收窄 :594-598（写 pipExpandedFloor，App.swift:251-253）
              ├─→ 开右栏时收左栏腾位 :176-178
              └─→ 持久化 @AppStorage("xhan_sidebar_width") :32 / 读 :691 / 写 :699-703
```

- `AppDelegate.windowWillResize` = `requiredMinContentWidth + chrome` 钳制宽度（`requiredMinContentWidth` 已由 ContentView 取「底栏 / 顶栏内在宽 / 750」的较大者，见 §4.1.3）。
- 前身 bug：`docs/bugs/014-pip-shrink-sidebar-overflow.md` —— PiP 缩窗溢出就是靠"先收左栏"缓解的。

### 2.7 导航层（近死，见 §4.1.4 证据）

`SidebarTab` 只有 1 个 case `.chat`（`SidebarView.swift:3-13`）；`NavigationRouter`（`NavigationRouter.swift`，42 行）的 `refresh()` 全客户端无人调用、`canRefreshCurrentTab/refreshDisabledMessage/loadedTabs/markLoaded/currentTitle` 无人读、`navigateTo(.chat)` 恒 no-op、`switch router.currentTab` 只有 1 case。**净死代码。**

### 2.8 iOS 安全边界

- `ContentView.swift` 的 macOS 代码在 `#if os(macOS)` 内（`:23-276`、`:302-360`、`:425-704`→`#endif :827`）；iOS 走 `TabView`，**不引用** `sidebarWidth` / `AppDelegate` / `RightSidebarManager`。
- `AppDelegate` 全体在 `xHelperMac/App.swift`（iOS 根本不编译）。
- `SidebarView.swift` 虽在 `Shared/` 但只被 macOS 引用；`LayoutConstants.swift` 的 sidebar 常量 iOS 无人用。
- ⚠️ **红线**：`ResizableDivider.swift:105-129` 的 `UnresizableDivider` **不能**移进 `#if os(macOS)`（`TopBarView` 在 iOS 也用它，`:103-104` 有警告）。
- （另：`ConversationListView.swift` 是 iOS 版无调用点的死代码，不在本次范围。）

---

## 3. 方案总览

```
改前：VStack { TopBar; HStack { [Sidebar] | seam | Chat | seam | [RightSidebar] } }
改后：VStack { TopBar(左=会话下拉+＋) ; HStack { Chat | seam | [RightSidebar] } }
```

三块：**① 删左栏（含 seam）**（纯客户端）→ **② 顶栏会话下拉**（纯客户端）→ **③ 输入栏无痕 pill**（客户端 + 后端）。

## 4. 详细设计

### 4.1 删左栏

#### 4.1.1 删视图（§2.1 的 4 件）

- 删 `:443-459` 整块（`SidebarView` + 左 seam），保留 `:441` 的 `HSpace(Layout.cardGap)`。
- 删左 overlay 两件：`ResizableDivider(id:"left")`（`:502-514`）、`HoverRevealButton`（`:493-497`）。
- 连带 state/helper：`sidebarWidth`、`storedSidebarWidth`、`isSidebarHidden`、`sidebarHovered`、`sidebarAutoCollapsed`、`leftSeamX`、`allowExpand`、`leftSidebarMaxAvailable`、`toggleLeftSidebar()`、sidebar 的 `onChange`/`onAppear`（`:553-664` 中与左栏有关的分支）。
- `SidebarView.swift` 整文件删除（`SidebarTab` 随它走，见 4.1.4）；`ContentView.swift:392-398` 的 `ConversationListView` iOS 分支不受影响。

#### 4.1.2 重写派生宽度常量（`LayoutConstants.swift`）

左栏项从所有公式移除，`middleMinWidth` 保留：

- `sidebarMinWidth/MaxWidth/SnapThreshold/DefaultWidth`、`sidebarHPadding`、`sidebarOuterPadding` → **删**。
- `middleAndRightMinSpan` → `seamWidth + middleMinWidth + seamWidth + rightSidebarMinWidth + rightSidebarOuterPadding`（去掉左栏项）。
- `rightSidebarOpenMinContentWidth` → 不再含 `sidebarMinWidth + sidebarOuterPadding`。
- `contentOverheadNoRightSidebar` / `contentOverheadRightSidebarOpen` → 去掉 `cardGap*2 + sidebarHPadding*2` 里的左栏专属部分（保留窗口左右 cardGap 与 seam）。

#### 4.1.3 固化「隐藏态」+ 窗口最小宽收敛（D7）

窗口最小宽公式收敛为（`updateWindowMinWidth` 唯一派生入口）：

```
无右栏：  max(cardGap*2 + middleMinWidth, minWindowContentWidth)          ≈ 750
右栏开：  cardGap*2 + middleMinWidth + seam(6) + rightSidebarMinWidth     ≈ 778
PiP：     max(cardGap*2 + rightSidebarMinWidth, minWindowContentWidth)    ≈ 750
```

数值（实测）：`middleMinWidth = rightSidebarMinWidth = 380`，`minWindowContentWidth = 750`。

- **顶栏兜底**：窗口最小宽再取 `max(…, 顶栏内在宽)`——顶栏是 VStack 最宽子项，短于它就会溢出居中、红绿灯压住下拉框（`TopBarView.intrinsicWidthProbe` 经 `TopBarMinWidthKey` 上报，`updateWindowMinWidth` 主动放大窗口）。
- 删除所有「靠收窄左栏降底线」的分支：自动收窄/恢复（`:571-577`）、PiP 缩窗收窄（`:594-598`）、开右栏收左栏腾位（`:176-178`）、`pipExpandedFloor`（`App.swift:251-253` 及其读写）。
- **删 `measuredContentMinWidth`**（geo 冻结实测机制：条件 `lastGeoWidth > contentW` 在每次收缩都成立 → 窗口被弹回上一宽度造成跳动），改由上面的显式探针提供顶栏最小宽。
- ⚠️ **真机验收点**：必须重跑 PiP 缩窗场景（bug 014 场景）确认无溢出回潮 —— 这是本次唯一强制的真机往返。

#### 4.1.4 导航层与死码清理（D6）

**结论：`NavigationRouter` + `SidebarTab` 整类删掉，无逻辑迁移。** 证据（§2.7）：`refresh()` 无调用 → `refreshCounts` 永不增长 → `ContentView.swift:820` 的 `onChange(refreshCounts)` 永不触发；`canRefreshCurrentTab/refreshDisabledMessage/loadedTabs/markLoaded/currentTitle` 零读者；`navigateTo(.chat)` 恒 no-op；`switch currentTab` 单 case。

- 删 `NavigationRouter.swift` 整文件。
- `ContentView.swift` 连带删：`:24 router`、`:60-65 sidebarTabBinding`、`:206`/`:331`/`:873` 三处 `navigateTo`、`:642-646` `onChange(currentTab)`、`:816-819` `setRefreshDisabled`、`:820-825` `onChange(refreshCounts)`。
- `macOSContentArea`（`:792-826`）`Group { switch }` 塌成直接 `ChatPage(...)`，保留 `.padding/.modifier(CardBackground)/.frame/.layoutPriority/.onHover`。
- 删 `navLeadingOffset` 死参（`TopBarView.swift:12` + `TopBarView.swift:22-23` 的 spacer + `ContentView.swift:429` 传参）；顺手修 `ContentView.swift:592/659` 过期注释。
- 删 `@AppStorage("xhan_sidebar_width")`（`ContentView.swift:32`）。
- 真刷新路径**原样保留**：60s 前台轮询（`ConversationListViewModel.swift:46-48`）、回前台 reload（`:722`）、scene 激活 sync（`:287-300`）、`onChange(isStreaming)` 若仅服务刷新门控则一并删。

### 4.2 顶栏会话下拉（`TopBarView.swift`）

**集成方式（D9 = ⓑ）**：在 `TopBarView` 里单开一个**不走 registry 的 leading View**（trigger + `＋`），直接放在 `body` 最前；**不扩 `TopBarButton` 模型、不碰 `TopBarRegistry`**（`.left` 槽本无任何注册者，无优先级/共存诉求；改模型是纯 YAGNI，还引入 `AnyView` 类型擦除）。

- **可见性**：`if !rightSidebar.isPipChat { conversationSwitcher }`（D4/G4：中栏被 PiP 接管则藏）。
- **trigger**：`[🎭?] 当前会话名 ⌄`（无痕前缀 🎭），胶囊样式同 `CapsuleGroup`；**`.frame(maxWidth: 240)` + `lineLimit(1)` + `truncationMode(.tail)`**（D5，防顶栏顶高窗口最小宽，见 §2.2）。
- **`＋`**：trigger 右侧常驻图标按钮 → `newChat()`（**普通新建**；无痕走输入栏 pill）。
- 数据源：`chatTabManager.currentTab?.title`（trigger）；`conversationListVM`（菜单）。
- **展开**：自定义 popover（**不用原生 `Menu`**，因行内要放 hover 出的置顶/删除）。内容：小标题「会话」+ `ForEach(conversationListVM.conversations)`（行点击 → `openConversation`；行尾 hover `📌` setPinned / `✕` delete；当前行 `✓`；无痕行 🎭；`hasMore` → 「加载更多」`loadMore()`）。
- **接口依赖（新增两条硬依赖）**：
  1. **构造签名扩参**：`TopBarView` 现只收 `rightSidebar`（`:11`），要加 `chatTabManager` + `conversationListVM`；`ContentView.swift:427` 调用处同步传参。
  2. **首次加载**：会话列表的初始 `load()` 原本挂在 `SidebarView.task`（`SidebarView.swift:144-149`）；删栏后必须由下拉 `onAppear`（或 ContentView `onAppear`）触发 `conversationListVM.load()`，否则列表永不加载（60s 轮询首轮也要等 60s）。

```
┌───────────────────────────────────────────────────────────────────────┐
│ [🎭 关于特斯拉的笔记 ⌄] [＋]   ·DEV·   🌐  📄  💰  📊  🧠   ⛶ ▯ ⚙︎   │
└───────────────────────────────────────────────────────────────────────┘
      ┌──────────────────────────────────────────────┐
      │  会话                                           │
      │  🎭 关于特斯拉的笔记                  ✓    📌 ✕ │ ← 当前
      │  项目排期讨论                         置顶     ✕ │ ← hover 出操作
      │  周报整理                             置顶     ✕ │
      │  … 加载更多                                    │
      └──────────────────────────────────────────────┘
```

### 4.3 输入栏无痕 pill（核心）

**为什么放输入栏**：输入栏现有 pill（模式 / 智能体 / 信任 / 模型 / Brief）全是**当前会话的属性**。无痕原生是「建会话时的一次性动作」，塞进输入栏别扭；把它重构成**可与其它 pill 并列、可开可关的会话属性**后落位就顺了。

**状态机**：

| 状态 | 触发条件 | 外观 | 点击行为 |
|------|----------|------|----------|
| 普通 | 空白或已有会话，未开 | 灰 `theatermasks` | → 开启 |
| 待生效 | **空白新会话**、已开、未发送 | accent `theatermasks` | → 取消（仅改本地 intent，无服务端动作） |
| 生效中 | **已有会话**、已开 | accent + 圆点 | → 取消（未进 Stage A 前） |
| 已销毁 | Stage A 已执行 | 置灰禁点 | 不可点 |

> **实现修正**：pill 位于 `ChatView`（只拿到 VM，macOS 经 `ChatPage(chatViewModel: currentTab.viewModel)`），
> 故状态落在 **`ChatViewModel`**（`ephemeralIntent` / `conversationEphemeral` / `isEphemeral`），
> `ChatTab.isEphemeral` 仅作顶栏数据源，由 `vm.onEphemeralChanged` 镜像。
> 外观上无「圆点」（`IconButton` 是纯图标）：开=`theatermasks.fill` + accent，关=`theatermasks` + 次要色。
> 「已销毁」态不可达：销毁后会话消失、tab 直接被关闭（见下「销毁静默」），无需禁用态。

**两类会话的不同处理（关键）**：

- **空白新会话**：点击只改 `viewModel.ephemeralIntent`（**不建会话**，避免"点一下多一条空会话"）；首次发送时 `/chat` 带 `ephemeral=true`，创建即无痕（`done` 事件里 `promoteEphemeralIntent()` 转成服务端标记）。取消 = 改回 false，零副作用。
- **已有会话**：点击调新增的 `PATCH /conversations/:id/ephemeral`，把 `Extra.Ephemeral` 置 true；再点置 false。
  - **必须补挂 session_end 任务**：否则已无 pending 任务的闲置老会话会出现"标了却不销毁"空窗（`ephemeral-conversation.md:94`）。置 true 时经 B3 排一个 `now + SessionGapMinutes` 的 session_end 任务，让现有 debounce/销毁链路接管。
  - **默认会话禁止无痕**（`ephemeral-conversation.md:9`）：客户端 `Conversation` 模型无 `isDefault`，不做预判，**由服务端 B2 返回 400 兜底**，客户端 toast 提示。
- **老会话转无痕不弹二次确认**（D4）：靠 pill 高亮 + 可撤销 + hover tooltip 明示「Stage A 后不可逆」。
- **销毁静默（D10）**：会话在服务端消失（无痕销毁 / 归档 / 他端删除）时，客户端**不弹任何提示**，静默关 tab + 从列表移除（删 `ChatTabManager.handleConversationGone/handleConversationsGone` 与 iOS `ContentView` 三处 toast）。

**文案**（pill tooltip）：关=`开启无痕：会话结束后自动销毁（不可恢复）`；空白待生效=`无痕已开（待发送）：首条消息后生效，约 30 分钟后自动销毁，点击取消`；生效中=`无痕生效中：最后一轮对话约 30 分钟后自动销毁，可撤销（进入销毁后不可逆）`。

```
普通：  [🎭 无痕] [模式] [模型] [Brief]                     [发送]
生效：  [🎭 无痕 ●] [模式] [模型]
        ⚠️ 无痕：最后一轮对话约 30 分钟后自动销毁，可撤销
```

**注册方式**：新增 `InputBarButtonID.ephemeral`，默认放**收起组**（D2，与 `.trusted/.model/.brief` 同）；用户可右键「常驻」钉住。加按钮只改 `InputBarTypes.swift`（枚举 + `defaultCollapsed`）+ `ChatView.inputBarButtonContent`，**不动 `InputBar.swift`**。

**两条易漏的接线**：

- **`InputBarLayoutState.swift:26` 的「新按钮补丁」必须同步**：老用户持久化集合里没有 `.ephemeral`，要加一行 `if !stored.contains(.ephemeral.rawValue) { restored.insert(.ephemeral) }`，否则新 pill 会落到"常驻行"，与 D2 正好相反。（该文件已为 `brief` 写过同样的补丁。）
- **无痕状态放 `ChatViewModel` 而非 `ChatTab`**：pill 在 `ChatView` 里，而 `ChatView` 只拿到 VM。VM 持 `ephemeralIntent` / `conversationEphemeral` / `onEphemeralChanged`；`resetForNewChat()` 一并清零；`ChatTabManager` 建 tab 时挂 `vm.onEphemeralChanged = { tab.isEphemeral = $0 }`，`openConversation` 经 `vm.seedEphemeral(ephemeral ?? false)` 灌入服务端标记。
- `D8` 删掉 `newEphemeralChat()` 后，`APIClient.createConversation(ephemeral:)` 变死码 → **已一并删掉 `ephemeral` 参数**（pill 空白流程改走 `/chat {ephemeral:true}`）。

### 4.4 后端改动

| # | 文件 | 改动 | 量 |
|---|------|------|-----|
| B1 | `controller/chat.go` | `/chat` 请求体新增 `ephemeral bool`，仅在新建会话分支生效（现有 `IsDefault/AgentID/init_mode` 旁） | ~5 行 |
| B2 | `controller/router.go` + `controller/conversations.go` | 新增 `PATCH /api/conversations/:id/ephemeral`，对齐现有 `PATCH /:id/mode`（`conversations.go:703`）；请求 `{ephemeral: bool}`；`requireConversationOwner` → 再 `FindByID` 取 conv 判 `IsDefault`（默认会话置 true → 400 `默认会话不能设为无痕`）→ `convRepo.UpdateExtraField`（`conversation_repo.go:350`）写 `Extra.Ephemeral`；返回 `{id, ephemeral}` | ~35 行 |
| B3 | `controller/conversations.go` + `controller/router.go` + `appinit/router.go` + `agent/cron_runner.go` | B2 置 true 时补挂 session_end 任务。**注意接线**：`RouterConfig`（`router.go:24-54`）**没有 CronRunner**，加 `NotifySessionEnd func(userID, convID, userMsgID string)` 字段并在 `appinit/router.go` 注入（`agents.CronRunner` + `cfg.Server.BackgroundTasksDisabled()` 守卫，同 `appinit/agents.go:241-246`）。另**必须把 `cron_runner.go` 的 `userMsgID[:8]` 日志改为 nil-safe**（`shortID(userMsgID)`）——转无痕时无新消息，传空串会 panic；`debounceSessionEndTask` 在 `userMsgID==""` 时保留原 `LastMsgID`（取消无痕后后处理仍需锚点消息） | ~20 行 |

不改：创建端点、销毁器、列表响应（`listConversations` 已返回 ephemeral，注意 `Extra==""` 时该 key 缺失，客户端按 false 处理）。销毁判定不依赖 `userMsgID`：`post_chat_group.go:89-94` 命中 `IsEphemeral` 直接 `BeginDestroy`，`lastMsgID` 只走非无痕分支。

**B3 已知边界（有意接受）**：`notifySessionEnd(userID, convID, "")` 补挂任务时无新消息——
- 若该会话**已有** pending session_end（用户刚聊过）：`debounceSessionEndTask` 检测 `userMsgID==""` 时保留原 `LastMsgID`，取消无痕后后处理照常。
- 若**没有** pending（闲置老会话）：走 `createSessionEndTask`，`LastMsgID` 落空串。此时该会话的上一 session 早已处理完（否则不会有 pending 被消化），后续取消无痕 → 到期 `GetByID("")` 返回 nil、后处理静默跳过，属**无副作用**（没有新内容可处理）。故不额外回查锚点。

**无痕标记的下发路径（P1 修正）**：`GET /conversations/:id/messages-v2` 的响应新增 `ephemeral` 字段（服务端从 `conv.Extra` 解析）。客户端 `loadConversation` 统一 `seedEphemeral(result.ephemeral ?? false)`，覆盖 openConversation / iOS 直连 / 历史回退全部加载路径，避免只在 `openConversation` 注入造成 pill 状态与服务端不一致。

备注：D8 删掉 `newEphemeralChat()` 后，`APIClient.createConversation(ephemeral:)` 变死码 → **已一并清掉该参数**（pill 空白流程改走 `/chat {ephemeral:true}`）。

### 4.5 旧入口 / 快捷键清理

- `SidebarView.swift`（连同无痕右键菜单）：随左栏删除（§4.1.1）。
- ⌘B：删 `AppShortcutBridge.onToggleLeftSidebar`（`AppShortcutBridge.swift:17-18`）+ `App.swift:112-117` 分支 + `ContentView.swift:338-340` 注册 + `ShortcutGuideView.swift:23` 行（D1）。
- **⌘⇧N 删除（D8）**：删 `AppShortcutBridge.onNewEphemeralChat`（`AppShortcutBridge.swift:23-24`）+ `App.swift:119-125` 分支 + `ContentView.swift:344-346` 注册 + `ShortcutGuideView.swift:22` 行；`ChatTabManager.newEphemeralChat()`（`:121-130`）随之成死码，**一并删**（无痕改走输入栏 pill）。
- ⌘N / ⌘⇧↑↓：保留（功能仍在）。
- 补 `AppShortcutBridge` 头注释遗漏的 ⌘V。

## 5. 风险与边界

| 风险 | 说明 | 缓解 |
|------|------|------|
| **PiP 缩窗溢出回潮** | 原靠"先收左栏降底线"，现删除该机制 | §4.1.3 收敛后真机重跑 bug 014 场景（**已验收通过**）；`windowWillResize` + 顶栏内在宽探针兜底 |
| 顶栏内在宽反噬窗口最小宽 | `VStack` 宽度取子项最大（bug 014 机制） | trigger `maxWidth:240` + 截断（D5）；隐藏探针量测顶栏内在宽，窗口最小宽取其较大者，变宽时主动放大窗口 |
| 删除范围超预期 | 左 seam/派生常量/导航层牵连广 | 按 §4.1.1–4.1.4 分步，每步 `swiftc -typecheck` |
| 老会话转无痕启动销毁倒计时 | 停聊约 30 分钟后 Stage A 不可逆 | pill 可再点取消；hover 明示；默认会话禁用；不弹确认（D4 知情） |
| 空会话标了不销毁 | 无 session_end 任务 | B3 补挂任务 |
| 空 `userMsgID` panic | `cron_runner.go:315/339` 对 `userMsgID[:8]` 无长度检查，转无痕无新消息 | B3 改为 nil-safe `truncate(userMsgID)` |
| `Extra` 缺 key | 老会话 `Extra==""` | 客户端 `ephemeral ?? false` |
| 会话列表不加载 | 初始 `load()` 原挂在被删的 SidebarView | §4.2 下拉 `onAppear` 触发（硬依赖） |
| iOS 连带破坏 | 共享文件里有 iOS 引用 | 严守 §2.8 红线；删共享 struct 前先 grep iOS 引用 |

## 6. 决定（已拍板）

- **D1**：删 ⌘B（不再有左栏），同步 `ShortcutGuideView`。
- **D2**：无痕 pill 默认**收起**（右键可常驻）。
- **D3**：（作废）左槽无浏览器冲突，无需让位策略。
- **D4**：老会话转无痕**不弹**二次确认。
- **D5**：顶栏 trigger 最大宽 **240**，超长截断。
- **D6**：**删** `NavigationRouter` + `SidebarTab`，无逻辑迁移（刷新门控本就是死码）。
- **D7**：接受放弃"自动收窄左栏"，窗口最小宽按 §4.1.3 收敛。
- **D8**：**删 ⌘⇧N**（`onNewEphemeralChat`）；`newEphemeralChat()` 一并删。
- **D9**：顶栏左区 **ⓑ 单开 leading View**（不动 registry / `TopBarButton` 模型）。
- **D10**：无痕/会话在服务端消失时**静默销毁**——不弹提示，静默关 tab + 移除列表项（范围＝一切「会话消失」，含他端删除）。

## 7. 估算（开工前）

`6 面 / 3 缝（1 条后端端点需先建 curl 探针）/ 2~3 次真机往返 / 9 个决定（已拍）`，约 **7–11 小时**（AI 会话工时，区间）。定稿口径：删左栏触及 seam + 派生常量 + 导航层三处；无痕 pill 需后端 1 端点 + `RouterConfig` 接线；顶栏集成取 ⓑ。

## 8. 分期

1. **删左栏（含 seam + 派生常量 + 导航层/死码）** + **顶栏会话下拉**（纯客户端，可见效果）。
2. **输入栏无痕 pill** + 后端 B1/B2/B3（含接线 + nil-safe 日志）。
3. **收尾**：旧入口/⌘B/⌘⇧N 清理 + `ShortcutGuideView` 同步 + 过期注释/文档修正 + 真机 PiP 缩窗验收。

## 9. 交付物

- 客户端：`ContentView.swift`、`TopBarView.swift`、`LayoutConstants.swift`、`ChatView.swift`、`InputBarTypes.swift`、`InputBarLayoutState.swift`、`ChatTabManager.swift`、`xHelperMac/App.swift`、`ShortcutGuideView.swift`；**删除** `SidebarView.swift`、`NavigationRouter.swift`、`HoverRevealButton.swift`（删调用后成孤儿）。
- 后端：`controller/chat.go`、`conversations.go`、`router.go`、`appinit/router.go`、`agent/cron_runner.go`（nil-safe 日志）。
- 文档同步：`docs/design/ephemeral-conversation.md`（破「不可变」约定）、`docs/design/archive/macos-left-sidebar-toggle.md`（已废弃归档）、`mac-apps/topbar-slot-system.md`（槽位现状修正）。
- UI 预览：`xHelperMac/TwoColumnLayoutPreview.swift`（真 token + 生产卡片材质 + `#Preview` 四形态，Xcode 画布 ⌘⌥P 看）。
