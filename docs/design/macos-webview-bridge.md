# macOS 客户端 WebView 桥接 — 监控看板接入

> 2026-08-12

## 背景

macOS 客户端右侧栏（BrowsePanel）已有多块面板（浏览器/账单/记忆/笔记/文档/主题），均为原生 SwiftUI。现在要把前端监控看板（含 LLM 成本中心）接入，需求：

1. **接近原生**：无浏览器 chrome（无 URL 栏/前进后退/书签）
2. **SwiftUI 实现**：不用 Electron 之类重方案
3. **可复用**：未来把更多看板内容搬进来，本次搭好基础设施
4. **入口**：右侧栏 BrowsePanel 网格，与笔记/浏览器平级

## 架构

```
┌───────────────────────────────────────────────────────────────┐
│  Layer 1: 通用基础组件（业务无关，可独立复用）                   │
│                                                               │
│  XHWebView.swift         XHPanel.swift                       │
│  ┌────────────────────┐  ┌─────────────────────────────┐     │
│  │ WKWebView wrapper  │  │ 通用面板容器                  │     │
│  │ ├─ loading overlay │  │ ← 返回  标题       🔄 刷新   │     │
│  │ ├─ error + retry   │  │                              │     │
│  │ ├─ nav interceptor │  │  [任意 Content View]          │     │
│  │ └─ 外链→系统浏览器  │  │                              │     │
│  │ 参数: URL + config │  │ 参数: title + onBack + content│     │
│  └────────────────────┘  └─────────────────────────────┘     │
│                                                               │
│  XHWebViewBridge.swift                                       │
│  ┌──────────────────────────────────────────────────────┐    │
│  │ Bridge 工厂：makeConfiguration() → WKWebViewConfiguration│  │
│  │ ├─ 注入自定义 JS user scripts                          │    │
│  │ └─ 注册自定义 script message handlers                  │    │
│  └──────────────────────────────────────────────────────┘    │
│                                                               │
├───────────────────────────────────────────────────────────────┤
│  Layer 2: App 层（BrowsePanelView）                            │
│  enum BrowsePanel { case dashboard = "监控看版" }              │
│  XHPanel(title: "监控看版") { XHWebView(url: ..., config) }    │
└───────────────────────────────────────────────────────────────┘
```

## URL 策略

```
         DEBUG                             RELEASE
         ─────                             ───────
前端:    Vite dev → http://localhost:5173   https://helper.xhanx.cn（nginx 已有部署）
API:     Vite proxy /api → :8080            同主机 /api

WebView: http://localhost:5173/admin        https://helper.xhanx.cn/admin
```

- 前端代码保持在 `front-end/src/`，不迁入 Swift
- 生产环境 nginx 已 serve 前端（无需 Go 改动）
- 零后端改动

## 最佳实践清单

| 实践 | 实现位置 |
|------|----------|
| 同源部署 | RELEASE 直接用 baseURL 拼 /admin |
| WKUserScript 注入 | XHWebViewBridge.makeConfiguration |
| WKScriptMessageHandler（JS→Swift） | XHWebViewBridge 注册 handlers |
| 导航拦截（外链→系统浏览器） | XHWebView Coordinator.decidePolicyFor |
| 新窗口拦截（window.open → 系统浏览器） | XHWebView Coordinator WKUIDelegate |
| 错误状态（服务器未启动 → 原生错误视图） | XHWebView error overlay |
| 加载指示器 | XHWebView ProgressView overlay |
| 无浏览器 chrome | XHWebView 只渲染 WKWebView，无工具栏 |

## XHWebView — 基础 WebView

```swift
struct XHWebView: NSViewRepresentable {
    let url: URL
    let configuration: WKWebViewConfiguration  // 调用方负责注入 scripts/handlers

    func makeCoordinator() -> Coordinator        // WKNavigationDelegate + WKUIDelegate
    func makeNSView(_:) -> WKWebView
    func updateNSView(_:,_:)                     // URL 变化时 navigate，无变化 skip
}
```

**Coordinator 行为**：

```
Coordinator : NSObject, WKNavigationDelegate, WKUIDelegate
  ├─ isLoading → 驱动 SwiftUI loading overlay
  ├─ didFail   → error 状态 → SwiftUI error overlay + 重试
  ├─ decidePolicyFor (.linkActivated) → NSWorkspace.open + .cancel
  └─ createWebViewWith (window.open) → NSWorkspace.open + return nil
```

## XHWebViewBridge — JS 桥梁工厂

```swift
enum XHWebViewBridge {
    static func makeConfiguration(
        userScripts: [String] = [],
        scriptMessageHandlers: [(name: String, handler: (Any) -> Void)] = []
    ) -> WKWebViewConfiguration
}
```

**桥接数据流**：

```
  Swift 端                       WebView 内
  ────────                       ─────────
  injectDictionary: [             window.XH_BRIDGE = {
    "baseURL": "...",       →       baseURL: "...",
    "platform": "macOS",    →       platform: "macOS"
  ]                           }

  handlers: [                   JS 调用:
    ("externalLink", { url in   webkit.messageHandlers
      NSWorkspace.open(url)  ←    .externalLink.postMessage({url})
    })
  ]
```

- 业务不感知：注入什么、注册什么，全由调用方决定
- 通用命名 `XH_BRIDGE`（组件级中性命名，app 层可再封壳）

## XHPanel — 通用面板容器

```swift
struct XHPanel<Content: View>: View {
    let title: String
    let onBack: () -> Void
    @ViewBuilder let content: () -> Content
    var trailingButtons: [PanelButton] = []   // 如刷新按钮
}
```

泛型容器，可装任意 View（WebView / 原生面板），未来可替换现有面板自绘 header。

## App 层接入 — BrowsePanelView

```swift
enum BrowsePanel: String, Identifiable, CaseIterable {
    case bills, memories, notes, documents, themes
    case dashboard = "监控看版"   // 新增，allCases 遍历自动出 tile
}

// panelContent() 加路由:
case .dashboard:
    XHPanel(title: panel.rawValue, onBack: { manager.browsePanelSelection = nil }) {
        XHWebView(url: frontendURL + "/admin", configuration: bridgeConfig)
    }
```

## 前端配合

AdminView.vue 加第 5 个 Tab「Token 消耗」：

```
┌──────────────────────────────────────────────────────────┐
│  概览卡片: 今日/昨日/本月 total │ calls │ cache_hit_rate    │
├──────────┬───────────────────────────────────────────────┤
│ 7d 15d 30d│ 分类 ▼(多选)  用户 ▼(多选)                     │
├──────────┴───────────────────────────────────────────────┤
│  柱状图: 每日 Token（stack 维度前端勾选 hit/miss/completion）│
│  饼图: 分类分布 │ 饼图: 模型分布 │ 饼图: 子类分布            │
└──────────────────────────────────────────────────────────┘
```

数据源：`/api/admin/cost/stats` + `/summary` + `/categories` + `/users` + `/details`（已实现）。

## 未来复用

| 场景 | 步骤 |
|------|------|
| 新增看板页面 | BrowsePanel + case → XHPanel + XHWebView(新 URL) → 前端加路由 |
| 前端触发原生操作 | XHWebViewBridge 注册新 message handler |
| 原生面板统一 header | XHPanel 替换现有面板自绘 header |

## 文件清单

| 文件 | 操作 | 定位 |
|------|------|------|
| `Shared/Views/XHWebView.swift` | 新建 | Layer 1 |
| `Shared/Views/XHPanel.swift` | 新建 | Layer 1 |
| `Shared/Services/XHWebViewBridge.swift` | 新建 | Layer 1 |
| `Shared/Views/BrowsePanelView.swift` | 修改 | Layer 2 |
| `front-end/src/views/AdminView.vue` | 修改 | 前端 |
