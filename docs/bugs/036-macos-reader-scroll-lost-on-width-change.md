# 阅读文档时展开批注面板，正文位置跑掉

## 问题表现

macOS 客户端，在文档详情页（右栏）读一篇长文，点右侧「划线与批注」面板展开 → 中间正文整篇重新折行，刚才在看的那句被推到别处，得手动滑回来。收起面板同理。窗口缩放、拖分隔线也一样。

## 根因

正文列宽跟着视口走：模板 CSS `.page { max-width: clamp(728px, 68vw, 1100px) }`。`68vw` 意味着 **WebView 宽度一变就整篇重排**。

而滚动位置只按像素（`scrollY`）保留 —— 像素没变、内容却换了，读者眼前那句自然就跑掉了。**滚动条没有归零，所以看着不像"回到顶部"，只像"位置自己飘了"**。

所以这不是「展开批注面板」专属：展开本文目录（220）、展开批注面板（320）、拖分隔线、缩放窗口、最大化侧栏，全是同一个根因；改字号（`setFontSize`）同属「重排 + 像素级保留」。

## 触发链（以展开批注面板为例）

1. `DocumentListView` 的头部按钮 / `annotationStrip` → `withAnimation { annotOpen = true }`（0.2s）
2. `annotExpanded` false → true → HStack 里多出 320pt 面板 + Divider
3. `DocumentReaderView`（WKWebView）宽度带动画收窄 → WebKit **逐帧派发 `window.resize`**（实测 1000→700 的 0.2s 动画派发 19 次）
4. 正文重排 → 折行变化 → `window.scrollY` 保持不变 → 读者丢位置

## 最终方案

阅读器 JS 加「阅读锚点」：滚动时记下视口顶部那一行的「文本节点 + 字符偏移 + 它离视口顶的高度」，`window.resize` 时把同一个字 `scrollBy` 回原高度。

- 锚在 **DOM 节点**上（重排只换位置、不换节点），重排多少次都稳。
- 动画期间连发十几次 resize 就逐次补 —— 视线全程钉住。
- `anchorHoldUntil`（400ms）：补位自己会 `scrollBy`，那也会触发 scroll 事件（即取样点）；重排这一小段时间内不许重取样，否则锚点跟着抖、越补越偏。
- `setFontSize` 前后夹一次 capture/restore（字号变化是同一个坑）。

**不选**：把 `.page` 改成固定 728 列宽（只盖住宽窗那半截，窄到一起还是重排，且改了阅读版式）；批注面板改浮层（另一个设计决定，不夹带）。

## 核心改动

| 文件 | 改动 |
|------|------|
| `client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift` | 模板 JS 新增 `charRect` / `captureAnchor` / `restoreAnchor` + `window.addEventListener('resize', …)`；scroll 监听里记锚点；`setFontSize` 前后夹一次。Swift / CSS / 桥接协议**零改动** |

## 验证

离屏真引擎（WKWebView）探针，滚到 900 记下视口顶部那个字（字符偏移 34，屏幕高度 dy=-40）：

| 步骤 | 结果 |
|------|------|
| 1000→700，**未打补丁** | 同一个字飘到 dy=-5（跑 35px）= 复现 bug |
| 1000→700，补丁在位 | dy=-40，差 **0.0** |
| 700→1200 | 差 **0.0** |
| 字号 17→24 | 差 **0.0** |
| 1000→700 分 30 步 / 每 8ms（模拟 0.2s 动画，19 次 resize） | 差 **0.0**，回位后 `scrollY` 精确回到 900，JS 无 error |

另：`client/xHelper/scripts/check-reader-html.sh` PASS（159 条断言）；`xcrun swiftc -typecheck`（macOS 26.3 真实设置）0 error。

## 经验教训

**「内容按视口自适应」+「滚动位置按像素保留」是两个各自合理、凑一起就丢位置的设定。** 凡是有「跟视口走 / 按比例」的版式的滚动容器，重排时都必须把「读者在看哪」锚到**内容**（字符 / 节点）上，不能指望像素。

`68vw` 这类版式的副作用是：**任何**宽度变化都重排，包括用户根本没打算动版的（拖一下分隔线、缩放窗口、开个面板）。

## 已知边界

视口顶部正好是一张 mermaid 大图时，锚会落到最近的一行文字上，图片随列宽变化的那点高度差补不回来（图本身随列宽缩放，也没法逐像素对齐）。
