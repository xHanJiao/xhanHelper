# 内嵌浏览器 Web 通知支持调研（BOSS直聘 场景）

> 日期: 2026-08-05
> 主题: WKWebView 是否支持 Web Push / Notification API
> 结论: **不支持**，WebKit 层面无回调通道，内嵌浏览器收不到站方 web push

---

## 一、背景

macOS 客户端内置浏览器（`client/xHelper/.../BrowserToolProvider.swift`，WKWebView 实现）目前无法收到如 BOSS直聘 这类站点的主动推送通知。调研目标：确认这是否受 WebKit 能力限制（做不了），以及将来是否有可行的替代路径。

## 二、能力判定

### 2.1 WKWebView 无通知权限回调（SDK 头文件证据）

`WKUIDelegate.h`（`MacOSX.sdk/.../WebKit.framework/Headers/`）中权限相关回调仅有三个：

| 回调 | 平台 |
|------|------|
| `requestMediaCapturePermissionForOrigin:initiatedByFrame:type:decisionHandler:` | macOS 12+ / iOS 15+ |
| `requestDeviceOrientationAndMotionPermissionForOrigin:` | iOS only |
| `requestGeolocationPermissionForOrigin:` | macOS / iOS |

**不存在** `requestNotificationPermission...` 之类的通知权限回调；`WKWebViewConfiguration.h` 中也没有任何 push/notification API。

### 2.2 Web Push 仅对 Safari 已安装 Web App 开放

Apple 的 Web Push（基于 APNs）只支持：

- **macOS 13.3+**：Safari 中"添加到程序坞"的 Web App
- **iOS/iPadOS 16.4+**：Safari 中"添加到主屏幕"的 Web App

不适用于 WKWebView 内嵌网页。站点调用 `PushManager.subscribe()` 无法成功注册。

### 2.3 由此推导的两条硬限制

1. **通知权限询问**：站点调用 `Notification.requestPermission()` 时，WKWebView 无回调通道，浏览器层的"允许通知"弹窗不会出现，权限直接为 denied。
2. **通知推送**：真正的 Web Push 收不到。

这是所有 WKWebView 套壳浏览器（Cordova/Capacitor/各类内嵌浏览器）的共有限制，不是本项目代码缺失。

### 2.4 当前代码现状

`BrowserToolProvider.swift:1993-2005` 只实现了媒体采集与定位的权限回调，且一律 `deny`（防恶意页采集设备/位置）。与上述能力边界一致。

## 三、可选替代路径（未实现，仅存档）

### 方案 A：页面监控 + 原生通知桥（app 内唯一可行）

- 对目标域名注入 JS：`documentStart` 时 hook 站点 WebSocket `onmessage` 拦截新消息帧
- 兜底：页面内轮询未读角标变化
- 检测到新消息 → `window.webkit.messageHandlers` 桥到原生
- 复用现有 `NotificationManager`（已用 `UNUserNotificationCenter` 处理提醒）弹系统通知

**局限**：
- 不是真正推送：必须 app 运行 + 对应 tab 存活才收得到
- 依赖站点页面内部实现，站点改版可能失效（WebSocket hook 中等风险，DOM 角标高风险）
- 纯客户端实现，无后端/沙箱推送服务依赖

### 方案 B：调用系统浏览器打开

把站点交给 Chrome/Safari 打开，站点正常 web push 可收到。脱离 app，无深度集成，但最可靠。

### 方案 C：内嵌 Chromium 内核

完整支持 web push，工程量巨大，不现实。

## 四、遗留问题

- 需要实测确认 BOSS直聘 站内实时消息确实走 WebSocket（若为轮询则需角标方案兜底）
- 若将来推进方案 A，建议先在 `docs/design/` 落设计文档再开发

## 五、结论

**内嵌浏览器无法原生支持 BOSS直聘 的 web 通知推送**，这是 WebKit 能力边界所致。若用户需要通知能力，走方案 B（系统浏览器）最可靠；接受"tab 存活"限制时方案 A 是 app 内唯一可选路径。
