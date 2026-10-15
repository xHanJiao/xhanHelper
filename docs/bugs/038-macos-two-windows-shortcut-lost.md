# 038 · macOS：同时存在两个主窗口 → 顶栏 `⌘1–5` 整体失灵（事件落到看不见的那个窗口上）

> 2026-10-15 定位并修复。表现：`⌘1–5`（文档/账单/监控/浏览器/记忆）**完全没反应**，但鼠标点顶栏那
> 五个图标一切正常。根因不是快捷键没注册 —— 是 app 里同时活着**两个主窗口**（另一个在已断开的
> 外接屏/iPad 那块屏的空间里），SwiftUI 把 `⌘1–5` 解析到了看不见的那一份上。
> **状态：已修**（主场景 `WindowGroup` → 单窗口 `Window`；macOS + iOS `swiftc -typecheck` 0 error；真机清单见 §4）。

## 1. 问题表现

- 按 `⌘1/2/3/4/5`：**屏幕上什么都没发生**（顶栏无按下反馈，右栏也不切面板）。
- 鼠标点顶栏那五个图标：正常切面板 → 说明动作链路（`TopBarView.openBrowsePanel` / `RightSidebarManager`）没问题。
- 各形态都一样：单窗口、PiP、浏览器聚焦、点一下窗口让它前台后再按 —— 全都不灵。
- 顶栏本身正常：五个图标都在栏上（降级不收导航组）、hover/点击都正常。

## 2. 排查过程（留着给下次：怎么在 5 分钟内证伪/证实）

先排掉的一圈（都不是原因，别重复走）：

| 候选 | 怎么排掉的 |
|---|---|
| 降级把导航组收进 `⋯` → 快捷键一起丢 | 导航组永不被收（`TopBarLayout` / `docs/design/mac-topbar-degrade`） |
| AppDelegate 的 `NSEvent` 监听吞了数字键 | 逐个 monitor 核对：只拦 `⌘Q/⌘N/⌘W/⌘V/⌘⇧B·M·E·↑↓/⌘T` 与浏览器那组，数字键一路 `return event` |
| 快捷键被别处重复注册 | 全仓只有 `TopBarView.swift:376-401` 一处注册 `⌘1–5`；隐藏探针那份走的是"无按钮"分支 |
| 系统/输入法/常驻工具抢键 | Xcode 里 `⌘1` 正常切导航器；`symbolichotkeys` 无数字键占用；输入法是原生 ABC；无 Karabiner 类工具 |
| 包装视图（`CapsuleButton`）挂不上 `keyboardShortcut` | 独立 SwiftUI 探针实测：包装视图 + `⌘数字` + `zIndex` + 卡片重叠 + `Menu`/`popover` 兄弟 + `onAppear` 里注册，**全部正常触发** |
| 同一窗口里其它 `keyboardShortcut` 也坏 | 反证：文档面板的 `⌘F`（`DocumentListView` 里的 `keyboardShortcut`）在同一个窗口里**是好的** |

决定性证据（三条命令）：

```bash
# ① app 有几个"主窗口"？—— 两个：10593 可见，9560 不在屏上
#    （用 CGWindowListCopyWindowInfo(.optionAll) 读 kCGWindowOwnerName == "xhanHelper"）
win=10593 layer=0 onscreen=1 name=xhanHelper 1327x870 @113,30
win=9560  layer=0 onscreen=0 name=xhanHelper 1274x870 @166,31

# ② 那个窗口不在主屏当前空间里：com.apple.spaces 的 Space Properties 只列了 10593
defaults read com.apple.spaces | grep -A8 "Space Properties"

# ③ 它却是"活的完整窗口"：按 ⌘3 后，它的右栏从「账单」跳到「监控看板」（可见窗口纹丝不动）
screencapture -x -l 9560 /tmp/hidden.png
```

结论：`⌘1–5` **确实触发了**，只是触发在那个看不见的窗口的顶栏上。

## 3. 根因

1. 主场景是 `WindowGroup`（多窗口），app 又是「关窗驻留」（`applicationShouldTerminateAfterLastWindowClosed = false`）。
2. 窗口被放到了 iPad（Sidecar，显示 3）那块屏上；那块屏断开后，它的空间被系统折叠（`com.apple.spaces` 里
   只剩一个 `Collapsed Space`）—— **窗口不会被销毁**，只是不渲染（`kCGWindowIsOnscreen=0`），也不出现在
   主屏当前空间的窗口名单 / app 的「窗口」菜单 / 辅助功能里。
3. 用户以为窗口没了 → 点 Dock 图标 / 重新激活 → `applicationShouldHandleReopen(hasVisibleWindows: false)`
   → SwiftUI 的 `WindowGroup` **又建了一个**新窗口（旧的那个还活着）。代码注释里早记过这个现象
   （`App.swift` 旧注释：「旧窗口有时已被释放、有时还活着，所以表现为『偶尔出现两个主窗口』」）。
4. 两个窗口各有一份顶栏 → 各注册一份 `⌘1–5` → SwiftUI 把键解析到了另一份（不是 key window 那一份），
   于是「按键有反应，但反应发生在看不见的窗口里」。

**为什么"以前好使、突然不好使"**：单窗口时不会撞车；只要出现第二个窗口（把窗口丢到外接屏再拔线、
或「文件 > 新建窗口」），`⌘1–5` 就整体失灵 —— 直到把多余窗口清掉（退出重开 / 连回那块屏）。

## 4. 最终方案（2026-10-15）

**主场景 `WindowGroup` → `Window`（单窗口场景）** —— `client/xHelper/xHelperMac/App.swift`：

```swift
Window("xhanHelper", id: "main") { … }
```

- 一份窗口 ⇒ 不会有两份顶栏/两份快捷键注册，从根上消掉这一整类问题（也顺带干掉 `docs/bugs/033` 那类
  「关窗重开变两个窗口」的隐患）。
- 菜单里的「文件 > 新建窗口」随 `Window` 场景自动消失（本来 `⌘N` 已被 AppDelegate 拦成"新建对话"）。
- `.windowStyle(.hiddenTitleBar)` / `.defaultSize` / `.windowResizability` 对 `Window` 同样有效；仓库里没有
  `openWindow` 调用，改动面只有这一处（外加三处注释：`AppDelegate.primaryWindow` / 关窗重开说明 /
  `ContentView.realWindowContentWidth` 的"单窗口假设" / `AppBackdrop`）。
- **实测过 `Window` 场景不会砸「关窗驻留」**（临时 bundle 探针，`applicationShouldTerminateAfterLastWindowClosed=false`）：
  关窗 → 进程仍驻留 → `open`（= 点 Dock 图标）→ 窗口回来；连点 3 次始终只有 1 个窗口。
- 未采纳：顶栏 action 里加"只在本窗口生效"判定（单窗口落地后是多余的第二只手）；保留 `WindowGroup` 只删
  「新建窗口」菜单项（挡不住 Dock 唤回那条路径）。

## 5. 验证

**自检（已完成）**：macOS + iOS `swiftc -typecheck` **0 error**（改动文件 0 新 warning）。

**真机（待跑）**：

1. 重启 app → `⌘1/2/3/4/5` 逐个生效（文档/账单/监控/浏览器/记忆）。
2. 关窗（红 X）→ 点 Dock 图标 → 窗口回来，`⌘1–5` 仍生效。
3. 「文件」菜单里**不再有**「新建窗口」；反复点 Dock 图标不会出现第二个窗口。
4. 顶栏观感不变（`hiddenTitleBar` + 卡片吸顶那套照旧）。

**复发时怎么自查**（不需要读代码）：`CGWindowListCopyWindowInfo(.optionAll)` 看
`kCGWindowOwnerName == "xhanHelper"` 有几个 `layer=0` 且带标题的主窗口；多于一个就中招。

## 6. 已知残余

- 若窗口被留在**已断开的外接屏**那块屏的空间里，`⌘1–5` 不会再失灵（只有一份注册），但**窗口本身**仍然
  在那块屏上、主屏看不到 —— 这是 macOS 的行为，退出重开 / 连回那块屏即可。修法见 `docs/bugs/033 §5`。
- iOS 目标仍是 `WindowGroup`（iPadOS 多窗口是平台特性，与本条无关）。
