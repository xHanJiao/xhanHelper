# 067 · 按 F3（调度中心）整个桌面一片空白

- 发现：2026-11-14（prod 版正常使用中，偶发）
- 影响：macOS 客户端；只要用过浏览器（CEF）并留下后台标签，之后随时可能触发

## 表现

- 按 **F3**（调度中心）→ 屏幕一片空白：本 app 的窗口不出现，**其它 app 的窗口也一起不见了**。
- Esc / 再按一次 F3 都回不来，**关掉 xhanHelper 才恢复**。
- 偶发：与开了哪些页面 / 开了多久无关，只跟"此刻有没有停在停车窗里的后台标签"有关。
- 曾在 prod 版上更常见 —— 实为使用习惯差异（dev 里不怎么用浏览器），不是构建配置差异。

## 根因

后台标签（agent 工作区、`activate=false` 的 tab、popup 落脚视图）需要落在一个窗口里 CEF 才肯
`CreateBrowser`，于是 `XHCefParkingWindow()` 给它们一个进程内共享的"停车窗"。它原来的形态是：

```objc
[[NSWindow alloc] initWithContentRect:NSMakeRect(-20000, -20000, 1280, 800) ...];
window.alphaValue = 0;
if (!window.isVisible) [window orderFront:nil];   // 常驻、且是"可见窗口"
```

- `orderFront` 之后 WindowServer 把它当成**在屏窗口**（`CGWindowListCopyWindowInfo` 里
  `onscreen = 1`，bounds `X:-20000 Y:20100 1280×800`），只是坐标在屏幕外 2 万点。
- `alphaValue = 0` **不豁免**：调度中心按「本空间所有窗口的包围盒」布局，完全透明的窗口照样
  参与计算。包围盒因此被撑到约 21341×8870，真实的 1440×900 桌面被压成几像素 →
  屏幕上就是"一片空白、所有窗口都不出现"。
- 这个窗口是进程级单例、`releasedWhenClosed = NO`，**只有退出 app 才会消失** → "关掉 app 就恢复"。
- 同类先例：macOS 26 上 kitty / matplotlib 的"幽灵窗口"就是把调度中心布局顶出大片空白
  （社区表现描述为"多出来一块空白、鼠标悬停显示 exit"）。

关键在**离屏也算、透明也算**，不是"窗口大才出问题"；而"干脆别 orderFront"这条路走不通 ——
停车窗不进窗口层级时 CEF 建不出渲染宿主（白屏，见 066 / 设计文档 B7）。

## 方案

1. `XHCefParkingWindow()`：屏内 `NSMakeRect(0, 0, 1, 1)` + `ignoresMouseEvents = YES`，
   保留 `alphaValue = 0` / `releasedWhenClosed = NO` / "被关掉就 orderFront 救回来"。
   1×1 在调度中心里等于不可见，包围盒不再被撑大。
2. `parkInHiddenWindowIfNeeded`：不再 `self.frame = parking.bounds`（1×1 会把后台 tab 的
   CEF 视口压成 1×1，挂回侧栏时要整页重排），改成固定停车视口 1280×800；
   视图比停车窗大只被裁剪，不影响渲染（`--disable-backgrounding-occluded-windows` 已开）。

## 验法

- 真机探针二分（已做，2026-11-14）：
  - A：`(-20000,-20000)` 1280×800、alpha 0、`orderFront` → 按 F3 **复现**一片空白
  - B：屏内 `(0,0)` 1×1、alpha 0、`ignoresMouseEvents` → 按 F3 **正常**
- app 内验收（Xcode 构建）：
  - 开着浏览器 + 至少一个后台标签，按 F3 → 调度中心正常，其它 app 窗口都在
  - 后台标签仍能加载（标题 + 正文，不是空壳）
  - `window.open` 弹窗仍能打开；关窗驻留后由 agent 开标签仍能加载

> 以后再看到"调度中心一片空白、关某个 app 就恢复"，第一反应查**屏幕外的窗口**：
> `CGWindowListCopyWindowInfo(.optionAll)` 里找 `onscreen = 1` 但坐标离谱的窗口。
