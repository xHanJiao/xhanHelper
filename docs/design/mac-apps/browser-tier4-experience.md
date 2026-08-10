# 浏览器体验专题（Tier 4）— 跳转修复 + 性能 + 检查工具

> 日期：2026-08-10
> 分支：feat/browser-tier4-experience
> 范围：macOS 右栏浏览器。改动集中在 `Shared/Tools/BrowserToolProvider.swift`（iOS 也编译该文件，但 iOS 无浏览器入口——`BrowserToolProvider` 仅在 `xHelperMac/App.swift` 注册、右栏 UI 仅 macOS 展示，故 iOS 行为不变）。
> 约定：客户端代码 agent 不编译验证，由用户在 Xcode 编译运行（CLAUDE.md）。

## 一、症状与根因分析

### 症状 1：点击链接/按钮偶现无法跳转（均为 https，非外部协议）

| # | 根因候选 | 机制 | 置信度 |
|---|----------|------|--------|
| R1 | **弹窗 tab 未接 UI 回调（已确认的代码 bug）** | `openPopupTab`（L1880）只调 `bindSessionTitle`，**没调 `bindSessionUI`** → 弹窗 tab 内再点 `target=_blank` / `window.open` → `createWebViewWith` 里 `onNewWindowRequest` 为 nil → 返回 nil → WebKit 静默阻止弹窗。JS alert/confirm 同样不弹。复现路径：百度搜索结果点链接（新 tab 打开）→ 在新 tab 里再点 `_blank` 链接 → 无反应。"偶现"正是因为只有当前 tab 本身是弹窗 tab 时才触发 | **高（代码实锤）** |
| R2 | **noopener `target=_blank`** | WebKit 对 `rel="noopener"` 的 _blank 链接**不调** `createWebViewWith`，而是走 `decidePolicyFor navigationAction` 且 `targetFrame == nil`；当前实现（L2307）只查 scheme 就 `.allow` —— 没有目标 frame，导航被静默丢弃。React 系站点普遍自动加 noopener | **高** |
| R3 | `window.open` 非 http(s) 目标 | 走确认弹窗路径且同步 `return nil` → JS 侧拿到 null，依赖返回句柄的流程断（OAuth 等场景） | 低（少见） |
| R4 | 点击与进行中导航的竞争等 | 待日志验证 | 待验证 |

### 症状 2：抖音/小红书卡顿（框架先渲染 → 卡一下 → 自动播放；Chrome 无此现象）

| # | 因素 | 机制 | 可控性 |
|---|------|------|--------|
| P1 | **每 tab 独立 `WKProcessPool`** | `TabSession()` 每次 new `WKWebViewConfiguration()` → 各自独立进程池 → WebContent 进程无法跨 tab 复用、无法预热；新 tab 首次导航承担冷进程启动成本；多 tab 内存压力大（更易触发系统节流） | ✅ 可控 |
| P2 | 每页 document-start 注入的 a11y 脚本 | 包装全局 `addEventListener/removeEventListener`（每次注册过 WeakMap）；重 SPA 启动期注册量大。该脚本只服务 agent 点击兜底（`hasListener` 查询），普通浏览白付成本 | ✅ 可控（但影响 agent 能力，需权衡） |
| P3 | `isFraudulentWebsiteWarningEnabled = true` | 每次导航做安全浏览检查（国内走腾讯源），可能增加导航启动延迟 | ✅ 可控（有安全取舍） |
| P4 | WebKit vs Chromium 引擎差异 | MSE 起播、解码管线、JIT 预热差异 | ❌ 不可完全抹平，需管理预期 |

结论：**先埋点量化（P1 的导航各阶段耗时 + FCP），拿基线数据后再做优化并对比**，避免盲改。

### 症状 3：缺少右键"检查"控制台

`WKWebView.isInspectable` 未设置。部署目标 macOS 26.3（≥ 13.3），无条件可用。开启后右键出现"检查元素"，调出完整 Safari Web Inspector（元素/控制台/网络/源代码），等价 Chrome 检查功能。

## 二、方案

### P1：诊断埋点 + 跳转修复 + 检查元素（先行，一次交付）

#### 1. 诊断日志（`LogBuffer.diag`：恒编译、写日志文件、LogDiagnosticView 可查看导出）

- **`decidePolicyFor navigationAction`**：仅记录主 frame 及 `targetFrame == nil` 的导航（iframe 噪音过滤）：
  `[BrowserNav] action type=linkActivated host=... target=main|nil decision=allow|cancel reason=...`
- **`createWebViewWith`**：源 host → 目标 URL、navigationType、最终是否成功开 tab（含 bindSessionUI 未接线的 nil 返回，修复后用于回归确认）。
- **导航计时**：`didStartProvisionalNavigation` / `didCommit` / `didFinish` 打点；`didFinish` 后 evaluateJavaScript 读 `PerformanceNavigationTiming`（responseStart=TTFB、domContentLoadedEventEnd、loadEventEnd）+ `first-contentful-paint`，单行输出：
  `[BrowserPerf] host=... ttfb=...ms dcl=...ms fcp=...ms load=...ms native(prov→commit=... commit→finish=...)`
- 日志用途：症状 1 复现时定位（哪类导航被丢/被拦）；症状 2 的基线数据（P2 优化前后同页面同路径对比）。

#### 2. 跳转修复

- **R2 修复**：`decidePolicyFor navigationAction` 中 `navigationAction.targetFrame == nil` 且 http(s) → 调 `openURL(_:)` 开新 tab + `.cancel`（对齐 Chrome 的 noopener 行为）。接线：TabSession 新增 `onNoTargetNavigation: ((URL) -> Void)?` 回调，`bindSessionUI` 中接到 `self.openURL(url)`。注意 `shouldPerformDownload` 判断在前，不受影响。
- **R1 修复**：`openPopupTab` 补 `bindSessionUI(session: newSession)`（修嵌套 _blank + JS 弹窗；`onNewWindowRequest` 闭包内 weak 引用指向 newSession，与 openBlankTab/openURL 行为一致）。
- R3 本期不动（确认弹窗保留）；R4 靠日志定论。

#### 3. 检查元素

- `makeWebView` 末尾 `wv.isInspectable = true`（一行）。右键 → 检查元素 → Web Inspector 独立窗口。

### P2：性能优化（P1 数据到手后实施，另起提交）

1. **共享 `WKProcessPool`**：`BrowserToolProvider` 加 `static let sharedProcessPool = WKProcessPool()`；`TabSession` 默认 `init()` 自建 configuration 时赋 `configuration.processPool`；**popup 的系统传入 configuration 不动**（保持 opener 关系与 WebKit 约束）。
2. **WebContent 进程预热**：首次 `enterBrowser()`/开首个 tab 前，用共享 pool 建离屏 WKWebView load `about:blank`，提前拉起暖进程，消除首次导航冷启动。
3. **显式媒体配置**：`configuration.mediaTypesRequiringUserActionForPlayback = .none`（保证自动播放策略确定）；`allowsPictureInPictureMediaPlayback = true`（按编译可用性补 `allowsAirPlayForMediaPlayback`）。
4. **候选项（看基线数据再定）**：a11y 脚本轻量化或按需注入（权衡 agent 点击兜底能力）；关闭 `isFraudulentWebsiteWarningEnabled` 对比导航延迟（有安全取舍，需用户拍板）；ContentBlocker 维持默认空。
5. 每步优化后用 `[BrowserPerf]` 同页面前后对比（冷/热各测），无效则回退。

## 三、验收（用户 Xcode 编译运行）

### P1 回归清单
1. **R1**：百度搜索 → 点结果开新 tab → 在新 tab 内再点 `_blank` 链接 → 正常再开新 tab（修复前此路径无反应）。
2. **R2**：noopener 测试页（自建 HTML：`<a target="_blank" rel="noopener">`）点击 → 新 tab 打开。
3. 小红书图文/视频贴、抖音站内点击 → 正常跳转；异常时抓 `[BrowserNav]` 日志分析。
4. 右键 → 检查元素 → Inspector 打开，Console 可执行 JS。
5. agent 浏览器工具链（open/click/snapshot）不回归。

### P2 验收
- 同页面（小红书视频贴/抖音）优化前后 `[BrowserPerf]` 数据对比 + 体感确认。

## 四、影响面与风险

- 改动集中：`Shared/Tools/BrowserToolProvider.swift`（埋点/修复/配置）+ 本文档；无新模型、无服务端改动。
- agent 工具链：TabSession 只新增回调、不改既有回调语义。
- noopener 自动开 tab 与现有"http(s) window.open 直接放行"策略一致，风险面不扩大；非 http(s) 仍走拦截/确认。
- 共享进程池：单 tab WebContent 崩溃可能连带同进程 tab 重载（同 Chrome 多进程模型，可接受）；回退成本低（删一行赋值）。
- iOS：无浏览器入口，行为不变（Shared 文件正常双端编译，注意不引入 macOS-only API 而不加 `#if os(macOS)`）。
