# 039 · macOS 文档导出 PDF：文字被切缝横切、末页重复内容（导出策略从第一天就带的缺陷）

> 2026-10-16 定位并修复。表现：导出的 PDF 里**有整行文字被拦腰切断**（上半截在本页底、下半截在下页顶），
> 且**末页会把上一页的底部重复一遍**。根因是导出管线「`createPDF` 渲成一张超长单页 → CoreGraphics 按
> 固定 842pt 高度切段」，切缝**只看几何、不知道文字行在哪** —— 从 2026-08-07 加导出功能那天就带着
> （`89cb369a`），不是后来改坏的，也从没写过设计文档说明为什么这么做。
> **状态：已修**（换成 WebKit 打印引擎按行盒分页；macOS + iOS `swiftc -typecheck` 0 error；真机清单见 §6）。

## 1. 问题表现

- 长文档导出后，某些页边界上**一行字只有上半截**（下半截跑到下一页顶部）——用户主要看到这个。
- 末页有一段内容**和上一页底部重复**（页数越多越容易被注意到）。
- 短文档（一页装得下）正常，所以问题一直像"偶发"。

## 2. 根因

原管线（`client/xHelper/xHelper/Shared/Services/DocumentPDFExporter.swift`）：

1. `WKWebView.createPDF` 把整篇渲染成**一张超长单页**（mediaBox 高度 = 全文高度，实测 793×9990）；
2. `slicePDF` 用 CoreGraphics 从顶往下按**固定 `842 / scale` pt**（实测 1122pt）切段。

- **切字**：切缝位置只由 `bandTop -= bandHeight` 决定，**没有任何"行"的概念** —— 落在缝上的那一行被横切。
  原注释「保证内容完整（不保证断字位置优雅）」说明当时就接受了这个取舍，只是它其实是主要症状。
- **末页重复**：最后一段的 `bandBottom` 被 `max(..., src.minY)` 夹到页面底部，但**绘制时没有 `clip` 到该段高度**，
  于是末页画的是整张 842pt 而不是剩下的那点高度 → 上一页底部的内容整段重现。
- **相位相关，所以看着像偶发**：缝落在段间空隙时一个字都不切（真模板样本实测 0 处），落在行上就切
  （同款样本换行距实测 4 行被切，文字盒越出页框最多 6pt）。用户手上的文档布局不同，命中率就不同。

## 3. 引入时间点

`git log -S slicePDF` 只有一次引入：**`89cb369a`（2026-08-07，「文档 PDF 导出（Markdown → HTML → WKWebView → CG 分页）」）**。
之后 `1e549641`（换非持久 store）、`8c631e6e`（图片绝对 URL）都没动切片逻辑。该功能**没有设计文档**，
所以"为什么不用打印管线"从没落盘。旁证：Web 前端 `front-end/src/composables/useExportPDF.js` 用的正是
`@page { size: A4 }` + `page-break-inside: avoid` 那套（浏览器打印引擎按行盒分页）—— **只有 macOS 客户端
这一条路是例外的固定高度切片**。

## 4. 最终方案（2026-10-16，用户拍板走 A）

**换成 WebKit 打印引擎分页**：`webView.printOperation(with:)` + `NSPrintOperation`，一次解决切字与末页重复。

```swift
let printInfo = NSPrintInfo.shared.copy() as! NSPrintInfo
printInfo.paperSize = pageSize          // 595×842，与模板 @page 一致
printInfo.topMargin = 0                 // 四边 margin 都要单独设（NSPrintInfo 没有 margins 属性）
printInfo.bottomMargin = 0; printInfo.leftMargin = 0; printInfo.rightMargin = 0
printInfo.jobDisposition = .save        // 与下面这条**必须成对**，少哪个都会自己弹 NSSavePanel/PDF 面板
printInfo.dictionary()[NSPrintInfo.AttributeKey.jobSavingURL] = tmpURL
let op = webView.printOperation(with: printInfo)
op.showsPrintPanel = false              // 默认 true：不关就弹系统打印对话框（Safari ⌘P 那一套）
op.showsProgressPanel = false           // 默认 true：不关会闪一下进度窗
await printWaiter.run(op, in: window)   // 见 §5「run() 会挂住」
let data = try Data(contentsOf: tmpURL) // 打印接口**不返回 Data**，只能落一次临时文件读回
```

模板侧（同一文件底部 `template`）：

- `@page { size: __PAGE_W__pt __PAGE_H__pt; margin: 44pt 52pt }` + `.page { width: auto }`：
  **页边距必须给 `@page`，不能给会跨页分片的容器当 `padding`**（理由与实测见 §5 的「为什么不能给
  padding」）。纸张尺寸由 `buildHTML` 注入（`__PAGE_W__/__PAGE_H__` 取自 `pageSize`），不再两处各写一个数字。
- **样式里的长度全部 `px` → `pt`**：打印引擎里 1px = 1/96in，沿用 px 的话 `595px` 只占 A4 的 3/4 宽
  （右边一大块空白）；pt 是绝对单位，`595pt` 就是 595pt 纸宽，与旧 createPDF「1px 映射 1pt」的观感一致。
  （附带证据：pt CSS 下旧路径的源长页变成 793px 宽，`scale = 595/793` → 内容被整体缩到 75%。**pt 与打印
  引擎是成对的，别只换一头。**）
- 分页规则：`pre, table, blockquote, img, .mermaid { break-inside: avoid }`、`h1..h6 { break-after: avoid }`
  —— 与 Web 端 `useExportPDF.js` 同一套。**故意不含 `p`**：比一页还长的段落引擎只能硬断，加了 `avoid`
  反而会把整段推到下一页、上一页留大片空白。
- 不设打印超时：`NSPrintOperation` 没有公开的取消 API（`cleanUpOperation`/`destroyContext` 的注释明写
  「由打印操作自己调用，你不要调」），超时硬抛会留下「会话还开着 + delegate（assign 语义）已释放 +
  临时文件被删」的烂摊子，比它要防的挂起更糟 —— 详见代码里 `PDFPrintWaiter` 的注释。
- `-webkit-print-color-adjust: exact` 保留（见 §5 背景色那条：实测不是它在起作用，是通行写法/保险）。

**没采纳**：B 栅格化找全白行再切（图表一多就找不到缝）；C JS 量元素高度自己分页（重复造打印引擎的轮子）；
D 只给 `slicePDF` 加 `ctx.clip(band)`（只治末页重复，切字照旧）。

## 5. 实测（真 WKWebView 离屏探针，非 Chrome、非估算）

样本 = **从 Swift 源码现抠的真模板** + 260 条编号段落 + 一个代码块；同一份 HTML 分别走新旧两条路，
逐页用 PDFKit 抽文字，检查每条 `Lnnnn abcdef` 是否**完整且只出现一次**，并量每页「文字盒到纸边」的四边留白。

| 路径 | 页数 | 上边距 p1 / p2+ | 被横切 | 重复内容 | 空白尾页 |
|---|---|---|---|---|---|
| 旧 · createPDF + 切片 | 12 | 3 / 3…26（逐页递增） | **9** | **13** | — |
| 新 · 打印引擎（边距给 `.page` 的 `padding`，review 前） | 10 | 47 / **3** | 0 | 0 | **有 1 张** |
| 新 · 打印引擎（边距给 `@page`，现行） | 11 | **47 / 47** | **0** | **0** | 无 |

### 为什么不能给 `padding`（review 抓到的第二个缺陷，实测坐实）

`.page` 是**会跨页分片**的块。CSS 分片语义下，`padding-top` 只落在第一个分片、`padding-bottom` 只落在
最后一个分片（左右是块盒横向属性，每页都在）。实测后果两条：

- 第 2 页起正文**顶到纸边**（上边距 47pt → 3pt）；
- 末尾那段 `padding-bottom` 装不进最后一页时，会**多顶出一张没有文字的空白页**（10 页里最后一张是空的）。

改法就是 Web 端早就那样做的（`useExportPDF.js:137` 的 `@page { margin: 18mm 16mm }` + `@media print { body { padding: 0 } }`）：
边距交给 `@page`，容器只负责填满可打印区。正文列宽不变（595−104 = 491pt），所以排版不动、页数只多
1 页（原来是靠"没有边距"多塞了几行）。

四个踩到的坑（都写进代码注释了）：

1. **`op.run()` 会挂住不返回** —— 离屏窗口里实测 45s 没动过（`success` 也永远不来）。必须用异步的
   `runModal(for:delegate:didRun:contextInfo:)`，完成回调里再读盘。真机上若用 `run()`，症状就是导出永远转圈。
2. **边距不能给跨页容器的 `padding`**（见上一节的实测）。
3. **背景色**：打印默认不印背景色是 Safari 的老毛病（代码块/表头灰底会变白）。实测本机 WebKit 的打印管线
   **两个开关都不影响** —— 翻转 `shouldPrintBackgrounds`、或去掉模板里那条 `-webkit-print-color-adjust`，
   产出 PDF **字节完全相同**，而底色本来就印出来了（采样到 `(246,246,248)` = `#f6f6f8`，连续游程
   166/200 采样宽）。所以这两处都留成"给将来 WebKit 改默认的保险"，不是当前生效的那道。
4. **`NSPrintInfo` 没有 `margins` 属性**（Swift 侧），要逐边设 `topMargin/bottomMargin/leftMargin/rightMargin`。

## 6. 真机验证（待跑）

1. 导出一篇**长文档**（几十页）：逐页翻，页边界上**没有半行字**；末页**不重复**上一页底部、**不是空白页**。
2. **每一页**（尤其第 2 页起）四周留白一致 —— 顶部/底部与第 1 页一样，不顶到纸边。
3. **不弹任何打印对话框/进度窗**，用户只看到我们自己的 `NSSavePanel`。
4. 代码块 / 表头 / 引用块的**灰底还在**（没被打印引擎丢成白的）。
5. 表格、图片、mermaid 图正常；文字可选中、可搜索（打印管线出的是矢量文字，比 createPDF 更干净）。
6. 导出期间 UI 不卡死；超大文档（100+ 页）不崩、耗时能接受（打印任务仍在主线程上跑，只是不阻塞返回）。

## 7. 影响文件

- `client/xHelper/xHelper/Shared/Services/DocumentPDFExporter.swift`：`createPDF` + `slicePDF` 换成
  `printOperation` 静默落盘 + 读回；新增 `PDFPrintWaiter`（`runModal` 是异步的，要有人接住回调；不设超时）；
  模板 CSS 改 pt、边距移到 `@page`、纸张尺寸改为注入 `__PAGE_W__/__PAGE_H__`；错误枚举 `pdfTimeout`
  （连带那句对不上的「15s」，代码里其实是 120s）换成 `printFailed`。
- `client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`：阅读版模板注释里那句 `595px` → `595pt`。
- `docs/design/document-images.md`（§8.3 里指向导出器的行号与 `baseURL` 写法）、
  `docs/design/document-highlight-annotation/design.md`（阅读版模板"不照抄导出模板"那句里的 `595px`）。
- 探针**没进仓库**（一次性，跑在 `$TMPDIR`）：要复发自查就照 §5 重建 —— 抠真模板 → 离屏 `printOperation` →
  PDFKit 逐页抽文字比对 + 量四边留白。需求若变成"常驻自检"，把它收进 `client/xHelper/scripts/` 即可
  （它现抠模板，不会漂移）。

## 8. 已知风险

- `NSPrintInfo.shared` 会带上**系统默认打印机**的纸/可成像区域：纸宽已显式设成 595×842，但若默认打印机的
  imageable area 小于纸张，个别机器上边距可能被系统裁一点。真机第 2 条要顺便看一眼四周留白是否一致。
- 不设打印超时（原由见 §4）：万一 `didRun` 真不来，导出会一直等 —— 这时日志里「printOperation run」之后
  没有「pdf ready」，就是证据。没有公开的取消 API，硬拆更危险，所以认了这个取舍。
- 打印任务在主线程发起（`runModal` 不阻塞返回，但排版/绘制在主线程上跑）：超大文档导出期间会有短暂卡顿，
  这是"不引入后台打印线程"换来的简单性。要更顺滑得把打印挪到后台线程，风险更高，暂不做。
- 导出用非持久 store（与阅读器**不同**，理由见 `docs/bugs/031`）：阅读器靠磁盘缓存能显示的图，离线或远程
  不可达时导出会裂图。符合预期（"导出看的是当下真实网络结果"），要跟阅读器一致就删掉那一行。
- 临时 PDF 是文档全文明文，落在 `$TMPDIR`（随机文件名、正常路径会删）；异常终止才残留到系统清理。知情即可。
