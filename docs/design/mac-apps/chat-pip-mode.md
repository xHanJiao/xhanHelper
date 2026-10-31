# 聊天画中画模式（Chat PiP）

> 日期：2026-08-03
> 分支：feat/chat-pip
> 范围：macOS 客户端。右栏浏览器打开时可选进入 PiP —— 中间聊天区让位给浏览器全宽，聊天缩成可拖动、可收起/展开的悬浮面板。

## 一、目标

用户在右栏浏览器里看网页时，想要"像朋友一样"边看边讨论。本期（V1）先解决**布局形态**：

- 右栏浏览器打开时，浏览器 tab 条出现 PiP 切换按钮（可选功能，不进 PiP 一切照旧）。
- 进入 PiP：中间聊天区隐藏，右栏自动扩展到全宽（原聊天+右栏空间）。
- 聊天以小型悬浮面板盖在浏览器上，可拖动、边缘吸附、收起/展开。
- 面板字号跟随全局字号缩至 0.75 倍（紧凑字号）。
- 全程可诊断（预埋双层日志）。

V2+（本期只做设计预留，不实现）：
- 收起态插槽换成"动画小人"（git 历史 `ef0c77f` 的 `SpritePetView`/`SpriteSheet`/`PetWindowController` 可原样恢复）。
- "上网伙伴"：感知 app 内浏览器内容，按事件主动评论。

## 二、用户决策记录

| 决策点 | 结论 |
|--------|------|
| 面板形态 | 主窗口内悬浮层（非独立 NSPanel） |
| 右栏扩展 | 进入时自动扩到全宽，退出恢复进入前宽度 |
| 按钮位置 | 右栏浏览器 tab 条（仅浏览器态显示） |
| 字号方案 | 注入紧凑 FontSizeManager（跟随全局 Cmd +/-） |
| 拖拽边界 | 窗口内钳制 + 边缘吸附 |
| 内容感知范围 | 仅 app 内浏览器（WKWebView JS 注入） |
| PiP 状态归属 | RightSidebarManager |
| 日志策略 | DEBUG 用 print，release 用 LogBuffer |

## 三、状态与触发

### 3.1 状态

`RightSidebarManager` 新增（均持久化到 UserDefaults，沿用 width 的既有模式）：

- `@Published var isPipChat: Bool`
- `@Published var pipPosition: CGPoint`（`xhan_pip_chat_x/y`）
- `@Published var pipCollapsed: Bool`（`xhan_pip_chat_collapsed`，记住上次收起态）

### 3.2 进入/退出

- 按钮：`RightSidebarView` 浏览器 tab 条最右侧（`+` 新标签页按钮左侧）PiP 图标，`!isBrowseActive` 时显示，激活高亮，点击 `manager.isPipChat.toggle()`。
- 面板头部提供退出按钮（`✕`）。
- **自动退出**两个触发器（`ContentView` onChange）：
  1. 右栏关闭（`rightSidebar.isOpen → false`）→ `reason=sidebarClosed`
  2. 左侧导航切到非 chat tab（`router.currentTab`）→ `reason=tabChanged`
- PiP 是「右栏最大化」的层级状态，**与右栏内容解耦**：切到浏览面板（Cmd+1/2/3 文档/灵阅/监控）、Cmd+4 进浏览器、关光浏览器标签，都不退出 PiP。

## 四、布局联动（ContentView）

### 4.1 宽度数学（pip 感知）

```swift
/// 中间跨度：PiP 时中间聊天区消失，只剩一个 seam
private var middleSpan: Double {
    rightSidebar.isPipChat
        ? Double(Layout.seamWidth)
        : Double(Layout.seamWidth + Layout.middleMinWidth + Layout.seamWidth)
}
```

`rightSidebarMaxAvailable` / `leftSidebarMaxAvailable` 均改用 `middleSpan` 推导。

### 4.2 进入/退出

- `enterPipChat()`：存 `prePipSidebarWidth` → `isPipChat = true` → `rightSidebar.width = rightSidebarMaxAvailable` → `updateWindowMinWidth()`。
- `exitPipChat(reason:)`：`isPipChat = false` → `rightSidebar.width = min(prePipSidebarWidth, rightSidebarMaxAvailable)` → `updateWindowMinWidth()`。
- `updateWindowMinWidth()`：**单一派生函数**，由 `(isOpen, isPipChat)` 实时推导窗口最小内容宽，幂等设置。消除"进入保存/退出恢复"的竞态。
  （2026-10-11 起写的是 `WindowMinWidthStore`，由根视图 `.frame(minWidth:)` 落到 AppKit `contentMinSize`；
  详见 `docs/bugs/033`。`rightSidebarMaxAvailable` 同时改为按**窗口真实内容宽**算，不再用会溢出失真的 `geo.size.width`。）

PiP 内容最小宽 ≈ `sidebarMinWidth + sidebarOuterPadding + seam + rightSidebarMinWidth + rightSidebarOuterPadding` ≈ 453。

### 4.3 中间区与面板

- `macOSContentArea` 在 `isPipChat` 时整个隐藏（PiP 只在 `.chat` tab 有意义，切 tab 会自动退出）。
- 面板 overlay 渲染在 HStack 的 `.overlay`（topLeading ZStack，`position(x:y:)` 绝对定位），与 ResizableDivider 手柄 overlay 并列。
- 面板出现/消失用独立动画（scale/opacity），**不做两边 cross-fade**——保证同一时刻只有一个 ChatView 实例（InputBar 焦点/双订阅问题）。

### 4.4 ResizableDivider

- `isPipChat` 时隐藏右手柄（两个 seam 重合）；左手柄保留调左栏宽。
- 已知权衡：面板可能盖住左手柄，需先挪开面板才能调左栏宽。

### 4.5 窗口 resize 联动

- 现有 `onChange(of: geo.size.width)`（ContentView 现有逻辑）里，追加**面板位置重钳制**，与 `clampRightSidebarWidth()` 并列、同源处理。

## 五、浮动面板（FloatingChatPanel）

### 5.1 结构

- 复用 `ChatPage`（同 viewModel：streaming / L3 工具确认条 / 计划便签全部自动生效）。
- 子树注入 `FontSizeManager.compact()`。
- 头部：会话标题 + 拖动区 + `[收起] [展开] [退出]`。

### 5.2 尺寸策略（跟随容器收敛）

```swift
// 面板尺寸 = min(标准, 容器 − 2×边距)，下限兜底
panelW = max(Layout.floatingPanelMinWidth, min(Layout.floatingPanelWidth, containerW − 2×margin))
panelH = max(Layout.floatingPanelMinHeight, min(Layout.floatingPanelHeight, containerH − 2×margin))
```

- 窗口够大：固定 400×560。
- 窗口小于面板：收敛到容器内（高度优先压缩），**永不出界**。
- 地板：`floatingPanelMinWidth=240`、`floatingPanelMinHeight=200`，被窗口既有最小值天然保证（宽 ~453、高内容区 ~360）。
- **重要**：拖动钳制与边缘吸附必须用"当前计算出的面板尺寸"，不能写死标准尺寸。

### 5.3 拖动 + 边缘吸附

- DragGesture：展开态拖头部，收起态拖整条。
- onChanged：候选位置 = 起点 + translation，钳制到 `[边距, 容器−面板尺寸−边距]`，实时写回 `pipPosition`。
- onEnded：距最近边缘 ≤ `floatingPanelSnapThreshold(16)` 时，吸附到该边（留 `floatingPanelEdgeMargin(8)`）；否则留在钳制位。x/y 独立判定。
- 收起态吸附体验最佳（小条贴边停靠）。

### 5.4 收起/展开

- `pipCollapsed` 记忆。
- 收起态：400×40 小条（会话标题 + 流式脉冲点 + 展开/退出）。
- `collapsedContent` 槽位：V1 放小条；V2 插 `SpritePetView`（git `ef0c77f` 恢复），流式通知机制（ChatViewModel post work/celebrate）当时已接好。

### 5.5 初始位置

- 无存储值 → 右上角 `(containerW − panelW − margin, margin)`；onAppear 钳制（防上次窗口变小导致越界）。

## 六、紧凑字号（FontSizeManager）

- 增加 `init(factor: Double, persist: Bool)`；`scaledSize = baseSize × factor × scale`。
- `static func compact()`：`factor = 0.75`、不持久化、**订阅 `shared.$scale` 同步**（面板里 Cmd +/- 按比例变化）。
- 现有 `scale` didSet 持久化逻辑保持，compact 实例跳过。

## 七、日志（预先埋设）

### 7.1 策略

统一入口 `pipLog()`，一处定义双层策略：

```swift
func pipLog(_ message: String, file: String = #file, line: Int = #line) {
    #if DEBUG
    print("[PiP] \(message)")                                    // debug：Xcode 控制台
    #else
    LogBuffer.shared.diag("[PiP] \(message)", file: file, line: line)  // release：落文件
    #endif
}
```

`LogBuffer` 新增**恒编译**的 `diag(_:file:line:)`（现有 `debug()/info()` 均为 `#if DEBUG` 门控，release 里调用会消失）。

### 7.2 埋点清单

| 埋点 | 位置 | 内容 |
|------|------|------|
| 进入 PiP | enterPipChat | prePipWidth / targetWidth / minContentWidth |
| 退出 PiP | exitPipChat | reason / restoredWidth / minContentWidth |
| 宽度数学 | ContentView | rightSidebarMaxAvailable 常态 vs PiP、windowW、leftW |
| 右栏 clamp | clampRightSidebarWidth | width、原因 |
| 面板 appear | FloatingChatPanel | container=WxH / pos / size |
| 拖动开始/结束 | DragGesture | from / to(clamped) / snapped |
| 容器 resize 重钳制 | onChange(geo.size) | oldPos → newPos、container |
| 收起/展开 | FloatingChatPanel | state、当时 isStreaming |
| compact 字号 | FontSizeManager.compact | global= / effective= |
| PiP 按钮 | RightSidebarView | toggle、tabs、activeTab |

## 八、V2+ 预留（不实现）

### 8.1 上网伙伴（感知 + 主动评论）

- 现有基础：`BrowserToolProvider.TabSession` 已能 `evaluateJavaScript`（snapshot/get_text 即此机制）；agent_browser 工具**按需**读页面已成熟。
- **被动感知**：注入 `WKUserScript`（document start）+ `WKScriptMessageHandler`，把导航 / DOM 变化（MutationObserver，debounce）/ scroll-idle 事件推回客户端；内容只在本机，按需才进 LLM。
- **低成本第一步**：把当前页面上下文（url/title/outline）注入既有对话 system prompt，让"看到你在看 X"自然可答，无需主动评论通道。
- **主动评论**：新消息通道（后端 companion 消息 or 客户端空闲时系统身份触发）+ 频率控制（每页 ≤1 条）+ 静音开关 + 忙时守卫（agent 在跑任务不发）+ 不抢焦点的气泡。
- 日志预留：`[Companion] nav: url/title` / `content extracted: chars/mode` / `comment suppressedBy=(rateLimit|muted|busy)`。

### 8.2 动画小人

- 收起态 `collapsedContent` 插 `SpritePetView`（git `ef0c77f` + `5fddd41` 的文件：`SpriteSheet.swift` / `SpritePetView.swift` / `PetWindowController.swift` / `robot.png` 从历史恢复）。
- 流式开始/结束通知（ChatViewModel 已接）驱动 work/celebrate 动画。

## 九、改动文件

| 文件 | 改动 |
|------|------|
| `docs/design/mac-apps/chat-pip-mode.md` | 新增（本文） |
| `client/xHelper/xHelper/Shared/Services/LogBuffer.swift` | 新增恒编译 `diag()` |
| `client/xHelper/xHelper/Shared/Services/PiPLog.swift` | 新增双层日志助手 |
| `client/xHelper/xHelper/Shared/Views/LayoutConstants.swift` | 面板尺寸/吸附/字号常量 |
| `client/xHelper/xHelper/Shared/Services/FontSizeManager.swift` | factor + compact() + 跟随后台 |
| `client/xHelper/xHelper/Shared/Views/RightSidebarManager.swift` | isPipChat/pipPosition/pipCollapsed |
| `client/xHelper/xHelper/Shared/Views/RightSidebarView.swift` | PiP 按钮 |
| `client/xHelper/xHelper/Shared/Views/FloatingChatPanel.swift` | 新增（面板本体） |
| `client/xHelper/xHelper/Shared/Views/ContentView.swift` | 布局联动 + 自动退出 + overlay + 重钳制 |

## 十、验证

- 客户端代码不跑 xcodebuild（约定），由用户在 Xcode 编译验证。
- 验证点：
  1. 打开右栏浏览器 → tab 条出现 PiP 按钮
  2. 进 PiP → 聊天区消失、浏览器全宽、面板右上角出现
  3. 拖动面板 → 钳制在窗口内；拖近边缘 → 吸附
  4. 收起/展开 → 小条/完整面板切换，流式脉冲点工作
  5. 面板输入、L3 工具确认、停止生成照常
  6. Cmd +/- 在面板内按比例缩放
  7. 窗口缩小 → 面板收敛不出界；最大化 → 面板保持标准尺寸
  8. 退出路径（✕ / 按钮 / 关右栏 / 切 tab / 清空 tab）均恢复布局
  9. DEBUG 控制台见 `[PiP]` print；release 见 LogDiagnosticView

## 十一、后续变更

- 2026-10-31：修「最大化侧栏时输入框里的图丢失」。进出 PiP 会拆掉并重建中栏 `ChatView`，`@State` 随之销毁——草稿类状态（`draftAttachments`）必须放 `ChatViewModel`（与 `draftText` 同层），不能留在视图里。同理 `isUploadingAttachments`（上传闸门）也挪进 VM：上传 Task 不随视图取消，标志留在视图会被重建复位，闸门失效。影响 `ChatView.swift` / `ChatViewModel.swift`。
- 2026-10-31（已知未修）：重建后 `inputFocused` 复位为 false，⌘V 的 `pastedImages` 会暂存在 VM，等新输入框重新上报焦点才被消费；不丢数据，属现有设计。

