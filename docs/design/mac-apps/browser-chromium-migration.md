# 浏览器内核迁移：WKWebView → CEF（Chromium）

> 日期：2026-11-12
> 状态：设计定稿（19 条决定已拍板），待进 P1
> 前置：`browser-chromium-embedding-evaluation.md`（选型 + Phase 0 尖刺已通过）
> 关联代码：`client/xHelper/xHelper/Shared/Tools/BrowserToolProvider.swift`（4288 行）、`Views/{BrowsePanelView,BrowserTabView,AppBrowserWebView}.swift`（1484 + 721 + 67）、`Services/` 下 13 个浏览器相关 store

估算（开工）：21 面 / 7 缝（3 条需先建探针）/ 3 次真机往返 / 4 个待拍决定 · ~120–240h（4~8 周）
估算（定稿）：23 面 / 7 缝（3 条需先建探针）/ 3 次真机往返 / 0 个待拍决定 · ~160–300h（5~8 周，含兼容 tab + 跨引擎路由）

## 一、目标与非目标

**目标**：把右侧栏「浏览器」面板的内核从 WKWebView 换成 CEF（Chromium 156+），做到
① 谷歌账号可正常登录；② 百度系（搜索/百科/地图）验证码不再常态化；③ agent 的 26 个浏览器工具与「验证码/登录墙手递手」体验不回退；④ B站 / 抖音 / 小红书的视频照旧能看（走 WK 兼容 tab，D12）。

**UI 一行不改**：现有 `BrowsePanelView` / `BrowserTabView` 的地址栏、收藏栏、标签栏全部保留，只换宿主视图与状态来源。问题定性是**引擎身份**（站点把 WebView 当非正规浏览器），不是 UI。

**非目标**：
- 不改**灵阅**（`DocumentReaderView`，1893 行）与 HTML 产物（`HTMLArtifactView`）——它们继续用 WKWebView，二者是"文档渲染"不是"浏览网页"，没有风控问题。
- **不自建 codec 版 CEF**（D12）：官方 CEF 二进制不含 H.264/AAC，自建门槛是 32GB RAM + 150GB 磁盘（本机 8GB / 50GB），不采用。视频站改用 **WK 兼容 tab** 兜底（见 9.1）。
- **不做 Chrome 扩展**（D9）：CEF 156 的 `include/` 里连扩展 API 都没有（无 `cef_extension*.h`、无 `LoadExtension`，2017 年已移除）；用户无扩展需求。商店扩展在 CEF/Electron 上都不可能，只有自建 Chromium 才有。
- **不做广告拦截**（D2）：WK 的 `WKContentRuleList` 在 CEF 无等价物；用户判断不重要，P2 直接砍，`ContentBlocker` 与设置开关一并下掉。CDP 的 `Network.setBlockedURLs` / `Fetch.requestPaused` 口子留着，以后想补随时能加（约 1~2 天）。
- **iOS 不提供浏览器面板**（D5）：`Shared/` 同时编进两个 target，CEF 只能 macOS。iOS 端不注册浏览器工具组、不显示面板，保持 WK 实现只服务于灵阅/产物这类文档渲染（不是浏览器）。
- 不做 DRM（Widevine 不随 CEF 分发）、Chrome 账号同步。
- **不自建 Chromium**（CEF 提供同款内核）。

## 二、架构

```
SwiftUI (BrowsePanelView / BrowserTabView)
        │  只依赖 BrowserEngine 协议（P1 第一件事）
        ▼
  BrowserEngine (protocol, Swift)
        ├── CEFEngine        主引擎（macOS，ObjC++ 包 CefClient/CefBrowser）
        └── CompatWebView    兼容引擎（WKWebView，仅服务视频域名 + 手动兜底）
              │
              ▼
   Chromium Embedded Framework  ←→  Helper.app ×4（Renderer/GPU/Plugin/主）
```

**双引擎是长期的，不是过渡**（D11 改）：CEF 官方二进制不含 H.264/AAC（D12），视频站（B站/抖音/小红书）继续走 WK 兼容 tab；其余站点、以及 agent 的**写操作**，都在 CEF 主 tab 上（兼容 tab 只开放只读工具，D18）。P1 期间现有 WK 面板原样留着当兜底开关，P2 再把它收成"只服务 3 个视频域名"的兼容 tab。

`BrowserToolProvider.swift`（4288 行）**不做一次性重写**：先抽薄协议、只换宿主调用点，把冲突面压到最小。

几条硬约束（来自 CEF 的模型，和 WKWebView 完全不同）：

1. **消息循环**：`CefSettings.external_message_pump = true`，在 macOS 主线程用 `NSRunLoop` 驱动 `CefDoMessageLoopWork`（或按 CEF 官方 `cefclient` 的 `CefRunMessageLoop` + 独立线程方案，二者择一，P1 定）。
2. **多进程**：CEF 初始化必须走 `CefExecuteProcess` 的子进程分支（在 `main` 最前面判断 `--type=`），这条会动到 `xHelperMac/App.swift` 入口。
3. **线程**：所有 `CefBrowserHost` 调用必须在 CEF UI 线程（macOS 上即主线程）；CEF 的回调也在该线程。Swift 侧沿用 `@MainActor`，但 ObjC++ 回调要显式 dispatch。
4. **视图**：`CefBrowserHost::CreateBrowser` 传 `NSView` 作为父视图，CEF 自己管理子视图层级与输入；SwiftUI 用 `NSViewRepresentable` 承载。

## 三、四层伪装（决定性，缺一不可）

Phase 0 已实测：**"真 Chromium 就自动过谷歌"不成立**，需要四层，且第 4 层是 CEF 独有。

| # | 层 | 落点 | 说明 |
|---|---|---|---|
| 1 | UA 字符串 | `CefSettings.user_agent` | 版本要与内核一致 |
| 2 | `sec-ch-ua*` 请求头 | `CefResourceRequestHandler::OnBeforeResourceLoad`（CEF 156 已从 `CefRequestHandler` 删掉同名钩子） | 服务端可见；CEF 默认不发/只报 Chromium。**必须实测抓包确认**（`cef-regression.sh` 的探针就是这条的取证） |
| 3 | `navigator.userAgentData.brands` | `CefRenderProcessHandler::OnContextCreated` 里 `CefV8Context::Eval`（同步，不是 `ExecuteJavaScript`） | document-start |
| 4 | **`window.chrome`** | 同上 | CEF 会注入 `setSyncEncryptionKeys` / `setClientEncryptionKeys` / `addTrustedSyncEncryptionRecoveryMethod` 三个真 Chrome 没有的函数；**带着它们 UA-CH 再正确也被拦**。整体替换成 `{app, csi, loadTimes, runtime}` |

策略保持"越少越好"：这四层是**名义层**（内核本来就是 Chromium），不要再退回 WKWebView 那种伪造 `plugins`/`mediaDevices`/WebGL 的深层指纹路线。

**B3 落地口径（代码即事实）**

- 版本号唯一来源是 `include/cef_version.h`（`CHROME_VERSION_MAJOR/MINOR/BUILD/PATCH`）；UA 与 brands 都从它拼，**升级内核不会出现"UA 忘改"**（10.2 的 A 档检查因此不必再 grep UA 常量）。伪装常量集中在 `xHelperMac/CEF/CefDisguise.h`，浏览器进程（`CefBridge.mm`）与渲染进程（`CEF/helper_main.mm`，`fetch-cef.sh` 单独编译）共用同一份。
- 第 1 层：`CefSettings.user_agent`（这条串同时管 `User-Agent` 请求头与 `navigator.userAgent`）。
- 第 2 层：**CEF 156 的 `CefRequestHandler` 已没有 `OnBeforeResourceLoad`**，改由 `GetResourceRequestHandler` 返回 `CefResourceRequestHandler`，在它的 `OnBeforeResourceLoad` 里 `SetHeaderByName(..., overwrite=true)`。只对 `https` / localhost 设（真 Chrome 在 http 上不发低熵提示，发了反而是异常信号）。
- 第 3/4 层：`XHCefRenderProcessHandler::OnContextCreated` 里 `context->Enter()` → `CefV8Context::Eval(...)` → `Exit()`（**不用 `frame->ExecuteJavaScript`**：那是投任务，"页面脚本 vs 注入脚本谁先跑"是赌博，赌输了等于没做）。**必须让 `CefExecuteProcess` 拿到带 render handler 的 `CefApp`**（`helper_main.mm`）；传 `nullptr` 就没有渲染进程钩子（CEF 154 起 `CefRegisterExtension` 已移除，不能再走那条路）。注入失败按层 `console.error("[cef-disguise] …")`，不静默降级。
- `GREASE` 陪跑品牌（`Not:A-Brand` / `99`）是跟着内核代次手写的常量；升级内核后抓包发现形态变了要同步改（见 10.1）。
- 取证：`--cef-probe[=回显地址]`（加载回显请求头的页面 → 页内 `console.log("XHPROBE:…")` 回传 → 落盘 `cef-probe.json` 后自动退出），由 `scripts/cef-regression.sh` 判读四层是否**互相自洽**；谷歌登录假邮箱、百度三站有没有验证码仍需人眼。
- 退路（探针若显示网络层把我们设的头又盖回去了）：开 `remote_debugging_port` 用 CDP `Network.setUserAgentOverride`（带 `userAgentMetadata`），一次把 UA / UA-CH 头 / `navigator.userAgentData` 三层都接管 —— 比自己拼头可靠，代价是引入 CDP 通道。

**回归**：脚本落在 `scripts/cef-regression.sh`（P1 第 3 步交付），查四件事：谷歌假邮箱探针、百度三站、httpbin 请求头、`window.chrome` 成员与真 Chrome 的差异。每次升级 CEF 后必须先跑（见第十节 10.2 的 stamp 检查）。

## 四、能力映射（WK → CEF）

| 现在用的 | 换成 | 备注 |
|---|---|---|
| `WKWebView.load/goBack/goForward/reload` | `CefBrowserHost::{LoadURL,GoBack,GoForward,Reload}` | 直接 |
| `evaluateJavaScript` | `CefFrame::ExecuteJavaScript` | 异步；封装成 `async throws -> Any` |
| `WKNavigationDelegate`（didStart/didFinish/didFail） | `CefLoadHandler`（`OnLoadingStateChange`/`OnLoadEnd`/`OnLoadError`） | 无 `estimatedProgress`，进度条改成"事件驱动 + 假进度"或去掉 |
| `WKUIDelegate`（新窗口/popup） | `CefLifeSpanHandler::OnBeforePopup` | 现在有 `openPopupTab`，一一对应 |
| `WKDownloadDelegate` | `CefDownloadHandler`（`OnBeforeDownload`/`OnDownloadUpdated`） | 下载历史 store 保留 |
| `WKURLSchemeHandler`（吞 `bitbrowser://` 等探测） | `CefRegisterSchemeHandlerFactory` | 见 `docs/bugs/043`，同样的坑要保留兜底 |
| 注入脚本 / fingerprint probe | `OnContextCreated` | 现在是 `WKUserScript` + `WKUserContentController` |
| `WKContentRuleList`（adblock） | **无等价** | D2 已定：不做（CDP `Network.setBlockedURLs` 口子留着） |
| `WKWebsiteDataStore` / `httpCookieStore` | `CefCookieManager` | cookie 导入/导出要重写 |
| `find` | `CefFindHandler` | 直接 |
| `takeSnapshot` | **CDP `Page.captureScreenshot`**（CEF 无原生快照） | 需开 in-process CDP |
| `createPDF`（导出） | 暂无——CEF 无 API，可用 CDP `Page.printToPDF` | 灵阅 PDF 导出不受影响（走 WK） |
| `setMagnification` | `CefBrowserHost::SetZoomLevel` | 换算不同 |
| KVO（title/url/canGoBack…） | CEF 回调 + 自己维护 `BrowserState` | 现在 `BrowserTabView` 里那 9 个 `@State` 合并进一个状态对象 |
| JS alert/confirm/权限 | `CefJSDialogHandler` / `CefPermissionHandler` | 直接 |
| 右键菜单 | `CefContextMenuHandler` | `AppBrowserWebView.willOpenMenu` 那套"用 X 搜索"逻辑要按 CEF 菜单重写 |

## 五、迁移清单

| 模块 | 规模 | 迁法 |
|---|---|---|
| `BrowserToolProvider.swift` 26 个 agent 工具 | 4288 行 | 上表逐项换宿主 API；`snapshot/click/fill/press/select` 这类靠 JS 注入的**逻辑可整体搬**，只换 `evaluateJavaScript` 的壳 |
| `BrowsePanelView` / `BrowserTabView` | 1484 + 721 | 地址栏/收藏栏/查找栏 UI 不动，只换宿主视图与状态来源；tab 增加"引擎类型"字段 |
| `AppBrowserWebView` | 67 | 保留给兼容 tab（右键菜单"用 X 搜索"那套逻辑只在 WK 侧有效） |
| **兼容 tab（WK）** | 复用现有 WK 实现 | 永久保留：3 个视频域名默认走它 + 任意页面播放失败时手动兜底；不接 agent 写操作 |
| **CEF 桥（ObjC++）** | 新文件 | **放 `xHelperMac/` 或新建 `SharedMac/`，绝不能放 `Shared/`**——iOS target 用同步组递归收 `Shared`，一个 `.mm` 或 `#import <Cef…>` 就让 iOS 构建立刻炸；CEF 的 header search path 也只能配在 mac target |
| 收藏 / 历史 / favicon / 下载历史 | 231+120+153+238 | 存储层不动，只换触发点 |
| `ContentBlocker` | 87 | **砍掉**（D2：不迁移，`ContentBlocker` 与设置开关一起删） |
| `BrowserFindCoordinator` | 66 | 换成 CefFindHandler 包装 |
| `BrowserActivityCenter` / `ActivityBrowserState` | 148+151 | 事件不变（`browser.nav.*`），换触发源 |
| `BrowserDiagnosticsCenter` | 86 | 保留，补 CEF 版本/进程信息 |
| 墙检测（`wallProbeJS` + 手递手） | — | JS 与信号不变，换执行宿主 |
| `CGEventInput`（拟人化输入） | 217 | **保留**——CEF 里 `Input.dispatch*` 走 CDP 或 CGEvent 都可行，先用 CGEvent（已验证可用） |
| `MediaExtractor`（媒体地址嗅探） | 492 | **两套并存**：CEF 用网络回调（比 performance 重放干净），但兼容 tab 的嗅探仍走现有 WK 实现——m3u8/mp4 嗅探几乎只发生在视频站，也就是几乎只在兼容 tab 里 |
| `BrowserEngineRouter`（新） | ~150 | 引擎判定 + 提示规则 + 域名偏好，全部规则收在这一个类型里（见 9.2），不许散到 view / tool |

## 六、打包 / 签名 / 公证（D13 / D14 已定）

- bundle 里放 `Chromium Embedded Framework.framework` + 5 个 `Helper.app`（主 / Alerts / GPU / Plugin / Renderer；官方包不预置，见 6.1），**+250~350MB**。
- **架构 `ARCHS=arm64`**（D14）：官方 CEF 包是单架构，Release 默认 universal 会链接失败；自用（M1）不需要 x86_64。Debug 的 `ONLY_ACTIVE_ARCH=YES` 本来就不受影响，只需补 Release/pbxproj 全局 `ARCHS`。
- `CefSettings` 必须显式给：`browser_subprocess_path`（Helper 可执行）、`framework_dir_path`、`main_bundle_path`、`resources_dir_path`、`locales_dir_path`（只留 `zh-CN`/`en-US` 可省几十 MB）。
- 签名顺序：framework 内 `Libraries/*.dylib` → framework → Helper×5 → Go 后端 → 主 app；`--options runtime --timestamp`。Renderer 需要 JIT 相关 entitlement（`allow-jit` 等），照 CEF 官方示例。**注意和 Xcode 自身签名的先后**：Run Script 提前签过再让 Xcode 整体签，容易出现 `code object is not signed at all` / 签名被覆盖，第一次构建预算半天（现有 `scripts/bundle-backend.sh` 的嵌套签名先例照它扩）。
- **不公证**（D13）：本机只有 `Apple Development` 证书，只自用不分发；跳过 `notarytool`，`--options runtime` + 本地签名足够。
- 本机未沙盒，不受沙盒限制；一旦将来开沙盒，CEF 多进程要额外 entitlement。

### 6.1 CEF 二进制放哪（不入 git）

- **不进仓库**：解包 1.2~1.5GB，进 git 会毁掉仓库历史。
- **放版本化共享缓存**：`~/Library/Caches/xhanHelper-cef/<CEF_VERSION>/`。多 worktree 共用一份（本机只剩 ~47GB 可用；每个 worktree 各存一份 + 各自 DerivedData 再 +1.5GB，两个 worktree 就吃掉 6GB）。
- 构建时按 `client/xHelper/CEF_VERSION` 里的版本号去找；缺失就由 Run Script 下载（mac target 的 `ENABLE_USER_SCRIPT_SANDBOXING=NO` 已确认允许联网）。
- 下载的 zip 要 `xattr -dr com.apple.quarantine`，否则签名直接失败——第一次构建必踩。

**落地口径（B0 实测后定稿，`client/xHelper/scripts/fetch-cef.sh`）**

| 事 | 结论 |
|---|---|
| 发行包 | `https://cef-builds.spotifycdn.com/cef_binary_<CEF_VERSION>_macosarm64.tar.bz2`（**standard** 版，要 headers + wrapper 源码）。`CEF_VERSION` 存**完整串** `156.0.7+g47dbc1d+chromium-156.0.8078.17`（URL/文件名要带 `+chromium-` 段） |
| 实际占用 | 解包 ~850MB（不是 1.2~1.5GB），下载 300MB（解完即删，不留 tar） |
| `libcef_dll_wrapper` | 用 CEF 自带 CMake 编（`-G Ninja -DPROJECT_ARCH=arm64 -DUSE_SANDBOX=OFF`），产物放 `<缓存>/lib/` |
| **Helper.app 要自己造** | **官方包不含预编译 Helper.app**（`Release/` 下只有 framework；cefclient 的那几个是它自己 CMake 造的）。脚本用 `tests/cefsimple/mac/helper-Info.plist.in` 当模板造 5 个（主 / Alerts / GPU / Plugin / Renderer），可执行文件同一份 stub |
| Helper stub | 源码 `client/xHelper/CEF/helper_main.mm`（**不能放 `xHelperMac/`**：它自带 `main()`，会和 App.swift 的入口撞符号）。由脚本用 `clang++` 单独编，不进 Xcode |
| 稳定入口 | 缓存里两个软链：`current → <版本目录>`、`dist → <发行包根>`；pbxproj 只写 `$(HOME)/Library/Caches/xhanHelper-cef/current`，升级只改 `CEF_VERSION` |
| 装载方式 | **不能直接链 framework**：CEF 156 的 wrapper 是「运行时加载」形态（C API 符号由 wrapper 内部函数指针表转发），framework 必须 `CefScopedLibraryLoader::LoadInMain()` dlopen；链上它会变 `@executable_path/../Frameworks/...` 相对依赖，Helper 嵌在 `Contents/Frameworks/<Helper>.app` 里必然找错路径（B0 踩过）。pbxproj 因此只需要 `HEADER_SEARCH_PATHS=$(CEF_CACHE)/dist` + `-lcef_dll_wrapper` + `LIBRARY_SEARCH_PATHS` |
| 签名 | Helper×5 → framework（含内部 `Libraries/*.dylib`）→ App，全部 **ad-hoc `codesign -s -`**（`ENABLE_HARDENED_RUNTIME=NO`，D13 只自用）。Xcode 自己那一步签 App 本体，不覆盖嵌套 |

## 七、分阶段

> 阶段是"做到哪里"，批次是"怎么分批提交 + review"——具体切分、工时与 review 节奏见**第十四章**。

### P1 骨架（~2 周）——顺序有讲究，D6 是门

| 步 | 内容 | 出口 |
|---|---|---|
| 0 | **面板内基线**（半天）：用现有 WK 面板跑百度地图 + 谷歌探针，记录"用不了"的具体形态（验证码 / 空白 / 卡死 / 降级），作为 CEF 的对照基线 | 一份能对比的失败清单 |
| 1 | **真机合成冒烟**（1~2 天）：把 CEF 视图塞进真实窗口，压照片底图 + 材质 + PiP 浮层，看灰闪/遮挡 | 正常 / 可修 / 不可用（不可用 → 方案作废；route B 已出局，不转） |
| 2 | 引擎抽象 + CEF 骨架（3~5 天）：`BrowserEngine` 协议（WK/CEF 双实现并存）、`CefInitialize`、`--type=` 子进程分支、单 tab NSView 渲染、导航状态 | **dev 构建默认走 CEF**，隐藏开关可切回 WK；面板里能开页 |
| 3 | 四层伪装 + 回归脚本转正（1~2 天） | 谷歌假邮箱探针过、百度三站 0 验证码、httpbin 头正确 |
| 4 | iOS 边界 + 开关收口（1 天）：`#if os(macOS)` 收口、iOS 不注册工具组 | iOS 构建通过、面板不出现 |

P1 只在**开发环境**看，不动主线（D4）。**默认引擎 = CEF**（D10），出问题用隐藏开关切回 WK，不必回退整条分支。**兼容 tab 层不在 P1**（P2 才做）；P1 期间视频站可以先手动切回 WK 看（D10 的开关就是兜底）。

### P2 浏览器功能（~2–3 周）

多 tab、popup、标签恢复、agent 标签归属与工作区、地址栏建议、收藏/历史/favicon、下载、查找、缩放、右键、JS 对话框、权限、**分屏双浏览器**、后台保活与媒体、墙检测 + 手递手、活动埋点、快捷键（⌘W / ⌘⇧T / ⌘⇧[ ]）。

**兼容 tab + 跨引擎（约 5~7 天，含在上面）**：
- `BrowserEngineRouter`（判定函数 + 提示规则 + 域名偏好表，D15~D17）
- `RightSidebarTab` 加不可变的 `engine` 字段；3 个视频域名 + 3 个短链域名默认开兼容 tab
- 三层视频检测（白名单 / `document-start` 探针 / media error）+ 地址栏常驻「兼容模式」图标
- `list_tabs` 标 `engine: compat, 只读`，写操作报 `engine_unsupported`（D18）
- 兼容 tab 的其余能力按现有 WK 实现照搬（收藏/历史/下载/查找/全屏/嗅探）

规则与全量 case 见 9.1 / 9.2。

**不含**：广告拦截（D2 砍）、扩展（D9 不做）。

出口：`browser-agent-workspace-test-guide.md` 全用例 + 跨引擎用例（9.2.5 那几张表）+ 用户日常浏览一周无阻断 → 并回 `main`（D4）。

### P3 agent 工具（~1–2 周）

26 个工具逐个迁移到 CDP（`Input.*` / `Accessibility.*` / `Network.*` / `Page.*`），工具名/参数/返回预算不变；手递手与工作区归属回归。

- 兼容 tab 只读开放、写操作禁（D18）；`open_url` 加 `engine` 覆盖（D19）。
- **`snapshot` 类工具的输出形态会变**：`Accessibility.getFullAXTree` 的节点结构和现在的 DOM 快照不同，提示词与返回预算要重调——P3 不是纯搬家，这块单独留时间。
- **后台自动化只走 CDP**，`CGEventInput`（拟人化输入）只在面板可见且聚焦时启用——否则会打进用户当前前台窗口。

### P4 打包发布（~1 周）

签名、体积、`ARCHS=arm64`、`scripts/update-cef.sh` / `scripts/check-cef-version.sh` / 回归 stamp、本机安装验证（D13 不分发，不公证）。

## 八、风险与回滚

1. **合成层（P1 第一道门）**：`docs/bugs/030` 已记录 WKWebView 与材质/底图共存会灰闪；CEF 是独立 GPU surface，且现在叠了分屏双浏览器 + PiP + 浮层，风险更高。P1 第 1 步就是验它，不过关则整个方案作废（route B 已出局，没有退路方案）。
2. **Google 风控是黑盒**：今天过的配置，Chromium 升级后可能失效。缓解：回归脚本进发版流程（见第十节），失败不阻塞发布（降级到"面板内不登谷歌"文案）。
3. **iOS 双实现已排除**：D5 砍掉 iOS 浏览器面板，避免"两套引擎语义 + 两套工具行为"的长期双维护。
4. **内存/进程回收**：多进程模型下 tab 关闭必须 `CefBrowserHost::CloseBrowser(force_close=true)` 并等 `OnBeforeClose`，否则会比 `docs/bugs/2026-08-browser-webview-leak` 的泄漏更严重（那次是 240MB/页）。
5. **入口文件改动**：CEF 子进程分支要写进 `xHelperMac/App.swift`，`swiftc -typecheck` 覆盖不到，必须真机构建验证（本项目约定：agent 不跑 xcodebuild，由用户验）。
6. **回滚**：`BrowserEngine` 协议 + tab 的 `engine` 字段是切换引擎的唯一开关；P1/P2 完成前不动主线，回滚靠 `main` 上的 `pre-cef` tag。WK 实现永久保留（D11），CEF 整体失守时可临时把默认引擎切回 WK 先保住可用性。
7. **跨引擎体验（本方案最易翻车处）**：规则散落会导致"视频放不了 / 怎么又没登录 / agent 动了我在看的页面"。缓解：判定收在 `BrowserEngineRouter` 一处、提示只在功能缺失时出现且同域名 24h 最多 2 次、视频"静默坏"三层检测 + 地址栏常驻兼容图标兜底（见 9.2）。
8. **CEF 整体起不来**：Helper 被 Gatekeeper 拦、初始化失败、framework 签名不匹配都会导致浏览器子系统全灭。实现必须自带启动降级（全站 compat + 顶部横幅），不能只靠"不崩"。

### 8.1 2026-11-12 能力盘点新发现（三条硬约束）

| # | 发现 | 影响 | 处置 |
|---|---|---|---|
| R1 | **官方 CEF 二进制不含 H.264 / AAC**（实测 `canPlayType('video/mp4; codecs="avc1…")` 为空、`MediaSource.isTypeSupported` 为 false；AV1/VP9/VP8/Opus/MP3 正常） | 视频站（B站/抖音/小红书）在 CEF tab 里**放不了**；YouTube（VP9/AV1）不受影响 | **已定 → D12 混合方案**：3 个视频域名走 WK 兼容 tab，其余走 CEF。自建 codec 版 CEF 已排除（需 32GB RAM + 150GB 磁盘，本机 8GB / 50GB） |
| R2 | **本机只有 `Apple Development` 证书，没有 `Developer ID Application`** | 无法公证；分发给他人会被 Gatekeeper 拦 | **已定 → D13 只自用**，不公证，`--options runtime` 本地签名即可 |
| R3 | **Release 默认是 universal（arm64 + x86_64），CEF 官方包是单架构** | 链接/嵌入会失败 | **已定 → D14 `ARCHS=arm64`**（Debug 的 `ONLY_ACTIVE_ARCH=YES` 本来就不受影响） |

其余盘点结论（已确认可用，P3 风险下降）：

- CDP 域全部可用：`Page.captureScreenshot`（116KB）、`Page.printToPDF`（209KB）、`Accessibility.getFullAXTree`（38 节点）、`Target.getTargets`、`Fetch.enable`、`Emulation.setDeviceMetricsOverride`、`Network.setBlockedURLs`、`Input.insertText`。
- 编解码：AV1 / VP9 / VP8 / Opus / MP3 支持；H.264 / AAC 不支持。

## 九、决定记录与关键规则（2026-11-12 已拍板）

| # | 决定 | 结论 | 影响 |
|---|---|---|---|
| D1 | 体积与内核维护节奏 | **接受**（+250~350MB；按需升级，非 4 周一次） | P1 起，长期 |
| D2 | 广告拦截 | **不做**（`ContentBlocker` 与开关一起下掉，CDP 口子留着） | P2 少 1~2 天 |
| D3 | 截图 / PDF | **走 in-process CDP** | P3 |
| D4 | 并回 `main` 的时机 | **P2 之后**；P1 只在开发环境看 | P3 起 |
| D5 | iOS 浏览器面板 | **砍掉**（PWA 已够用，桌面形态本来也没人用） | P1 加 `#if os(macOS)` 边界 |
| D6 | 接受"P1 可能证伪" | **接受**，P1 第一步就是合成冒烟 | P1 第 1 步 |
| D7 | CDP 通道 | **进程内 `CefDevToolsMessageObserver`**（不开调试端口；Debug 可临时开） | P1 起 |
| D8 | 登录态迁移 | **不迁移**，切换时提示一次"需重新登录" | P2 |
| D9 | Chrome 扩展 | **不做**（CEF 无扩展体系，用户无需求） | 无 |
| D10 | P1 期间 dev 的默认引擎 | **默认 CEF**，隐藏开关可切回 WK | P1 第 2 步 |
| D11 | 是否保留 WK 实现 | **永久保留**，改名 `CompatWebView`，只服务 3 个视频域名 + 手动兜底（不是过渡） | 全程 |
| D12 | H.264 / AAC 缺失 | **混合方案**：CEF 主引擎 + WK 兼容 tab；不自建 codec 版 CEF | P1 定协议、P2 落地 |
| D13 | 是否分发给别人 | **不分发，只自用** → 不公证，`Apple Development` 签名 + `--options runtime` | P4 |
| D14 | 构建架构 | **`ARCHS=arm64`**（CEF 官方包单架构；universal 需 lipo，体积翻倍） | P1 改 pbxproj |
| D15 | 跨引擎切换方式 | **引擎粘性**：tab 创建时定引擎，之后不可变；换引擎 = 新开 tab（不做同 tab 切换） | P2，见 9.2.1 |
| D16 | 白名单命中 | **留在原引擎 + 提示条**（不自动切）；用户点过即写入域名偏好，之后静默按偏好走 | P2，见 9.2.3 |
| D17 | popup / `window.open` | **引擎跟 opener 一致**（保 `window.opener` 与 cookie 域），不做 `noopener` 特例 | P2，见 9.2.2 |
| D18 | 兼容 tab 的 agent 能力 | **只读开放**（snapshot / 读文 / 截图 / PDF）、**写操作禁止**，报结构化 `engine_unsupported` | P3 |
| D19 | agent 打开页面 | 默认走判定，但 `open_url` 加 `engine` 覆盖参数（要读 bilibili 页面时可强制 CEF） | P3 |
| D20 | CEF 的 `window.open` / `target=_blank` | **让 CEF 自己建 popup browser，再把它嵌进我们的 tab**（真 `window.opener`）；页面 `window.close()` 时自动收掉 tab | P2，见 9.2.14 |

### D1 细则：升级到底要做什么

CEF 升级分三层，只有中间那层是"必须重编"：

| 层 | 升级动作 |
|---|---|
| CEF 二进制 | 换 `Chromium Embedded Framework.framework` + 5 个 `Helper.app` |
| CEF 的 C++ 封装 `libcef_dll_wrapper` | **必须跟着重编**（按头文件 ABI 编译，版本不匹配直接崩） |
| 我们的 Swift/ObjC++ | 一般不动（除非用到的 API 跨版本改了签名） |

一次升级的完整动作：

```
下载 CEF 包（~130MB）→ 重编 wrapper（几分钟）→ 替换 framework/Helper
→ 重签（Helper → framework → App）→ 跑回归脚本 → 用户在 Xcode 构建一次
```

- **要用户重新构建**：本项目约定 agent 不跑 `xcodebuild`（避免抢 build DB 锁），所以每次升级需要用户点一次构建（增量，几分钟）。只自用不分发（D13），无需重新公证和重新下载。
- **不需要 4 周一次**：那只是 Chromium 发版节奏。实际策略见第十节。
- **UA 必须跟内核版本对齐**（不是跟本机 Chrome）：尖刺已证伪"版本对不齐也没事"。

### 9.1 兼容 tab 规则（D11/D12 落地口径）

| 场景 | 行为 |
|---|---|
| `bilibili.com` / `douyin.com` / `xiaohongshu.com` | 默认开 **WK 兼容 tab**（记住用户手动改过的选择） |
| 其它任意域名 | 默认开 **CEF 主 tab** |
| 分享短链 `b23.tv` / `v.douyin.com` / `xhslink.com` | 同白名单：先判域名再决定引擎，别等 302 落到视频站才发现开错 |
| **主框架导航每一步都重判**（不只是初始 URL、也不只新建 tab） | 从百度点进 bilibili、从短链 302 到 douyin、`target="_blank"` 开新 tab —— 都按**目标 URL** 判；命中白名单就在 CEF tab 顶部提示「此站在兼容模式更合适」并给一键切换 |
| 任意页面 `<video>` 报 `MEDIA_ERR_SRC_NOT_SUPPORTED` | 顶部提示条「此页面需要兼容模式」→ 点一下同 URL 在当前面板重开为 WK 兼容 tab（**不做自动切换**，否则用户不知道自己在哪个引擎里，agent 也会莫名失效） |
| agent 工具 | **写操作只对 CEF tab 生效**；兼容 tab 标 `engine: compat, 只读`（只读工具可用，见 D18），写操作返回 `engine_unsupported` |
| 兼容 tab 的功能边界 | 按现有 WK 实现照搬（收藏/历史/下载/查找/分屏能留就留）；不接 agent 写操作 |
| **隐藏开关（回滚路径）** | 能让**任意 URL** 走 WK，不限于白名单 —— P2 并回 main 后 CEF 出大问题时的唯一退路，别把 WK 收窄到只会开 3 个站 |

**收藏/历史不存引擎**：引擎由打开时的 URL 现算，否则白名单以后调整会让老收藏打开在错的引擎里。

已知残留：小红书是图文 + 视频混合页，整站走 WK 后 agent 无法操作（用户当前不用该场景）。若将来需要，再单独决定"兼容 tab 是否开放只读工具"。

**实现口径（判定函数 / 提示规则 / 全量 case / 数据模型）见 9.2。**

### 9.2 跨引擎设计（D15~D19）

跨引擎是这个方案里唯一"设计难度 > 工程难度"的部分：规则错一次，用户遇到的是"视频放不了 / 怎么又没登录 / agent 操作了我在看的页面"。所以判定、提示、偏好三件事**收在一个类型里**（`BrowserEngineRouter`），其它代码只调 `resolveEngine` / `shouldHint`。

#### 9.2.1 模型：引擎粘性，不做同 tab 切换（D15）

| 模型 | 做法 | 后果 |
|---|---|---|
| A · URL 驱动 | 同一 tab 每次导航重算引擎 | 两个引擎的 history / cookie / 滚动位置 / 表单状态无法移植 → 切一次 = 销毁重建：丢滚动、丢表单、丢 tab 内回退栈；agent 绑定的 tab 语义也崩 |
| **B · tab 粘性（采用）** | 创建时按 URL 定引擎，**之后不再变**；要换引擎 = 新开一个 tab（原 tab 保留） | 多一个 tab，换来状态一致、agent 绑定稳定、实现简单 |

#### 9.2.2 判定函数（唯一真相来源）

```
resolveEngine(url, opts) -> .cef | .compat

1. 非 http(s)（file:// / PDF / about:）        → compat
2. opts.engineOverride（agent 或用户显式指定）   → 用它
3. opts.openerEngine 存在（window.open / 新窗口）→ 跟 opener 一致
4. EnginePreference[host] 命中                 → 用它（用户上次的选择）
5. VIDEO_HOSTS 命中（含短链，见下）             → compat
6. 其它                                        → cef
```

- **第 3 条不可省**：跨引擎的 `window.open` 会断 `window.opener` + 断 cookie 域，B站/抖音/谷歌的登录弹窗、支付回调都会挂。宁可在弹窗里看不了视频，也不要登录失败。
- **第 4 条是"别烦用户"的关键**：用户手动选过一次「用兼容模式打开 bilibili」，以后静默按这个走，不再提示。偏好按 **eTLD+1** 存。
- **收藏 / 历史 / 会话恢复都不存引擎**，只存 URL，打开时现算；否则白名单以后调整会让老收藏打开在错的引擎里。会话恢复要**连偏好表一起恢复**，否则用户手动选过的站会被打回默认。
- `VIDEO_HOSTS`：`bilibili.com` / `douyin.com` / `xiaohongshu.com`，**加短链域名** `b23.tv` / `v.douyin.com` / `xhslink.com`（否则短链先落 CEF、302 之后才发现开错）。

#### 9.2.3 提示规则（和判定分开，D16）

只在**会造成功能缺失**时提示，不做"每次跨引擎都弹"：

| 触发 | 提示 |
|---|---|
| CEF tab 导航到白名单域名 | 「此站的视频需要兼容模式 › 用兼容模式打开」 |
| CEF tab 检测到 H.264 不可用且页面在放视频 | 同上（见 9.2.4） |
| 兼容 tab 导航到白名单外的站 | 「用主引擎打开更稳 ›」（低优先级，可只留图标） |
| 偏好表里已有该 host 的选择 | **不再提示** |

形态：**28pt 细条，5 秒后收成地址栏右侧的常驻图标**（点开还是同一个动作）。同域名 24h 内最多提示 2 次。

#### 9.2.4 视频"静默坏"（最阴的 case）

H.264 缺失**不一定报 `MEDIA_ERR_SRC_NOT_SUPPORTED`**：大量播放器先 `MediaSource.isTypeSupported()` 探测，探测失败就直接黑屏/转圈/"加载失败"，**不触发 media error**，我们检测不到。所以三层叠加：

1. **域名白名单**（最可靠）
2. **`document-start` 探针**：`MediaSource.isTypeSupported('video/mp4; codecs="avc1…") === false` 且页面有 `<video>` 或 MSE 使用痕迹 → 提示
3. **`MEDIA_ERR_SRC_NOT_SUPPORTED` 事件兜底**（覆盖既没探针又报错的站）

再加一条兜底出口：**地址栏常驻「兼容模式」图标**，任何 tab 都能一键切。检测永远有漏网，这个图标不能砍。

#### 9.2.5 全量 case

**创建入口**

| Case | 引擎 | 备注 |
|---|---|---|
| 地址栏输入 URL / 搜索词 | 判定 | 结果页点进去按目标域名重判 |
| 聊天 / 文档 / 消息里的链接 | 判定 | 与地址栏同一入口 |
| 收藏 / 历史点开 | 判定（现算） | 不存引擎 |
| 右键"用 X 搜索"选中文字 | 判定 | `AppBrowserWebView` 那套只在 WK 侧有效，要补 CEF 版 |
| agent `open_url` / `navigate` | 判定 + `engine` 覆盖 | 视频站默认 compat，但 agent 之后只能只读；需要读 bilibili 页面时显式传 `engine: cef`（D19） |
| 拖入本地文件 | compat | 本地文件不需要 Chrome 身份；**CEF 能否显示 PDF 待实测**，不支持就写死 compat |

**popup / 新窗口**

- `target="_blank"`、`window.open` → **引擎跟 opener 一致**，即使目标是白名单域名（D17）。
- 抖音/小红书的"扫码登录"弹窗、支付回调都依赖同引擎。
- 不做 `noopener` 特例——统一跟 opener，少一条分支。

**主框架导航**

| Case | 行为 |
|---|---|
| 站内跳转（B站首页 → 视频页） | 同引擎，正常 |
| CEF tab 跳到白名单域名（百度结果 → B站） | **留在 CEF + 提示**（不自动切）；用户点提示 → 新 compat tab，原 tab 停在百度结果 |
| 兼容 tab 跳到白名单外（B站里点外链） | 留在 WK + 轻提示 |
| **重定向链内部**（短链 302、登录回跳、支付跳转） | **一律不评估、不打断**；等 `didFinishNavigation` 且无 pending redirect 后再看 |
| 站点 `location.replace` 到白名单 | 同上：链结束后提示，不中途切 |

重定向链不能中途切的原因：切引擎 = 新进程 + 新 cookie 上下文，登录/风控 nonce 全断，OAuth 直接失败。

**登录态（两套 profile）**

| Case | 行为 |
|---|---|
| 百度在 CEF 登录、B站在 WK 登录 | 无感 |
| 同一站在两个引擎都开过 | **两套 cookie 互不相通**，表现为"怎么又没登录"；提示条要写清"兼容模式是独立会话" |
| 设置里"清除浏览数据" | **必须两边都清**，否则用户以为清了、CEF 里还留着 |
| 谷歌登录 | 全程 CEF；登录链是重定向，判定函数天然不会切 |

**agent 工具与兼容 tab（D18）**

| 能力 | 兼容 tab |
|---|---|
| 只读（`snapshot` / read_text / screenshot / PDF） | **开放**（WK 侧已有实现，成本≈0；顺带解决"让 agent 读小红书笔记"） |
| 写操作（click / fill / press / select / 上传） | **禁止**（用户可能正在这上面看视频） |
| `list_tabs` | 标 `engine: compat, 只读`，让模型一开始就不试 |
| 写操作报错 | 结构化 `engine_unsupported` + "该 tab 为兼容模式，仅支持只读"，别让模型反复重试 |

**会话恢复 / 历史 / 收藏 / 分享**：只存 URL；历史按 URL + 时间去重（同一 URL 两个引擎不记两条）；分享导出 URL，与引擎无关。

**两套实现的共有功能（别漏）**

| 功能 | 说明 |
|---|---|
| 下载 | 两套 handler，同一个下载目录 + 同一个历史列表 |
| 媒体嗅探 | 兼容 tab 仍走 WK 版（见第五章） |
| 截图 / PDF | 按 engine 分支：CEF 走 CDP，WK 走 `takeSnapshot` / `createPDF` |
| 查找 | CEF `CefFindHandler` / WK `find(_:)` |
| 缩放 | CEF `SetZoomLevel` / WK `pageZoom`；**按域名共用一份** |
| 全屏 | 兼容 tab 用 WK 原生（观感和现在一致）；CEF 侧要自己实现，观感会有差异 |

**崩溃 / 降级**

- CEF renderer 崩 → 只重载该 tab；兼容 tab 在另一套实现里，不受影响（混合方案的隐性优势）。
- CEF 整体起不来（Helper 被拦 / 初始化失败）→ 启动降级：全部走 compat + 顶部横幅「主引擎未启动，已切兼容模式」。这条要写进实现，不能只靠"不崩"。

#### 9.2.6 数据模型（最小）

```swift
enum BrowserEngine { case cef, compat }

struct RightSidebarTab { … var engine: BrowserEngine }   // 创建后不可变

struct EnginePreference {                                 // 按 eTLD+1 记忆
    var host: String
    var engine: BrowserEngine
    var source: EngineSource                              // .user / .default
}
```

#### 9.2.7 待实测（会影响 case）

- **CEF 能否显示 PDF**（PDFium 是否随官方二进制分发未确认）→ 不能则 PDF / 本地文件固定 compat（判定函数第 1 条已预留）。
- **CEF 全屏观感**（`CefWindowHandler` 自己实现）与 WK 原生是否差太多。

#### 9.2.8 已拍板补充（2026-11-12，进开发前）

| # | 决定 | 落地 |
|---|---|---|
| **D-a** | 回 WK 的隐藏开关 = **`UserDefaults` key + 设置页 dev 区块的 Picker**，不做连点彩蛋 | key `xhan_browser_engine_<env>`（值 `compat` / `cef`，与 `LocalPaths.scoped` 同形）；设置页仅 `#if DEBUG && LocalPaths.isDev` 露出 |
| **D-b** | B2 期间**视频白名单不生效**：`resolveEngine` 全写完但 `videoWhitelistEnabled = false`，白名单 B5 才打开 | `BrowserEngineRouter.videoWhitelistEnabled`；B2 里视频站要回 WK 就靠 D-a 的开关 |

**B2 实现口径（代码即事实，便于 B3 接着改）**

- 协议：`BrowserPage`（`Shared/Services/BrowserPage.swift`）只放面板真的用到的六个读状态 + 七个动作 + `hostingView` + 状态广播。**B6a/B6b 已把页内查找、缩放、下载、JS 对话框收进协议**（`find`/`zoomScale`/`onDownloadPathRequested`…），现在只有 JS 求值与快照还走 `wkWebView` 过渡口；CEF 标签那里是 `nil`，调用方必须给明确失败（`TabSession.cefUnsupported`）。
- `TabSession` 持有 `page`，`webView` 变成 `page.wkWebView`（可选）。CEF 没有 delegate，标题 / URL / 加载态由 `CefPage` 转成同一套 `onTitleChange` / `onURLChanged` / `loadContinuation`。
- ~~agent 标签在 B2 里固定走兼容引擎~~ **B7 已改回 `resolveEngine`**（agent 标签与用户标签同一套判定 + `engine` 参数，D19）：JS 求值通道接通后快照/读正文/点击都不再依赖 WKWebView。
- CEF 起不来（缺 framework / `CefInitialize` 失败）→ `TabSession.makePage` 就地降级 WK + Toast「主引擎未启动，已切兼容模式」，`engine` 以实际造出来的页面为准（不谎报 cef）。
- 新标签页在 CEF 侧走 `data:text/html;base64,...`（CEF 没有 `loadHTMLString`）；地址栏对 `about/data/file` 一律显示空。
- **`target=_blank` / `window.open`**：B4 时是"拦掉 → 用 `openURL` 开兄弟 tab"（`window.open()` 返回 null，opener 关系丢失）；**B9b 已改成真弹窗**（见 9.2.14）：CEF 自己建 popup browser，只把它的渲染视图嵌进我们的 tab。

#### 9.2.9 B4 实现口径（多 tab 面）

- **D17 落地**：WK 的 popup / `noopener` 跳转仍走 `openURL(url, openerEngine:)` 带上 opener 的引擎；CEF 侧 B9b 起由 CEF 自己建 popup browser（引擎天然跟 opener）；收藏 / 历史 / 地址栏 / 右键搜索不带（按 URL 现算，它们不是"从某页弹出来的"）。
- **~~已知限制：CEF 弹窗没有 `window.opener`~~** 2026-11-13 已解决（B9b，见 9.2.14）：CEF 弹窗现在是真 popup browser，`window.open()` 返回可用的 WindowProxy，`postMessage` / `popup.close()` / `window.opener.location` 都能用。
- **强制回收**：关 tab → `CloseBrowser(force_close)`；另补两处兜底，否则会留 renderer：① 视图已释放时 `OnAfterCreated` 里直接关（tab 开出来就被配额回收）；② `CreateBrowser` 的异步窗口期内被关（`_closeRequested`），created 后立刻关。
- **标签恢复**：`closedStack` 只存 URL，开的时候现算引擎（§9.2.2）；目前没有跨启动的会话恢复。
- **后台 tab（B7 前置）**：CEF 的 browser 在**视图进窗口时**才创建（`viewDidMoveToWindow`），而侧栏只挂激活 tab → 后台开的 tab 不会加载。现在没有"CEF 后台 tab"的用户路径（agent 标签 B2 起固定 compat），**B7 把 agent 标签接 CEF 之前必须先解决**（二选一：视图一创建就建 browser；或后台 tab 保留挂载）。
- ~~无 URL 的 `window.open`（先开窗再 `document.write`）仍然丢弃~~ B9b 起也能承接了（CEF 建的 popup browser 本身就是 about:blank，页面随后自己写内容）。

#### 9.2.10 B5 实现口径（兼容 tab + 跨引擎）

- **白名单已生效**：`BrowserEngineRouter.videoWhitelistEnabled = true`（D-b 的临时关闭结束）。视频站含短链（`b23.tv` / `v.douyin.com` / `xhslink.com`）**新建 tab 时**就落兼容引擎，不走"先开 CEF、302 之后才发现"。
- **提示条（D16）**：CEF tab 落到白名单域 → 页面顶部 28pt 细条「此站的视频需要兼容模式」+「用兼容模式打开」（开新 compatible tab，原 tab 停在原地）；离开该域自动收起；同一 tab 会话内同域只弹一次；同 host 24h 最多 2 次（`EngineHintGate`，按 eTLD+1 计数）；偏好表里已有该 host 的选择 → 不弹。
- **地址栏换引擎图标**：主引擎页显示 safari 图标（换兼容打开）、兼容页显示 globe 图标（换主引擎打开）。这是 §9.2.4 的兜底出口——检测永远有漏网，任何页都得能一键换内核。点一次同时写进偏好表（D15 第 4 条「别再问同一站」）。
- **降级横幅**：`CefInitialize` 失败过（`cefInitFailed`）→ 兼容 tab 顶部常驻「主引擎未启动，当前为兼容模式」；地址栏的"换主引擎"按钮同时置灰，不给"点了必然降级回来"的按钮（原来只有一次 Toast）。
- **三层检测的现状**：第 1 层（域名白名单）已生效；第 2/3 层见 §9.2.13（B9 已实现，走 `XHMEDIA:` 回报，不做第二条协议通道）。

#### 9.2.11 B6 实现口径（浏览器功能补齐，分批）

**B6a（本批）**

- **页内查找**：`BrowserFindCoordinator` 从"持有 WKWebView"改成"持有 `BrowserPage`"。WK 侧仍只报命中与否（`WKFindResult` 没有计数），CEF 侧经 `CefFindHandler::OnFindResult`（只在 `finalUpdate` 时取）报命中数；两边都归一成 `onFindResult(count)`。⌘F/⌘G/⌘Shift+G/Esc 与查找栏 UI 不动。
- **缩放**：`BrowserPage.zoomScale/setZoomScale`。WK 走 `pageZoom`，CEF 走 `CefBrowserHost::SetZoomLevel`（`level = ln(scale)/ln(1.2)`，CEF 自己按 origin 记住）。⌘±/钳制 0.5–2.5 不变。
- **JS 对话框**：`CefJSDialogHandler::OnJSDialog` → `CefPage.onJSDialog` → 复用 WK 那套非阻塞 sheet（alert/confirm；prompt 与 WK 侧一致简化为提示框并回默认值）。桥里做了 **reply 只调一次**的护栏，且没接线时立刻回"取消"，绝不让页面干等。

**B6b（本批）**

- **下载**：`CefDownloadHandler`。`OnBeforeDownload` 里同步问宿主路径（`BrowserToolProvider.userDownloadDestination`，与 WK 侧同一套：`~/Downloads/xhanHelper/` + sanitize + 重名序号），`Continue(path, false)` 不弹 CEF 自带的另存为框；不接线/取不到路径时不执行 callback → CEF 按取消处理（Alloy 模式不接这条就是"点了没反应"）。
  - `DownloadHistoryStore` 加 `cancelHandler`（不落盘）：CEF 这类**外部驱动**的下载由内核侧取消，`cancel()` 时回调。
  - 记录**在第一次收到进度时才建**（那时才有 `fullPath`/真实文件名）；`OnDownloadUpdated` 早于 `OnBeforeDownload` 时 `fullPath` 为空，等下一拍。完成/取消走 `finish/`cancel``，状态守卫保证不覆盖用户手动取消。
- **右键菜单**：Alloy 模式（子视图窗口）**没有默认菜单实现**，光改模型不会显示 → 自己实现 `RunContextMenu`：把 `CefMenuModel` 递归摊成 `NSMenu`（tag 存 command_id，选中后 `callback->Continue` 交回 CEF 执行默认动作；Esc/点空白 `menuDidClose` → `callback->Cancel`）。`OnBeforeContextMenu` 只在有选中文字时追加一条「用百度搜索「…」」（`MENU_ID_USER_FIRST+1`），动作回 Swift 开新 tab（默认承运商会跳系统浏览器，与 WK 的 `AppBrowserWebView` 同因）。
- **菜单标签反转义**：CEF 的 label 带 Windows/Linux 助记符（`&Back` → Alt+B），macOS 原生菜单不认，直接显示会带一串 `&` → `XHMenuLabel` 按标准规则处理（`&&` → 字面 `&`，单个 `&X` → `X`）。
- **DevTools**：CEF 的默认模型在 Alloy 下不带这一项 → 自己追加「检查元素」（`MENU_ID_USER_FIRST+2`），`ShowDevTools` 传空 `CefWindowInfo`（macOS 的 `CefWindowInfo` 只有 `SetAsChild`，没有 `SetAsPopup`，cefclient 同样传默认值）由 CEF 开一个独立原生窗；带 `inspect_element_at` 坐标，打开即定位到右键处。**真机待验**：Alloy 能否起 DevTools 窗（libcef 里有 `devtools_window_runner`），起不来就把 DevTools 挪到 B9 的窗口宿主里做。
- **`<input type=file>`**：不实现，靠 CEF 默认（`file_dialog_runner` + `select_file_dialog_bridge` 都在 libcef 里，macOS 原生 NSOpenPanel）。真机弹不出来再自己接 `CefDialogHandler`。

**B6 剩下没做的**（都在等 CEF 的 JS 结果通道，B7 一起）：favicon、权限（`CefPermissionHandler`：摄像头/麦克风/定位一律拒，与 WK 同策略）、埋点迁移、后台媒体保活（要"页面是否在播"的信号）、墙检测/手递手。

#### 9.2.12 B7 实现口径（agent 工具接 CEF）

- **JS 求值通道**（本批核心）：CEF 没有"执行并返回结果"的 API（`CefFrame::ExecuteJavaScript` 无返回值），改成自建应答通道：
  - 浏览器进程 `XHCefBrowserView.evaluateJavaScript:completion:` → `SendProcessMessage(PID_RENDERER, "xh_eval")`（`id` + `script`）；
  - 渲染进程 `XHCefRenderProcessHandler::OnProcessMessageReceived`（`CefDisguise.h`，与伪装注入同一个类）在 frame 的 `CefV8Context` 里跑 `EvalWrapper(script)`，回 `"xh_eval_result"`（`id` + `json` + `error`）；
  - `EvalWrapper` 用 `(0,eval)(<脚本字面量>)` 包一层：**表达式和语句都能跑**（取完成值），结果 `JSON.stringify`，异常转成 `{ok:false,error}`。**回包只认字符串**（`JSON.stringify({ok,…,json|error})` 的信封）——第一版回 V8 对象，实测跨回浏览器进程 `GetValue("ok")` 读不到值（表现为所有 eval 报”JS 抛异常”），改成信封字符串后稳定。脚本以 JS 字符串字面量传入（转义引号/换行/反斜杠/控制字符），不做拼接，避免注入与语法碎片。自检：8/8（表达式 / IIFE / 语句块 / throw / undefined / 可选链 / 嵌套结构 / 引号反斜杠）。
  - 三条收尾路径都必须只走一次：正常回包、**10s 超时**（renderer 崩了/页面在导航时回包可能永远不来，agent 工具不能挂死）、**关页**（`cefBrowserDidClose` 把待回包一律失败）。
- **协议**：`BrowserPage.evaluateJS(_:) async throws -> Any`（WK = `webView.evaluateJavaScript`，CEF = 上面这条）。agent 的四个 JS 助手（`jsResult` / `jsBool` / `jsJSON` / `eval`）全部改成走 `page.evaluateJS`，`cefUnsupported` 在读写类工具上不再出现。
- **agent 标签回到 `resolveEngine`**（含 `open` 的 `engine` 参数，D19）：不再固定兼容引擎。仍只在 WK 标签可用的：`cg_click` / `cg_drag`（真实鼠标注入按 WK 视图坐标写，CEF 上给明确失败 `cefUnsupportedCGEvent`）。
- **后台 renderer 必须关降频**：`XHCefApp::OnBeforeCommandLineProcessing` 追加 `--disable-background-timer-throttling` / `--disable-renderer-backgrounding` / `--disable-backgrounding-occluded-windows`。agent 标签停在隐藏窗口里，不关这三个就是「标题正确、`readyState` 永远不是 complete、页面空壳」。
- **还留在 WK 侧的**（不阻塞读类工具）：滚动位置保存/恢复（CEF 切 tab 由内核维持）、后台媒体保活探测、媒体嗅探面板、活动埋点的浏览状态机。这些在 CEF 标签上是静默跳过，不是报错。

#### 9.2.13 B9 实现口径（三层媒体检测第 2/3 层）

白名单（第 1 层）只覆盖已知视频站；**非白名单站点**（微博 / Twitter / 各种 embedding）的 H.264 视频在主引擎上会**静默播不了**（黑屏/无声，不抛错）。B9 补后两层：

- **探针注入**：`xhdisguise::MediaProbeJavaScript()` 在渲染进程 `OnContextCreated` 里紧跟伪装脚本同步 Eval（document-start、页面脚本之前）：
  - 第 2 层：`<video>` 触发 `loadstart` 时判能力（`MediaSource.isTypeSupported('video/mp4; codecs="avc1.42E01E"')` 或 `canPlayType`），不支持就报 `no-h264`。**只对看着像 H.264 的源报**（`.mp4/.m4v/.mov`，含 `<source>` 子元素）——否则 VP9/WebM 页面明明能播也会被劝去兼容引擎；`blob:` / 无 src / MSE 判不出来的不报，交给第 3 层；
  - 第 3 层：监听媒体元素的 `error`，`error.code === 4`（`MEDIA_ERR_SRC_NOT_SUPPORTED`）→ 能力不符报 `no-h264`，否则报 `media-src-unsupported`。
  - **判断不了就当能播**（嗅探异常时 `supported = true`）：宁可漏报，也不要天天误报。
- **回报通道**：页内 `console.error("XHMEDIA:<reason>")` → 浏览器进程 `CefDisplayHandler::OnConsoleMessage` 抓前缀（与回归探针的 `XHPROBE:` 同一条通道，不新增协议）→ `CefPage.mediaIssueReason` → `stateObservers.fire()`。
- **UI**：`BrowserTabView` 复用白名单那条提示条（`engineNoticeBar`），文案按原因区分（缺 H.264 / 编码不支持），动作仍是「用兼容模式打开」并写偏好表；频率闸复用 `EngineHintGate`（同 host 24h 2 次），同一 tab/view 只弹一次。
- **自检**：探针 JS 抽出来用 node 跑桩对象 11/11（无 H.264 × mp4 loadstart 报、webm/blob/无 src 不报、`<source>` 里是 mp4 也报、无 H.264 × error4、有 H.264 × error4（编码不支持）、普通 error 不报、img 的 error 不报、audio 覆盖）。

> 2026-11-13 代码评审补丁（见 `docs/code-review/browser-chromium-migration/2026-11-13.md`）：第 2 层加了上面那条 H.264 源过滤；提示条按 host 收起（换站不再残留）；`CefPage` 换 host 时清 `mediaIssueReason`。

#### 9.2.14 B9b 实现口径（`window.opener` 真弹窗）

B4 把 `window.open` / `target=_blank` 拦成**兄弟 tab**，代价是弹窗页拿不到 `window.opener`：`window.open()` 返回 `null`、`opener.postMessage` 不存在、弹窗里的 `window.close()` 被浏览器拒绝（"Scripts may close only the windows that were opened by them"）。扫码登录、OAuth 回调、"登录完自动关窗"这类流程全都要靠它，所以这里改成**让 Chromium 自己建 popup browser**——只有它自己建的 WebContents 才带 opener 关系（CEF 没有"给已有浏览器设 opener"的 API）。

- **`OnBeforePopup`（`CefLifeSpanHandler`）**：不再一律 `return true`（拦掉）。
  1. 先造一个"弹窗宿主视图" `XHCefBrowserView initAsPopupHostWithURLString:`（浏览器不由它建，它只提供 client + 落脚视图）；
  2. 回调 `popupPageRequested(popupView, url)` 给宿主 → Swift **同步**开 tab（视图从这一刻起有人持有）；
  3. `client = popupView` 的 client、`windowInfo.SetAsChild(<隐藏窗口里的宿主视图>, 1×1)`、`return false`。
- **落脚 + 搬家**：CEF 建浏览器要求父视图已经在某个窗口里，而弹窗 tab 这时刚建出来、视图还没进窗口 → 先挂在常驻的隐藏窗口（`XHCefParkingWindow`，B7 后台标签用的同一个）；`OnAfterCreated` 里把 CEF 的渲染子视图 `removeFromSuperview` + 加到自己身上（就是 cefclient `BrowserWindowStdMac::ShowPopup` 的做法）。
- **必须给独立 client**：`OnBeforePopup` 不给 `client` 时 CEF 会复用 opener 的 client → 弹窗的 URL / 标题 / `OnBeforeClose` 全串到 opener 的 tab 上（B4 实测过"地址栏变百度地图、页面还是百度首页"，更糟的是会把 opener tab 关掉）。
- **页面自己关窗**：`window.close()` → CEF 关浏览器 → 不是我们主动关的（`_closeRequested == NO`）→ 回调 `pageClosedExternally` → 宿主关掉这个 tab（否则留一张不会再响应的死页面）。通知走 `dispatch_async`，不在 `OnBeforeClose` 栈里关 tab。
- **兜底**：CEF 建浏览器失败只回调 opener 的 `OnBeforePopupAborted`（拿不到弹窗视图），所以弹窗宿主自己挂 12s 定时器：还没 `OnAfterCreated` 就按"这个 tab 废了"关掉——不留永远空白的页。
- **没接线的宿主**（`--cef-smoke` 那种裸窗口）仍按老口径拦掉，Console 留 `[cef] popup dropped (url=…, no host)`。
- **引擎**：弹窗天然是主引擎（要真 opener 只能让 Chromium 建浏览器）。WK opener 的弹窗走原路径（父 configuration 派生），两边都保持 D17。
- **未处理**：`windowFeatures` 里的尺寸/位置仍不适用（弹窗是我们的 tab，不是原生窗口）；`noopener` / `rel=noopener` 由 Chromium 自己处理。

## 十、CEF 版本运维

**为什么需要**：谷歌那条判定是黑盒，靠"名义层"伪装；内核太旧（落后一年）本身会被当旧浏览器，反而提高风控概率。所以既要能"该升就升"，又不能"每天发版被迫跟"。

### 10.1 升级触发策略

| 触发 | 动作 |
|---|---|
| 例行 **3~6 个月** | 升一次 |
| 常用站点提示"浏览器版本过低/不支持" | 立即升 |
| CEF 修高危 CVE | 立即升 |

落后 2~3 个大版本（约 3 个月）基本无感；落后一年开始有风险。

### 10.2 两档检查（每天发版也扛得住）

| 闸门 | 触发 | 行为 | 耗时 |
|---|---|---|---|
| **A 构建期** | Xcode Run Script（macOS target，编译前） | 落后就 `warning:`（黄标），**构建照常成功** | 缓存命中 ~50ms |
| **B 发版期** | `scripts/check-cef-version.sh --strict`（发版前手动跑） | 超阈值红字 + 非 0 退出 | ~1s |

不默认硬失败的理由：每天发版，网络抖动或 CEF 发版当天没升，不该卡住构建。

**A 档查什么**

```bash
# 1. 实际装进 app 的版本（权威来源：framework 的 Info.plist）
FRAMEWORK="$BUILT_PRODUCTS_DIR/$FRAMEWORKS_FOLDER_PATH/Chromium Embedded Framework.framework"
SHIPPED=$(defaults read "$FRAMEWORK/Resources/Info.plist" CFBundleShortVersionString)  # 156.0.7.0

# 2. 仓库 pin 的版本与日期
PINNED=$(cat "$SRCROOT/CEF_VERSION")        # 156.0.7+g47dbc1d
PINNED_AT=$(cat "$SRCROOT/CEF_VERSION_AT")  # 2026-11-12

# 3. 最新 stable（24h 缓存；失败回落旧缓存，永不阻塞）
#    cef-builds.spotifycdn.com/index.json → macosarm64 + stable → 取最大 chromium 版本
```

| 落后程度 | 输出 |
|---|---|
| ≤1 个大版本 且 ≤90 天 | 静默 |
| ≥2 个大版本 **或** ≥90 天 | `warning: CEF 落后 N 个大版本（156 → 159），已停更 M 天` |
| ≥4 个大版本 **或** ≥180 天，且 `${ACTION}` 是 archive | 强提示（`CEF_STRICT=1` 时 `exit 1`） |

**两个附加检查（更值钱）**

1. **伪装与内核版本一致性**：B3 起 UA / brands / `sec-ch-ua` 全部从 `cef_version.h` 拼（见第三章），**编译期就不可能对不上**，这条检查从"grep UA 常量"降级为"不用做"。真正会漂的是 GREASE 陪跑品牌（手写常量）——它只能靠 `cef-regression.sh` 的探针发现，所以第 2 条比第 1 条更值钱。
2. **升级未回归**：`scripts/cef-regression.sh` 成功后写 stamp（`{"version":"156.0.7"}`）；构建时 `shipped != stamp` → `warning: 156.0.7 是升级后首次构建，先跑 scripts/cef-regression.sh`。

### 10.3 工程细节（已核实）

- macOS target 的 `ENABLE_USER_SCRIPT_SANDBOXING = NO`（`project.pbxproj` 461/504 行），**脚本可联网**；iOS target 是 YES，但 iOS 已砍浏览器。
- 已有 `scripts/bundle-backend.sh` 这个 Run Script 先例，加一个 phase 同构。
- 首次构建 framework 还不存在 → 静默跳过，不报红。
- 网络失败 → 只用缓存，最多提示"无法检查更新（离线）"。

### 10.4 产物清单（P1 第 3 步一起交付）

```
client/xHelper/CEF_VERSION            # 156.0.7+g47dbc1d
client/xHelper/CEF_VERSION_AT         # 2026-11-12
client/xHelper/scripts/check-cef-version.sh   # Xcode Run Script 与发版前 --strict 共用
client/xHelper/scripts/cef-regression.sh      # 自动：UA/sec-ch-ua/userAgentData/window.chrome（探针）；人工清单：谷歌假邮箱、百度三站
client/xHelper/scripts/update-cef.sh          # 下载/替换/签名一条龙（P4，可选）
```

## 十一、开发流程（worktree / 双引擎 / dev 隔离）

### 11.1 分支与 worktree

```
main                       ← 继续每天发版（WK，现状不变）
└── feat/browser-cef       ← 长期特性分支，P1~P4 都在这
```

- **一条长期分支**，按阶段打 commit（`P1: 骨架` / `P1: 四层伪装` / `P2: 多 tab`…），不按阶段开多条——阶段之间耦合紧，来回 rebase 更痛。
- worktree 只用于**构建验证**，代码都在同一条分支。
- 合并点只有 **P2 结束一次**（D4）；P1 期间不并回 main。回滚靠 main 上的 `pre-cef` tag。

### 11.2 双引擎与开关

- `BrowserEngine` 协议先行，`CEFEngine`（主）+ `CompatWebView`（WK，永久）并存。
- **dev 构建默认 CEF**（D10），隐藏开关可切回 WK；CEF 挂了不必回退整条分支。
- 主 tab 走 CEF、视频域名 tab 走 `CompatWebView`，由 tab 的 `engine` 字段决定，不是全局开关。
- `BrowserToolProvider.swift`（4288 行）**不做一次性重写**：先抽薄协议、只换宿主调用点，把冲突面压到最小。WK 那份不删（D11）。

### 11.3 dev 数据隔离（必须）

- dev app 数据在 `~/Library/Application Support/xhanHelper-dev/`；**CEF 的 cache / cookie / profile 必须落在 `.../xhanHelper-dev/CEF/`**，绝不能与 prod 共用。
- 否则：dev 登录态污染 prod；dev 进程崩溃可能损坏 prod 的会话数据。

### 11.4 worktree 首次构建

- `bundle-backend.sh` 在 `front-end/node_modules` 缺失时直接报错退出 → 新 worktree 先 `npm ci`，或只验客户端时设 `SKIP_BUNDLE_BACKEND=1`。
- Debug 构建 → `config.local.dev.yaml`（dev 库 + 18924 端口），符合"只在开发环境用"。

### 11.5 构建节奏与协作边界

- 本项目约定 **agent 不跑 `xcodebuild`**；Swift 可用 `swiftc -typecheck` 自检，**ObjC++ 桥与 `project.pbxproj` 改动 agent 验不了** → 每次涉及 CEF 的改动由用户构建。
- 为减少往返，按"一坨功能一起提、一次构建"的节奏，不做一行一构建。
- `project.pbxproj` 的改动（Framework 嵌入、Helper 拷贝、签名/检查 Run Script）**集中成一个 commit 尽早完成**，之后不再碰，避免和 main 的工程改动撞车。
- 第一次 P1 构建预计失败最多（链接、Helper 路径、签名），预算半天到一天。
- **agent 能自己验的部分**（都不算 xcodebuild）：`.mm` 走 `xcrun clang++ -fsyntax-only -std=c++20 -fobjc-arc -I<CEF_CACHE>/dist`；再在 `/tmp` 里链一个小可执行文件验"只链 wrapper 能过"；Swift 侧照旧 `swiftc -typecheck`，但**必须带** `-import-objc-header xHelperMac/xHelperMac-Bridging-Header.h`（漏了会把 `XHCefRuntime` 报成 unresolved）**且带 `-D DEBUG`**（冒烟窗整块在 `#if DEBUG` 里，漏了会把该文件整段跳过 —— B1 就这么漏过一次编译错误）。真机合成冒烟只能由你构建。

## 十二、验收

- 谷歌：真实账号能登录到账号页（2FA 由用户完成）。
- 百度搜索 / 百科：各 10 次，验证码 ≤1 次。
- 百度地图（高频工具，单列）：路线搜索 10 次成功 ≥9；拖动/缩放 30 秒不卡死、瓦片正常加载；全景/3D 能开；0 次验证码。
- `docs/design/mac-apps/browser-agent-workspace.md` 的手递手用例全通过。
- 视频：B站 / 抖音 / 小红书在**兼容 tab** 里能正常播放（拖动进度、全屏、弹幕），且启动页默认就落在兼容 tab。
- 兼容 tab 的 agent 能力符合 D18：只读工具（snapshot / 读文 / 截图 / PDF）可用；写操作返回明确的 `engine_unsupported`，`list_tabs` 标注 `engine: compat, 只读`。
- 任意 CEF tab 内 `<video>` 播放失败时能出现「兼容模式」提示条，点一下同 URL 在 WK 里重开。
- **跨引擎导航**（9.2.5 各表逐条走一遍）：
  - 百度搜"B站 ××"点进 B站 → 留在 CEF + 提示；点提示 → 新 compat tab，原 tab 仍停在百度结果
  - 短链 `b23.tv` / `v.douyin.com` / `xhslink.com` 打开 → 直接落兼容 tab，不出现"先 CEF 再 302"
  - `target="_blank"` / `window.open` → 新 tab 引擎跟 opener 一致；抖音扫码登录、支付回调不断
  - 谷歌登录、OAuth 回跳全链路不切引擎
  - 用户点过一次「用兼容模式打开」→ 该域名之后静默走 compat，不再提示
  - 设置里"清除浏览数据"后，两个引擎的 cookie 都清干净
  - 收藏/历史里打开 B站链接 → 落到兼容 tab（引擎现算，不读旧字段）
- CEF 整体不可用时能降级：全站 compat + 顶部横幅，浏览器面板不白屏。
- 关闭 4 个 tab 后，Activity Monitor 里对应 Renderer 进程 30s 内退出。
- 冷启动到浏览器面板可交互 ≤2s（CEF 初始化在后台完成，不阻塞主界面）。
- iOS 构建通过，且浏览器面板/浏览器工具组不出现。
- dev 构建默认引擎是 CEF，隐藏开关能切回 WK 且功能可用。
- 发版前 `scripts/check-cef-version.sh --strict` 有明确结论、升级后 `scripts/cef-regression.sh` 全绿。

## 十三、已知坑与验法（实施前必读）

### 13.1 macOS 平台坑（P1 就该验）

| 坑 | 表现 | 处理方向 |
|---|---|---|
| **⌘ 快捷键被 CEF 吞** | ⌘K 聚焦聊天输入、⌘W、⌘F、⌘+ 落到网页里 | **已由现有机制覆盖**：`AppDelegate` 的局部 `NSEvent` monitor 在 `sendEvent:` 之前拿到事件（B1 未加 `CefKeyboardHandler`，因为它的 `return true` 是"我已处理"、不是"交回 AppKit"，加错方向会把 ⌘←/⌘C 这类网页编辑键一起吞掉）。冒烟时手测确认；漏网的某键再按 keyCode 精确拦 |
| **入口必须改 `static func main()`** | SwiftUI `@main` 无法在 `NSApplicationMain` 前跑 `CefExecuteProcess --type=` 分支 | 改入口；这条正好在 `swiftc -typecheck` 盲区，只能真机验 |
| **Swift 6 严格并发 × ObjC++** | 项目带 `-default-isolation MainActor`，回调里的非 Sendable 类型会一片红 | 预计一批 `nonisolated(unsafe)` / `MainActor.assumeIsolated` 包装；不是难点但是实打实工作量（P1 可能从 3~5 天撑到 5~7 天） |
| **遮挡 / Space 切换后黑帧** | 窗口模式下 `WasResized` / `WasHidden` / `NotifyScreenInfoChanged` 都是 **OSR 专用**（头文件原文 "only used when window rendering is disabled"），调了也是空操作 | 窗口模式交给 CEF 自己的 NSView 跟随尺寸；真出现黑帧别往这三个 API 上找，改查视图形状/材质叠加（`docs/bugs/030`） |
| **后台 tab 根本不加载**（B7 实测） | CEF 的 `CreateBrowser` 要求父视图落在一个窗口里；agent 标签默认不进侧栏（`activate=false`），视图从没 `viewDidMoveToWindow` → 浏览器压根没建 → `open` 全部卡到 15s 超时（页面看着没问题，实际一个都没加载） | `createBrowserIfNeeded` 里若 `self.window == nil` 先停进进程内的隐藏 "parking window"（1280×800、alpha 0、永不 orderFront），真挂进侧栏时只是换父窗口，CEF 的子视图跟着走；`loadURLString:` 在浏览器未建时也主动触发一次创建 |
| **后台 tab 被节流** | Chromium 对隐藏 renderer 降频，agent 自动化会莫名卡住 | `OnBeforeCommandLineProcessing` 加 `--disable-background-timer-throttling --disable-renderer-backgrounding --disable-backgrounding-occluded-windows` |
| **`Page.captureScreenshot` 对不可见 tab** | 可能出空白图 | P1 顺手验，结论决定 agent 截图工具的边界 |
| **中文输入法** | 候选窗位置、`ImeSetComposition` | 内嵌 NSView 一般没问题，但要手打一句验证 |
| **定位权限** | 百度地图"我的位置"需要 handler + TCC | 不验就是"地图能看不能定位" |
| **签名顺序** | framework 内 `Libraries/*.dylib` → framework → Helper×5 → Go 后端 → App，和 Xcode 自身签名冲突 | 照 `scripts/bundle-backend.sh` 扩，预算半天 |
| **官方包不含 Helper.app**（B0 实测） | `Release/` 下只有 framework，4/5 个 Helper 是各工程自己 CMake 造出来的 | 脚本按 `helper-Info.plist.in` 模板造（见 6.1） |
| **不能直接链 framework**（B0 实测） | wrapper 是运行时加载形态；直接链会被装成 `@executable_path/../Frameworks/...`，Helper 在 `Contents/Frameworks/<Helper>.app` 里必然找不到 | 只链 `libcef_dll_wrapper`，framework 由 `CefScopedLibraryLoader::LoadInMain()` dlopen |
| **`NSApplication` 必须实现 `CefAppProtocol`**（B0 实测崩过） | 不实现就抛 `-[NSApplication isHandlingSendEvent]: unrecognized selector`（页面已经加载完才炸，很容易误判成别的问题） | SwiftUI 不能给 NSApplication 建子类 → ObjC category 补 `isHandlingSendEvent/setHandlingSendEvent:` + 换掉 `sendEvent:` 的实现（`CefBridge.mm` 里 `InstallSendEventSwizzleIfNeeded`） |
| **首次运行弹「Chromium Safe Storage」钥匙串授权** | 第一次起 CEF 会问"允许使用你储存在钥匙串的 Chromium Safe Storage 中的机密信息"（Chromium 用它加密 cookie/密码）。**没人点的话 profile / 网络初始化就停在那儿**：renderer 进程起得来、页面永不 commit、History 表空、连 network service 都不启动 —— 极容易被误判成"CEF 嵌不进来" | 首次点「允许」（或「始终允许」）。系统按"允许过的 App"记，**换签名身份 / 每次重签都可能重新弹**（ad-hoc 与 Developer 混签、反复重建换 cdhash 时最明显）。排查"页面不加载"先确认没有这条弹窗在等 |
| **页面永不 commit（最费时间的坑）** | renderer/GPU 进程都起得来、浏览器创建成功，但页面永远不 commit：`Default/History` 的 `urls` 表 0 条、network service 不启动、`chrome_debug.log` 干净。看着像"CEF 嵌不进来"，实际是消息泵饿死 | **根因：`CefDoMessageLoopWork` 单次处理的工作量有限**，`external_message_pump` 下一次调度只调 1 次不够推进导航。修法：每次调度里循环调（实测 100 次，稳定 5/5；1 次 3/3 失败、16ms 定时器 1/3 成功）。对照实验：同环境官方 `cefclient` 正常，别怀疑网络/签名 |
| **`process_requirement.cc` 报错**（只在命令行真空壳里出现过；真机没复现，疑与上一条同源） | 自建 `.app` 里 CEF 初始化、Helper 起、browser 创建都正常（`--no-sandbox` 确实传给了子进程），但重跑时出现 `Unable to derive validation category for current process` + `ProcessIsSignedAndFulfillsRequirement`，renderer 活着、导航不提交。同环境下官方 `cefclient.app` 正常 | 真机构建第一件事就复验这条。若复现：优先试 ① Helper 里补 `CefScopedSandboxContext`（改用 CEF sandbox，去掉 `no_sandbox`）② 换 `cache_path`/`root_cache_path` 到非临时目录 ③ 对照 `cefclient.app` 的 Info.plist 逐项对齐 |
| **CEF 156 删了一批老 API**（B3 实测） | `CefRegisterExtension`（`CEF_API_REMOVED(15400)`）、`CefRequestHandler::OnBeforeResourceLoad` 都已不在头文件里，照旧教程写会编译失败 | 注入走渲染进程 `OnContextCreated`；改请求头走 `GetResourceRequestHandler` → `CefResourceRequestHandler::OnBeforeResourceLoad` |
| **渲染进程钩子要有 CefApp**（B3） | `CefExecuteProcess(main_args, nullptr, nullptr)` 时 `OnContextCreated` 永远不触发（症状：页面正常、伪装全无） | `helper_main.mm` 里传 `CefRefPtr<CefApp>`（带 `GetRenderProcessHandler`）；只改浏览器进程的 `XHCefApp` 没用 |
| **popup 不给独立 client 会串台**（B4 实测 / B9b 定案） | `OnBeforePopup` 里不设 `client` → CEF 复用 opener 的 client：弹窗的 URL / 标题 / `OnBeforeClose` 全打到 opener 的视图上（地址栏变弹窗的 URL；弹窗关掉会把 opener tab 一起关） | 每个 popup 单独 `new XHCefClient(popupView)` 交回去；这是 cefclient `DefaultClientHandler` 注释里明写的要求 |
| **popup 的父视图必须已经在窗口里**（B9b） | `OnBeforePopup` 返回 false 时 CEF 立刻需要一个能落渲染视图的 NSView；弹窗 tab 此刻刚建出来、视图还没 `viewDidMoveToWindow` → 不给父视图会建失败（或被 CEF 建成裸原生窗） | 先 `SetAsChild` 到隐藏窗口里的落脚视图，`OnAfterCreated` 再把渲染子视图搬进真视图（cefclient `ShowPopup` 同款）。反过来：**先让 Swift 同步开 tab 再返回 false**，否则视图没人持有 |


### 13.2 资源预算（8GB 内存 / ~47GB 空闲）

- CEF 常驻（GPU + renderer）**再加**兼容 tab 的 WK 内存，而 WK 有"每页 240MB 泄漏"先例 → 兼容 tab 要限数量、关 tab 必须强制释放。
- DerivedData 每份 +1.5GB；CEF 二进制本身放 `~/Library/Caches/xhanHelper-cef/<version>/` 多 worktree 共用（见 6.1），别每个 worktree 存一份。

### 13.3 行为回退（P3 前想清楚）

- **后台自动化只走 CDP**；`CGEventInput` 拟人化输入只在面板可见且聚焦时启用——否则会打进用户当前前台窗口（不是失效，是危险）。
- **`Accessibility.getFullAXTree` 的节点形态与现有 DOM 快照不同**，`snapshot` 类工具输出结构会变，提示词与返回预算要重调。
- **建议加一个工具等价性对照跑**：同一份工具脚本在 WK tab / CEF tab 各跑一遍、diff 输出，比人工点 26 个工具靠谱。

## 十四、开发批次（实施顺序与 review 节奏）

原则：**每批一次真机构建、diff 可读（≤~1000 行）、能独立回滚**；风险验证全部排在机械劳动之前。

| # | 批次 | 主要交付 | 工时 | 构建 | review 重点 |
|---|---|---|---|---|---|
| **B0** | 工程底座 | `CEF_VERSION`/`CEF_VERSION_AT`、`scripts/fetch-cef.sh`（下载 → 解包 → 清 quarantine → 编 wrapper → 造 Helper×5 → `--install` 装包 + 签名）、`CEF/helper_main.mm`、pbxproj（`ARCHS=arm64`、两个 Run Script phase、header/lib 路径、bridging header） | 0.5–1 天 | 1 | **工程改动集中一次**，之后不再碰 pbxproj；签名 phase 与 Xcode 自身签名的先后 |
| **B1** | 合成冒烟 | 入口改 `static func main()` + `CefExecuteProcess`；最小 `CEFEngine`（init/shutdown/单 browser/NSView）；**隐藏调试入口**开一个固定页 | 1–2 天 | 2–3 | 关闭回收（`CloseBrowser(force_close)`）、线程约束、初始化/退出时机。**不过就停** |
| **B2** | 引擎抽象 + 开关 | `BrowserEngine` 薄协议、`BrowserEngineRouter` 骨架、`RightSidebarTab.engine`、dev 默认 CEF + 隐藏开关回 WK、iOS `#if os(macOS)` 边界 | 3–5 天 | 2 | 协议是否够薄（别把 WK 语义带进来）、开关持久化、iOS 构建 |
| **B3** | 四层伪装 | UA / `sec-ch-ua` / brands / `window.chrome` 清洗 + `cef-regression.sh` 转正 | 1–2 天 | 1 | 伪装是否全在三个钩子里、UA 与内核版本一致性检查。**可独立回滚** |
| **B4** | 多 tab 面 | 多 tab、`target=_blank`、popup（引擎跟 opener）、强制回收、标签恢复、分屏（先都 CEF） | 5–7 天 | 2 | 进程回收、popup 策略、恢复逻辑（引擎现算） |
| **B5** | 兼容 tab + 跨引擎 | 两笔提交：① 判定函数/偏好表/白名单/短链 + WK 兼容 tab 能开 ② 提示条 + 三层检测 + 地址栏图标 + 降级横幅 | 5–7 天 | 2 | **规则是否只在 router**；case 漏没漏（弹窗继承、重定向不切、清数据两边清） |
| **B6** | 浏览器功能补齐 | 下载、查找、缩放、右键、JS 对话框、权限、收藏/历史/favicon、埋点、快捷键、墙检测/手递手、后台保活 | 5–7 天 | 2 | 逐条对第五章迁移清单，确认没有"功能悄悄少了" |
| **B7** | agent 读类 | `list_tabs`（标 engine）、snapshot、读文、截图、PDF、`open_url` 加 `engine`、兼容 tab 只读 | 3–5 天 | 1–2 | AX 树形态变化、输出预算、只读边界 |
| **B8** | agent 交互类 | click / fill / press / select / scroll / upload / 等待条件；CGEvent 只在可见时启用 | 4–6 天 | 1–2 | 坐标系、可见性判断、失败语义、误操作边界 |
| **B9** | agent 高级 | 网络拦截、两套媒体嗅探、下载触发、多 tab 协作、工作区归属；**WK vs CEF 等价性对照跑** | 3–5 天 | 1 | 等价性差异清单（哪些工具行为变了，写进文档） |
| **B10** | 打包 / 运维 / 合并 | `update-cef.sh`、stamp 检查转正、体积、安装验证、第十二章验收逐条、`pre-cef` tag | 1–2 天 | 1 | 升级流程能重放；整批 diff + 验收证据 |

合计 **31–53 天（6~10 周）**——比头部的 5~8 周略高，那是"顺利"估计，这里算的是带 review 往返的实际节奏。

### 14.1 排序理由

- **B0 / B1 前置**：`pbxproj` 只碰一次；合成层 + 输入法 + ⌘ 键是唯一"能不能做"的门，用最小代码验，别等 4000 行改完才发现不行。
- **B3 排在多 tab 之前**：伪装是横向能力，越早接越省事；它小、可独立回滚，适合当"第一个能完整 review 的样板"。
- **B4 在 B5 之前**：跨引擎的 popup / 重定向 / 恢复 case **依赖多 tab 存在**，顺序反了就得回头补。
- **B6 在 B7 之前**：agent 工具要用查找、下载、截图这些 handler，先把功能面补齐，工具才不是"迁完发现底层没好"。
- **B7 → B8 → B9 按"读 / 写 / 高级"切**：读类先跑通能立刻验证 AX 树形态；写类风险最高单独一批；高级类最后。

### 14.2 每批的固定动作

```
1. 方案摘要 + 改哪些文件 → 用户确认
2. 写代码 → 一次真机构建（用户）
3. 提交（commit 前缀 cef(bN): …，用户确认后再推）
4. reviewer 子代理 review 当前 diff → 落 docs/code-review/browser-chromium-migration/<日期>.md
5. 修完 review 项 → 进下一批
```

- **一批 ≤2 次真机构建**；批内可多次 commit，但只 review 一次。
- **B1 失败不是"再试一次"**：回头改本文档的结论（方案作废 / 降级），这点先讲清。

### 14.3 可合并的批次

- **B0 + B1**（都属 plumbing，必须一起验）
- **B2 + B3**（伪装依赖引擎骨架，同一套文件）
- **B7 + B8**（review 得动，但 B8 风险高，默认分开）

建议只合 **B0 + B1**，其余保持独立——尤其 B5 最容易回炉。

### 14.4 进度记录

| 批次 | 完成日期 | commit | review 结论 |
|---|---|---|---|
| B0 | 2026-11-12 | `3c8940d3` | 待真机构建（脚本本机实跑通过：下载/编 wrapper/造 Helper×5/装包签名/增量跳过） |
| B1 | 2026-11-12 | `5315d41d..d30dc5b2` | **通过**：真机 CEF 起、页面真实加载（`Default/History` 有「百度一下，你就知道」）、profile 落盘、无 `process_requirement`；合成外观人眼确认无灰闪/黑帧。review 2 轮（P1×2、P2×6、P3×6 已处理，剩 1 条 P3 留给 B2） |
| B2 | 2026-11-12 | `a412156e` | **通过**（MINOR，P0/P1=0，P2×5 已修）。交付：`BrowserPage`/`WKPage`/`CefPage`、`BrowserEngineRouter`（swift test 7 条）、`RightSidebarTab.engine`、dev 默认 CEF + 设置页 dev 开关回 WK、CEF popup 拦成新 tab（B4 提前一块）；口径见 §9.2.8。取证：19:29 点百度地图后 `History` 只有一条 map（不再有第二个 browser），19:41–19:42 百度→百科是正常 tab 流 |
| B3 | 2026-11-12 | `1d29b5b9` | **通过**（review MINOR，P0/P1=0，P2×4/P3×3 已修，见 `docs/code-review/browser-chromium-migration/2026-11-12.md`）。交付：`CefDisguise.h`（版本取自 `cef_version.h`）、UA、`sec-ch-ua*`（`CefResourceRequestHandler`）、`userAgentData`、`window.chrome` 清洗、`--cef-probe` 探针 + `scripts/cef-regression.sh`；口径见第三章「B3 落地口径」。取证：① 探针 14/14 PASS，**网络层没有覆盖我们设的 `sec-ch-ua`**（`"Not:A-Brand";v="99", "Chromium";v="156", "Google Chrome";v="156"` + `?0` + `"macOS"`），页面侧 brands/`[object NavigatorUAData]`/chrome 键（app,csi,loadTimes,runtime）三者互相自洽；② 谷歌登录从 `identifier` 直接进到 `/challenge/pwd`（20:10:51），百度首页/搜索/百科同时段正常无验证码 |
| B4 | 2026-11-12 | 待提交 | **通过**（多 tab / popup 有取证；分屏与关闭回收靠人眼）。交付：popup 引擎跟 opener（D17）、秒关 tab 的 renderer 回收兜底、CEF 焦点判定修（见 9.2.9 / tier1-ux §3.1）。取证（20:25–20:28 那次运行）：6 个不同站点都落了 CEF profile（多 tab）；20:26:57 百度结果页点链接 → `[Browser] cef popup → new tab` + 新 tab 的 `baike.baidu.com` 记录 → popup 走的是我们自己的 tab（不再是裸原生窗）；退出后无残留 Helper 进程。**未验**：⌘W/⌘Shift+T 的焦点修复（这次构建早于修复），且该快捷键在 prod（WK）上也失效 → 记为 main 老 bug，不算本批验收项；分屏两窗格同时跑 CEF 未单独点验 |
| B5 | 2026-11-12 | 待提交 | 待真机构建 + 人眼。交付：白名单生效（含短链）、提示条（D16 + 频率闸）、地址栏换引擎图标、主引擎降级横幅；口径见 §9.2.10。三层检测的第 2/3 层按 §9.2.10 末条推到 B7 |
| B6a | 2026-11-12 | 待提交 | **通过**（⌘F 查找、⌘±/⌘0 缩放已验；⌘G/⌘Shift+G 保留标准语义「查找下一个/上一个」，只在查找栏打开时有意义，不算缺陷）。交付：`BrowserPage` 增加 zoom/find、`CefFindHandler`、`CefJSDialogHandler`；口径见 §9.2.11。**未验**：页面 alert/confirm/prompt 的真机表现（无本地 http 测试页，B6b 一起验）；其余功能面在 B6b |
| B6b | 2026-11-12 | 待提交 | 待真机构建 + 人眼（点链接下载、右键菜单、`<input type=file>`、页面 alert/confirm）。交付：CEF 下载落盘 + 进度（`downloadHandler`）+ `DownloadHistoryStore` 外部驱动取消、自绘 `NSMenu` 的右键菜单（含「用百度搜索」+「检查元素」→ DevTools）、file input 走 CEF 默认；口径见 §9.2.11。剩余（favicon/权限/埋点/保活/墙检测）等 B7 的 JS 通道 |
| B7 | 2026-11-12 | 待提交 | 待真机构建 + agent 实操（对 CEF 标签跑 snapshot / read / click / fill）。交付：CEF JS 求值通道（`xh_eval` / `xh_eval_result`，10s 超时 + 关页失败）、`BrowserPage.evaluateJS` 统一 WK/CEF、agent 标签改回 `resolveEngine` + `open` 的 `engine` 参数（D19）；口径见 §9.2.12 |
| B9a | 2026-11-13 | 待提交 | 待真机构建 + 人眼（非白名单站的视频：微博/Twitter 等应弹提示条）。交付：媒体检测第 2/3 层（`MediaProbeJavaScript` document-start 探针 + `XHMEDIA:` 回报 + 提示条复用）；口径见 §9.2.13。自检：探针 JS 7/7 |
| B9b | 2026-11-13 | 待提交 | 待真机构建 + 人眼（`window.open` 弹窗里 `window.opener` 非 null、`opener.postMessage` 能到、`window.close()` 能关掉 tab）。交付：CEF 真 popup browser 嵌进 tab（D20 / §9.2.14）；页面自关窗收 tab；12s 兜底。自检：clang++ / swiftc typecheck |

每批完成后在本节追加一行：`B<N> · 完成日期 · commit · review 结论（通过 / 遗留 N 项）`。
