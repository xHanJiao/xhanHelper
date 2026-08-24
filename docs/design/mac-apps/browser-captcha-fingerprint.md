# 浏览器验证码问题：指纹探针核对与修复

> 日期：2026-08-24
> 分支：feat/browser-captcha-fingerprint
> 前置：`browser-safari-ua.md` 声称 "WKWebView 与 Safari.app 仅存两处 JS 差异（webdriver + window.safari）"，但改完 UA 后谷歌/百度仍持续弹验证码（手动操作也弹）——该前提不成立，需先核对再修复。

## 一、问题定性

验证码不是"操作方式"触发的。Google（reCAPTCHA）/百度的验证码在**页面加载时对 JS 浏览器指纹打分**决定是否出题，分数不足直接弹，与后续是否人工点击无关。手动操作仍弹验证码 ⇒ WKWebView 的 JS 指纹与真实 Safari 不一致，被风控识破。

已知破绽（按可探测性排序）：

| # | 探针项 | 真实 Safari | WKWebView 现状 | 说明 |
|---|--------|------------|----------------|------|
| 1 | `navigator.webdriver` | 属性不存在（`'webdriver' in navigator === false`，值为 `undefined`） | 被覆盖为 `false`，`in` 为 true | 伪装方式错误：false 是 Chrome 形态，与 Safari UA 自相矛盾；`if (navigator.webdriver !== undefined)` 仍可穿 |
| 2 | `navigator.plugins` | 非空（含 QuickTime 等条目） | 恒空 PluginArray | `browser-safari-ua.md` 声称"空 PluginArray 与 Safari 一致"不准确 |
| 3 | `MediaSource.isTypeSupported('audio/mpeg')` | true | false | reCAPTCHA v2 最经典 WKWebView 判别特征 |
| 4 | `window.safari` | object，多成员 | 壳子只有 pushNotification | 最小壳子过薄 |
| 5 | `navigator.mediaDevices` | enumerateDevices resolve | 权限一律 deny，行为可能异常 | 加分项 |

## 二、第一步：指纹探针核对（本次任务）

不依赖人工在 Web Inspector 里逐项核对（用户要求自动化为日志）。

**改动点**：`client/xHelper/xHelper/Shared/Tools/BrowserToolProvider.swift`
- `TabSession.webView(_:didFinish:)`（L2853-2859）在现有 `logPerf` 旁加 `logFingerprint`，完全复刻其模式（`jsResult` 求值 + `LogBuffer.shared.diag`）。
- 探针 JS 一次性读出下表全部字段，返回 JSON。
- `mediaDevicesEnumerate` 需异步 Promise，单独一次 `jsResult` 拼接。
- 日志前缀 `[BrowserFP] <host> <json>`，与 `[BrowserPerf]` 风格对齐。`LogBuffer.diag` 恒编译并写日志文件。

**探针字段**：ua / platform / vendor / webdriverValue / webdriverInNavigator / plugins / pluginsLen / mimeTypesLen / mediaDevicesType / mediaDevicesEnumerate / mediaSourceType / mseAudioMpeg / mseH264 / safariType / safariMembers / notificationType / permissionsType / hardwareConcurrency / deviceMemory / maxTouchPoints / languages / screen / webglRenderer / webglVendor / perf100Now / appVersion

**验证方式**：用户在 Xcode 跑 App，手动打开 google.com + baidu.com，把 `[BrowserFP]` 日志行贴回。逐项比对结论由 agent 依据内置 Safari 基线给出，用户仅需贴日志。

**验证结果**：实测全绿（webdriver 删除后 `in===false`、mediaDevices object、plugins 为 Safari 形态），谷歌/百度验证码消失。探针（`logFingerprint` + `[BrowserFP]` print）为一次性诊断，验证闭环后已从 `didFinish` 移除。

## 三、第二步（核对后按结论实施）：指纹修复

探针实测（baidu.com）命中项与结论：
- `webdriverValue:false` / `webdriverInNavigator:true` → 伪装方式错误，须彻底删除属性
- `mediaDevicesType:undefined` → 须补壳
- `plugins` 原生暴露 Chrome/Edge PDF 插件名（非我方注入）→ 须覆盖为 Safari 形态
- `webglRenderer:WebKit WebGL`（基线常见 Apple GPU，低置信）→ 列为可选，先不做
- 谷歌探针缺失不影响判定（指纹是 WebView 级，任意站点一致）

已实施（`makeWebView` 的 `safariSpoof` 脚本，L2518-2577）：

1. **webdriver 彻底删除**：`delete navigator.webdriver` + `delete Navigator.prototype.webdriver`，仍残留时兜底为 `undefined`，让 `'webdriver' in navigator === false` 对齐 Safari。
2. **补 `navigator.mediaDevices`**：`Navigator.prototype` 上 defineProperty，返回最小壳（`enumerateDevices` resolve 空数组、`getUserMedia/getDisplayMedia` reject NotAllowedError）。
3. **覆盖 plugins / mimeTypes**：`Navigator.prototype` 上 defineProperty 返回 Safari 形态条目（QuickTime Plug-in 7.7.3 + WebKit built-in PDF），伪造带 `length/item/namedItem/iterator/refresh` 的 PluginArray/MimeTypeArray。
4. **脚本注入改 `forMainFrameOnly: false`**：reCAPTCHA 校验跑在跨域 iframe 里，main-frame-only 会漏掉主 frame 以外的上下文；全 frame 注入该幂等脚本，无副作用。

可选（暂不做，YAGNI）：WebGL `getParameter` hook（VENDOR→Apple Inc./RENDERER→Apple GPU）、window.safari 壳子补成员。

## 四、验收

- `[BrowserFP]` 探针各命中项与 Safari 基线一致
- 谷歌/百度首页不再弹验证码（或频率显著下降）
- agent 工具链（snapshot/click/download/fill）不受影响
- 客户端代码不做 Xcode 编译验证（CLAUDE.md 约定），运行验证由用户在 Xcode 完成
