# macOS 窗口最小宽守卫失效 —— delegate 只在启动时挂一次，窗口重建后整条链静默失效

> 2026-10-11 定位。表现：双栏 / PiP 下窗口能被拖到极窄（实测 430pt），内容居中溢出 ——
> 聊天栏左半被裁、顶栏最左的会话下拉被顶出窗外、按钮压到红绿灯上。
> **状态：已修（本机双端 typecheck 通过 + 探针验证落点；待真机验收 §6）。**
> 2026-10-11 代码评审后的收口也已在 §5 记录（删除重复的 `requiredMinContentWidth`、store 独立成文件、
> 侧栏 clamp 补窗口帧层触发点）。

## 1. 问题表现

- 窗口能一路缩到约 430pt 宽（贴屏幕右缘、满屏高），远低于内容真实底线。
- 内容被**居中溢出**：聊天栏只剩右半截（左半被窗口左缘裁掉），顶栏左侧的会话下拉整体被挤出窗口，
  浏览器按钮组从窗口左缘开始画 → 压住红绿灯。
- PiP 模式下同样成立（"最小宽守卫"对 PiP 也是同一条链）。
- 特征：**关窗（红 X，app 驻留）→ 从 Dock 重开之后才出现**；上一次窗口正常。

## 2. 根因（怎么引入的）

> 下面的行号是**修复前**的代码位置（修复后该处已删/已改，见 §5）。

不是某次提交改坏的 —— `git log` 里这一带没有语义改动。它是**设计缺陷**，三个脆弱点叠加：

1. **delegate 只在启动时挂一次**（`App.swift:228-230`，`applicationDidFinishLaunching` 里
   `for window in NSApp.windows { window.delegate = self }`）。SwiftUI 在关闭最后一个窗口后
   再打开时**会新建 NSWindow**（代码里自己的注释也写了"旧窗口有时已被释放、有时还活着"）。
   新窗口经 `LocalWindowReader`（`App.swift:554-560` → `LocalSettingsViews.swift:658-681`）
   照样被 `configureWindow` 配置，**外观正常，但 delegate 是 SwiftUI 自己的**。
2. **唯一的执行器是 `windowWillResize`**（`App.swift:294-303`）。苹果文档明说它**只在用户交互式
   拖拽窗口时调用**：程序化 `setFrame`、zoom 还原、系统平铺等都走不到。它被当成唯一防线。
3. **动态值走全局 static**（`AppDelegate.requiredMinContentWidth`，`App.swift:233-234`，
   由 `ContentView.updateWindowMinWidth` `ContentView.swift:81-103` 写入）。跨层共享可变状态，
   一旦窗口不是"启动那一个"，这个值再正确也没人读。

三条叠加的后果：没有 delegate ⇒ 唯一剩下的下限是 AppKit 从 SwiftUI 根视图
`.frame(minWidth: 400)`（`App.swift:694`）算出的 `contentMinSize = 400`。
而双栏 + 浏览器顶栏的内容真实最小宽 ≈ 900（顶栏最宽，两栏 778 次之）。

## 3. 定位证据（可复现）

| # | 证据 | 取法 |
|---|------|------|
| 1 | 运行中窗口 = `x=1010 y=30 430x870` | `CGWindowListCopyWindowInfo`（非截图推测）；截图 806x1734px 是 2x，即 403x867pt |
| 2 | 截图几何反推内容 = 6+380+6+510+6 = 908，居中塞进 430 → 左侧溢出 239pt；预测聊天栏可见 147pt(294px)、右栏左缘落在 291px | 截图逐行像素扫描，实测 ~288px / ~290px |
| 3 | 截图同刻日志：`right sidebar clamp: 787 -> 510` + `geo.width 变化 → 903.5` | `~/Library/Caches/com.xhan.xhanHelper/logs/current.log`，2026-10-11T08:37:17Z |
| 4 | 根 `.task` 的 `ContentBlocker.load()` 全日志 3 次，其中两次**同一进程**：10-10 22:17（`ps` 证实进程启动）与 **10-11 09:06** | 同上日志 |
| 5 | 复刻探针 app（/tmp，未动工程）：拖边缘时 `windowWillResize` 每帧被调用、窗口卡在 900；同 app 的 SwiftUI `contentMinSize` 只有 400 | 临时 SwiftUI app + 合成鼠标事件 |
| 6 | 该窗口已把 `430x870` 写进持久化 frame（当前 build 的 WindowGroup 签名 key） | `~/Library/Preferences/xhanx.cn.xHelperMac.plist` |

**落点探针（改代码前跑的，决定"主约束"写在哪）**

| # | 结论 | 取法 |
|---|------|------|
| 7 | 直接写 `window.contentMinSize = 900` → 下一轮布局**被 SwiftUI 覆盖回**根视图声明值（400）⇒ 这条路不可用 | 临时探针 app，逐 tick 打印 `contentMinSize` |
| 8 | 把动态 min 声明在**根视图** `.frame(minWidth:)`（含"放在 `_ConditionalContent` 分支里"的真实结构）→ AppKit `contentMinSize` 实时跟随（400→900）且保持；拖边缘被卡在 900 ⇒ 这条路可用 | 同上 + 合成鼠标拖拽 |
| 9 | App 层读"子视图写入的单例 store"能收到更新 ⇒ 「ContentView 写 / App 层根视图读」这条链成立 | 同上（`@StateObject` + 外部单例对照） |
| 10 | SwiftUI 自己**会**给窗口挂 delegate（内部类 `AppKitWindowController`，在 `viewDidMoveToWindow` 时已存在）；我们在其后覆盖成 `AppDelegate`，**不会被抢回去**（持续观察 6s 不变）⇒ 在"每窗口一次"的入口挂是安全且充分的 | 同上（打印 `window.delegate` 类型） |

结论：**用户此刻看的窗口没有接上 `AppDelegate`**，所以 `windowWillResize`（顺带 `windowShouldClose`
的驻留提示）对这个窗口一次都不触发；而"把 min 写进 `contentMinSize`"这种补法又被 SwiftUI 覆盖，必须从根视图声明。

## 4. 顺带查出的相邻问题（一个真缺陷 + 一个只是噪音）

- **侧栏 clamp 用错了尺子**（`ContentView.swift`）：`rightSidebarMaxAvailable`
  用 `geo.size.width` 当"可用宽"，但内容溢出时 geo 报的是**溢出后的内容宽**（908）而不是窗口宽
  （430）→ 永远算不出该收多窄，自锁（证据 3 的 `787→510` 即此）。**已随本次修掉**（改用窗口真实内容宽）。
- **PiP 拖动期间的逐帧 churn**：`right sidebar fill`（按 geo 写 `rightSidebar.width`）与
  `FloatingChatPanel.re-clamp` 在拖拽时各刷一行；日志里 1 秒 60+ 行是"每帧一对"，容器按帧递减
  （1122→703，步长从几十 px 收敛到 1px），**是拖拽本身在推进，不是自激振荡**。不构成缺陷，
  只是每次拖拽都刷两组日志（后续可只在窗口宽真变化时 fill）。**本次未动。**

## 5. 最终方案（已落地，待真机验收）

底线口径按 **A**：窗口硬下限 = `max(底栏推导, 顶栏内在宽, 750)`（顶栏不降级，~900 就是有浏览器时的真实底线）。
先跑探针定落点（结论见 §3 证据 7-10），再按根因的三个脆弱点逐个拆：

1. **主约束改为根视图声明**：新增 `WindowMinWidthStore`（`xHelperMac/WindowMinWidthStore.swift`，
   **唯一来源**）—— `ContentView.updateWindowMinWidth()` 把算出的值写进 store；App 层两处 `ContentView(...)`
   的 `.frame(minWidth: 400, …)` 改读 `windowMinWidth.width`。SwiftUI 会把它落到 `NSWindow.contentMinSize`，
   这是唯一被所有缩放路径（拖拽 / 缩放 / 系统平铺 / 程序化）尊重的执行器。
2. **每个窗口都挂 delegate**：`LocalWindowReader` 回调里 `window.delegate = appDelegate`；
   删掉 `applicationDidFinishLaunching` 里"只挂当时那些窗口"的循环。`windowWillResize` 保留为
   **第二道栅栏**（只覆盖用户拖拽这条路径），且**直接读 `WindowMinWidthStore.shared.width`** ——
   原 `AppDelegate.requiredMinContentWidth` 静态量已删除（两份拷贝必须成对写，正是本 bug 的同一种失败形状）。
   顺带把 `LocalWindowReader` 的"只上报一次"改成"窗口实例变了就重新上报"（否则窗口被复用时同样会漏挂）。
3. **侧栏换尺子**：新增 `realWindowContentWidth()`（读 `NSWindow.contentView` 宽），
   `rightSidebarMaxAvailable` / 分隔条 `rightMax` / 两处扩窗 deficit 全部不再用会溢出失真的 `geo.size.width`。
4. **复位**：ContentView `onDisappear` 时把最小宽复位成 `defaultWidth` —— 登出 / 未配置页 / 解锁门的窗口
   不会被上个界面算出的底线卡住（delegate 现在挂在每个窗口上，不复位就会卡）。
   注意：**关窗→重开这条路径不依赖复位**，新窗口的 `onAppear` 会把 store 覆盖成正确值。
5. **侧栏 clamp 的第二触发点（缓解，非根治）**：`clampRightSidebarWidth()` 原先只挂在
   `onChange(of: geo.size.width)` 上，而 geo 在内容比窗口宽时会冻结（014 的布点教训）→ 补
   `NSWindow.didResizeNotification` 触发（过滤主窗口）。平铺真把窗口压到内容以下时，这条把侧栏收到 380 下限、
   内容从 ~908 降到 ~778 —— **仍然溢出，只是减损**；根治要么平铺尊重 `contentMinSize`（待实测），
   要么走"窄窗优雅降级"（另起需求）。实测若证明平铺尊重，本条应删回注释。

影响文件：`xHelperMac/WindowMinWidthStore.swift`（新增）、`xHelperMac/App.swift`、
`xHelperMac/LocalSettingsViews.swift`、`Shared/Views/ContentView.swift`。

**未做（另起会话做，现已完成）**：顶栏降级（把底线从 ~900 还给两栏 778）+ 顶栏会话下拉常驻（含 PiP）——
属需求变更，不在本次 bug fix 范围内。**已另立** `docs/design/mac-topbar-degrade/design.md`
（顶栏不再决定窗口最小宽，改按可用宽降级；本 bug 的 `WindowMinWidthStore` → `.frame(minWidth:)` → `contentMinSize`
链不动，顶栏探针降级为安全网）。

**已知残余（写清，避免下次误判）**

- **系统平铺是否尊重 `contentMinSize`**：未验证（本机无法安全复现平铺手势）。若不尊重，平铺路径仍可能把窗口
  压到内容以下；此时 §5.5 只减损不根治。
- **多窗口假设**：`WindowMinWidthStore` 是 App 级共享、`realWindowContentWidth()` 读 `NSApp.mainWindow/keyWindow`
  —— 两个窗口时读到的是当前活动窗口、且两个窗口拿同一个底线。彻底收窄要 per-window 状态（每个窗口一份），
  不在本次范围。代码里已就地注释说明。

## 6. 验收

**本机自检（已完成）**：macOS / iOS 双 target `xcrun swiftc -typecheck` 均 0 error；
主约束落点四条探针见 §3（含"直接写 `contentMinSize` 不可用"与"根视图声明可用"的对照）。

**真机往返（待做）**

- [ ] **关窗 → Dock 重开 → 再拖边缘：必须卡在底线**（本次漏掉的正是这条路径）
- [ ] 重建后的窗口，`windowShouldClose` 恢复生效（第一次关窗出现"仍在后台运行"驻留提示）
- [ ] 双栏 + 浏览器缩窗：不再出现聊天栏被裁 / 顶栏压红绿灯，底线约 900
- [ ] PiP 缩窗无溢出回潮（重跑 `docs/bugs/014` 场景）
- [ ] 登出 / 进首次配置页：窗口不被上一个界面的底线卡住（复位生效）
- [ ] 顺手试一下**系统平铺**（拖窗口到屏幕边缘）：若窗口被压到内容以下，按 §5 残余项①处理
    （平铺尊重 `contentMinSize` → 删掉 §5.5 的第二触发点；不尊重 → 走"窄窗优雅降级"需求）

## 7. 关联文档

- `docs/bugs/014-pip-shrink-sidebar-overflow.md` —— 同一族（同容器内各部分最小宽不一致，顶栏拖累底栏）；
  014 的"内容实测底线 + 收窄降底线"机制已被 014 自己后继删除、并由本文的根视图声明路径取代
- `docs/design/mac-two-column-layout/design.md` §2.6 / §4.1.3 —— 本次改了"窗口最小宽唯一派生入口"，
  已在文档尾部追加「后续变更」（2026-10-11）
- `docs/bugs/025-right-sidebar-width-jump-on-browser.md` —— 右栏宽度抖动前案
- 通用结论（供后续设计参考）：**硬下限要落在 `minSize`/`contentMinSize` 这类平台原生约束上
  （且在 SwiftUI 里必须从根视图声明，直接写 `window.contentMinSize` 会被布局覆盖）；
  工具栏/并排面板不该用"所有元素宽度之和"定义窗口底线，窄了应当降级；父容器宽与子视图宽之间不能互相喂。**
