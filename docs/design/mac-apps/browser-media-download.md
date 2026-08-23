# 内置浏览器「本页资源」媒体下载

> 2026-08 · 用户手动触发 · 视频优先（图片次要，截图兜底）· 首期站点：小红书 / 抖音

## 背景与定位

内置浏览器（WKWebView）已有完整的 agent 媒体下载链路：

- `agent_browser_get_media_sources`：通用 DOM 扫描 video/img（`BrowserToolProvider.mediaSourcesJS`）
- `agent_browser_download`：UA/Referer/域匹配 Cookie + 魔数校验 + 进度/取消/历史（`runDownload`）

但这些只有 LLM 工具入口，用户没有手动入口。本设计补一个**用户手动触发**的面板：
浏览小红书/抖音时点按钮 → 扫描本页视频/图片 → 勾选三五条 → 顺序下载。

非目标：后台自动检测、整页批量抓取、更多站点（按同模式加 host 分支即可）、服务端参与（Go 后端零改动）。

## 核心思路：读现成数据，不构造请求

提取全部发生在真实登录态的页面 JS 上下文里，只读页面自己已经加载好的 DOM 和全局状态
JSON——不构造任何 API 请求。因此 x-s/x-t 签名、设备指纹等反爬体系完全不被触碰；
CDN 媒体 URL 本身是带签名自包含地址，配合现有 `runDownload` 的 Referer/Cookie/真实 UA 直下即可。
抖音 CDN 签名有时效，但我们是"扫描完立刻下"，不存在过期问题。

## 交互

- 入口：浏览器地址栏右侧新增按钮（`photo.on.rectangle.angled`），仅 http(s) 页面可用
- 点击 → 右栏打开特殊 tab「本页资源」（与下载历史/收藏夹同模式，每次进入重新绑定来源页并重扫）
- 面板：视频列表（主要）+ 图片区（默认折叠，次要）；每行缩略图 + 尺寸/时长 + 上下文标题 + 勾选框
- 「下载选中(N)」→ 逐条顺序下载、间隔 ~1s；复用现有链路：进下载历史、顶部进度条、存 `~/Downloads/xhanHelper/`
- 单条失败就地标红（签名过期/资源失效），重新勾选可重试，不影响其他条目
- 放弃项：右键菜单下载（WKWebView contextMenu 拿不到媒体 src，做不了）

## 提取策略

分层提取，优先级从高到低，URL 去重保留高优先级（实测数据驱动的设计，见下方「实测校准」）：

| 层 | 小红书 | 抖音 |
|---|---|---|
| ① 站点 state 直读 | `__INITIAL_STATE__.note.noteDetailMap[id].note.video.media.stream.h264[0].masterUrl`（原图/原画直链） | RENDER_DATA 里的 play_addr（新版页面多已不存在，保留一行成本） |
| ② perf 层（通用） | `performance.getEntriesByType('resource')` 里已加载的 `.mp4` 直链——播放器预加载也会命中，不依赖 DOM/state 结构和水合时序；**显式排除抖音 DASH 分轨**（`douyinvod`/`media-video-`/`media-audio-`，纯视频或纯音频，下载了没法看） | 同左 |
| ③ DOM 层（通用兜底） | `<video>` currentSrc / `<img>` src，过滤 blob:/data: | 同左 |
| ④ 抖音数据接口泛化重放 | — | 仅当①②③无可用视频时：同步 JS 收集 perf 里所有 `/aweme/` 数据接口候选（detail/tab-feed/搜索通吃，排除 emoji/comment 等噪声，新→旧最多 5 条），Swift 原生 URLSession 逐条重放（最多 3 条，页面真实 UA——a_bogus 签名混入 UA 必须一致、Referer、域匹配 Cookie，不受 CORS 约束），在响应 JSON 里**递归深查**带 `video.play_addr.url_list` 的 aweme 对象（`aweme_id`/`group_id` 匹配 modal_id 目标优先，否则第一个可用项） |

配套：
- 面板扫描后视频为空且是已知站点 → 静默等 1.5s 自动补扫一轮（治 SPA 水合竞态，只补一轮）
- 扫描输出带各层命中统计，视频为空打 LogBuffer 日志（失效可从日志定位，不用开 Inspector）

### 实测校准（2026-08）

- 小红书分享链（带 xsec_token）进入的视频笔记页：state 路径验证正确，`masterUrl` 为 `http://sns-video-v2.xhscdn.com/....mp4?sign=...`（ATS 已放开可直下）；未播放时 `<video>` 只有 blob:。首次扫描偶发空 → 水合竞态，自动补扫覆盖。✅ 已实测通过
- 抖音视频页：无 `__INITIAL_STATE__`；douyinvod 直链是 DASH 分轨（video-avc1 / audio-mp4a），MSE blob 拼流播放，分轨不可直接交付；数据接口（detail/tab-feed）响应含合成 mp4 的 play_addr。信息流页（jingxuan?modal_id=...）走批量 feed 接口，URL 里没有 aweme_id 参数 → 必须泛化收集 + 递归深查，不能只认 detail 接口。
- **教训 1**：WKWebView `evaluateJavaScript` 不解析 Promise（async JS 报 WKErrorJavaScriptResultTypeIsUnsupported「执行JavaScript返回结果的类型不受支持」），注入 JS 必须同步返回；需要异步能力时用同步 JS 取参数 + Swift 原生请求完成（顺带免 CORS、UA 可精确对齐签名输入）。
- **教训 2**：BrowserTabView 的 `\.url` KVO 观察器曾被 `!urlFocused` 整体拦截——新标签页 onAppear 自动聚焦地址栏，首次导航的 URL 更新被吞 → `currentURL` 永 nil → 媒体按钮/收藏永远不可用。修复：`currentURL` 无条件跟随，仅地址栏文本受聚焦保护。

## 关键实现点

1. **显式来源 session**：面板是侧栏特殊 tab，激活它之后 provider 的 `activeSession` 可能漂移
   （切到别的 tab 或被 deselect）。所以扫描和下载都显式持有打开面板那一刻的来源 webView，
   下载时按 webView 反查 TabSession，Cookie/Referer/UA 一律取自来源页，不依赖 activeSession。
2. **脱离视图层级不影响功能**：面板激活后来源 WKWebView 可能被摘出视图层级，
   但实例存活在 `BrowserToolProvider.tabs`，JS 可执行、页面状态保留。
3. **复用下载链路**：`runDownload` 加可选 `session` 参数；新公开入口
   `downloadMedia(url:preferredName:sourceWebView:)`（delay=false，拟人间隔由面板层控制），
   enforceMedia=true 保留魔数校验。
4. **文件名**：有上下文标题用「标题前 24 字符.mp4/jpg」，否则交给响应 Content-Disposition 建议。

## 文件改动

| 文件 | 改动 |
|---|---|
| `Shared/Services/MediaExtractor.swift`（新增） | 扫描 JS（通用+两站点增强）、ScanResult/Item 模型、evaluateJavaScript 包装 |
| `Shared/Views/MediaPickerPanelView.swift`（新增） | 面板 UI：列表/勾选/顺序下载/进度状态 |
| `Shared/Tools/BrowserToolProvider.swift` | runDownload 加 session 参数；新增 downloadMedia 入口 |
| `Shared/Views/RightSidebarManager.swift` | mediaTabID 加入 specialTabIDs；openMediaPanel(sourceWebView:) |
| `Shared/Views/BrowserTabView.swift` | 地址栏加媒体按钮 |

## 已知取舍

- 小红书笔记页若同时存在 state 直链与 perf 预加载直链（`_259` vs `_108` 分辨率变体）会显示两条，默认取排序靠前的高清版
- 抖音 API 重放偶发可能被风控拒（时间戳/nonce 校验），届时该层拿不到直链；若实测频繁失效，再考虑 documentStart 注入 hook XHR 缓存响应的重型方案
- 直播流（m3u8）不支持（魔数校验也会拒绝）
- Live Photo、无水印原片等需求明确出现再加

## 验收

客户端不由 agent 编译验证（约定）：用户 Xcode 编译 + 真机各开一个小红书笔记视频页、抖音视频页，
扫描 → 勾选 → 下载 → `~/Downloads/xhanHelper/` 文件落盘可播，下载历史与顶部进度条正常，日志无报错。
