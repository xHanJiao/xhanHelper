# Web 端灵阅点图放大白屏

## 问题表现

macOS 客户端打开灵阅（HTML 产物）点图片正常弹出纯 CSS 放大层；Web 前端同一页点图后 iframe 整块变白，放大层不出现，页面内容也没了。

## 根因

灵阅详情的 HTML 由模型生成，放大层普遍写成**纯 CSS `:target`**：`<a href="#pic-zoom"><img></a>` + `<div id="pic-zoom">`。macOS 用 WKWebView `loadHTMLString` 渲染，页内锚点是正常同文档跳转。

Web 端用 `<iframe srcdoc="...">`（`ArtifactDetailView.vue`）渲染，问题出在两层：

1. `srcdoc` 文档的 URL 是 `about:srcdoc`，点 `#锚点` 会尝试导航到 `about:srcdoc#pic-zoom`。Chrome 把这个导航当成一次新文档加载，而 `about:srcdoc` 只能在带 `srcdoc` 属性加载时存在，于是文档被卸载 → iframe 白屏。
2. 就算换成 blob URL，`sandbox=""` 给的是**不透明源**，跳到 `blob:http://…` 被当成跨源导航拦掉，锚点不生效，放大层仍然打不开（这次不白屏，只是没反应）。

## 最终方案

`front-end/src/views/ArtifactDetailView.vue`：

- 内容改成 `URL.createObjectURL(new Blob([html], { type: 'text/html' }))` + `<iframe :src>`，不再用 `srcdoc`；换页/卸载时 `revokeObjectURL`。
- `sandbox` 从 `""` 改为 `allow-same-origin`：锚点变成同文档跳转，`:target` 放大层正常展开，点大图 `href="#"` 关闭也正常。

安全边界未变松：产物入库时已清洗（`script`/`iframe`/`form` 等直接删、`on*` 与外部 URL 过滤、只留页内锚点和 `data:image`），HTML 里带 `default-src 'none'` 的 CSP；iframe 仍不给 `allow-scripts` / `allow-forms` / `allow-top-navigation`，所以页内没有任何脚本能跑，拿不到父页面。`allow-same-origin` 只影响锚点解析。

## 验证

mock 一页带 `:target` 放大层的灵阅（`<a href="#pic-zoom"><img></a>` + fixed 遮罩层），headless Chrome：

- 修复前：点图后 iframe 白屏（截图确认）。
- 修复后：点图弹出深色遮罩 + 居中大图；点大图回 `href="#"` 恢复正文。

`npm run build` 通过。

已知行为：iframe 里的锚点跳转会进浏览器历史，放大层打开时按返回 / 顶栏「返回」先关放大层（iframe 回退），第二下才离开详情页——等同「浮层先关」的常见模态行为，没有额外处理。

## 经验教训

「关了 JS 也安全」的页面仍可能有交互（CSS `:target` / `:hover` / 锚点），而 `srcdoc` + `sandbox=""` 恰恰把锚点这一层废掉了。渲染宿主从 WKWebView 换到浏览器 iframe 时，得按「页面里靠什么机制动」逐条验，而不是只看静态排版对不对。
