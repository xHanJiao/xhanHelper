# 内置浏览器把网页探测的 `bitbrowser://` 甩给 macOS，弹「未设定用来打开 URL 的应用程序」

- **状态**：已修（2026-10-31）
- **发现于**：用户反馈（截图 `WX20261030-111550@2x.png`，2026-10-30 11:15:50）
- **性质**：站点协议探测 + WebKit 把无主的自定义协议外抛给 LaunchServices；不是崩溃、不影响任何功能

## 一、问题表现

使用客户端时偶发系统模态框：

> 未设定用来打开 URL `bitbrowser://cc/` 的应用程序。

用户侧症状是「什么都没点，正上网就弹出来」，只能点「取消」。

## 二、定位证据

1. 系统日志：
   ```
   /usr/bin/log show --start "2026-10-30 11:15:43" --end "2026-10-30 11:15:46" \
     --predicate 'eventMessage CONTAINS[c] "bitbrowser"' --style compact
   ```
   `11:15:44.016` `CoreServicesUIAgent` 报 `kLSApplicationNotFoundErr`，`URLs = ("bitbrowser://cc/")`。
2. 同一秒 `xhanHelper[83147] (LaunchServices) LaunchServices.launchViaCSUA`，且 `CoreServicesUIAgent` 连接的 quarantine-resolver peer 就是 `[83147]` —— **请求方是客户端**，不是别的 App。
3. 客户端日志（`~/Library/Caches/com.xhan.xhanHelper/logs/current.log`）`2026-10-30T03:15:33Z`~`03:15:35Z`（本地 11:15）内置浏览器停在抖音用户页；系统日志里同一秒该页 `WebContent` 正在加载 iframe，随后就发出了这个 open。
4. 本机**没装比特指纹浏览器（BitBrowser）**：`/Applications` 无、LaunchServices 无该 scheme 的 handler，所以系统只能弹「选取应用程序」。
5. `bitbrowser://` 是 BitBrowser 注册的私有协议，抖音网页 JS 会探测它（同类公开报告：flatpak/xdg-desktop-portal#2131 里点到 `bitbrowser://`、`snssdk1128://`、`bytedance://`、`aweme://`）。**客户端里没有任何一处会自己拼这个 scheme。**

## 三、根因

- 直接原因：网页发起 `bitbrowser://cc/`，macOS 无人认领 → 系统弹框。系统框本身不可编程拦截。
- 客户端侧的缺口：导航策略虽然对所有非 `http(s)` 协议 `cancel`（`BrowserToolProvider.swift` 旧 `decidePolicyFor`），但这次 WebKit 在策略回话前 13 ms 就把 URL 交给了 LaunchServices，靠导航策略兜不住。
- 用最小 WKWebView 复现脚本（`/tmp/wkprobe*.swift`，跑完即弃）测过：带 `navigationDelegate` 时 cancel 能挡住；所以真实触发的那条 WebKit 内部路径没能精确复现（疑似弹窗/派生配置那条）。结论是「**只靠导航策略不够，得在 WebKit 内部把协议吃掉**」。

## 四、修复

1. `TabSession.makeWebView`（`Shared/Tools/BrowserToolProvider.swift`）：给 WebView configuration 注册 `WKURLSchemeHandler`，
   `swallowedSchemes = ["bitbrowser"]`，handler 里 `didFailWithError(NSURLErrorUnsupportedURL)`。
   WebKit 在进程内处理这些 URL，不再问 LaunchServices。
   - 弹窗 tab 的 configuration 由父配置派生，注册前用 `urlSchemeHandler(forURLScheme:)` 判空，避免重复注册抛异常。
   - 加一行日志 `[Browser] swallowed probe <url>` 便于确认生效。
2. `DocumentReaderView` 的导航策略：`xhanhelper://` 内部跳转、放行 `http/https/about/mailto/tel`，其余一律 `cancel`（原实现是「其余一律 allow」，等于把未知协议交给 WebKit）。
3. 探测被 sink 主动 fail 会带 `NSURLErrorUnsupportedURL`（−1002），`isBenignNavigationError` 把它算作良性，否则主 frame 探测会亮「加载失败」红条并往活动埋点写一条假的 `browser.nav.ended(failed)`。
4. 探测日志同一 tab、同一 scheme 只记一次（`SwallowedSchemeSink.logged`）。

复现脚本验证：iframe / `<img>` / `<object>` / 顶层 `location` 跳转 / `window.open` / 代码 `load()` 六条路径，注册 handler 后**全部进 handler、无 `launchViaCSUA`**；不注册的对照组每次都触发 LaunchServices。

代价：未知协议在内置浏览器里本来就已经被 cancel 掉、打不开（阅读器侧保留 `mailto`/`tel`，与改前一致），功能上没有损失。

## 五、验收

- 打开抖音用户页静置几十秒：不再弹框；出现 `[Browser] swallowed probe bitbrowser://cc/` 日志（同一 tab 只记一次）。
- 文档正文里的 `[标题](xhanhelper://document/<id>)` 仍能跳转；正文里 http(s) 与 `mailto:`/`tel:` 链接行为不变，也不弹框。
- 主 frame 被 sink 拦掉的探测不出现「加载失败」红条、活动埋点无 `browser.nav.ended(failed)`。
- `xcrun swiftc -typecheck`（macOS 26.3 / iOS 26.0 两个 target）无 error。

## 六、遗留

- 只挡了**已观测到**的 `bitbrowser`。若站点改探测 `snssdk1128://` / `bytedance://` / `aweme://` 等，同样症状会重现 —— 往 `swallowedSchemes` 加一行即可（这些协议在内置浏览器里同样本来就是被 cancel 的）。
- 真实触发 LSOpen 的那条 WebKit 路径未精确复现，本次是「handler 兜住所有 WebKit 路径」，不是精确修某一行。
