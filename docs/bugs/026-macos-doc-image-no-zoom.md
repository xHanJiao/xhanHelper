# macOS 文档里的图不能点击放大

## 问题表现

macOS 客户端打开文档，正文里的图（mermaid 图表、上传的文档配图）点击无反应，不能像聊天里的图那样点开大图浮层。

## 根因

回归。正文换 WKWebView 阅读器时，点击放大这条桥整个丢了。

正文阅读分两代：

- **旧**：文档正文用 MarkdownUI 渲染，mermaid 走 `MermaidDiagramInlineView`，该视图自带 `.onTapGesture → LightboxCenter.present`（`MermaidDiagramInlineView.swift:61`），点图即放大。
- **新**：`DocumentListView.swift:516` 的 macOS 正文改用 `DocumentReaderView`（WKWebView）。正文里的所有图都成了 HTML 里的普通 `<img>`：
  - mermaid：`ReaderHTML` 预渲染成浅/深两张 PNG data URI 塞进模板；
  - 上传配图：`![](/images/xxx.png)` 经 marked 渲染成 `<img src="/images/...">`。

新阅读器两边都没接点击：

1. 模板 JS 的 `content` click 处理器只认 `mark.hl / .ann-bubble`，命中不到图就 `hideCtx()` 返回；`post()` 从不发图片类消息。
2. 原生 `DocumentReaderController.apply(message:)` 的 switch 没有 image 分支。

`LightboxCenter` 在 `DocumentReaderView.swift` 的历史里从未出现 —— 即 WKWebView 阅读器从落地起就没实现过点击放大（mermaid 是回归；上传配图则是从没有过）。

## 最终方案

两端接一条桥，复用现成大图浮层（用户拍板：mermaid + 上传图都接；mermaid 按当前主题取对应那张）：

- **JS**（`DocumentReaderView.swift` 模板）：`content` click 先判 `e.target.tagName === 'IMG'`，`preventDefault()` 后 `post({ type:'image', src: e.target.currentSrc || e.target.src })`。mermaid 浅/深两张靠 CSS 只显示当前主题那张，隐藏那张 `display:none` 收不到点击，命中的天然就是该显示的那张；src 已由浏览器解析成绝对地址（配图 http URL / mermaid data URI）。
- **原生**（`apply` 加 `case "image"`）：present `DocumentImageLightbox`，data URI 直接 `NSImage(data:)`、http URL 异步 `URLSession` 拉一次，加载好复用 `AttachmentPreviewView`（与聊天图放大同款）。

## 验证

- `client/xHelper/scripts/check-reader-html.sh` PASS（新增「点 mermaid 图 / 点正文配图回传 image」断言）。
- `client/xHelper/scripts/snapshot-reader.sh` 无回归。
- `xcrun swiftc -typecheck`（macOS 26.3 真实设置）通过，0 error。
- 真机（用户在 Xcode）：文档里点 mermaid 图、点上传配图都能弹出大图浮层，点图外/Esc 关闭。

## 经验教训

换渲染引擎（MarkdownUI → WKWebView）是「静默降级」的高发场景：交互能力（点击放大）挂在旧的 SwiftUI 视图上，改渲染路径时不会有编译错误，也不会有人提醒，直到用户发现某个手势没了。以后正文/宿主渲染路径更换，收尾要拿旧路径的能力清单逐条比对。
