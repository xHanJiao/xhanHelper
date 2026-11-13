# 066 · 关一个 CEF 标签会把整个 app 主窗口关掉

- 发现：2026-11-14（CEF 迁移合并进 `main` 之后）
- 影响：macOS 客户端，浏览器内核 = Chromium（CEF）时

## 表现

- 按 **⌘W**（本意关当前浏览标签）→ **主窗口整个消失**，进程还活着（Dock 里还有图标，点回来是新窗口）。
- 点标签的 **X** / 页面自己 `window.close()`（弹窗登录完自关那种）/ agent 的 `agent_browser_close` 有时也会。
- 后台保活 tab 关掉后再开新 tab，偶发白屏（页面永远不 commit）。

## 根因

CEF 的 `CefLifeSpanHandler::DoClose` 我们**没实现**，走的是默认行为。CEF 头文件原文：

> When windowed rendering is enabled … returning false from `DoClose()` will send the standard close
> notification to the browser's top-level parent window (e.g. WM_CLOSE on Windows, **`performClose:` on OS X** …)

macOS 侧对应实现（`browser_platform_delegate_native_mac.mm`）：

```cpp
void CefBrowserPlatformDelegateNativeMac::CloseHostWindow() {
  NSView* nsview = CAST_CEF_WINDOW_HANDLE_TO_NSVIEW(window_info_.view);
  [[nsview window] performSelectorOnMainThread:@selector(performClose:) withObject:nil waitUntilDone:NO];
}
```

我们是用 `SetAsChild` 把 browser 的宿主视图挂在 **app 自己的 NSView** 里（`CefBridge.mm` 的 `createBrowserIfNeeded`），于是 `[nsview window]` 就是 app 主窗口：

- `⌘W` → `closeActiveTab()` → `page.close()` → `CloseBrowser(true)` → `DoClose`（默认 false）→ `performClose:` **主窗口**
- 页面 `window.close()` / agent close → 同一条
- 后台 tab 停在隐藏的 parking window 里时，`performClose:` 打在那个**看不见的窗口**上：主窗口没事（这就是"有时候"），但 parking window 被关掉；它 `releasedWhenClosed = NO`，对象还在、已脱离窗口层级，之后往里挂的 CEF 浏览器建不出渲染宿主 → 白屏

## 方案

1. `XHCefClient` 实现 `DoClose`，**返回 `true`**（= "窗口的事我自己处理，别发 `performClose:`"），并 `RunOnMain` 调 `[view cefBrowserShouldClose]`。
2. `XHCefBrowserView.cefBrowserShouldClose`：
   - `_closeRequested` 已置（⌘W / 关 tab / agent close 发起的）→ 只置 `_cefDoCloseSeen`，不重复通知；
   - 否则（页面自关 / popup 自关）→ 置标记并调 `pageClosedExternally`，宿主关掉这个 tab。
3. 浏览器真正销毁走既有链：宿主关 tab → 视图被释放 → CEF 内部 `CefBrowserHostView` dealloc → `WindowDestroyed()` → `OnBeforeClose` → `cefBrowserDidClose`（renderer 回收）。
   这条链要求**视图真的被释放**：后台标签停在 parking window 里时，`addSubview:` 会强持有它，
   所以 `closeBrowser` 先把"superview 就是 parking contentView"的视图摘掉（否则 view 永不 dealloc、
   renderer 永不回收——评审 P1）。
4. 顺手：`XHCefParkingWindow()` 若已被关掉（`!isVisible`）就 `orderFront` 救回来（alpha 0，用户看不见）。
5. `closeBrowser` 在"已经进过 `DoClose`"时不再重复 `CloseBrowser(true)`（CEF 156 没有 `IsClosing()`，用自己记的 `_cefDoCloseSeen`）。

## 验法

- CEF 标签按 ⌘W：标签关、主窗口在、还能继续操作
- 点标签 X、页面 `window.close()`、agent `close_tab`：同上
- 连关 4 个 tab：主窗口不消失；对应 Renderer 进程 30s 内退出
- 后台保活 tab 关掉后新开 tab：不白屏

## 后续变更

（暂无）
