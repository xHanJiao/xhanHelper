# 文档划线批注（v1）— macOS 阅读器换 WebView + 锚定批注

> 状态：**UI 已确认（`mockup.html`），待实现**（2026-09-17）
> 范围：server（Go：模型 + API + LLM 工具）+ macOS 客户端（SwiftUI + WKWebView）
> 预览：`docs/design/document-highlight-annotation/mockup.html`
> 唯一入口：macOS 客户端。iOS / Web / 分享页 **不做**。

---

## 一、背景与范围

文档是"作品"，用户会反复回看（dev 库 106 篇，平均 4872 字符 / 158 行）。现在的阅读体验缺两件事：

1. **不能划重点** —— 回看时要重新扫一遍才知道哪句是结论。
2. **不能写自己的话** —— 想说"这条待验证""和上次结论冲突"只能另开笔记，脱离了上下文。

### 顺带解决的一个技术债

正文渲染要从 `MarkdownUI`（SwiftUI 原生）换成 **WKWebView**。不是因为批注，而是因为文档形态：

| 形态 | 占比 | 平均 |
|---|---|---|
| 标题 | 100% | — |
| 粗体 | 93% | — |
| 无序列表 | 90% | — |
| **表格** | **67%** | **每篇 7 张、共 941 个表格行** |
| 引用块 | 70% | — |
| 有序列表 | 69% | — |
| 行内代码 | 48% | — |
| 代码围栏 | 35% | — |
| Mermaid | 5% | — |
| 链接 | 3% | — |
| 图片 | 0% | — |

表格密集 + 要支持跨块选区 + 要按字符精确锚定 —— 在 `NSTextView` 里自绘这套代价极高，而 WebView 里 `Range` / `getClientRects` 是白送的。

### 产品切割

- **v1：能划、能批、能回看** —— 划线 + 批注 + 批注列表 + 重定位降级。
- **v2：颜色语义 / 标签 / 跨文档汇总** —— 另立任务。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| D1 | 平台范围 | ✅ 仅 macOS 客户端。iOS / Web / 分享页不渲染划线 |
| D2 | 正文渲染 | ✅ **直接替换**为 WKWebView（marked.js + HTML 模板），**不保留** MarkdownUI 阅读器；不做两套并存 |
| D3 | 划线实体 | ✅ 单一实体两种表现：`note` 为空 = 纯划线；非空 = 划线 + 批注。**不分表** |
| D4 | 视觉区分 | ✅ 单色划线 + **行尾小气泡图标**区分有无批注；颜色语义留给 v2 |
| D5 | 选区工具条 | ✅ HTML 浮动工具条，4 个动作：划线 / 批注 / 复制 / 问 AI |
| D6 | 批注输入 | ✅ HTML 浮层（quote 预览 + textarea + ⌘↵ 保存） |
| D7 | 跨块选择 | ✅ 允许（跨段落 / 跨列表项）；**跨表格、跨代码围栏除外**（不支持，选中时工具条只给"复制"） |
| D8 | 重叠 | ✅ 允许重叠；hover 时 popover 列出该位置**全部**批注，>1 条打「重叠划线」标签 |
| D9 | 点击划线 | ✅ 菜单：编辑批注 / 复制原文 / 删除划线 |
| D10 | 重定位 | ✅ 三级降级：① quote 精确匹配（唯一命中走快路径）→ ② Markdown 纯文本投影后匹配（带 prefix/suffix 消歧）→ ③ 前后文夹逼（覆盖"span 内部被插字/局部改写"，带相似度门槛）。全失败标 `orphaned`。多命中而消歧不达标时**判失活而不是猜第一处** —— 静默挪位比失活更糟。v1 提供划线列表抽屉，orphan 灰态可见，可「重新定位 / 删除」 |
| D10b | 创建时的消歧 | ✅ 客户端额外回传 DOM 选区的前后文（`prefix`/`suffix`，纯文本 ≤60 字）。没有它，`结论` 这类短句在文中重复时只能锚到第一处，且返回的快照也是按第一处算的 → 客户端会在错的地方画线且无提示 |
| D10c | 快照与 quote 刷新 | ✅ 每次成功定位都刷新 `Prefix`/`Suffix`，夹逼改写时同时刷新 `Quote` 并置 `QuoteRewritten`。让锚点始终描述当前正文，且"服务端改了用户划的原文"这件事可见 |
| D11 | 重定位职责 | ✅ **服务端权威**（在 markdown 源文上算），客户端只在渲染后的 DOM 上做同样的文本查找用于绘制。两处逻辑一致但分别实现，见 §4.4 |
| D12 | LLM 暴露 | ✅ 只给 `list_annotations(document_id)`（只读）。`update_document` 改完正文触发重定位，返回里报「影响 N 条批注」 |
| D13 | 流水线隔离 | ✅ 批注**不进**记忆 / 画像 / 摘要流水线（它是读者笔记，不是用户事实） |
| D14 | 「问 AI」 | ✅ v1 就做。落点已定：**复用 PiP 聊天浮层**，走**真实会话**（用户在 PiP 输入框里敲问题，不再弹中间对话框）。完整 orchestrator 能力（能调工具）。详见 §十二 |
| D15 | 批注内容 | ✅ 纯文本（不做富文本 / @ 引用 / 图片） |
| D16 | PDF 导出 | ✅ 不带划线与批注（导出的是文档本身） |
| D17 | 配额 | ✅ 单条 quote ≤ 500 字符、批注 ≤ 2000 字符、单文档 ≤ 500 条 |
| D18 | 全局快捷键 | ❌ 无 |
| D19 | LLM 改写时正在阅读 | ❌ 不自动刷新（避免读到一半正文跳走）。退出重进或手动刷新才更新 |
| D20 | 顶栏入口 | ✅ 分享 / 导出 PDF / 下载 `.md` **保持**在窗口顶栏 `document-actions` 组（`DocumentSelectionCoordinator.swift:50-69`），文档详情页顶栏只放「返回文档列表」+ 查找。mockup 顶栏把两组画在了一起，属预览简化，实现以本条为准 |

---

## 三、数据模型（`server/internal/model/document_annotation.go`）

```go
// DocumentAnnotation — 文档划线 / 批注。note 为空 = 纯划线，非空 = 划线 + 批注。
type DocumentAnnotation struct {
    ID          string `gorm:"primaryKey;size:36" json:"id"`
    UserID      string `gorm:"index;size:36;not null" json:"user_id"`
    DocumentID  string `gorm:"index;size:36;not null" json:"document_id"`

    // 锚定四元组（服务端在 markdown 源文上计算）
    Quote       string `gorm:"type:text;not null" json:"quote"`       // 用户选中的 DOM 文本（不含 Markdown 标记），空白已归一
    Prefix      string `gorm:"type:text" json:"prefix"`              // 创建时的前文快照（≤60 字，源文原文）
    Suffix      string `gorm:"type:text" json:"suffix"`              // 创建时的后文快照（≤60 字，源文原文）
    StartOffset int    `gorm:"not null" json:"start_offset"`          // 源文字符偏移（runes）
    EndOffset   int    `gorm:"not null" json:"end_offset"`

    Note        string `gorm:"type:text" json:"note"`                 // 空 = 纯划线
    Orphaned    bool   `gorm:"index;not null;default:false" json:"orphaned"`
    // QuoteRewritten 重定位走了夹逼（原文被局部改写），Quote 已被服务端改写成新文本。
    // 记下来是为了让"动了用户数据"这件事可见，而不是静默发生（P4 决定 UI 怎么表现）
    QuoteRewritten bool `gorm:"not null;default:false" json:"quote_rewritten"`

    CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
    UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
```

要点：

- **偏移是 runes 不是 bytes**（中文）。服务端权威，客户端不用。
- `Orphaned` 是**服务端唯一裁定**的，客户端只按字段渲染灰态。
- `Prefix` / `Suffix` 是**创建时**的前后文快照，但每次成功重定位都会刷新成"当前正文"的样子 —— 让锚点始终描述现状，消歧依据不会随文档演进变旧（见 §4.5）。
- 不做软删（与 `Document` 一致，物理删）。删文档时同事务批量删批注（`Delete(&DocumentAnnotation{}, "document_id IN (?)", ids)`）。
- 不加 GORM `Belongs to` 关系（项目里 `Document` 也没加），靠 `document_id` 手 JOIN。

---

## 四、锚定与重定位

### 4.1 为什么是 quote + prefix/suffix

三种候选方案的取舍：

| 方案 | 问题 |
|---|---|
| 只存字符偏移 | LLM 一改正文全废 |
| 存块序号 + 块内偏移 | 块序号会平移（插入一段就全错） |
| **quote + 前后文窗口** | 天然抗平移。quote 被局部改写时靠前后文夹逼救回来（rung 3）；真的整句被换掉才失活 |

选第三种，这是 Readwise / Hypothes.is 的成熟做法。夹逼那一层是本项目补的 —— 没有它，"LLM 在你划的句子中间插了个词"就变成失活，抽屉里很快会攒一堆灰条目。

### 4.2 创建

客户端回传 `quote`（DOM 选中的纯文本）+ **可选的** `prefix` / `suffix`（DOM 选区的前后各 ≤60 字纯文本）。`start_offset` / `end_offset` 与落库的前后文快照全部由服务端在 `content` 上算。

为什么快照必须服务端算，而前后文还要客户端给一份：

- 客户端手里是 **DOM 文本**（`这是重点内容`），服务端手里是 **Markdown 源文**（`这是**重点**内容`）。让客户端去猜源文里的前后文必然对不上 → **快照只能服务端截**。
- 但反过来，服务端不知道用户点的是第几处：`结论` 在文中出现 3 次时，服务端没有 DOM 位置信息，只能锚到第一处 —— 而且返回的前后文快照也是按第一处算的，客户端照它渲染，会**一致地在错的地方画线，全程无提示**。
- 所以客户端把 DOM 前后文当**消歧提示**发过来。两边都是纯文本，投影（§4.3 rung 2）之后就同空间了，能直接比对。

优先级：**客户端的 DOM 提示 > 存储快照**。两者都不会被直接落库，落库的是服务端算出来的那份。

定位失败**也要存**（`orphaned = true`，偏移留 0），因为用户在正文里看得见划线的 DOM 位置，抽屉里必须能列出这条。

### 4.3 重定位算法（`server/internal/service/document_anchor.go`）

触发器：`update_document` 改完正文之后（`DocumentAnnotationRepo.RelocateAll`）。

四级阶梯。**rung 1 只负责「唯一命中」这个快路径，多命中一律落到 rung 2** —— 那里客户端提示、存储快照、正文都在投影空间，消歧只需一份实现：

```
rung 0  quote 为空 → 失败

rung 1  源文精确匹配
          命中 1 处 → 直接返回（最常见、最可信，最快）
          命中 >1  → 落到 rung 2
          命中 0   → 落到 rung 2

rung 2  投影空间整段匹配（needle = project(quote)）
          命中 1 处 → 返回
          命中 >1  → 按前后文打分消歧：
                       score(p) = 2*commonTail(prefix, hay[:p])
                                + 2*commonHead(suffix, hay[p+len(quote):])
                       最高分 ≥ 0.5 × (len(prefix)+len(suffix)) → 用它；
                       同分 → 取离上次已知偏移更近的；
                       命中点 > 64 个（复读机文档）→ 放弃消歧
                       ✓ 达标              → 返回
                       ✗ 不达标 + 有前后文  → 落 rung 3
                       ✗ 不达标 + 无前后文  → 取第一处
                                             （仅创建且客户端没给提示时；
                                              刚划的线立刻显示失活更让人困惑）
          命中 0   → 落到 rung 3

rung 3  投影空间前后文夹逼 —— 覆盖"LLM 在划线 span 内部插字 / 局部改写"
          P = 前文投影的尾部 ≤30 字，S = 后文投影的头部 ≤30 字（各要求 ≥8 字）
          在投影正文里找全部 P / S 命中点
          配对条件：P 在 S 之前，且 gap ≤ max(原span×3, 原span+200)
          挑配对：cost = |toOrig[p]-旧Start| + |toOrig[s]-旧End| 最小
          夹出的区间裁掉两侧空白 → 这就是新锚点，quote 换成这段新文本
          相似度门槛：LCS(新文本, 原quote) / len(原quote) ≥ 0.5
                        ✓ → 返回（Fuzzy=true，QuoteRewritten 置位）
                        ✗ → 说明整句被换掉，不是被改写 → 失败

全部失败 → ok=false，调用方标 orphaned。
```

三处判断的用意：

- **rung 2 不达标时不"猜一个"**。多命中、前后文又对不上，说明无从判断；猜第一处会把划线**静默挪到另一段**且 `orphaned=false`，用户完全看不到。判失活反而信息量更大（抽屉「已失活」里可见、可重挂）。
- **rung 3 要过相似度门槛**。光靠前后文夹逼，会把"整句被换掉"也认成"被改写"，等于又一次静默改用户数据。LCS 占比 < 0.5 就判失活。
- **rung 3 的区间只裁空白、不裁标点**。夹出来的是「前文末尾 → 后文开头」，句末标点会留在里面，这是可接受的。

阈值全部写死（`ponytail: 写死 0.5 / ×3 / +200，等真的误挂或误失活再暴露成配置`）。

投影（rung 2/3 的 haystack）只做**字符级剔除**，不解析链接目标 `(url)`、不处理图片 alt：多留几个字符只会让评分多一点噪声，不会让定位错位（每个投影字符都映射回真实源文下标），而且图片在现有 106 篇里是 0 篇。

### 4.4 客户端为什么还要再实现一次

服务端算的是 **markdown 源文**里的偏移；客户端要画的是 **marked 渲染后的 DOM**。两者字符流不同，源的偏移在 DOM 上无意义。

客户端因此要在 DOM 文本流上再跑一次定位。**但只需要 rung 1 + rung 2 两条**（约 40 行 JS），不用实现夹逼 —— 因为服务端在夹逼成功时已经把 `quote` 刷成了当前文本，客户端简单的整段查找就能找到。

两边可能给出不同结果 —— 以**服务端为准**：

- 服务端 `orphaned = true` → 抽屉里灰态，正文里**不画**
- 服务端正常、客户端在 DOM 里找不到 → 抽屉正常显示，正文不画（下次 `setAnnotations` 重试）。**不**因为客户端找不到就标 orphan

前后文提示直接用服务端回传的 `prefix` / `suffix` 字段（列表接口本来就带），投影后比较。

### 4.5 快照刷新与 `QuoteRewritten`

`RelocateAll` 里，**任何成功定位都顺手刷新** `Quote` / `Prefix` / `Suffix` 快照（含 rung 3 的 Fuzzy 结果）：

- 刷新让锚点始终描述"当前正文"，下一次改写时才是有效参照，不会累积漂移
- Fuzzy 时额外把 `QuoteRewritten` 置位，让"服务端改写了用户划的原文"这件事**可见**
- 失活时不写（没有可刷新的目标）
- 全字段比对后再决定是否落库；同一正文重复调用是幂等的，不产生无谓写

关于「会不会在界面上闪一下」：刷新是**服务端字段**行为，用户看不到。视觉上唯一的风险是「正文正在显示时被 LLM 改写」，而 D19 已经定了**不自动刷新**，所以正常路径零闪。真出现闪烁再按现象调整。

## 五、服务端 API

`server/internal/controller/annotation.go`（新增），挂在认证组下：

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/documents/:id/annotations?limit=&offset=` | 列表（含 orphan），按 `start_offset, id` 升序 |
| `POST` | `/api/documents/:id/annotations` | 创建 `{quote, note?, prefix?, suffix?}`。`prefix`/`suffix` 是 DOM 选区前后文（消歧提示，≤60 字）；锚点与落库快照由服务端算 |
| `PATCH` | `/api/annotations/:id` | 更新 `note`（`""` → 变纯划线，不清除实体）。**不**允许改锚点 |
| `DELETE` | `/api/annotations/:id` | 删除 |
| `POST` | `/api/annotations/:id/relocate` | 手动重挂 `{quote, prefix?, suffix?}`（抽屉「重新定位」，客户端重选一段后调），成功则 `orphaned` / `quote_rewritten` 都清零 |

路由在 `router.go` 的认证组，紧挨 documents 一组。约束：

- 所有接口按 `user_id` 过滤（越权一律 404，不区分"不存在"和"不是你的"）
- 创建时校验配额：`quote ≤ 500 字`、`note ≤ 2000 字`、单文档 ≤ 500 条
- `quote` 入库前做空白归一化（折叠连续空白为单空格、去首尾），与 §4.3 投影的空白折叠对齐
- `prefix` 截尾、`suffix` 截头（保留靠近选区的那 60 字）
- `DELETE /api/documents/:id` 级联删批注（`DeleteByDocument`，失败只记日志不阻断）

创建接口的返回体就是完整批注对象（含算好的 `start_offset` / `end_offset` / `orphaned`），客户端拿到即可画线，不必再拉一次列表。

---

## 六、LLM 工具

### 6.1 新增 `list_annotations`（只读）

挂在 `DocumentTool` 上（`server/internal/tool/document.go`），与 `get_document` 同族：

```
list_annotations(document_id, limit?, offset?)
→ { annotations: [{quote, note, orphaned, created_at}], total, hint }
```

- 返回里带 `quote` 和 `note`，**不带** offset（对 LLM 无意义）。
- `note` 为空的（纯划线）也返回，标 `"note": ""` —— LLM 能看出"用户圈了这句但没写"。
- 分页沿用 `resolvePaging`（`document.go:34`）。
- 只读：进了 `ReadOnlyFuncs()`（plan 模式可用），也进了 `Catalog().Funcs` 高频组（注册表启动校验会强制这两处与 `Functions()` 一致）。

### 6.2 `update_document` 的报告

正文变了就调 `RelocateAll`，把结果塞进返回值（只是改标题/标签时不触发）：

```
{
  "success": true,
  "id": "...",
  "message": "文档已更新；本次改动使 1 条用户划线批注失效，可在文档批注列表里看到",
  "annotations_affected": 1,   // 失活状态发生变化的条数
  "annotations_orphaned": 1    // 其中新失活的（改回原文会变 0）
}
```

两个字段只在 `affected > 0` 时才出现，避免每轮都刷无用字段；`RelocateAll` 出错不阻断正文更新，就地 `log.Printf`。

`Description()` 里补了引导：改正文前先 `list_annotations` 看看用户标了什么，尽量避开。

### 6.3 工具目录

`Catalog()`（`document.go:69`）里把 `list_annotations` 放进 `get_document` 那一组高频函数。

---

## 七、客户端渲染（WebView 替换）

### 7.1 现状

`DocumentFullView`（`client/xHelper/xHelper/Shared/Views/DocumentListView.swift:428`）当前：

```
VStack
 ├ HStack(返回文档列表 + 查找)          :443
 ├ findBar (可选)                       :469
 ├ Divider
 └ HStack(alignment: .top)
    ├ tocSidebar (220) / tocCollapsedStrip (32)   :561 / :631   ← 原生，保留
    ├ Divider
    └ ScrollViewReader → ScrollView → VStack
       ├ 标题 + tags + Divider
       └ readingContent  (splitMarkdownBlocks:366 / attributedMarkdown:393)
```

改造后：

```
VStack
 ├ HStack(返回文档列表 + 查找)          ← 保留，查找改走 JS
 ├ findBar                              ← 保留，改成 JS 查找
 ├ Divider
 └ HStack(alignment: .top, spacing: 0)
    ├ tocSidebar / tocCollapsedStrip    ← 保留（原生，不变）
    ├ Divider
    ├ DocumentReaderView (WKWebView)    ← 新，吃整个剩余宽度
    ├ Divider (抽屉开时)
    └ AnnotationDrawer (SwiftUI, 300)   ← 新
```

**目录栏、返回按钮、查找栏都不搬进 WebView** —— 它们已经能用，WebView 只负责正文。这砍掉了大量坐标转换和桥接代码。

### 7.2 `DocumentReaderView`（新，`Shared/Views/DocumentReaderView.swift`）

- 复用 `DocumentPDFExporter.swift:301-373` 的 HTML 骨架与 `marked.min.js`（`xHelperMac/Resources/marked.min.js`），CSS 换成**屏幕阅读版**：不加 `.page { width: 595px }` / `12pt` 那套打印尺寸，改正文列宽 728px、字号 `FontSizeManager.shared.scaledSize * 1.18`。
- **Mermaid 沿用现有链路**：`MermaidUtility` 原生渲染 PNG → 注入 `<img>`（与 `DocumentPDFExporter` 同路）。**不要**在 WebView 里引 Mermaid JS。
- 透明硬约束（否则糊掉卡片材质）：
  ```swift
  webView.isOpaque = false
  webView.backgroundColor = .clear
  webView.underPageBackgroundColor = .clear   // macOS 14+
  ```
  且 HTML `body` 不铺任何不透明底色。
- 主题（浅/深）+ 字号变化时**不重载 HTML**，走 `evaluateJavaScript` 改 `<html>` 上的 class / CSS 变量（重载会丢滚动位置）。注意 CSS 派生变量的坑：带 `var()` 的变量必须在**声明它的那个元素**上求值，主题 class 挂 `html` 不是 `body`。
- `loadHTMLString(html, baseURL: nil)`。`XHWebView.swift` 目前只支持 `URL`（`XHWebViewRepresentable` 里 `load(URLRequest)`），**不复用**它，直接写一个薄的 `NSViewRepresentable`。理由是文档正文不需要导航白名单 / 加载态 / 外链拦截那一套。

### 7.3 桥接协议

Swift → JS（`evaluateJavaScript`）：

| 方法 | 说明 |
|---|---|
| `reader.setContent(markdown)` | 首次装载（也可直接烤进 HTML，二选一看实现方便） |
| `reader.setAnnotations(json)` | 全量替换划线集合 |
| `reader.setTheme('light'\|'dark')` | 切主题 |
| `reader.setFontSize(px)` | 实时调字号 |
| `reader.scrollToAnnotation(id)` | 抽屉点击 → 滚到对应划线并闪烁 |
| `reader.scrollToHeading(index)` | 目录点击（替代原来的 `scrollToAnchor`） |
| `reader.find(query)` / `findNext()` / `findPrev()` / `clearFind()` | 查找栏 |

JS → Swift（`WKScriptMessageHandler`，沿用 `XHWebViewBridge` 的命名与错误处理模式）：

| 消息 | 载荷 | 用途 |
|---|---|---|
| `readerReady` | — | Swift 收到后再灌 annotations（避免竞态） |
| `annotationCreate` | `{quote, note}` | 新建划线/批注（锚点由服务端算，返回值带回） |
| `annotationUpdate` | `{id, note}` | 编辑批注 |
| `annotationDelete` | `{id}` | 删除 |
| `annotationAskAI` | `{quote}` | 「问 AI」（落点待定 → v1 先 toast） |
| `contentHeightChanged` | `{h}` | WebView 自适应高度（滚动交给外层 SwiftUI ScrollView？否 —— 见下） |

**滚动归属**：正文滚 **WebView 自己**（`reader` 的 `overflow-y: auto`），不外包给 SwiftUI `ScrollView`。理由：跨块选区的 `getClientRects` 在自身滚动容器里才准；外包后 mousemove 期间要不停回传高度，抖动且贵。

### 7.4 浮层放哪

| 组件 | 位置 | 理由 |
|---|---|---|
| 选区工具条 | **HTML** | 紧贴选区、跨块时要在多行间定位 |
| 批注输入浮层 | **HTML** | 同上，要引到选区下方 |
| hover popover（重叠列表） | **HTML** | 坐标在 JS 手里 |
| 点击菜单 | **HTML** | 同上 |
| 目录栏 | SwiftUI（已有） | 页面级，无坐标依赖 |
| 查找栏 | SwiftUI 外壳 + JS 搜索 | 输入框是原生，高亮是 `::highlight()` |
| **划线列表抽屉** | **SwiftUI** | 页面级面板，和目录栏同级；要跟目录栏一起收放 |

---

## 八、交互规格

以下全部与 `mockup.html` 一致，实现时以 mockup 为准逐条对齐。

### 8.1 选区 → 工具条

- `mouseup` 出工具条，光标离开选区且不 hover 工具条则收起（需要 `hideToolbar` / `hideAll` 两层，别用一个 —— 一轮里踩过 mouseup/click 竞态）
- 工具条 4 个动作：**划线** / **批注** / **复制** / **问 AI**
- 跨块选区：对 `range.extractContents()` 后的每个块分别包 `mark`，**共享同一个 `data-id`**（一个批注 = 多个 `<mark>`）
- 跨表格 / 跨代码围栏：不支持，工具条降级为只有「复制」

### 8.2 绘制划线

JS 里维护 `id → [{mark}]`。定位流程：

```
1) 把 #content 的文本节点拍平成一条字符串 + 节点偏移索引（一次，缓存）
2) 精确 indexOf(quote) → 失败则投影后 indexOf（与 §4.3 同样两条 rung）；
   多处 → prefix/suffix 打分（前后文取服务端回传的字段）
3) 命中区间 → 按文本节点边界切成 N 段，每段 wrap 一个 <mark class="hl" data-id>
4) 未命中 → 不画，不报错
```

批注气泡：只挂在**最后一段** `mark` 之后（`::after` 或内联 `<span class="hl-bubble">`），避免每段都挂一个。

### 8.3 hover / 点击

- hover 划线段 → popover：`elementsFromPoint` 聚合该位置所有 `mark` 的 `data-id`，逐条渲染 `quote + note`；>1 条打「重叠划线」标签
- 点击划线段或气泡 → 菜单：**编辑批注** / **复制原文** / **删除划线**
- 「编辑批注」打开 §8.4 的浮层，`⌘↵` 保存
- 批注留空保存 = 该实体退化成纯划线（不发 `annotationDelete`）

### 8.4 批注输入浮层

- quote 预览（截断到 2 行）+ `<textarea>` + `⌘↵` 保存 + Esc 取消
- 空 note 保存按钮文案是「保存为纯划线」，有文字是「保存批注」

### 8.5 查找

- 用 **CSS Custom Highlight API**（`::highlight(find)` / `::highlight(find-cur)`），不改 DOM，因此**不会**破坏划线 `mark` 的节点结构
- SwiftUI 查找栏输入 → `reader.find(query)`；返回命中数给 `findbar` 的计数 label
- 跨小节查找时先 `scrollIntoView` 到当前命中项

### 8.6 划线列表抽屉

- 三个 tab：**全部 N** / **仅批注 N** / **已失活 N**
- 每行：`quote`（2 行截断）+ `note`（1 行截断）+ 相对时间
- 点击行 → 关闭抽屉 + `scrollToAnnotation(id)` + 闪烁 2 次
- orphan 行：整个灰掉 + quote 加删除线 + 右侧两个动作「重新定位」「删除」
- 「重新定位」→ 进入重选模式（正文里提示"请重新选中一段文字"）→ 选中后 `POST /api/annotations/:id/relocate`

---

## 九、边界与配额

| 项 | 值 | 校验位置 |
|---|---|---|
| 单条 quote | ≤ 500 字符 | 服务端（超了 `400`） |
| 单条批注 note | ≤ 2000 字符 | 服务端 + 客户端 `textarea` 上限 |
| 单文档批注数 | ≤ 500 | 服务端 |
| 锚点前后文窗口 | 各 60 字符 | 客户端截取 |
| 文档删除 | 同事务删批注 | 服务端 |
| 无痕 / 会话删除 | 批注跟文档走，无独立生命周期 | — |

---

## 十、阶段划分

每阶段独立可验收，不要求一次做完。

| 阶段 | 内容 | 验收方式 |
|---|---|---|
| **P1 服务端** ✅ | 模型 + migration + 5 个 API + `list_annotations` + `update_document` 影响报告 + 重定位算法单测 | 见下「P1 完成情况」 |
| **P2 WebView 阅读器** | `DocumentReaderView` 替换 `readingContent`；目录跳转改走 JS；查找改走 JS；主题/字号联动 | 真机读 5 篇真实文档（含表格/代码块/Mermaid），视觉与旧版无回归 |
| **P3 划线批注交互** | 选区工具条 / 绘制 / hover / 点击菜单 / 批注浮层 / 创建-编辑-删除回传 | 真机：跨段落划线、重叠划线、批注增删改 |
| **P4 抽屉 + orphan** | SwiftUI 抽屉三 tab + 滚动定位 + 重新定位流程 | 真机：改标题制造 orphan，灰态可见可重挂 |
| **P5 问 AI** | 复用 PiP 承载文档问答：轻量进 PiP / `doc_quote` 注入 / `brief_answer` 长度约束 / 引用条 / 底部「存为批注」窄条 | 见 §十二。**服务端 + 客户端 `brief_answer` 开关已完成**，其余待 P2/P3 |

依赖：P2 与 P1 无依赖，可并行。P3/P4 依赖 P1+P2。**P5 依赖 P2（WebView 才拿得到选区）+ P3（浮层技法、批注编辑浮层）**，所以顺序是 P2 → P3 → P5，不提前。

### P1 完成情况（已完成）

新增：

- `server/internal/model/document_annotation.go` — `DocumentAnnotation`（含 `QuoteRewritten`）
- `server/internal/service/document_anchor.go` — `Anchorer`：`Locate` / `Relocate` / `Context` + Markdown 纯文本投影 + 夹逼 + LCS
- `server/internal/service/document_annotation_repo.go` — CRUD + `RelocateAll`（含快照刷新）
- `server/internal/controller/annotation.go` — 5 个 handler
- `server/scripts/api-test-annotations.sh` — 26 项 HTTP 冒烟（独立 SQLite 实例，不碰 dev 库）

改动：`init_db.go`（AutoMigrate）、`init_repos.go`、`appinit/router.go`、`appinit/tools.go`、`controller/router.go`、`controller/document.go`（级联删）、`tool/document.go`（`list_annotations` + 影响报告）。

测试（分三层，各管一类问题）：

| 层 | 文件 | 管什么 |
|---|---|---|
| 定位算法单测 | `document_anchor_test.go` | 7 类定位形态（普通/跨粗体/表格/行内代码/列表/引用/换行折叠）、抗平移、消歧、orphan、投影映射、LCS |
| **不变量测试** | 同上 `TestRelocateInvariants*` | 不预设实现，只断言"不许发生什么"：① 成功时命中文本与原 quote 相似度 ≥ 0.5（不许指向无关处）② 失败必须是 ok=false ③ 撤销编辑必须回到原偏移 ④ 锚点周围文本指纹逐字不变 |
| 失败导向 case | 同上 | 多命中+前后文不符必须失活（A）／带 DOM 提示必须锚到第 3 处（B）／内部插字必须长大且 Fuzzy（C）／整句替换必须失活／重复前后文不许乱配 |
| 仓库层 | `document_annotation_repo_test.go` | 抗平移不失活、失活可恢复、快照刷新、`QuoteRewritten` 置位、幂等不写、空 note 退化、级联删、越权隔离 |
| 工具层 | `document_test.go` | 改正文报失活 → `list_annotations` 可见 → 改回恢复；注册表启动校验不 panic |
| 集成冒烟 | `scripts/api-test-annotations.sh` | 接线：表/路由/JSON 形状/级联/越权 404/配额边界/重挂 |

**三个钉子做过变异验证** —— 把实现改回旧行为，测试必须变红：

| 变异 | 变红的用例 |
|---|---|
| `disambiguate` 不达标时 `return hits[0]`（旧行为） | `TestDisambiguateRefusesWhenNoContextMatches`、`TestRelocateInvariants_RepeatedQuote/锚点前后文被全部改写` |
| 忽略客户端 `prefix`/`suffix` 提示 | `TestLocateUsesDOMHintsForRepeatedQuote` |
| 关掉 rung 3 夹逼 | `TestFuzzyMatchGrowsSpanOnInnerInsert` |
| 夹逼不裁两侧空白 | `TestFuzzyMatchPicksNearestPair` |

验证结果：

- `go build ./... && go vet ./... && go test ./...` 全绿
- dev MySQL 已迁移：`document_annotations` 含 `quote_rewritten`，两个 index 都在
- 冒烟脚本连跑两次全绿（26/26），退出后端口无残留、临时配置已删
- 8080 上的 dev server 已用新二进制重启

一个写脚本时踩到的坑记在这里：`go run` 只杀掉父进程，编译出的子进程会留在端口上 —— 下次跑就会静默测到旧实例。脚本改成先 `go build` 再直接跑二进制，并在启动前断言端口空闲。

---

## 十一、验收

1. `go build ./... && go test ./...` 全绿
2. 重定位单测覆盖：唯一命中 / 多处命中靠前后文消歧 / 带 DOM 提示锚到指定重复处 / 换行重排后仍命中 / span 内部插字夹逼长大 / 整句替换后 orphan / 多命中且前后文不符时判失活
   并且三个钉子做过**变异验证**（把实现改回旧行为，测试必须变红），见 P1 完成情况
3. 真机（用户在 Xcode 构建）：
   - 打开一篇 7 张表格的文档，表格渲染正常、不横向溢出
   - 跨段落划一条线 → 退出文档 → 重进，划线还在原位置
   - 让 LLM `update_document` 在该划线**前面**插一段 → 重进，划线仍在原句（验证抗平移）
   - 让 LLM 改写该划线所在句子 → 重进，抽屉「已失活」里能看到它
   - 浅色 / 深色各看一遍，正文底下是卡片材质（不是暖米色、不是白块）
   - 找一句话，查找高亮与已有划线**共存**不互相破坏
4. `./server/scripts/api-test-annotations.sh` 全绿（26 项，独立 SQLite 实例，不碰 dev 库）

---

## 十二、P5「问 AI」用 PiP 承载

> UI 稿：`docs/design/document-highlight-annotation/mockup-p5.html`（九种状态可切）

### 12.1 决策

| 编号 | 决策 | 结论 |
|---|---|---|
| P5-1 | 落点 | ✅ **复用 PiP 聊天浮层**（`FloatingChatPanel`）。不新画浮层、不动右栏布局 |
| P5-2 | 会话归属 | ✅ **真实会话**。点「问 AI」**直接进 PiP**，问题在 PiP 输入框里敲；不弹中间对话框 |
| P5-3 | 智能程度 | ✅ **完整 orchestrator** —— 能调工具（检索记忆/笔记/其他文档）。回答里会有工具气泡 |
| P5-4 | 回答→批注 | ✅ 可以。走文档侧的批注编辑浮层，**预填 AI 回答、可改完再存** |
| P5-5 | 长度控制 | ✅ **不用 `max_tokens`**（那是硬截断，且全链路无 `finish_reason` 感知，截断了系统不知道）。改为往 system prompt 注入一段**输出长度约束**（≤500 字），靠 prompt 管而不是靠截断管。接口是 `brief_answer` 布尔参数，见 §12.7 |
| P5-6 | 没划线就问 | ✅ 不自动建划线。点「存为批注」时才建（复用 `POST /api/documents/:id/annotations`） |

### 12.2 链路

```
文档页选中文字 → 工具条「问 AI」
  → 客户端暂存选区（quote + DOM 前后文），轻量进 PiP（展开态）
  → 用户在 PiP 输入框敲问题（输入栏上方一条可删的「引用胶囊」）
  → POST /api/chat 带 doc_quote{document_id, title, quote, prefix?, suffix?}
  → 服务端把它做成 context packet 注入本轮 XMLContext（**仅 LLM 可见**）
  → 完整 orchestrator：检索 / 工具 / 计划 / L3 确认全在
  → 回答流式出现在 PiP
  → PiP 底部窄条（仅文档问答轮）：「来自《标题》· 存为批注 / 忽略」
  → 存为批注 → 文档滚到该段 + 批注编辑浮层预填 AI 回答（可编辑）
              → 保存时该划线还不存在就先建
```

### 12.3 为什么用 XMLContext 那条通道，而不是拼进用户消息

`Message.Content` 是用户原始文本，`Message.XMLContext` 是拼给 LLM 的那一大块（notice + 检索资源 + `<user_query>`），**客户端从头到尾不读 XMLContext**（全仓 grep 零命中，`model/message.go:23`）。

`ModeNotice` 就是走这条路的先例（`chat.go:197-202` → `orchestrator.go:718-720`）。所以文档引用可以**不污染用户消息正文** —— 用户敲的问题就是他的问题，引用作为 packet 只喂 LLM。这比会话接力那套「拼进正文」（`ChatViewModel.swift:866-902`）干净，也符合 AGENTS.md「动态内容注入当前 user 消息」的既有约定。

用户能看到引用，靠的是**气泡上方的引用条**（客户端 transient 渲染，不落库）：

```
引自《N.E.K.O 记忆系统切面调研》
在渲染期把反思结果直接拼进 persona
```

### 12.4 必须新增的「轻量进 PiP」

`enterPipEffects`（`ContentView.swift:201-221`）现在做四件事：`pipCollapsed = true`、`router.navigateTo(.chat)`、左栏缩到最小、**窗口动画变宽**。

文档页在右栏、PiP 是盖在右栏之上的 overlay（`ContentView.swift:538`），所以文档不会消失 —— 但窗口变形 + 左栏跳走 + 面板还收起，从阅读中途点一下「问 AI」变成这样是不能接受的。

所以要加一个「轻量进入」路径：只置 `isPipChat = true` + `pipCollapsed = false`，**跳过**跳左栏 / 缩左栏 / 改窗口宽度。这不是可选项，不加等于每次提问都打断阅读。

### 12.5 落点在哪

| 事情 | 放在哪 | 为什么 |
|---|---|---|
| 提问输入 | **PiP 的输入栏** | 用户定的：直接进 PiP，省一层对话框 |
| 引用胶囊（发送前） | PiP 输入栏上方 | 可见、可删；发送后变成气泡上方的引用条 |
| 引用条（发送后） | 用户消息气泡上方 | 滚动回看时这一轮自解释 |
| 「存为批注」 | **PiP 底部窄条** | 助手气泡刻意无右键菜单（`MessageListView.swift:207-232`），不改它；窄条只在文档问答轮出现 |
| 批注编辑 | **文档侧浮层**（P3 已有） | 批注属于文档；预填 AI 回答让用户能改 |
| 工具调用细节 | PiP 里折成一行 | 窄面板放不下完整工具卡片；点开才展开 |

### 12.6 改动清单

**服务端**（比 P1 小）

| 文件 | 改动 |
|---|---|
| `controller/chat.go` | 请求体加 `doc_quote`（`chat.go:107-126`） |
| `agent/orchestrator.go` | `RunRequest` 加同名字段（`:56-100`）；`buildUserMessage` 注入 packet（`:718-720` 旁边） |
| `agent/ctxflow/blocks.go` | 加一个 block 类型（仿 `NoticePacket`，`blocks.go:152-160`） |

**客户端**

| 文件 | 改动 |
|---|---|
| `ContentView.swift` | 轻量进 PiP 分支（`:201-221`）+ 一个「轻量进入」标志 |
| `ChatViewModel.swift` | 本轮载荷 `docQuote` + 发起函数（仿 `beginHandoff` 的「标志位 + 载荷暂存 + `.done` 消费」，但**不用** `hiddenRound` —— 那是整轮剔除，粒度太粗） |
| `SSEClient.swift` | `connect` 加 `doc_quote` 参数 |
| `FloatingChatPanel.swift` | 底部「存为批注」窄条；空会话态提示 |
| 文档页（P3 的 WebView 侧） | 引用胶囊、气泡上方引用条、「存为批注」→ 批注浮层预填 |

**不改**：`FloatingChatPanel` 的数据来源。它现在取 `chatTabManager.currentTab`（`ContentView.swift:540`）—— 两种去向都落在 currentTab，所以不用改。

### 12.7 `brief_answer`：参数与注入实现（服务端已完成）

**接口**：`POST /api/chat` 请求体加 `brief_answer: bool`（默认 false）。语义是「本会话要求精简回答」，不是解码参数 —— 它不改 `max_tokens`，只往 system prompt 加一段约束。

**注入位置**：`promptbuilder` 的 **Layer 1（行为准则）**，新增 `answerLengthProvider`（`static.go`），章节标题「回答长度」：

```
## 回答长度

本轮要求精简回答：正文控制在 500 字以内。
1. 先给结论与判断，再给必要的依据；能一句说清就不写三句
2. 需要列举时用短要点，不要展开成段落文章
3. 不要复述用户引用的原文
```

三个实现细节，都有测试钉住：

1. **注册顺序必须排在 `userContextTriggerProvider` 之后** —— 后者的 `9. 用户信息深挖` 是 `corePrinciples` 编号列表的**续写**，中间插一个带标题的章节会把它截断。
2. **条件判断写在 `Build` 里，不写在 provider 里** —— `StaticProvider.Render(ctx)` 签名不带 `opts`，条件没有别的入口。这与 Layer 3 里 plan / `ClientToolCatalog` 的既有写法一致。
3. **不注入时整段跳过** → 输出与"从未有过这个 provider"**逐字节相同**（`TestBriefAnswerOffKeepsPrefixCache` 用摘掉 provider 的方式对比验证），所以不开 brief 的会话前缀缓存零影响。

**开关改成了会话级（客户端本地存）**：

最初设计的是「逐轮参数」—— 只在点「问 AI」那一轮带上，别的轮不带。这在缓存上很亏：system prompt 在 `S` 与 `S+长度块` 之间来回横跳，**每跳一次丢一次**，等于点一次问 AI 就付两次全量 prefill。

后来用户拍板：**做成会话级通用能力，像其他按钮一样**。于是改为：

- 开关存在客户端本地，按会话存（`xhan_conv_brief_answer_<cid>`，与 `deepMode` / `verifyEnabled` 同一套写法）
- 每轮随请求发，服务端**零改动**（`brief_answer` 字段早已就绪）
- 结果：同一会话内 system prompt 恒定，**只有「开/关」那一次切换会分叉**，之后一直命中。缓存抖动问题自动消失

**副产品**：`brief_answer` 既然会话级稳定，它就和其他「会话级稳定态放 system prompt」的东西同级了 —— plan 只读提示敢放 system prompt 也是同一个理由（`Mode` 会话级稳定）。原先那段「要不要降级成 XMLContext notice」的纠结作废，**保持 system prompt 注入**。

**客户端 UI（已实现）**：

输入栏里一颗 icon pill，与「深度模型 / 信任 / 接力」同族，**默认收在「更多操作」行**（`InputBarButtonID.defaultCollapsed`）。未选中 `text.alignleft` + appAccent，选中 `text.badge.minus` + orange。mockup 里那三个「醒目程度」候选（不显示 / 一行小字 / 可点 chip）**已弃用** —— 直接做成按钮更符合输入栏既有的语言，也不用新造视觉。

**有个坑**：`InputBarLayoutState` 的折叠配置是持久化的，老用户存下来的集合里没有新 id → 直接 `Set(stored)` 会把它当"未收起"，显示在常驻行，与"默认收起"相反。`init` 里加了一次性补入（`InputBarLayoutState.swift:22-26`）。

**另一个不要再动的地方**：`/api/chat` 请求体里的 `temperature` / `max_tokens` / `thinking` 解析了但**有意不传进 `RunRequest`**（`chat.go:118-120` vs `chat.go:307-327`）—— 服务端是 LLM 参数的唯一事实来源。这不是 bug，别"修"。

### 12.8 还没定的

- **「新建会话 / 在当前会话继续」去哪了**：用户后来说「直接出 PiP 省点事儿」，所以中间对话框砍掉了，**默认在当前会话继续**。想要新会话的用户走 PiP 自己的入口（会话列表切/新建）。mockup 里没画这条分支 —— 如果实际用起来发现"我想对同一段文字另起炉灶"是常见诉求，再加
- 引用条要不要持久化（见 §十三 待定）
- PiP 轻量进入的边界（见 §十三 待定）
- ~~「精炼」标记的醒目程度~~ / ~~要不要可点~~ —— **已定**：做成输入栏 icon pill，会话级可切，见 §12.7

---

## 十三、不做 / 待定

### 不做（v1 边界）

- ❌ iOS / Web 前端 / 分享页渲染划线
- ❌ 划线颜色语义、标签、跨文档汇总
- ❌ 批注富文本 / @ 引用 / 图片
- ❌ 批注进记忆 / 画像 / 摘要流水线
- ❌ PDF 导出带划线
- ❌ LLM 写批注（只读 `list_annotations`，不给写）
- ❌ 全局快捷键
- ❌ 正文自动刷新（LLM 改写时正在阅读不打断）
- ❌ 跨表格 / 跨代码围栏选区
- ❌ 批注软删 + 回收站

### 待定

- ⏳ **引用条刷新后消失** —— 现在用客户端 transient 状态渲染（不新增持久化字段），重进文档后本轮引用条不再显示。要不要持久化，等真机看过再说
- ⏳ **PiP 轻量进入的边界** —— 文档问答进 PiP 时不跳左栏/不缩左栏/不改窗口宽度；用户主动 ⌘⇧M 进 PiP 时是否也该这样，还是保留现状
- ⏳ 重定位阈值（`0.5` / `×3` / `+200`）是否要暴露成配置 —— 现在写死，等真的误挂或误失活再说
- ⏳ 目录栏是否也搬进 WebView（现在保留原生，若以后要做"滚动时跟随高亮当前小节"，原生侧需要 WebView 回传 scroll 位置）
- ⏳ `quote_rewritten` 在抽屉里怎么表现（挂"原文已改写"徽标？还是只做排序/筛选用）
- ⏳ 「快照刷新会不会让界面闪」—— 已按"服务端刷新 + D19 不自动刷新"实现，视觉零闪；等真机跑完看现象再定要不要调

### 已知的预览与实现差异

- mockup 顶栏把「窗口顶栏的分享/导出/下载」和「文档详情页顶栏的返回/查找」画在了同一行 —— 实现按 D20 分成两处
- mockup 里的 `marked` 渲染是**手写的静态 HTML**（不是真跑 marked.js），所以排版细节（尤其表格边框、行内代码底色）以实现出来的为准，配色 token 以 mockup 为准
