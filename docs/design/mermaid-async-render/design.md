# mermaid 异步渲染（消除开文档时的彩球）

> 状态：**未实现 · 已立项待排期**（2026-10-12 立项）
> 范围：macOS 客户端 `DocumentReaderView.swift`（HTML 模板 + JS + `ReaderHTML`）。服务端 / Web / 分享页 **不涉及**。
> 前置：`docs/design/document-highlight-annotation/design.md` §7.2（mermaid 走 `MermaidUtility` 原生渲染 PNG → 注入 `<img>`、**不在 WebView 引 Mermaid JS** 的决定来自这里）；§7.2 下方「不要在 WebView 里引 Mermaid JS」这条硬约束在本方案里继续成立。
> 本文件只记**待办方案**；本轮已落地的止血改动见 §二。

---

## 一、问题（真机复现，2026-10-12）

**现象**：在侧栏「文档列表」点一条打开文档，**含 mermaid 的文档会卡一下、有时直接出彩球**（主线程被占 ≥1~2s）；不含 mermaid 的文档不卡。

**证据链**（都在 `DocumentReaderView.swift`）：

```
点侧栏一条 → openDocument = doc
 → 外层 .id(doc.id) 拆掉重建整个详情子树（含 WKWebView）
 → makeNSView → buildHTML() → ReaderHTML.build()
     → 每个 ```mermaid 块调 mermaidDataURI() 【两次：浅色 + 深色】      :528-529
         → MermaidImageRenderer().renderPNG()   解析 + 布局 + CGContext 按 scale=2 画位图
         → NSImage 解码 → lockFocus 翻转重绘 → tiff → 再 PNG 编码 → base64   :578-601
```

- **全程主线程同步**，代码里自己标着「mermaid 是原生渲染，**很贵**」，还留了「图多时是双倍渲染成本」。
- **没有任何缓存**：切来切去每篇都从头烤，所以是"有时候卡"（只在 mermaid 文档 + 只在切过去那一下）。
- 侧栏那一下点击只是"打开文档"的唯一入口，**不是主因**（无 mermaid 的文档不卡即证）。同批侧栏改动引入的次要成本已在本轮清掉（见 §二）。

## 二、已落地的止血（2026-10-12，同一批改完）

| # | 改动 | 收益 |
|---|---|---|
| 1 | `ReaderHTML.mermaidCache`（`NSCache`，key = 主题 + 源码，`countLimit=64`） | 同一篇来回切 / 多篇含相同图 → 秒开。**首次打开仍慢** |
| 2 | `formatConversationDate` 的三个 formatter 提到文件级复用 | 去掉侧栏每行每渲染的两个 formatter 现建（`RelativeDateTimeFormatter` 构造很贵） |
| 3 | `extractHeadings` 的 `NSRegularExpression` 提到文件级 | `tocItems` 是计算属性，一次渲染调两次，不再重编译 |
| 4 | 查那处「双重编码」能不能砍 | **不能砍**：`:576-577` 注明翻转是对齐 `DocumentPDFExporter` 的刻意补偿（Mermaid 输出 y 向下、不翻会倒）。原样保留 |

**止血的边界**：首次打开一篇 mermaid 文档仍然会卡（缓存只在第二次起生效）。要连首次也不卡，见 §三。

## 三、根治方案：两段式加载（待实现）

**思路**：主线程只负责"先把页面画出来"，图表烤图挪到后台。

1. `makeNSView` → `buildHTML()` **不含图表**：mermaid 块只写占位元素（如 `<div class="mermaid" data-diagram="0"></div>`），秒出。
2. 同时起 `Task.detached(priority: .userInitiated)`：逐块渲染浅/深两张 PNG data URI（**同一个 `NSCache` 兜底**，跨文档复用）。
3. 每块渲染完回 `MainActor` 调新 JS：`reader.setDiagram(index, lightURI, darkURI)` → 把占位替换成现有的 `<img class="m-light"> / <img class="m-dark">` 结构（现有 `render()` 里的替换逻辑可复用）。
4. 主题切换仍是 CSS 媒体查询切显隐，两张图都在 DOM 里 → 「切主题不重载」这条既有收益不丢。

**必须处理的风险**

- ⚠️ **注入会改布局**：图表从 0 高度变成实体，正文下移 → 已画好的**划线/批注矩形与锚点全部错位**。方案：注入后让 JS 重跑一次画线与测量（Swift 侧 `pushAnnotations()` 已有重画入口；连线矩形 `measureConnector` 也要重取）。
- **滚动位置**：注入发生在 `ready` 之后，用户可能已经滚了；不要用 `restoreScrollY` 硬回滚，只保证"当前视口内容不跳"（图表在视口上方时下移是不可避免的，属可接受）。
- **单块渲染失败**：占位保留并显示「图表渲染失败」文案（现有的失败分支搬过来）。
- **首屏与 `ready` 的时序**：`ready` 仍要按"无图版本"发；批注拉取不依赖图表。

**改动面评估**：`ReaderHTML.build`（拆出"无图版"与占位）、HTML 模板（加 `.mermaid` 占位样式 + `setDiagram` JS）、`ReaderWebView`（后台任务的生命周期与取消：换篇/销毁时要 `cancel`）、`DocumentReaderController`（注入调用 + 注入后重画）。约 40~60 行，**需真机验划线位置**。

**可选微优化（独立、与上面无关）**：现在每图有「PNG 编码 → NSImage 解码 → 翻转重绘 → tiff → 再 PNG 编码」两次编码。若改成直接拿 `CGImage` 走 `CGImageDestination` 出 PNG，可省一次编码 + 一次解码；翻转仍要留。**收益未测，且必须真机核对图没倒**，单独小改。

## 四、验收

- 打开含 mermaid 的文档：**首屏立刻出**，图表短暂后到位，无彩球（用 Instruments 的 Time Profiler 或主线程耗时日志确认，避免凭手感）。
- 图表到位后：划线/批注位置正确（开一篇既有划线的带图文档，看划线是否压在原文上）、连线不错位。
- 浅/深主题切换：图仍正确、不重载、不丢滚动。
- 切走再切回、快速连点两条：后台任务被取消、无交叉污染。

## 五、不做什么

- 不在 WebView 里引 Mermaid JS（既有决定，见 `document-images.md` §六）。
- 不为省一半时间改成"只渲染当前主题"（会让切主题需要重载并丢失滚动，收益换不回体验）。
- 不动服务端渲染 / Web / 分享页。
