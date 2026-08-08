# 浏览器 tab 关闭后 WKWebView 内存泄漏

## 现象

- **触发方式**：打开 2~4 个网页（如 xiaohongshu/bilibili/抖音），全部关闭。
- **表现**：关闭 3 分钟后，Activity Monitor 仍能观察到已开过网页的 WebKit 子进程（如 `xiaohongshu.com` 占用 240MB、`bilibili.com` 占用 120MB）。退出 App 后进程才消失。**每开一个页面对应新增一个常驻进程，纯累积泄漏。**
- **影响范围**：所有通过浏览器打开的网页 tab（`handleOpen`、`openBlankTab`、`openPopupTab` 三条路径均受影响）。
- **严重程度**：高，内存持续积压，长期运行不重启将导致 app 交换/占用飙升，但**不会崩、不报错、不卡 UI**——纯无声泄漏。

## 根因分析

### 引用链

`BrowserToolProvider.swift` 的 `bindSessionTitle()` 方法为每个 TabSession 安装 URL 变化回调：

```swift
// 修复前
session.onURLChanged = { [weak self] url in
    self?.handleNavigation(session: session, tabId: tabId, url: url)
    //                           ^^^^^^^ 强捕获 session！
}
```

闭包体中的 `handleNavigation(session: session, ...)` 引用了 `session` 参数，Swift 自动对其做**强捕获**。而该闭包又存储在 `session.onURLChanged` 属性上，形成**永久引用环**：

```
TabSession
  ├── onURLChanged ────→ (closure)
  │                        └── captures → TabSession (strong)
  ├── webView (WKWebView)
  │     └── navigationDelegate → TabSession (weak，不参与环)
  │     └── 持有 GPU 纹理 + JS 堆 → WebKit 子进程 (240MB+)
  └── ... (deinit 永不触发)
```

### 为什么 `tearDownWebView` 没兜住

关闭 tab 时调用链为 `performCloseTab → cancelLoadWait → tearDownWebView`。`tearDownWebView` 做了以下清理：

- `cleanupObservations()` — 失效 KVO
- `webView.stopLoading()` — 停止导航
- `removeAllUserScripts()` — 移除注入脚本
- `navigationDelegate/uiDelegate = nil` — 断开 delegate
- `removeFromSuperview()` — 从视图层级移除

**唯独没有置空回调闭包**（`onTitleChange`、`onURLChanged`、`onNewWindowRequest`、`onJSAlert`、`onJSConfirm`、`onNavigationError`）。所以即使 `tabs` 字典移除、侧栏 `AnyView` 释放，环还在，Session 永远不死。

### 其他闭包排查结论

| 闭包属性 | 捕获方式 | 是否形成环 |
|---|---|---|
| `onTitleChange` | `[weak self]`，不引用 session | 否 |
| `onURLChanged` | `[weak self]`，**强引用 session** | **是 ← 根因** |
| `onNewWindowRequest` | `[weak self, weak session]` | 否 |
| `onJSAlert` / `onJSConfirm` | `[weak session]` | 否 |
| `onSetupErrorBinding` | `[weak session]` | 否 |
| `readyPollTask` | `[weak self]` | 否 |
| `loadTimeout` Task | 强捕获 self | 闭环 15s 自毁 + cancelLoadWait 置 nil，非永久 |

## 为什么难发现

1. **无声泄漏**：不崩、不报错、不卡顿，用户体验无感知，只有通过 Activity Monitor 观察子进程才能察觉。
2. **表象与根因背离**：关闭 tab 后 `tabs` 字典和侧栏 `RightSidebarManager.tabs` 数组都正确递减，给人“内存已经清理干净”的错觉——但它们只是**清掉了引用 session 的容器**，session 自己被自己的闭包强挂着。
3. **SwiftUI 视图层正常释放**：`BrowserTabView.onDisappear` 确实在关闭时触发了，说明 SwiftUI 的 `AnyView` 释放链是通的。排除了视图层持有的可能性后，才会追到 provider/session 自身的闭环。

## 诊断路径：Logger-Driven 三板斧

### 第一步：deinit 日志打点

在 `TabSession.deinit` 中加 `print("[LeakDiag] TabSession deinit, lastURL=\(...")`。打开 3 个网页 → 全关 → 控制台 **0 条 deinit 日志**。立即确认 session 根本没释放，泄漏是强引用所致，非 WebKit 进程池缓存。

### 第二步：生命周期打点

在关键路径上同时打点，形成对照：

| 打点位置 | 打印内容 | 判读 |
|---|---|---|
| `performCloseTab` 闭后 | `remainingTabs`、`sidebarTabs` | 字典+侧栏是否清干净 |
| `BrowserTabView.onDisappear` | 当前 URL | SwiftUI 视图是否卸载 |
| `TabSession.deinit` | 最后 URL | session 是否释放 |

结果：前三行都打出来了（disappear 4 次，字典和侧栏递减正确），**deinit 0 次**。精确锁定：持有者既不在字典也不在侧栏数组，在 session 自身或 session 闭包内部。

### 第三步：逐层排除闭包持有链

搜索 TabSession 类的所有闭包属性（`onTitleChange`/`onURLChanged`/`onJSAlert`/...）和 Task（`readyPollTask`/`loadTimeout`），逐一核验捕获列表。发现 `onURLChanged` 的 `[weak self]` 无法阻止 weak self 仍通过**被捕获的 `session` 形参**强引用了自身。这是最容易被漏掉的模式。

### 方法论要点
- **先不说话直接加日志**：比 Memory Graph/instrument 更快，一次编译跑 30 秒出结果，成本极低。
- **对照打点多层**：外层（数组/字典）、视图层（onDisappear）、对象层（deinit）三层对照，一次跑就知道泄漏在哪一层。
- **排查闭包环的原则**：存到对象自身属性的闭包，即使 `[weak self]` 也要检查闭包体中是否通过其他方式引用了该对象（形参、`tabs[id]` 查找等）。

## 修复

**BrowserToolProvider.swift，两处改动：**

### 1. 打破引用环（`bindSessionTitle`）

```swift
// 修复后
session.onURLChanged = { [weak self, weak session] url in
    guard let self, let session else { return }
    self.handleNavigation(session: session, tabId: tabId, url: url)
}
```

改为 `[weak self, weak session]` + guard，闭包不再强持有 session → 环断开。

### 2. 纵深防御（`tearDownWebView`）

```swift
session.onTitleChange = nil
session.onURLChanged = nil
session.onNewWindowRequest = nil
session.onJSAlert = nil
session.onJSConfirm = nil
session.onNavigationError = nil
```

关闭时置空全部 6 个回调闭包。即使将来再有人加强了某个闭包的捕获，关闭时也会兜底释放。

### 验证

修复后跑一次开 3 个网页 → 全关 → 控制台 3 条 `TabSession deinit` 日志 → Activity Monitor 子进程在几秒内消失 → 移除诊断日志。

## 预防规则

1. **存到对象自身属性的闭包，若闭包体内通过任何方式引用了该对象，必须使用 `[weak self/weak xxx]`**。不只是显式的 `self.xxx`，也包括函数调用传参、捕获的形参、closure 内的局部变量。
2. **teardown/cleanup/deinit 路径必须置空所有闭包属性**（防御性编程，兜底未来的意外强捕获）。
3. **怀疑内存泄漏时的诊断顺序**：Logger-driven 打点（30 秒）→ 对照多层输出锁定层级 → 逐层排查闭包捕获。不必一开始就用 Memory Graph/Instruments——它们的场景是**已知泄漏对象后的持有链分析**，而打点能帮你更快走到那一步。

---

**相关代码**：`client/xHelper/xHelper/Shared/Tools/BrowserToolProvider.swift` `bindSessionTitle()` / `tearDownWebView()`

**修复提交**：`72d774f` 修复浏览器 tab 关闭后 WKWebView 内存泄漏并完善 agent 标签管理

**诊断日志本**：本次复盘中的 `[LeakDiag]` 打点策略和输出判读方法可直接作为后续泄漏排查模板。
