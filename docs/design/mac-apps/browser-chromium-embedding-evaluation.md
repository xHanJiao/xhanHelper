# 浏览器内核选型评估：WKWebView → 内嵌 Chromium（CEF）

> 日期：2026-09-15
> 状态：评估完成，待实施（先做 Phase 0 尖刺）
> 前置：`browser-safari-ua.md`（Chrome 伪装 → Safari 原生）、`browser-captcha-fingerprint.md`（指纹破绽修正）
> 关联代码：`client/xHelper/xHelper/Shared/Tools/BrowserToolProvider.swift`、`client/xHelper/xHelper/Shared/Views/{XHWebView,BrowserTabView,AppBrowserWebView}.swift`、`client/xHelper/xHelper/Shared/Services/{XHWebViewBridge,CGEventInput,MediaExtractor,ContentBlocker}.swift`

## 一、背景与问题

macOS 内嵌浏览器当前用 **WKWebView**。实践中遇到两类站点拒绝：

1. **百度频繁弹验证码**——风控指纹/行为打分。
2. **谷歌直接不让登录**——登录页提示"此浏览器或应用可能不安全"。

参照对象：tabbit、workbuddy 看起来都"直接内嵌 Chromium"，想确认（a）换 Chromium 难度多大、（b）能否解决问题。

## 二、问题定性：两件事，别混为一谈

Google 的拦截分两类，性质完全不同：

- **A. 内嵌系统 webview 伪装** → WKWebView、Android WebView。Google 能识破"UA 说是 Safari、环境却不是 Safari"（`navigator.webdriver`、缺 API、行为差异），判定为非安全内嵌浏览器 → 拦。**当前 WKWebView 属于这类。**
- **B. 真正的 Chromium 内核** → CEF / Electron。它在 JS/网络层**就是 Chrome**，指纹自洽。Google 视其为 Chrome，sign-in 正常放行。**tabbit 能登谷歌，根因是它内嵌真 Chromium，不需要任何 cookie 操作。**

结论：**真 Chromium 内嵌能原生走登录流程，谷歌问题可解**；百度验证码主因是风控指纹 + IP/行为（见 `browser-captcha-fingerprint.md`：Safari 伪装后首页验证码曾消失），换引擎是"指纹更干净"，**不保证根治**。

**关键推论：把"谷歌"和"引擎"解耦。** Google 封的是"在内嵌 webview 里走登录流程"这个动作，不是"内嵌浏览器本身"。理论上只要内嵌引擎带着有效 Google 会话 cookie 即可操作，无需在内嵌引擎里登录（cookie 导入方案）；但用户明确**不接受 cookie 导入**（太 hack），故走"真 Chromium 原生登录"。

## 三、实测证据（本机 App bundle 内核核查）

| App | 内核 | 形态 |
|---|---|---|
| **WorkBuddy** | `Electron Framework.framework` + `WorkBuddy Helper*.app` | **整个 App 就是 Electron** |
| **Tabbit Browser** | 234MB 的 `Tabbit Browser Framework`，内核字符串 `Chrome/152.0.7977.65`，含完整 `chrome/browser/...` 源码路径、`chrome_crashpad_handler`、Helper 多进程、`icudtl.dat`/`.pak`、WidevineCdm、PrivacySandboxAttestationsPreloaded | **自建 Chromium 浏览器**（非 Electron，非 CEF） |

Tabbit 控制面：`~/.local/bin/tabbit-cli` → `~/.local/share/tabbit-playwright/instances` → `LocalAgent/endpoint.json` + `browserPid`。即：**Tabbit 本身是完整 Chromium 浏览器，对外用 Playwright/CDP 暴露自动化接口**。

**关键事实：两者都不是"Cef 嵌入"。** 一个用 Electron（App 即 Electron），一个自建 Chromium（App 即浏览器）。**都没有把 Chromium 作为子面板嵌进一个已有的原生 App**——因为 macOS 做不到（不能像 Windows 那样把别的进程窗口 reparent 进你的窗口）。

## 四、三个澄清

### 4.1 CDP 串流方案的浏览器界面在不在我们的 App 里？

**不在（原生意义上）。** 那是独立进程的真实浏览器窗口，面板里只是：

- 收 `Page.startScreencast` 的 **JPEG 视频帧**贴进 SwiftUI（看起来像内嵌）；
- 鼠标/键盘通过 `Input.dispatch*` 转发回去。

内容本质是**视频**，非原生视图。代价：输入延迟、滚动/动画/视频播放卡顿、文字选择/字体渲染降级；地址栏/标签页需自行重画。对"用户也日常浏览"的诉求体验明显不如原生。

### 4.2 选 Electron 后做 CEF 会更简单吗？——概念纠正

**不会。Electron 和 CEF 是并列的两条"嵌入 Chromium"路线，不是叠加关系。**

- **CEF** = "把 Chromium 作为子视图嵌进已有原生 App"。C++/ObjC++ API，**正是当前 WKWebView 的形态**。代价：自写 C++ 桥、打包 Helper、公证。
- **Electron** = "整个 App 用 Node + Chromium 重写"。**已内建 Chromium**，直接用 `BrowserWindow`，**根本不需要 CEF**。但模型是"App 即 Electron"，**无法把 Electron 面板塞进 SwiftUI 原生 App 当子视图**（macOS 无窗口 reparent）。WorkBuddy 就是整个 App 是 Electron。

结论：Electron 不简化 CEF，它**替代** CEF，并顺带把整个原生 App 也替代掉。若诉求是"原生 App + 面板内嵌 Chromium"，只能 **CEF**。

### 4.3 "像 Tabbit 一样自建 Chromium，更重"怎么说？

**CEF vs 自建 Chromium：**

| 维度 | CEF | 自建 Chromium |
|---|---|---|
| 源码 | 不碰 | 几十 GB + 全套 depot_tools/gn/ninja |
| 构建 | 下载即用 | 单次全量编译数小时，CI 需 100GB+ 磁盘和大机器 |
| API 稳定性 | CEF 封装，跨版本改动小 | 直接用 Content API/内部符号，Chromium 一升级嵌入代码就炸 |
| 维护 | 跟 CEF release 走 | **维护一个浏览器 fork**：每次发版 rebase + 重编 + 回归 |
| 人力 | 一个人能搞 | 基本要一个浏览器团队 |
| 产物 | 嵌入到你的 App | 你是浏览器厂商 |

**CEF 是"买现成的嵌入内核"，自建是"自己当浏览器厂商"。** Tabbit 自建是因为它的产品就是浏览器（要完整 Chrome UI、扩展、更新器、DRM 等）；对"在原生 App 面板里嵌一个内核"的诉求，自建多给的能力用不上，纯属买单不点菜。**CEF 提供同款 Chromium 内核，谷歌/百度行为一致，不应自建。**

## 五、方案对比

| | A. CEF 面板 | B. Tabbit 式（独立 Chromium + Playwright/CDP） | C. 现状 WKWebView |
|---|---|---|---|
| 面板内嵌 | ✅ 原生子视图 | ❌ 独立窗口/视频流 | ✅ |
| 用户日常浏览体验 | ✅ 原生丝滑 | ⚠️ 独立窗口 OK / 串流差 | ✅ |
| 谷歌登录 | ✅ 真 Chromium | ✅ 真 Chromium | ❌ |
| 百度指纹 | ✅ | ✅ | ⚠️ 需继续补 |
| 自动化质量 | 中（自实现） | ✅ Playwright/CDP | 低（CGEvent + 注入） |
| Agent 登录态操作 | ✅ 同进程 | ✅（浏览器独立，agent 连它） | ❌ |
| 工作量 | 大（C++ 桥 + 打包 + 公证，数周~两月） | 中 | 零 |
| 是否动现有 SwiftUI 架构 | 保留 | 保留（浏览器独立） | 保留 |

**Electron 不在候选**（它换掉整个 App，与"面板内嵌"目标不符）。

**已确认的约束（决策依据）**：两者都要（agent 自动化 + 用户日常浏览）· agent 要在已登录 Google 状态下操作 · 必须内嵌在面板 · iOS 可放弃 · 不接受 cookie 导入。
→ 四者叠加，**真 Chromium 是唯一能同时满足的引擎，A（CEF）是唯一保留"原生面板内嵌"的路线。**

## 六、成本与硬约束

- **CEF 工作量：数周~两月**。ObjC++ 桥 + `Helper.app` 多进程打包 + 签名/公证 + 重写现有约 4000 行自动化层（`BrowserToolProvider.swift` 单文件 4158 行）与相关桥接/下载/媒体/PDF/ContentBlocker。且需跟 Chromium 版本维护。
- **iOS 禁止非 WebKit 引擎**（App Store 规定），浏览器层 iOS/macOS 共享；换 CEF = 浏览器层 macOS 分叉（iOS 已放弃，可接受）。
- **本机未沙盒**（`client/` 下无 `.entitlements`）。沙盒是内核强制的权限白名单（文件/启动进程/跨 App 数据），开启后无法启动外部 Chrome、无法读用户 Chrome profile；本机未沙盒才使外部方案可行。注意**沙盒 ≠ 公证 ≠ 强化运行时**，三者独立；另有 **TCC**（隐私授权，如辅助功能，`CGEventInput.swift` 用到的）与沙盒不同。
- **现有资产**（拟人化输入 CGEvent、媒体提取 performance 重放、a11y snapshot、下载 cookie 镜像）大部分要重做；部分可用 Chromium DevTools 协议简化。

## 七、推荐与 Phase 0 尖刺

**推荐路线 A（CEF）**，但**先做半天尖刺**验证根因（`browser-safari-ua.md` 曾因"仅两处差异"前提被实测证伪，勿凭理论重写 4000 行）。

### 尖刺目标
- **R1（行为）**：CEF 内嵌 Chromium（真 Chrome 身份）能完成 Google 登录，且百度验证码显著少于 WKWebView。
- **R2（集成）**：CEF 能作为**子视图**嵌进原生 App（NSView）。

**决策门**：R1+R2 都过 → 写正式迁移设计文档；R1 挂 → 放弃 CEF，转 B。

### 打法：用 CEF 官方 sample，不写业务代码
CEF 二进制包自带 `cefclient`/`cefsimple` 源码。**用 `cefclient`**：既内嵌 NSView 渲染（证 R2），又有地址栏可导航/登录（证 R1）。
尖刺是**独立 CMake 工程**（CMake/ninja 构建），不碰 `client/`，不触发 Xcode build DB 锁。

### 环境（本机已核查）
Apple M1 / arm64 · macOS 26.5.1 · Xcode 26.4.1 · cmake/ninja **缺失**需装 · 磁盘可用 **仅 14GB**（用 minimal 包，完事清理）· 本机 Chrome **152.0.7977.83**（真 Chrome 指纹基线）。

### Runbook（约半天，确认后执行）
1. `brew install cmake ninja`
2. 查 `cef-builds.spotifycdn.com/index.json` → 取最新 stable、`macosarm64`、`minimal`、Chromium 152 branch 的包
3. 解压 → `cmake -G Ninja -DCMAKE_BUILD_TYPE=Release .` → `ninja`（产出 `cefclient.app`，CEF 自动拷 framework + Helper.app）
4. 跑 `cefclient.app`：确认内嵌 NSView 渲染 + 地址栏可导航（**R2**）
5. **人工**登 `accounts.google.com`（含 2FA，agent 不代劳——合规要求）→ 记录：过 / 被"不安全"拦。若被拦，用 `--user-agent=<本机 Chrome 152 完整 UA>` 再试一次排除 UA 变量（**R1-谷歌**）
6. 百度对照：现有 WKWebView App 搜 10 次 vs cefclient 搜 10 次，数验证码（**R1-百度**）
7. 指纹自检：browserleaks 对比 cefclient vs 本机真 Chrome 152
8. 出结论
   - R1+R2 过 → 写 `docs/design/mac-apps/browser-chromium-migration.md`（引擎抽象、tab/a11y/下载/媒体逐项迁移、bundle/签名/公证计划）
   - R1 挂 → 转 B（独立 Chromium + Playwright/CDP）

### 尖刺边界
- 不签名/不公证（R3 留到正式实施，通过后单独抽一天验）。
- 不自动化 Google 登录（合规 + 2FA）。
- 磁盘仅 14GB：只用 minimal 包，结束删临时目录。
- 尖刺工程放**临时目录**（完事删），结论写入本仓库 docs 即可。

## 八、待办 / 未来工作

- [ ] Phase 0 尖刺（Runbook 见上）
- [ ] 尖刺通过后：`docs/design/mac-apps/browser-chromium-migration.md`（CEF 迁移设计）
- [ ] 引擎抽象：把 `TabSession`/`WKWebView` 背后抽成协议，UI 层不直接依赖 `WKWebView`（**只抽当前真正用到的接口**，不做投机抽象）
- [ ] 若走 B / C+B 混合，评估"WKWebView 面板保留 + 独立真 Chromium 后端专供 agent 与登录态操作"
- [ ] R3：CEF bundle 的签名/公证可行性验证
