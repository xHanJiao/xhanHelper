# 文档划线批注（v1）— macOS 阅读器换 WebView + 锚定批注

> 状态：**UI 已确认（`mockup.html`），待实现**（2026-09-17）
> 范围：server（Go：模型 + API + LLM 工具）+ macOS 客户端（SwiftUI + WKWebView）
> 预览：`docs/design/document-highlight-annotation/mockup.html`
> 唯一入口：macOS 客户端。iOS / Web / 分享页 **不做**。
>
> ⚠️ **P5「问 AI」已于 2026-09-30 整体下线**（客户端工具条/菜单入口、引用胶囊/窄条、`DocumentAIBridge`、`DocQuote`；服务端 `doc_quote` / `document_quote` 块 / `DocQuoteMeta` 全删）。本文档中 **D5 / D9 / D14、消息表 `annotationAskAI`、§12、P5 各段均为历史记录，勿照此重新实现**。新方案（批注下 LLM 回复线程）见 `docs/design/document-annotation-reply/design.md`（AR-12）。

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
| D4 | 视觉区分 | ✅ 波浪线 + **行尾线色小圆点**区分有无批注（原方案是绿色圆底 + 气泡图标，真机太抢眼，已换）；颜色语义留给 v2 |
| D5 | 选区工具条 | ✅ HTML 浮动工具条，4 个动作：划线 / 批注 / 复制 / ~~问 AI~~（**问 AI 已下线，现 3 个动作**） |
| D6 | 批注输入 | ✅ HTML 浮层（quote 预览 + textarea + ⌘↵ 保存） |
| D7 | 跨块选择 | ✅ 允许（跨段落 / 跨列表项）；**跨表格、跨代码围栏除外**（不支持，选中时工具条只给"复制"） |
| D8 | 重叠 | ✅ 允许重叠；~~hover 时 popover 列出该位置全部批注~~ **hover popover 已于 2026-10-06 删除**，重叠在正文里无提示（右侧面板仍能看到两条） |
| D9 | 点击划线 | ✅ **左键 = 快捷定位到右侧批注面板**（滚动 + 闪烁）；**右键 = 菜单**：编辑批注 / 复制原文 / 删除划线（~~问 AI~~ 已下线）。见 `docs/design/document-annotation-focus/design.md` |
| D10 | 重定位 | ✅ 三级降级：① quote 精确匹配（唯一命中走快路径）→ ② Markdown 纯文本投影后匹配（带 prefix/suffix 消歧）→ ③ 前后文夹逼（覆盖"span 内部被插字/局部改写"，带相似度门槛）。全失败标 `orphaned`。多命中而消歧不达标时**判失活而不是猜第一处** —— 静默挪位比失活更糟。v1 在批注面板的卡片里 orphan 灰态可见，可「删除」（手动「重新定位」已于 2026-10-06 取消，仅保留自动重挂） |
| D10b | 创建时的消歧 | ✅ 客户端额外回传 DOM 选区的前后文（`prefix`/`suffix`，纯文本 ≤60 字）。没有它，`结论` 这类短句在文中重复时只能锚到第一处，且返回的快照也是按第一处算的 → 客户端会在错的地方画线且无提示 |
| D10c | 快照与 quote 刷新 | ✅ 每次成功定位都刷新 `Prefix`/`Suffix`，夹逼改写时同时刷新 `Quote` 并置 `QuoteRewritten`。让锚点始终描述当前正文，且"服务端改了用户划的原文"这件事可见 |
| D11 | 重定位职责 | ✅ **服务端权威**（在 markdown 源文上算），客户端只在渲染后的 DOM 上做同样的文本查找用于绘制。两处逻辑一致但分别实现，见 §4.4 |
| D12 | LLM 暴露 | ✅ 只给 `list_annotations(document_id)`（只读）。`update_document` 改完正文触发重定位，返回里报「影响 N 条批注」 |
| D13 | 流水线隔离 | ✅ 批注**不进**记忆 / 画像 / 摘要流水线（它是读者笔记，不是用户事实） |
| D14 | 「问 AI」 | ❌ **已下线**（2026-09-30）。原 v1 方案：复用现有聊天（PiP / 普通聊天栏）、会话级引用每轮携带、完整 orchestrator 能力。现改为批注下 LLM 回复线程，见 `document-annotation-reply/design.md` |
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

**不变量：`project()`（Go）与 `fold()`（JS）对同一份正文必须给出同一条纯文本流** —— 投影是两边唯一的公共空间，任一边多剥或少剥一个字符，症状都是「划线保存成功、但正文里画不出来」（抽屉正常、点行 toast 定位不到；见 `docs/bugs/015-nested-list-marker-in-projection.md`）。必须对齐的项：连续空白折叠（含 NBSP/`\u3000` 等，客户端用 `/\s/`）、**反斜杠转义**（`\*` → `*` 再按标记丢，见 `docs/bugs/020`）、行内标记 `*_`~|`（**不含 `[` `]`**：字面方括号在 DOM 里真实存在，剥掉会让 `[x]` 划不满、`[ ]` 选区 quote 变空，见 `docs/bugs/021`）、**行首块标记（含缩进后的：`  - ` / `  12. ` / `  > `）**。此外 `<br>`（源文行尾两空格硬换行）在 DOM 里无字符，客户端 `flatten` 必须在 `<br>` 处补分隔符，否则跨硬换行少一个空格（见 `docs/bugs/021`）。

最后一条踩过坑：缩进的两个空格曾把「行首」状态抹掉，于是 `  - ` 的 `-` 被当成正文留在投影里（`点评： - 信息论角度`），而 DOM 那边 marked 早已渲染成 `<ul>`，压根没有 `-` 这个字符 —— 选区一旦跨过嵌套列表项就 rung 1、rung 2 双输，掉进 rung 3 把用户 quote 改写成带 `- ` 的文本，客户端再也定位不到。

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
| `POST` | `/api/annotations/:id/relocate` | 手动重挂 `{quote, prefix?, suffix?}`，成功则 `orphaned` / `quote_rewritten` 都清零。**接口保留，但 2026-10-06 起客户端不再调用**（手动「重新定位」已取消） |

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
list_annotations(document_id, pending?, limit?, offset?)
→ { annotations: [{id, quote, note, orphaned, created_at, reply_count, last_reply_role, last_reply_at, replies, replies_truncated}], total, hint }
```

- 返回里带 `id` + `quote` + `note`，**不带** offset（对 LLM 无意义）。`id` 是主聊天 `reply_annotation(annotation_id, ...)` 的唯一来源，不可省。
- `note` 为空的（纯划线）也返回，标 `"note": ""` —— LLM 能看出"用户圈了这句但没写"。
- `last_reply_role` / `last_reply_at`（秒级）为该批注最新一条回复的角色与时间；无回复时留空。供主聊天判断「谁在等谁」。
- `pending=true` 只返回**等待 AI 回复**的批注（`note` 非空，且无回复或最新一条回复是用户），按最近活跃倒序——用于批量回复 / 继续回应用户追问（见 `docs/design/document-annotation-reply/design.md` §5.5）。**前提**：只有用户在批注抽屉里的追问会进线程；主聊天里的追问不写入线程，故不体现在 pending 清单。
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
  - ⚠️ **2026-10-12**：这条渲染**全在主线程同步**且无缓存，含 mermaid 的文档每次打开都重烤一遍（图多时卡出彩球）。已立止血 + 异步化方案：`docs/design/mermaid-async-render/design.md`。本条的「不引 Mermaid JS」约束不变。
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
| ~~`reader.beginRelocate(id)` / `cancelRelocate()`~~ | **2026-10-06 删除**：手动「重新定位」取消（失活条目只留删除） |

> 实现里 JS→Swift 的消息名比下表短（`ready` / `create` / `update` / `delete` / `copy` / `ask` / `findResult` / `focus`），以 P3 / P4 完成情况为准。

JS → Swift（`WKScriptMessageHandler`，沿用 `XHWebViewBridge` 的命名与错误处理模式）：

| 消息 | 载荷 | 用途 |
|---|---|---|
| `readerReady` | — | Swift 收到后再灌 annotations（避免竞态） |
| `annotationCreate` | `{quote, note}` | 新建划线/批注（锚点由服务端算，返回值带回） |
| `annotationUpdate` | `{id, note}` | 编辑批注 |
| `annotationDelete` | `{id}` | 删除 |
| `annotationAskAI` | `{quote, prefix, suffix}` | ~~「问 AI」（P5）~~ **已删除**：Swift 侧不再有 `case "ask"`，JS 不再发此消息 |
| ~~`relocate`~~ | `{id, quote, prefix, suffix}` | **2026-10-06 删除**（手动重定位取消） |
| ~~`relocateCancel`~~ | — | **2026-10-06 删除**（手动重定位取消） |
| `focus` | `{id}` | 左键点划线/气泡：请求右侧批注面板定位到该批注（滚动 + 闪烁），见 `docs/design/document-annotation-focus/design.md` |
| `contentHeightChanged` | `{h}` | WebView 自适应高度（滚动交给外层 SwiftUI ScrollView？否 —— 见下） |

**滚动归属**：正文滚 **WebView 自己**（`reader` 的 `overflow-y: auto`），不外包给 SwiftUI `ScrollView`。理由：跨块选区的 `getClientRects` 在自身滚动容器里才准；外包后 mousemove 期间要不停回传高度，抖动且贵。

### 7.4 浮层放哪

| 组件 | 位置 | 理由 |
|---|---|---|
| 选区工具条 | **HTML** | 紧贴选区、跨块时要在多行间定位 |
| 批注输入浮层 | **HTML** | 同上，要引到选区下方 |
| ~~hover popover（重叠列表）~~ | **已删（2026-10-06）** | ~~坐标在 JS 手里~~ |
| 点击菜单 | **HTML** | 同上 |
| 目录栏 | SwiftUI（已有） | 页面级，无坐标依赖 |
| 查找栏 | SwiftUI 外壳 + JS 搜索 | 输入框是原生，高亮是 `::highlight()` |
| **批注面板** | **SwiftUI** | 页面级面板，和目录栏同级；要跟目录栏一起收放 |

---

## 八、交互规格

以下全部与 `mockup.html` 一致，实现时以 mockup 为准逐条对齐。

### 8.1 选区 → 工具条

- `mouseup` 出工具条，光标离开选区且不 hover 工具条则收起（需要 `hideToolbar` / `hideAll` 两层，别用一个 —— 一轮里踩过 mouseup/click 竞态）。**只认左键**（`e.button === 0`）：WebKit 右键会先自动选中光标下的词，不判键会让右键划线叠出「工具条 + 右键菜单」（见 `document-annotation-focus`）
- 工具条 4 个动作：**划线** / **批注** / **复制** / **问 AI**
- 跨块选区：对 `range.extractContents()` 后的每个块分别包 `mark`，**共享同一个 `data-id`**（一个批注 = 多个 `<mark>`）。注意 P3 实现里包的是**重新定位得到**的 range，不是用户那个活选区（见「P3 完成情况」第 1 条）
- 跨表格 / 跨代码围栏：不支持，工具条降级为只有「复制」

### 8.2 绘制划线

JS 里维护 `id → [{mark}]`。定位流程：

```
1) 把 #content 的文本节点拍平成一条字符串 + 节点偏移索引（一次，缓存）
2) 精确 indexOf(quote) → 失败则投影后 indexOf（与 §4.3 同样两条 rung）；
   多处 → prefix/suffix 打分（前后文取服务端回传的字段）
3) 命中区间 → 按文本节点边界切成 N 段，每段 wrap 一个 `<mark class="hl" data-id>`
4) 未命中 → 不画，不报错
```

**第 3 步只能逐文本节点包，不许用 `range.extractContents()` + `insertNode()` 搬 DOM**（mockup 里就是那么写的，Chrome 下看着正常）。真机踩过：跨块时 `extractContents` 会给被部分包含的块级元素造**克隆**，再把克隆插到 range 起点 —— 起点在 `<h2>` 里就变成「标题里套列表」，表格行（`thead`/`tbody` 不在 `BLOCK_TAGS` 里）更是被整段塞进 `<mark>`，都是非法内容模型，WebKit 于是少画几个字或把整段行重组（用户报的「划完字消失了一部分」「bullet 被拆成两条、气泡另起一行」）。那坨克隆不是 mark，`unwrapAll()` 也擦不掉。逐文本节点包（`splitText` + 各自 `insertBefore`）结构一点不动，标题/列表/表格/`pre` 全都天然安全。

批注标记：只挂在**最后一段** `mark` 之后（内联 `<span class="ann-bubble">`），避免每段都挂一个。**就是一个 5px 线色小圆点**（没有图标、没有底色），见 §「P3 收尾」。

### 8.3 hover / 点击

- ~~hover 划线段 → popover：`elementsFromPoint` 聚合该位置所有 `mark` 的 `data-id`，逐条渲染 `quote + note`；>1 条打「重叠划线」标签~~
- ~~hover 浮层（只有带 note 的划线出）封顶 `min(62vh, 520px)` 内滚，见 §12「长批注的 hover 浮层」~~
- **（2026-10-06 起）hover 划线/圆点不再弹任何浮层**，`mousemove` 监听、`#popover` 元素与 `.p-*` 样式、圆点的 `title="查看批注"` 原生 tooltip 一并删除；note 只在右侧批注面板看。`mark.hl:hover` 线色变实保留。
- **左键**点划线段或气泡 → **快捷定位到右侧批注面板**（面板滚动到该条并闪烁；面板收起则先展开）。JS 发 `post({type:'focus', id})`，见 `docs/design/document-annotation-focus/design.md`
- **右键**点划线段或气泡 → 菜单：**编辑批注** / **复制原文** / **删除划线**（拦截 `contextmenu` + `preventDefault`；正文空白处保留系统菜单）
- 「编辑批注」打开 §8.4 的浮层，`⌘↵` 保存
- 批注留空保存 = 该实体退化成纯划线（不发 `annotationDelete`）

### 8.4 批注输入浮层

- quote 预览（截断到 2 行）+ `<textarea>` + `⌘↵` 保存。**不用 Esc 取消**（误触丢正在写的批注，用户要求去掉）—— 不保存退出只走「取消」按钮
- 空 note 保存按钮文案是「保存为纯划线」，有文字是「保存批注」

### 8.5 查找

- 用 **CSS Custom Highlight API**（`::highlight(find)` / `::highlight(find-cur)`），不改 DOM，因此**不会**破坏划线 `mark` 的节点结构
- SwiftUI 查找栏输入 → `reader.find(query)`；返回命中数给 `findbar` 的计数 label
- 跨小节查找时先 `scrollIntoView` 到当前命中项

### 8.6 批注面板（右侧第三列 · 单视图）

> 形态选型：`mockup.html` 控制条的「抽屉形态」两个候选（A 第三列 / B 覆盖右侧）× 「画布宽」（1180 / 840）四种组合都截了图对比，**用户选 A 第三列**。

> **变更（2026-10-06）**：原「讨论 / 全部」两个视图合并为**单视图** —— 只保留讨论卡片（quote + note + 可折叠线程 + 追问），删掉扁平「全部」列表与三个 tab；失活的「删除」并入讨论卡片的失活条目（手动「重新定位」同日取消）。详见 `docs/design/document-annotation-single-view/design.md`。

- 位置：正文右侧的第三列（宽 300），和目录栏同级；**开面板时自动收起目录栏**（用户还能手动展开）。选 A 的理由：和目录栏交互一致、不遮挡正文；代价是 840 的侧栏里正文只剩 ~150px，实际使用时要把侧栏拉宽（或收起目录栏）。
- 每条批注 = 一张卡片：引文（点引文 → `scrollToAnnotation(id)` + 闪烁，面板不关）+ `note`（左侧 `appAccent` 竖条）+ 可折叠线程（AI 回复按 markdown 渲染 / 追问）+ 相对时间（今天 `HH:mm`、更早 `yyyy/MM/dd HH:mm`）。
- 失活卡片（`orphaned`）：引文灰 + 删除线 + 「已失活」徽标；同处一行的元信息行（徽标 + 时间）右侧接「删除」（**常显** —— 真机鼠标不在卡上时等于不存在）；不给「让 AI 回复」入口。
- （**2026-10-06 变更**：手动「重新定位」已取消 —— 失活条目只留「删除」，正文里不画失活划线。原重选模式链路见文末「后续变更」。）
- 数据全部来自 `DocumentReaderController.annotations`，面板自己不拉取、不缓存；**正文被改写后**（`document.content` 变）重拉一次，失活状态由服务端重算

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
| **P2 WebView 阅读器** ✅ | `DocumentReaderView` 替换 `readingContent`；目录跳转改走 JS；查找改走 JS；主题/字号联动 | 见下「P2 完成情况」；真机验收待用户 |
| **P3 划线批注交互** ✅ | 选区工具条 / 绘制 / hover / 点击菜单 / 批注浮层 / 创建-编辑-删除回传 | 见下「P3 完成情况」；真机验收待用户 |
| **P4 抽屉 + orphan** ✅ | SwiftUI 抽屉（第三列，三 tab）+ 滚动定位 + orphan 灰态 + 重新定位流程 | 见下「P4 完成情况」；真机验收待用户 |
| **P5 问 AI** ❌ | ~~复用现有聊天承载文档问答~~ **已于 2026-09-30 整体下线**（客户端 + 服务端 `doc_quote`）；见文首提示与 `document-annotation-reply/design.md` AR-12 | — |

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
| 定位算法单测 | `document_anchor_test.go` | 7 类定位形态（普通/跨粗体/表格/行内代码/列表/引用/换行折叠）、抗平移、消歧、orphan、投影映射、缩进块标记、跨嵌套列表项不掉夹逼、LCS |
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

### P2 完成情况（已完成）

新增：

- `client/.../Shared/Views/DocumentReaderView.swift` — `DocumentReaderView` + `ReaderWebView`（NSViewRepresentable）+ `DocumentReaderController`（Swift↔JS 把手）+ 阅读版 HTML 模板与 JS
- `client/.../Shared/Services/MarkdownWebUtils.swift` — 抽出来的公共零件：`escapeHTML` / `jsString` / `markedJS`（原本这三段在 `DocumentPDFExporter` 里是 private，两边都要用）
- `client/xHelper/scripts/check-reader-html.sh` — 阅读器 JS 的可跑自查（见下）

改动：

- `DocumentListView.swift`——`DocumentFullView` 的正文区在 macOS 上换成 `DocumentReaderView`；目录点击改走 `reader.scrollToHeading`；查找改走 `reader.find`，计数来自 JS 回话；新增 `headingOccurrence(at:)`
- `DocumentPDFExporter.swift`——改用 `MarkdownWebUtils`，删掉三个私有副本
- `MermaidDiagramInlineView.swift`——`mermaidTheme(for:)` 由 private 提为 internal，阅读器预渲染 mermaid PNG 时复用同一套色

**与原计划的偏差**（都是实现时才暴露的）：

1. **透明不能照抄 iOS 的写法**。计划里写的是 `isOpaque=false` + `backgroundColor=.clear`——那是 iOS 的 API，macOS 上 `isOpaque` 只读、`backgroundColor` 根本不存在。macOS 得用 KVC 关 `drawsBackground`，再加公开的 `underPageBackgroundColor = .clear`，两条都要。
2. **主题不给 Swift 推，走 CSS 媒体查询**。计划里桥接协议有 `reader.setTheme()`；实际不需要：WKWebView 跟随窗口 appearance，`prefers-color-scheme` 在 CSS 里自动生效 —— 于是切主题零桥接、不重载、不丢滚动位置。代价是 mermaid 图（PNG，颜色烤死在图里）**浅深各渲一张**，靠媒体查询切显隐，`ponytail:` 注释标了双倍渲染这个天花板。
3. **`breaks: false`**。PDF 导出用的是 `breaks: true`，阅读器跟旧的 MarkdownUI 阅读器对齐（CommonMark：单个换行按空格接续），否则软换行的文档会平白多出一堆硬换行 —— P2 的验收标准是"视觉与旧版无回归"。
4. **iOS 保持 MarkdownUI 正文**。D1 是"仅 macOS"，所以 `DocumentFullView` 里正文区按平台分叉，iOS 的 MermaidUI/分块查找那套原样留着（`#if os(macOS)` 分叉）。避免动一个用户不会去验的平台。
5. **目录跳转传「文字 + 序号」而不是下标**。TOC 来自 markdown 源码、标题来自渲染后的 DOM，传下标在两边错位时会静默跳到别处；传 `(level, text, occurrence)` 至少错得可见。顺带发现**原来的目录跳转是坏的** —— `scrollTo(anchor)` 里 anchor 是 slug 字符串，而块 `.id` 是 Int，从来没匹配上过。
6. **查找用 CSS Custom Highlight API**（`::highlight(find)`）而不是包 `<mark>`：不改 DOM，所以和 P3 要画的划线 `mark` 结构互不干扰（计划里就是这么定的，这里记一笔因为它决定了查找不能复用地画 `<mark>` 的路径）。

**选区：先自绘，退回引擎画（P2 期间用户实测发现的两个问题，结论都指向「别自绘」）**

起因：用户先在正文里划词，发现「跨段选中时整段两侧的空白都被填上了色」，而且有一行**画不上**（从第一个 bullet 拖到子 bullet，「已产出素材共 4 条…」那一行始终没有高亮；Cmd+C 剪贴板里**有**这行 → 选区本身是好的，纯粹是绘制）。

- 第一个坑是 **WebKit 自己的选区绘制**：选中范围把整块包住时，它画的是一个横跨整个视图的大矩形（段间空隙、两侧留白一起填），比正文列还宽。CSS 关不掉：`inline-block` 照旧填块，改 `display:inline` 更糟（拖出一整条横跨视图的大十字）。Blink（Chrome）真实拖拽逐行贴字，只在合成的块边界 Range 下才填块 —— 所以**拿 Chrome 判断选区绘制是错的**，一开始的复现就是被这一点骗了。
- 于是第一版改成**自绘**：`::selection { color:#3B2E1F; background-color: transparent }` + Custom Highlight API（`::highlight(sel)`），`selectionchange` 时把 `getSelection()` 的 range 塞进 `CSS.highlights['sel']`、折叠时删掉。原生选区本身还在，复制粘贴/拖拽不变，只是不着色。`color` 必须写死 —— 不写会被涂成引擎自己的高亮前景色（白），而 `color: var(--text)` 在 `::selection` 里**解析不出来**（实测写死 `red` 生效）。
- 自绘之后冒出两个新问题，**都是自绘这条路修不掉的**：
  1. **容器 li 的文本永远画不上**（就是开头那行）：`snapshot-reader` 的 `nested` job + 探针量得清清楚楚 —— range 覆盖了那个文本节点、`getClientRects()` 也返回 1 个矩形，**WebKit 的高亮绘制就是跳过它**（该节点是「自己带文本 + 后面跟子列表」的 `li` 的直接文本）。用户侧看到的就是跨段选区中间空一行。
  2. **偶发地不消失**（用户报「点别处不取消，再等等又没了」）：探针显示**我们的簿记是对的**（折叠后 `hasSel=false`、折叠后的截图也确实干净），所以不是逻辑错，而是 **JS 没有「选区此刻是否可见」这个查询** —— 引擎把选区标成不可见（例如失焦）而 `getSelection()` 仍返回那个非折叠 range 时，自绘的色带就只能赖在那儿，等某次重绘才掉。
- **结论：把选区交回引擎，只改字形颜色**（`::selection { color: #2F7D63 }`，深色 `#92C8B4`，`background-color: transparent` 必须留）。两个问题一起消失：引擎自己知道选区可不可见；跨块大矩形只在填底时才出现，不填底就没有。代价是失去色带、只剩字形变色 —— 用户看过浅/深截图后拍的这个取舍。**自绘那套（`paintSelection` + `::highlight(sel)`）已删除，别再往回加**；要往回加，先跑 `snapshot-reader.sh` 看 `nested` 和 `after-collapse*` 这两张。 这也**反证了 P3 的划线要用真 DOM `<mark>`**（§7 原计划就是这么定的）：`<mark>` 是元素背景，不走引擎那套 range 矩形/失效机制，这两个坑都碰不到 —— 别为了「不改 DOM」把 P3 优化成 `::highlight(annotation)`。

**自查脚本 `check-reader-html.sh`**：模板与 JS 是嵌在 Swift 源码里的一大块字符串，`swiftc -typecheck` 只能证明它是个合法字符串，证明不了里面的 JS 能跑。脚本用正则把模板**从 Swift 源文件里抠出来**（所以不存在副本漂移），灌进 headless Chrome，断言分五组（条数随用例增长，脚本每次会把条数打出来）：渲染（标题/表格/代码块/行内代码/mermaid 占位替换成 data URI/透明底/`--fs` 生效）、查找（计数、`CSS.highlights` 大小、next/prev 回绕、无命中、清空）、选区（不自绘 / 原生选区可用 / `::selection` 不铺底且 color 不写 `var()`）、目录跳转（同名标题靠 occurrence 区分、按级别匹配）、`setFontSize`。P3 之后又加了划线批注一整组（画/擦/定位/形状矩阵，见「P3 完成情况」）。JS→Swift 的 `postMessage` 在 harness 里被换成一个收集器，`scrollIntoView` 被换成一个记录器，于是这两件本来异步的事都变成同步可断言。

**真引擎自查脚本 `snapshot-reader.sh`（+ `snapshot-reader.swift` + `reader-template.py`）**：把阅读版模板加载进真正的 WKWebView，按 `jobs` 逐步合成选区 / 折叠，每步截一张 PNG 并打印探针（选区长、`hasSel`、以及选区覆盖到的每个文本节点各有几个矩形）。**选区绘制、真实渲染这类问题只能在这儿看** —— Chrome 测不准（上面那条）。当前 jobs 就是上面那两个钉子的显影液：`nested`（容器 li 文本有没有被画上）、`collapse` 后紧跟的那张（折叠后有没有残留像素），外加探针保证「簿记 vs 像素」能分开判。工具本身也踩了几个坑，都修了：外观必须显式定（跟随系统时同一份代码午后截出浅色、入夜截出深色，看图的人会以为代码变了）；截图是透明的，得把宿主底色合成进去，否则在看图器里被衬成白色（深色下就成了「白底白字」，看着像坏了）；输出目录不存在时 `try?` 会静默吞掉写入失败，所以要主动建目录并把失败报出来。

模板的**抠取**现在只在 `reader-template.py` 里做一次，两个脚本共用（`check-reader-html.sh` 与 `snapshot-reader.sh`），避免副本漂移。

**这个脚本立刻抓到一个真 bug**：`new Highlight.apply(null, findRanges)` 会被解析成 `new (Highlight.apply)(...)` → `Highlight.apply is not a constructor`，也就是一查找就抛异常、连一个高亮都画不出来。改成 `new Highlight(...findRanges)` 后全绿。

验证结果：

- `swiftc -typecheck` macOS / iOS 两个 target 均 0 error（按约定不做 `xcodebuild`，真机编译由用户完成）
- `client/xHelper/scripts/check-reader-html.sh` → PASS（含「选区不再自绘」「::selection 不铺底、color 不写 var()」等断言；P2 收尾时 29 条，P3 + 搬 DOM 那个 bug 修完是 115 条，改成波浪线后 120 条，换小圆点标记后 121 条）
- 截图确认（浅/深）：标题 / 标签 / 标题层级 / 表格 / 代码块 / 行内代码渲染正常

### P3 完成情况（已完成，真机验收待用户）

新增 `client/xHelper/xHelper/Shared/Models/DocumentAnnotation.swift`；改 `APIClient.swift`（4 个方法）、`DocumentViewModel.swift`（暴露 `apiClient`）、`DocumentListView.swift`（`DocumentFullView` 收 `apiClient`、`reader` 改按文档构造）、`DocumentReaderView.swift`（控制器 + 模板一整套）。

**与计划的偏差 / 计划里没写明的决定**

1. **不在原地包选区，改「服务端回推 + 重新定位画」**。原计划（§8.1）是拿 `extractContents()` 直接包用户那个选区；实现改成：JS 只把 `{quote, prefix, suffix, note}` 发给 Swift，落库成功后 Swift **全量重推**列表，JS 按 `quote + 前后文` **重新定位**再包 mark（`locate()`，即 §4.4）。好处是「服务端为准」不需要对账：既没有乐观更新要回滚，也不会出现「DOM 画在一处、库里记在另一处」。代价是划线在往返（几十~几百 ms）之后才出现。**服务端判 orphan 时不画并 toast**，客户端 `locate()` 失败则**静默不画**（§4.4 原定）。
2. **`flatten()` 必须在块边界补一个 `\n`**（P2 遗留的必要修正）。marked 渲染出的 `<p>a</p><p>b</p>` 拍平后是 `"ab"`，而跨段选区的 quote 在服务端被折叠成 `"a b"` —— 不补分隔符，**跨段划线在 DOM 里根本搜不到**。补上后两边投影到同一空间。
3. **客户端定位复刻的是服务端 rung 1 + rung 2**（§4.4 原计划）：投影（丢 `*_`~|` 行内标记、解析反斜杠转义、折空白；方括号保留）→ 精确 indexOf → 多命中按前后文打分（阈值 `0.5`、无前后文取第一处、命中 > 64 个不猜）。rung 3 的夹逼不做 —— 服务端夹逼成功时已把 `quote` 刷成新文本，整段查找即可命中。
4. **重叠 = 嵌套 mark**。两条划线交叠时，跨过外层边界的那个 range 会带出外层 mark 的**克隆片段**（`extractContents` 对「部分包含的祖先」就是这么处理的），于是两层 mark 互相嵌套 —— 方向取决于哪条跨了边界，所以别断言方向。hover 用 `elementsFromPoint` 聚合该点所有 `mark`/气泡的 `data-id`，>1 条打「重叠划线」标签（§8.3）。
5. **重画走「全擦全画」**（`unwrapAll()` + 逐条 `locate()`），不做增量修补：跨块 + 重叠时增量状态机很难对，而全擦全画只依赖服务端那一份，成本是几十 KB 正文重挂几十个 `<mark>`（可忽略）。
6. **剪贴板交给原生**（JS 发 `{type:'copy', text}` → `NSPasteboard`）：`navigator.clipboard` 在 `loadHTMLString` 出来的页面里不是安全上下文，能不能用全看运气。
7. **用户提示统一走 Swift 的 `ToastCenter`**（成功不弹、失败必弹；orphan 一条），模板里不再做一套 HTML toast。
8. ~~**「问 AI」暂时只 toast**~~ —— **P5 已接真聊天**（§12），占位 toast 已删。
9. **抽屉（§8.6）没做** —— 归 P4；`reader.scrollToAnnotation(id)` + `.flash` 已经留好（P4 点击抽屉行直接调）。
10. **真机第一划就抓到一个真 bug：`wrapRange()` 搬 DOM 把块结构搬坏了**（详见下面「真机 bug」一段）。修法是把包 mark 改成**逐文本节点切**，并补上两条以前缺的硬断言。

**真机 bug：第一次划线，划到的字消失了一部分（已修）**

用户第一次划线（选区从 `## 目标` 这个标题里起手、跨过列表、收在 `## 重要细节` 的 bullet 中途）后，正文里**一部分字不见了**；重开文档字回来了，但**排版被打乱**：标题下挂了两个多缩进的 bullet、`## 重要细节` 后面跟了一个 bullet、气泡独占一行、bullet 被拆成两条。

根因就是上一段那条禁令：`wrapRange()` 照 mockup 用 `extractContents()` + `insertNode()`，起点在 `<h2>` 文本里 → 块级克隆被插进标题（`<h2><h2>目标</h2><ul>…`），`<ul>` 也跟着进了 `<h2>`；`<li>` 被克隆后一分为二。WebKit 对这种非法嵌套的重排就是少字/挪行，而 Chrome（mockup 的演示环境）容忍它。判断依据：查 dev 库里那条批注（quote 65 字、源文偏移 3–78）拿到的正是「标题打头 + 列表 + 中途截断」这个形状。

修法与验证：

- `wrapSpan(flat, start, end, ann)`：遍历拍平索引里的文本节点，`splitText` 切边界、各包一个 mark；跳过纯空白节点（块之间的换行是独立文本节点，包了只会徒增段数、把气泡顶到空行）。`locate()` 相应改成返回 `{flat, start, end}` 而不是 Range。
- 补两条**以前完全缺**的断言，它们才是能拦住这类 bug 的：① 每条批注画完 `#content.textContent` **逐字符不变**（丢字/挪位当场变红）；② 同一 `data-id` 的 mark 文本拼起来（投影折叠后）== 命中区间文本。再加一条结构断言：`mark` 里不许出现块级标签、标题里不许套 `p/ul/li`，且 `p/h2/h3/li/td` 数量不变。
- 形状矩阵：段中 / 整段 / 跨段 / **标题打头跨块** / 跨列表项 / 跨表格行 / `<strong>` 内 / `<pre>` 内 / 带批注 / 重叠 / 反复重画（3 轮）。
- 真引擎加三个 job：`marks`（滚到「三、它们之间怎么说话」，正是出事那个形状）、`marks-table`（跨表格行）、`marks-overlap`（两条重叠的线）。

**P3 收尾：划线从「底色高亮」改成「文字底部波浪线」**

用户看完真机截图后提了两条：① hover 浮层里的「点击编辑」四个字去掉；② 要的是**划线**、做成了高亮 —— 改成文字底部的**波浪线**。

- **实现走原生 `text-decoration`**：`underline` + `wavy` + `text-decoration-color/thickness` + `text-underline-offset`，引擎逐行片段绘制，跨段/跨列表/跨表格天然正确，零额外资源。代价是**波幅与周期不可调**（只能调粗细与颜色）；若以后嫌波形不对，退路是 `background-image` 一条 repeat-x 的波浪 SVG（浅/深各一条写死颜色的 data-URI，`var()` 进不了 background-image）。
- **`mark` 的 UA 默认底色是黄色**，`mark.hl` 里必须显式 `background: transparent` —— 去掉原来那行 `background: var(--hl)` 之后，黄底会从波浪线底下透出来（mockup 第一次截图就是这么露出来的）。这条已进自查断言。
- `--hl` / `--hl-hover` 语义从「底色」变成「线色」，值也从半透明（`.38`/`.26`）改成实色 —— 半透明的线在暖米底上几乎看不见。浅色最终落在**赭金 `rgba(217,151,27,1)`**（hover `rgba(186,125,10,1)`）、深色薄荷 `rgba(146,200,180,.90)`/1：第一版线色是琥珀 `rgba(240,186,60,.85)`，真机看下来**浅色下太浅不显眼**（用户反馈），压深一档后清楚多了。`.p-quote` / `.note-editor .quote` 的左边条引用的就是 `--hl-hover`，跟着变成一条实色短线。
- **重叠不错开**（用户选）：两条线落在同一处就是一条，重叠只靠 hover 浮层的「重叠划线」标签提示。所以**没有**加 `mark.hl mark.hl { text-underline-offset }` 那条规则 —— 真机上两条是否都画也就不必验。
- **标题照画**（用户选）：h2 自带一条 `border-bottom` 浅色分隔线，波浪线压在它上方 3px，二者不打架。
- `flash` 动画（P4 抽屉跳转要用）原来动 `background`，去掉底色后会变得不可见 → 改成动 `text-decoration-color` + `text-decoration-thickness`。
- 取值（线色 / 线粗 / 偏移 / 降部是否断开）做成 `mockup.html` 底部控制条的切换项，选定的默认值：**浅色赭金 `rgba(217,151,27,1)`（深色薄荷 `rgba(146,200,180,.90)`）/ 1.5px / 偏移 3px / `text-decoration-skip-ink: none`（降部不断开）**。要改就是模板里那四个变量。
- 「点击编辑」直接从 `showPopover()` 的 HTML 里删掉（连同 `.p-foot .spacer` 规则），点击划线出菜单的行为不变。
- **批注标记也从「绿色圆底 + 气泡图标」换成句尾 5px 线色小圆点**（用户：「有点丑，而且很突兀」）。候选做进 mockup 底部控制条的「批注标记」共 5 个：A 小圆点（**选中**）/ B 上标序号 `¹²³` / C 完全不标 / D 一个标记都不加、纯划线赭金而带批注的线转薄荷绿 / E 现状绿圆图标对照。`span.ann-bubble` 变成 `display:inline-block; width/height:5px; border-radius:50%; background: var(--hl-hover)`，`BUBBLE_SVG` 变量整个删掉（不再需要图标）。hover 放大到 1.6 倍、`title="查看批注"` 不变；hover 浮层与点击菜单的命中逻辑用的是 `data-for`/`data-id`，跟标记长什么样无关。
  - 踩坑：**headless 截图会冻在 `text-decoration-color` 那个 130ms 过渡的起始帧**（`transition` 只在有帧推进时才跑，虚拟时间下不保证），第一次截 D 方案是琥珀色、看着像「CSS 没生效」；`getComputedStyle` 紧接点击后读也是旧值。加 `transition:none` 重截才是真实观感。

**后续变更：浅色线色改主题绿（用户拍板）**

用户真机看下来，浅色赭金与整个 app 的主题色（appAccent 绿）不搭，要求划线（波浪线）与「批注卡片→划线」的连线都改成绿色。

- 浅色 `--hl` 由赭金 `rgba(217,151,27,1)` 改为主题绿 **`#2F7D63`（`rgba(47,125,99,1)`）**，`--hl-hover` 压深一档 **`#245F4B`（`rgba(36,95,75,1)`）**；深色本来就是薄荷绿，不动。
- Swift 侧 `AnnotationHighlightColor.value(_:)`（`AnnotationDrawer.swift`）浅色分支同步为 `Color(red:47/255,green:125/255,blue:99/255)` —— 批注连线与预览波浪线跟着变。
- 连带自动变：`span.ann-bubble` 句尾小圆点、`.p-quote` / `.note-editor .quote` 左边条（都引 `--hl-hover`）。查找高亮（`::highlight(find)` 橙黄）、`::selection`（本来就是绿）不受影响。
- `mockup.html` 的线色候选默认值同步为主题绿（历史候选保留在切换项里）。

**后续变更：连线终点移到正文列右侧（用户拍板）**

用户反馈「批注卡片→划线」的连线连在划线中间、会挡住后半行文字，要求连在右侧。根因：终点原取划线**首个文字片段自身的右缘**（`annotationRect` 返回 `rects[0]`），短划线结束在行中间时终点就落在正文列中间。

- `annotationRect`（`DocumentReaderView.swift`）改为返回「**正文列那一行**」：x/w 取 `#content` 的左右缘、y/h 仍取划线首行片段。于是终点 x = 正文列右缘，连线只在右侧留白里走。
- `connectorEnd`（`DocumentListView.swift`）终点再 **+6px** 进 padding（满行时尾端不贴最后个字）；"就近端点"逻辑不变（面板恒在右 → 取右缘）。预览 `ConnectorPreviewDemo` 同步 +6。
- `check-reader-html.sh` 补 3 条：`annotationRect` 的 x/w == `#content` 左右缘、y/h == 划线首行片段、未命中返回 null（166 → 169 条）。

**自查**

- `check-reader-html.sh`：**121 条断言**（脚本现在会把条数一起打出来）。新增划线一整套：单处命中 / 跨块多 mark / 标记只在有 note 时挂 / quote 带 markdown 标记也能命中 / 多命中按前后文挑对位置 / 前后文对不上就不画 / 失活不画 / 重叠嵌套 / 交叠处 hover 列出全部 + 「重叠划线」标签 / `scrollToAnnotation` 闪烁与未命中 / 点击菜单 / 工具条 4 动作 / 浮层保存发 `create` 且文案随内容变 / 编辑带出原 note 并发 `update`）。收尾时补的 5 条钉在划线样式上：`underline` + `wavy` / **底色透明**（防 UA 黄底或回退成高亮）/ 线色确实取 `--hl`（不是 `currentColor`）/ hover 线色比常态实 / popover 里没有「点击编辑」/ **标记是 5px 小圆点而不是 svg 图标**（盯住这次换掉的绿圆图标别回来）。
- `snapshot-reader.sh`：三个 job —— `marks`（跨块 / 带批注 / li 内 / **标题打头跨块**，滚到出事的那个形状）、`marks-table`（跨表格行）、`marks-overlap`（`k7` 与 `k2` 重叠在同一句上），浅/深各一张；探针报 `marks=11 bubbles=1 textLen=407=不变` + 逐 id 段数（`k1=2 k2=1 k3=1 k4=2 k5=2 k6=2 k7=1`），**`=不变` 就是「一字不少」的现场证据**。波浪线只在真引擎里看才算数（Blink 的画法不作依据）。
  - 顺带踩到：**画完之后再滚，截图会带上「滚走那块」的陈旧像素**（DOM 探针显示那里根本没有背景），所以 `__marks` 改成先滚到目标位置再画。
- `swiftc -typecheck` macOS / iOS 均 0 error。

### P4 完成情况（已完成，真机验收待用户）

> ⚠️ **本节为历史记录。** 2026-10-06：两视图合并为单视图；同日**取消手动「重新定位」**（失活条目只留「删除」）。下面提到 relocate/三 tab 的条目均已过时，以 §8.6 与文末「后续变更」为准。

新增：

- `Shared/Views/AnnotationDrawer.swift`（`#if os(macOS)`）—— 头（标题 + 总数 + 收起）/ 三 tab（全部 / 仅批注 / 已失活，都带计数）/ 行（quote 2 行 + note 1 行 + 时间）/ orphan 灰态 + 「重新定位」「删除」/ 空态三文案。**没有任何自己的数据源**：读 `DocumentReaderController.annotations`，动作也全走 controller。
  - **2026-10-06 变更**：三个 tab 与扁平列表已删除，面板改为**单视图**（只留讨论卡片）；失活动作迁入讨论卡片的失活条目。见 `docs/design/document-annotation-single-view/design.md`。

改动：

- `APIClient.swift` —— `relocateAnnotation(id:quote:prefix:suffix:)`（`POST /api/annotations/:id/relocate`，返回更新后的整条）。服务端 P1 就已就绪，**P4 零 Go 改动**。
- `DocumentReaderController` —— `@Published relocatingID`；对外 `scrollToAnnotation`（新增 `evaluateBool` 拿 JS 返回值，未命中 toast）/ `refreshAnnotations` / `deleteAnnotation` / `beginRelocate` / `cancelRelocate` / `relocate`；`apply` 收 `relocate` 与 `relocateCancel`。
- 模板 JS —— `relocateId` 状态 + `beginRelocate`/`cancelRelocate`/`relocateFromSelection`；重定位模式下 `mouseup` 不弹工具条、直接把选区当新锚点 `post({type:'relocate'})`；`Esc` 退出模式并回 `relocateCancel`。**不加横幅、不动 CSS**。
- `DocumentListView.swift` —— 顶栏「查找」旁加抽屉开关（macOS only）；抽屉进 `HStack` 第三列，开时顺手收目录栏；`.onChange(of: document.content)` → `refreshAnnotations()`。

两个必须知道的点：

- **「正文改写后重拉」这条不加，P4 的验收根本过不了**。`loadAnnotations` 原来只在首次 `ready` 拉一次（`hasLoadedAnnotations` 一置位就短路），正文被 LLM 改写后客户端手里还是旧列表，`orphaned` 全为 false —— 抽屉里看不到任何失活条目。现在 `document.content` 一变就重拉（也顺带覆盖「改回原文恢复」）。
- **失活行的两个动作常显**，不做 hover 才出现。原设计的 hover 方案在真机上等于「鼠标不在行上就没有这个功能」。

**自查**

- `check-reader-html.sh`：**129 条断言**（P4 加了 8 条）—— 失活的不画 / 重定位模式下选中发 `relocate` 且带 id 与选区四元组 / 此时工具条不弹 / 此时不误发 `create` / `Esc` 回 `relocateCancel` / `Esc` 后模式退出（选中回到工具条）/ `Esc` 后不再发 `relocate`。
- `snapshot-reader.sh` 三个 job 重跑仍 `marks=11 bubbles=1 textLen=407=不变`（P4 只动了选区路由，没碰画线路径）。
- `swiftc -typecheck` macOS / iOS 均 0 error。
- mockup 侧记一条坑（已进 `AGENTS.md`）：**整个 `<script>` 是一个 IIFE，新增 `const`/`let` 与已有变量重名就是静默 SyntaxError** —— 所有交互全死、headless 也不报错，症状只是「控件不生效」。这次栽在 `q` 与 `wIdx` 上，靠 `--dump-dom` 看 `<html>`/`<body>` 的 class 才定位到。

### P5 完成情况（❌ 已于 2026-09-30 整体下线，以下为历史记录）

改动清单见 §12.6（服务端 5 层 + 客户端 8 个文件），这里只记状态、自查与偏离。

**自查**

- 服务端 `go build ./... && go test ./...` 全绿（新增 7 个测试：`ctxflow` 块渲染 3 条 `TestDocumentQuoteBlock_*`、`normalizeDocQuote` 2 条、`buildUserMessage` 2 条 —— 后者钉「进的是 `XMLContext` 而不是 `Content`」）
- `check-reader-html.sh`：**157 条断言**（P5 加了 28 条）—— 工具条「问 AI」发 `quote + 前后文` / 点击菜单里的「问 AI」发的是**这条线自己存的四元组**且点完闪一下那段、纯划线同样有该入口 / 窄条条件（`quoteAnswerText`）/ `openNoteFromAI` 新增与追加两条路 / 定位不到返回 false / 临时闪烁层不弹菜单 / 长批注浮层封顶内滚、无滚动条、内滚不被当成页面滚动关掉（这一组做过变异验证：改回旧行为立刻红）
- `snapshot-reader.sh`：**11 个 job**（新增 `marks-longnote` 与 `marks-longnote-scroll`）—— 真引擎里浮层 `高 508 / 可滚 2576 / client 506 / 视口 820`，内滚 200px 后 `innerScroll=open scrollTop=200`（改前这一步是 `closed scrollTop=0`，就是用户看到的「一滚浮层就没了」）、`卡内滚动条口=2`（只有描边）
- `swiftc -typecheck` macOS / iOS 均 0 error
- 真机（用户在 Xcode）跑出来的两个 bug 已修并各自钉住，记录见 `docs/bugs/016`

**与本文档的偏离（3 条）**

1. 引用胶囊与窄条落在 **`ChatView`**（输入栏上/下各一行），没给 `InputBar` 加第七个泛型槽 —— `ChatPage` 里那层就是 `ChatView`，PiP 与普通聊天自动共用，diff 小得多
2. 引用类型是 **`retrieval.DocQuoteMeta`**（不是 `agent.DocQuote`）—— `ContextPacket.Meta` 只收 `retrieval` 包内定义的类型（`PacketMeta` 是包内标记接口），放这儿省一层转换：请求体直接绑它、`RunRequest` 直接放它、块构建器直接读它
3. 前后文超长是**截断**不是 400（它只是消歧提示，报错会把整轮提问打断）；标题同理截到 200

**顺带更正 §12.4 的一句**：原稿写「切会话/关 tab 引用自然消失，**不需要额外的清理代码**」；实现时发现 `ChatViewModel` 是跨会话复用的（切会话走 `loadConversation`），所以是显式清 —— §12.4 已就地改掉。其余（不落 UserDefaults、不消费、隐藏轮不带）与文档一致。

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
   - 目录点任意一条能跳到对应标题（同名标题要跳到各自那一个）—— 这条以前是坏的
   - **P3**：划线是**文字底部的波浪线**（不是底色高亮），跨段/跨列表/跨表格行都连续、一字不少、排版不移位
   - **P3**：划一条跨段落的线（一次划过去，两段都带波浪线，中间没有大色块、也没有连线）
   - **P3**：hover 浮层里**没有**「点击编辑」文案（点划线仍出菜单）
   - **P3**：同一句话在文中出现两次，划**第二处** → 划线落在第二处（消歧靠 DOM 前后文）
   - **P3**：两条线交叠 → 悬停时 popover 同时列出两条并标「重叠划线」
   - **P3**：加批注 → 句尾小圆点出现；点圆点改批注（⌘↵ 保存 / Esc 取消）；清空批注保存 = 退化成纯划线；删除后气泡一起消失
   - **P3**：撤销/重连都不会让划线错位（每次增删改都按服务端那一份重画）
   - **P4**：顶栏开关出抽屉 → 三个 tab 计数对；点行滚到该划线并闪烁（抽屉不关）
   - **P4**：让 LLM 改写某条划线所在句子 → 抽屉「已失活」里出现它（灰 + 删除线 + 「已失活」徽标）
   - **P4**：失活行点「重新定位」→ 抽屉那行变成「请在正文中重新选中」，正文里选一段 → 行恢复常态、划线出现在新位置；Esc 能取消（行退回两个按钮）
   - **P4**：失活行点「删除」→ 行消失，且服务端也删了（重进文档不复现）
   - **P4**：抽屉开着的时候把侧栏拉窄 —— 正文被压到很难读时按设计应自动收目录栏；确认不会出现「抽屉消失/正文 0 宽」这类布局崩坏
   - **P5**：选中一段 → 工具条「问 AI」→ 聊天栏出现引用胶囊（已是 PiP 就展开 PiP，否则切回聊天 tab；布局不变）；胶囊里点引用文字能跳回文档那段
   - **P5**：点一条**已有批注的划线** → 菜单「问 AI」→ 那段闪一下 + 聊天出现同一条引用（不用重新手动选中）
   - **P5**：问一句 → 回答完成后输入栏下方出现窄条「存为批注 / 忽略」；「忽略」清引用、胶囊与窄条一起消失
   - **P5**：文档开着时点「存为批注」→ 滚到那段 + 批注浮层预填 AI 回答，改完保存，窄条即刻消失；**对同一段再问一次再存** → 追加到已有批注（原批注 + 空行 + 回答），不新建重复划线
   - **P5**：文档没开着（或开着的是另一篇）时点「存为批注」→ 直接落库 + toast「已存为批注」，刷新文档能看到
   - **P5**：hover 一条上千字的长批注 → 浮层封顶在视口内、**卡片里没有滚动条**、滚轮滚的是卡片内容而不是文档，且滚完卡片还在（不消失）
   - **P5**：切换会话 → 引用清掉（胶囊与窄条都不在）；接力那种自动轮不带引用
4. `./server/scripts/api-test-annotations.sh` 全绿（26 项，独立 SQLite 实例，不碰 dev 库）

---

## 十二、P5「问 AI」复用聊天（❌ 已整体下线，以下为历史记录）

> UI 稿：`docs/design/document-highlight-annotation/mockup-p5.html`（九种状态可切）—— ⚠️ **这份稿没有跟着实现同步**（文件顶部有醒目提示），P5 一律以下文与代码为准，差异清单见 §13
>
> 实现上的两处偏离（见 §12.5、§十三）：**不做「轻量进 PiP」**（按当前是不是 PiP 二选一，见 §12.5）、**不做气泡上方的引用条**（引用改成会话级常驻后，胶囊已经把话说清了）。

### 12.1 决策

| 编号 | 决策 | 结论 |
|---|---|---|
| P5-1 | 落点 | ✅ **复用现有聊天**。已经是 PiP（`FloatingChatPanel`）就在 PiP 里续；不是就用普通聊天栏（必要时把中间栏切回聊天）。不新画浮层、**不动任何窗口/栏布局** |
| P5-2 | 会话归属 | ✅ **真实会话**（当前 tab 的会话）。点「问 AI」不弹中间对话框：引用先暂存，问题在输入栏里敲 |
| P5-3 | 智能程度 | ✅ **完整 orchestrator** —— 能调工具（检索记忆/笔记/其他文档）。回答里会有工具气泡 |
| P5-4 | 回答→批注 | ✅ 可以。走文档侧的批注编辑浮层，**预填 AI 回答、可改完再存** |
| P5-5 | 长度控制 | ✅ **不用 `max_tokens`**（那是硬截断，且全链路无 `finish_reason` 感知，截断了系统不知道）。改为往 system prompt 注入一段**输出长度约束**（≤500 字），靠 prompt 管而不是靠截断管。接口是 `brief_answer` 布尔参数，见 §12.7。**不自动开**：沿用用户自己那个会话级开关 |
| P5-6 | 没划线就问 | ✅ 不自动建划线。点「存为批注」时才建（复用 `POST /api/documents/:id/annotations`） |
| P5-7 | 引用生命周期 | ✅ **会话级**（挂在 `ChatViewModel` 上），不是逐轮参数：只要引用还在，**每一轮都带** `doc_quote`。否则追问轮 LLM 手里就没有原文了（历史重放不带 XMLContext，§12.4 有完整论证）。点胶囊 × / 窄条「忽略」/ 切会话即消失 |

### 12.2 链路

```
文档页选中文字 → 工具条「问 AI」
  → 客户端组引用（quote + DOM 前后文 + 文档 id/标题），交给当前聊天
  → 已是 PiP → 展开它；否则把中间栏切回聊天（文档在右栏，不受影响）
  → 输入栏上方出现可删的「引用胶囊」；用户在胶囊下敲问题
  → 每轮 POST /api/chat 都带 doc_quote{document_id, title, quote, prefix?, suffix?}
  → 服务端把它做成 context packet 注入本轮 XMLContext（**仅 LLM 可见**）
  → 完整 orchestrator：检索 / 工具 / 计划 / L3 确认全在
  → 回答流式出现；回答完成且引用还在 → 底部窄条「存为批注 / 忽略」

另一种进法（已有划线/批注，不想重新选中那段）：点那条线 → 菜单「问 AI」→ 载荷就是它存着的四元组，点完那段闪一下当回执 → 后续同上（回答完成 → 窄条 → 存批注时同 quote 则追加）
  → 存为批注 → 文档滚到该段 + 批注编辑浮层预填 AI 回答（可编辑）
              → 保存时该划线还不存在就先建；已有同 quote 的批注则追加
```

### 12.3 为什么用 XMLContext 那条通道，而不是拼进用户消息

`Message.Content` 是用户原始文本，`Message.XMLContext` 是拼给 LLM 的那一大块（notice + 检索资源 + `<user_query>`），**客户端从头到尾不读 XMLContext**（全仓 grep 零命中，`model/message.go:23`）。

`ModeNotice` 就是走这条路的先例（`chat.go:197-202` → `orchestrator.go:718-720`）。所以文档引用可以**不污染用户消息正文** —— 用户敲的问题就是他的问题，引用作为 packet 只喂 LLM。这比会话接力那套「拼进正文」（`ChatViewModel.swift:866-902`）干净，也符合 AGENTS.md「动态内容注入当前 user 消息」的既有约定。

用户能看到引用，靠的是**输入栏上方的引用胶囊**（可删，见 §12.5）。**不做气泡上方的引用条** —— 见 §13 的差异说明。

### 12.4 引用挂在哪儿：一条数据的一生

核心思想：**引用不是"一次请求的参数"，而是聊天会话里的一个状态**（与草稿、精炼开关同级）。只要它还在，每轮请求都自动带上；它不进文档侧的状态，也不落任何新表。

**客户端：挂在 `ChatViewModel` 上**

| 东西 | 放哪 | 说明 |
|---|---|---|
| `docQuote: DocQuote?` | `ChatViewModel` 的 `@Published` | `DocQuote = {documentID, title, quote, prefix?, suffix?}`，新模型文件（约 10 行 Codable） |
| 引用胶囊（可删） | `ChatView` 输入栏上方一行 | 读 vm 上的 `docQuote`；点 × → `vm.docQuote = nil`；点引用文字 → 跳回文档那段 |
| 窄条（存为批注 / 忽略） | `ChatView` 输入栏下方一行 | PiP 与普通聊天都是同一个 `ChatView`（`ChatPage` 里那层），**一处实现两处生效**。书到用时才出现：引用之后又出过一轮回答且在流式中则不显示（`quoteAnswerText`） |
| 桥 | `DocumentAIBridge.shared` | 两个方向各一个口子（见下） |

**不落 UserDefaults**（与 `briefAnswer` 不同）：引用是临时上下文，重启后还挂着反而危险（文档可能已被改写），而它只有几百字，重新问一次成本极低。关 tab、无痕会话销毁时引用随之消失；**切会话要显式清**（`loadConversation` 里 `docQuote = nil`）—— `ChatViewModel` 是跨会话复用的那一个，不是每个会话一个实例（原稿写「不需要额外的清理代码」，实现时更正）。

**点「问 AI」那一刻**

```
JS(工具条 ask) → 只发 {quote, prefix, suffix}          // DOM 折叠文本 + 60 字前后文
DocumentReaderController.apply("ask")                   // 它有 documentID，init 再补一个 title
  → 组 DocQuote
  → DocumentAIBridge.shared.onAsk?(payload)             // ContentView 注册的闭包
      → vm.docQuote = payload
      → 已是 PiP：pipCollapsed = false（展开让用户看见胶囊）
        否则：router.navigateTo(.chat)（保证输入栏可见；文档在右栏不受影响）
```

文档侧**不保留任何引用副本** —— 它只负责"递出去"和"被叫回来"。

**每轮发送**

`ChatViewModel.setupSSE`（`:964`）现在从 vm 读 `briefAnswer`，同位置读 `docQuote` 传给 `SSEClient.connect(..., docQuote:)` → body 里 `if let q { body["doc_quote"] = [...] }`。

- **不消费**：发完不清空，所以追问轮照样带原文 —— 这是 P5-7 的全部理由。反过来，**把它编码进 `<user_query>` 是没用的**：历史重放整块 XMLContext 都会被丢掉（`BuildMessagesForFlash` 对历史 user 消息只发 `SimpleUserContent()` = `[时间] 原文`，`orchestrator.go:1915`），`<user_query>` 和 `<document_quote>` 在这件事上没有任何区别
- **对缓存的影响**：
  - system prompt 一个字不变 → 前缀缓存不受影响（与 `brief_answer` 那种改 system prompt 的情况本质不同）
  - 历史轮重放走 `SimpleUserContent()`，**过去那些轮的引用不会被重复发送** → 不会 N 倍放大 token
  - 唯一成本：本轮 user 消息多 ~600 字
- **隐藏轮不带**：接力那种 `hidden_round` 是系统自动发起的，带上引用没意义 → `setupSSE` 里 `hiddenRound ? nil : docQuote` 一行判断

**服务端：请求 → XMLContext**

| 层 | 改动 | 坐标 |
|---|---|---|
| 请求体 | `DocQuote *retrieval.DocQuoteMeta \`json:"doc_quote"\``，校验走 `normalizeDocQuote`：quote 非空 ≤500（复用批注常量），prefix/suffix 截断到 60（不报错）、title 截断到 200 | `chat.go:107-131` |
| RunRequest | `DocQuote *retrieval.DocQuoteMeta`，与 `ModeNotice` 同级 | `orchestrator.go:56-100` |
| 组装 | `DocQuote: req.DocQuote` | `chat.go:307-327` |
| 注入 | `extraPackets = append(extraPackets, ctxflow.DocQuotePacket(*req.DocQuote))`，就在 ModeNotice 那两行旁边 | `orchestrator.go:718-720` |
| 块定义 | `docQuoteBlock` + `DocQuotePacket()` + `blockBuilders` 注册 + `typeOrder` 里加一项（排在 notice 附近） | `ctxflow/blocks.go:27 / 209 / 439` |

**不做 document_id 的归属校验**：它只是喂给 LLM 的一段引用文本，不是权限操作（LLM 真要读文档会走 `document` 工具，那条路有自己的用户校验），省一次 DB 读。

LLM 最终看到（在 `FormatUserMessage` 里排在 `<user_query>` 之前）：

```xml
<document_quote title="N.E.K.O 记忆系统切面调研" document_id="...">
  <quote>在渲染期把反思结果直接拼进 persona</quote>
  <prefix>...</prefix><suffix>...</suffix>
</document_quote>
<user_query>那它的 pending → confirmed 两阶段值得抄吗？</user_query>
```

**落库：进了哪个字段、没进哪个字段**

- `buildUserMessage` 最后 `userMsg.XMLContext = &userContent`（`orchestrator.go:787`）→ 存进 `messages.xml_context`（mediumtext）。**引用原文随那一轮消息持久化**，可审计、可 replay
- **不动 `Content`、不动 `OriginalContent`** → 用户气泡显示的还是他敲的那句话；检索/向量/记忆/摘要这些流水线读的是原文，绝不会把"引用原文"当成用户说的话收进记忆
- **不加表、不加列、不改 migration**

**什么时候消失**

| 触发 | 结果 |
|---|---|
| 胶囊 × / 窄条「忽略」 | `vm.docQuote = nil` |
| 切会话 / 关 tab / 无痕销毁 | vm 重建 → 自然没了 |
| 文档被删、划线被删 | **不联动**（引用只是一段文本，不是外键）。故意不做：LLM 真去查发现文档不在了会自己说，不引入失效状态机 |

**存为批注那一侧怎么拿到东西**

窄条手上只有 `vm.docQuote`（四元组 + 文档 id/标题）和最后一条 assistant 文本，两个都是客户端现成的：

```
DocumentAIBridge.shared.reader                  // 文档页 onAppear 注册、onDisappear 注销（weak）
  且 reader.documentID == payload.documentID     // 防"拿着 A 的引用、人在 B 文档"
  → reader.beginNoteFromAI(quote, prefix, suffix, note: 回答截断到 2000)
      → JS: locate() → 打开预填的批注浮层 → 保存走已有的 create/update 消息
  → 否则兜底：直接 POST /api/documents/:id/annotations + toast「已存为批注」
     （省掉"自动打开那篇文档再跳过去"的接线）
```

「已有同 quote 的划线」就在 `beginNoteFromAI` 里判：命中已有批注 → 预填「原批注 + 空行 + AI 回答」，保存走 `update`（**追加**，不覆盖用户写过的字、不新建重复划线）。

**两个要写进 packet 的提醒（真会咬人）**

1. **quote 是 DOM 纯文本，不是 markdown 源文**：没有 `**`、没有 `- ` 列表标记，空白是折叠过的。块里必须说清，否则 LLM 拿它去 `document` 工具里搜会搜不到
2. **只给了这一段 + 前后 60 字**：要让 LLM 需要全文时自己用 `document` 工具按窗口读（工具已支持 `char_offset`），所以块里要带上 `document_id`

### 12.5 落在哪里

**哪个聊天栏**（用户定的：不折腾布局）

- 已经是 PiP → 就在 PiP 里续（`pipCollapsed = false` 展开它）
- 不是 → 用普通聊天栏。必要时 `router.navigateTo(.chat)` 把中间栏切回聊天；左栏宽度、右栏宽度、窗口宽度**一律不动**，文档一直待在右栏

**各元素的位置**

| 事情 | 放在哪 | 为什么 |
|---|---|---|
| 提问输入 | 当前聊天的输入栏 | 用户定的：不弹中间对话框，省一层 |
| 引用胶囊（可删） | **`ChatView` 输入栏上方一行**（不是 InputBar 加槽） | PiP 与普通聊天共用同一个 `ChatView` → 用同一个 vm 状态，**一处实现两处生效**，且不动 InputBar 那六个泛型槽 |
| 引用条（气泡上方） | ❌ 不做 | 引用会话级常驻后胶囊已经把话说清了；它还需要 per-message 映射，v1 不值得 |
| 「存为批注」 | **`ChatView` 输入栏下方窄条**（两种宿主共用） | 助手气泡刻意无右键菜单（`MessageListView.swift:207-232`），不改它；窄条只在「引用还在、引用之后又出过一轮回答、不在流式中」时出现 |
| 批注编辑 | **文档侧浮层**（P3 已有） | 批注属于文档；预填 AI 回答让用户能改 |
| 工具调用细节 | PiP 里折成一行 | 窄面板放不下完整工具卡片；点开才展开 |

### 12.6 改动清单

**服务端**（比 P1 小，全部集中在 §12.4 那张表）

| 文件 | 改动 |
|---|---|
| `controller/chat.go` | 请求体加 `doc_quote` + 限额校验（`chat.go:107-131`） |
| `agent/orchestrator.go` | `RunRequest` 加同名字段（`:56-100`）；`buildUserMessage` 注入 packet（`:718-720` 旁边） |
| `agent/ctxflow/blocks.go` | 加一个 block 类型（仿 `NoticePacket`）+ 注册进 `typeOrder` / `blockBuilders` |

**客户端**

| 文件 | 改动 |
|---|---|
| `Models/DocQuote.swift`（新） | `DocQuote` Codable（documentID / title / quote / prefix / suffix） |
| `ChatViewModel.swift` | `@Published var docQuote: DocQuote?` + `attachDocQuote` + `quoteAnswerText`（窄条条件：引用之后又出过一轮回答）；`setupSSE` 里读它（隐藏轮除外）；会话切换时清 |
| `SSEClient.swift` | `connect` 加 `docQuote` 参数，body 里拼 `doc_quote` |
| `ChatView.swift` | 输入栏上方：引用胶囊（点引用文字跳回文档、点 × 清）；输入栏下方：窄条「存为批注 / 忽略」+ `storeQuoteAsNote`（同一份代码覆盖 PiP 与普通聊天）；**存完即清引用**（窄条随之消失，交浮层那一支也一样：回答已经进了编辑框） |
| `DocumentReaderView.swift` | `ask` case 从 toast 改成组载荷走桥；控制器 `init` 多收标题、`documentID` 对外可读；新增 `revealQuote` / `beginNoteFromAI`；JS 加 `revealQuote()`、`openNoteFromAI()`、临时闪烁层 `__ai_flash`（复用现成的 `rangeAt` 做「拍平下标 → Range」）、点击菜单里多一项「问 AI」（`data-cact="ask"`，载荷用注解自己的四元组 + 调 `revealQuote` 闪一下） |
| `DocumentAIBridge.swift`（新，`#if os(macOS)`） | `onAsk`（ContentView 注册）+ `weak reader`（文档页 onAppear 注册 / onDisappear 注销）；存为批注 → 预填浮层，文档不在则直接 POST |
| `ContentView.swift` | 只加注册 `onAsk` 的那几行（**不再需要**「轻量进 PiP」那套） |
| `DocumentListView.swift` | `DocumentFullView` 构造 reader 时多传标题；正文那层挂 `onAppear/onDisappear` 注册、注销 reader |
| `Tools/webfetch`… 无 | — |

**不改**：`FloatingChatPanel` 的数据来源。它取 `chatTabManager.currentTab`（`ContentView.swift:540`）—— 两种去向都落在 currentTab。

**已实现（与本文档的两处偏差）**

1. **胶囊与窄条落在 `ChatView`，没给 `InputBar` 加第七个泛型槽**：`ChatPage`（两种宿主共用）里那层就是 `ChatView`，把两行 UI 放它上/下，PiP 与普通聊天自动都有 —— 比改 InputBar 那六个槽 + 两个 init 的 diff 小得多。
2. **引用类型放在 `retrieval`（`DocQuoteMeta`），不是 `agent.DocQuote`**：`ContextPacket.Meta` 只收 `retrieval` 包内定义的类型（`PacketMeta` 是包内标记接口），所以类型定义在那儿最省一层转换 —— 请求体直接绑它、RunRequest 直接放它、块构建器直接读它。
3. 细节：前后文超长是**截断**不是 400（它只是消歧提示，报错会把整轮提问打断）；点回文档靠**点胶囊里的引用文字**（不是"引用条"）；闪一下用临时 `mark`（1.1s 后 `unwrapAll + redrawAnnotations` 拆掉），临时层没有对应批注，右键菜单会跳过它。

> **⚠️ 以下两段描述的 hover popover 已于 2026-10-06 整体删除，仅作历史记录。**

**长批注的 hover 浮层（P5 之后才暴露）**：AI 的长回答存成批注后，hover 浮层动辄上千字。浮层是 `position: fixed` 且原本没有 `max-height` —— 超出视口那截永远拿不到，滚轮还会冒泡去滚正文。现在封顶 `min(62vh, 520px)` + 内滚 + `overscroll-behavior: contain`（防连滚），宽度放宽到 `min(420px, 88vw)`（少滚），`.p-note` 加 `overflow-wrap: anywhere`（长 URL 不横向溢出）。`showPopover()` 的上下翻转逻辑没动（高度被 cap 之后本来就够用）。

**还有个坑（真机复现）**：`window` 上那个 capture 阶段的 scroll 监听（`hideAll(); hidePopover()`）**连后代元素的滚动都收得到**（`scroll` 不冒泡，但 capture 会命中，`e.target` 就是被滚的元素）。浮层自己是滚动容器 → 一滚浮层就被它关掉，紧接着滚轮落到正文上，用户看到的是「浮层滚不动、只有文档在滚、长批注还是读不完」。现在监听里加一句 `if (popover.contains(e.target)) return;`（浮层内滚不算页面滚动；页面滚动照旧关浮层，P3 语义不变），并把 `hoverKey` 的复位收进 `hidePopover()` —— 否则任何一条隐藏路径都可能留下卡住的 `hoverKey`，让同一处再也 hover 不出浮层。`overscroll-behavior: contain` 在这里才真正派上用场：滚到浮层底部不把滚动串给正文，也就不会触发页面 scroll 把浮层关掉。 另外按用户要求**卡片里不显示滚动条**（`scrollbar-width: none` + `.popover::-webkit-scrollbar { width: 0 }`；内容照样能滚，与 macOS 覆盖式滚动条的观感一致）。

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

**与 `doc_quote` 的区别（别看串了）**：两者都是"会话级、每轮随请求发"，但理由和位置都不同 —— `brief_answer` 是因为**放 system prompt** 才必须会话级稳定（否则前缀缓存来回丢）；`doc_quote` 是因为**追问也要带得到原文**（历史重放不带 XMLContext），而它在 user 消息里，本来就不影响前缀缓存。所以 `doc_quote` 不需要按会话存 UserDefaults，重新问一次的成本只是几百字。

**客户端 UI（已实现）**：

输入栏里一颗 icon pill，与「深度模型 / 信任 / 接力」同族，**默认收在「更多操作」行**（`InputBarButtonID.defaultCollapsed`）。未选中 `text.alignleft` + appAccent，选中 `text.badge.minus` + orange。mockup 里那三个「醒目程度」候选（不显示 / 一行小字 / 可点 chip）**已弃用** —— 直接做成按钮更符合输入栏既有的语言，也不用新造视觉。

**有个坑**：`InputBarLayoutState` 的折叠配置是持久化的，老用户存下来的集合里没有新 id → 直接 `Set(stored)` 会把它当"未收起"，显示在常驻行，与"默认收起"相反。`init` 里加了一次性补入（`InputBarLayoutState.swift:22-26`）。

**另一个不要再动的地方**：`/api/chat` 请求体里的 `temperature` / `max_tokens` / `thinking` 解析了但**有意不传进 `RunRequest`**（`chat.go:118-120` vs `chat.go:307-327`）—— 服务端是 LLM 参数的唯一事实来源。这不是 bug，别"修"。

### 12.8 还没定的

- **「新建会话 / 在当前会话继续」去哪了**：用户后来拍了「直接聊，省点事儿」，所以中间对话框砍掉了，**默认在当前会话继续**。想要新会话就走会话列表自己切/建。mockup 里没画这条分支 —— 如果实际用起来发现"我想对同一段文字另起炉灶"是常见诉求，再加
- ~~引用条要不要持久化~~ —— **作废**：引用条不做了（§13 不做）
- ~~PiP 轻量进入的边界~~ —— **作废**：不引入「轻量进 PiP」这个概念，改为按现状二选一（§12.5）
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
- ❌ **气泡上方的引用条**（P5）—— 引用改成会话级常驻后，输入栏上方的胶囊已经把"在聊哪一段"说清了；引用条还要额外做 per-message 映射，v1 不值当
- ❌ **「存为批注」的撤销**（P5）—— mockup ⑥ 有，但只有"新建"那一支能干净回滚（"追加到已有批注"要还原原 note），v1 只 toast「已存为批注」

### 待定

- ⏳ 重定位阈值（`0.5` / `×3` / `+200`）是否要暴露成配置 —— 现在写死，等真的误挂或误失活再说
- ⏳ 目录栏是否也搬进 WebView（现在保留原生，若以后要做"滚动时跟随高亮当前小节"，原生侧需要 WebView 回传 scroll 位置）
- ⏳ `quote_rewritten` 在抽屉里怎么表现（挂"原文已改写"徽标？还是只做排序/筛选用）
- ⏳ 「快照刷新会不会让界面闪」—— 已按"服务端刷新 + D19 不自动刷新"实现，视觉零闪；等真机跑完看现象再定要不要调
- ⏳ P5 引用跨重启要不要留（现在不落 UserDefaults，重启即失）—— 等真机用起来看是不是"一天内会重复问同一段"

### 已知的预览与实现差异

- mockup 顶栏把「窗口顶栏的分享/导出/下载」和「文档详情页顶栏的返回/查找」画在了同一行 —— 实现按 D20 分成两处
- mockup 里的 `marked` 渲染是**手写的静态 HTML**（不是真跑 marked.js），所以排版细节（尤其表格边框、行内代码底色）以实现出来的为准，配色 token 以 mockup 为准
- mockup 画划线用的是 `extractContents()` + `insertNode()` 搬 DOM（Chrome 下正常），**实现里必须改成逐文本节点包** —— 见 §8.2 那条禁令，真机因此丢过字
- mockup 的划线样式可以切「波浪线 / 底色高亮(旧)」，**实现里只有波浪线**（底色高亮是被用户否掉的那版）
- mockup P5 里引用胶囊只活在"发送前"那一态，实现改成**会话级常驻**（可删）—— 追问才带得到原文，见 §12.4
- mockup P5 画的是「直接进 PiP」，实现按当前是否已是 PiP 二选一（**不做轻量进 PiP**），原因见 §12.5
- mockup P5 里助手回答上方那条引用条、以及窄条上的「撤销」，实现都不做（见上「不做」）
- **`mockup-p5.html` 没有再按实现同步**（文件顶部挂了一条醒目提示）：它画的是"发送前胶囊 + 直接进 PiP + 有引用条/撤销"那一版，实现是"会话级常驻胶囊 + 按现状二选一 + 都没有 + 长批注浮层内滚"。**对 P5 一律以本文与代码为准**；这份预览只用来回看当时讨论过的形态。

---

## 后续变更

### 2026-10-06 · 点划线定位到右侧批注面板（左键定位 / 右键菜单）

- **背景**：原先左键点划线只弹「编辑/复制/删除」菜单，正文划线 → 右侧批注条目这个方向不可达（反方向「卡片→正文」早有）。
- **决定**：**左键点划线/气泡 = 快捷定位到右侧面板对应条目**（滚动 + 闪烁，面板收起则先展开，不切当前视图）；**右键 = 原「编辑批注 / 复制原文 / 删除划线」菜单**（拦截 `contextmenu`）。左键拖动选词不触发定位。
- **数据流**：新增 JS→Swift 消息 `focus {id}`；`DocumentReaderController.focusRequest`（含单调递增 `tick`，连点同一条也能重触发）→ `AnnotationPanel` 观察后 `ScrollViewReader.scrollTo` + 本地 `flashID` 闪烁 ~1.2s。（当时列表分支 `AnnotationListBody` 也接了同一套；该视图已于同日「合并为单视图」删除。）
- **影响文件**：`DocumentReaderView.swift`（JS 模板 + controller）、`DocumentListView.swift`（收起则展开 + 透传）、`AnnotationDrawer.swift`（`ScrollViewReader`、行 `.id`、聚焦描边；新增 `AnnotationFocus`）、`check-reader-html.sh`。
- **不改**：服务端 / 数据模型 / API。
- **边界**：焦点条目被当前视图过滤掉 → 静默不滚；窗宽硬地板下自动展开可能仍只成窄条；orphan 划线正文里没画，仍走面板右键菜单。
- **详见**：`docs/design/document-annotation-focus/design.md`。

### 2026-10-06 · 批注面板合并为单视图（去掉「全部」列表）

- **背景**：右侧面板原有「讨论（卡片 + 线程）/ 全部（扁平列表 + 三 tab）」两个视图，本质都是同一份 `annotations` 的列表。用户要只留一个。
- **决定**：保留**讨论**（承载 AI 回复/追问，核心），删「全部」视图与两个切换按钮及三 tab 筛选；「全部」唯一不可替代的能力 —— 失活条目的**操作** —— **并入讨论卡片的失活条目**（正文里不画失活划线，这是唯一入口）。删 `@AppStorage("xhan_doc_annot_mode")`。
- **不变**：点划线定位（`focusRequest` / 闪烁）仍由讨论卡片承接；数据源、服务端 **零改动**。
- **影响文件**：`AnnotationDrawer.swift`（删 `AnnotationPanelMode` / `AnnotationListBody`；`AnnotationThreadCard` 加失活动作行）、`DocumentListView.swift`（删 mode 绑定与 AppStorage）、`DocumentReaderView.swift`（仅注释措辞）。
- **详见**：`docs/design/document-annotation-single-view/design.md`。

### 2026-10-06 · 取消手动「重新定位」（失活条目只留「删除」）

- **决定**：手动重挂没啥用，删掉「重新定位」按钮与整条客户端链路；失活条目此后**只能删除**。
- **删除**：`AnnotationDrawer.swift`（卡片/面板 `relocatingID`·`onRelocate`·`onCancelRelocate` 与重选提示）、`DocumentReaderView.swift`（`relocatingID` / `beginRelocate` / `cancelRelocate` / `relocate` / `case "relocate"`·`"relocateCancel"` / JS relocate 模式与导出）、`APIClient.relocateAnnotation`、`DocumentListView` 装配、`check-reader-html.sh` 8 条重定位断言（176→168）。
- **保留**：D10 的**自动**三级重定位（正文改写后服务端重挂，与本手动流程无关）；服务端 `POST /api/annotations/:id/relocate` 接口保留（不再被客户端调用）。
- **详见**：`docs/design/document-annotation-single-view/design.md` 的后续变更。

### 2026-10-06 · 右键不再叠出「选区工具条」

- 右键划线曾同时弹出选区工具条与右键菜单：`mouseup` 未判鼠标键，而 WebKit 右键会先自动选中光标下的词。修复：`mouseup` 只认左键并清掉右键的自动选区，`contextmenu` 先 `hideToolbar()`。
- 详见 `docs/design/document-annotation-focus/design.md` 的「2026-10-06 · 右键不再叠出选区工具条」；`check-reader-html.sh` 168→171。

### 2026-10-06 · 删除 hover popover（用户拍板）

- **决定**：正文 hover 划线不再弹浮层（原「显示 note + 交叠列全部 + 重叠划线标签 + 长批注封顶内滚」整块删）；`.ann-bubble` 圆点的 `title="查看批注"` 原生 tooltip 也去掉。note 只在右侧批注面板看。**重叠划线失去可见提示**（用户已确认可接受）。D8、§7.4、§8.3 已就地更新；§12.6「长批注的 hover 浮层」两段标注作废。
- **删除**（`DocumentReaderView.swift`）：`#popover` 元素、`.popover`/`.p-quote`/`.p-note`/`.p-foot`/`.p-item`/`.p-tag` 样式、`content.mousemove`/`mouseleave` 监听、`showPopover`/`hidePopover`/`scheduleHidePopover` 及仅供它用的 `esc`/`timeStr`、各处 `hidePopover()` 调用、scroll 监听的 `popover.contains` 守卫、`b.title`。
- **保留**：`mark.hl:hover` 线色变实。
- **测试**：`check-reader-html.sh` 171→159；`snapshot-reader.sh`/`.swift` 删 `marks-longnote`、`marks-longnote-scroll` 两 job 与探针 `popover` 字段。
- **详见**：`docs/design/document-annotation-focus/design.md` 的「2026-10-06 · 删除 hover popover」。

### 2026-10-12 · 去掉文档换篇快捷键与目录栏上/下篇按钮（用户拍板）

- **背景**：§298/§313 的 `tocSidebar` 页脚原有「上一篇 / 下一篇」方向行，另有 ⌘←/⌘→ 快捷键载体（`shortcutCarrier`）。
- **决定**：两条换篇路径**全删**（快捷键、页脚方向行）；「文档阅读」整节从设置页快捷键说明里去掉。切文档只剩「返回文档列表 → 再点开」。
- **影响文件**：`DocumentListView.swift`（删 `shortcutCarrier` / `TOCNavRow` / 页脚行，连带删 `DocumentFullView` 的 `canGoPrevious`/`canGoNext`/`isLoadingNeighbor`/`onNavigate` 与 `DocumentViewModel` 的 `canGoPrevious`/`canGoNext`/`openNeighbor`/`neighbor`）、`ShortcutGuideView.swift`、`ChatInputFocus.swift`（仅注释）。
- **不变**：服务端 / API / 模型 **零改动**。
- **后续**：同日的「侧栏双模式」把换篇能力搬进了侧栏，见下一条。

### 2026-10-12 · 侧栏改双模式：目录 / 列表（用户拍板）

- **决定**：`tocSidebar` 头部加节名菜单「本文目录 / 文档列表 · N 篇」；列表 = 同一份筛选条件下的 `DocumentViewModel.documents`（筛选状态本就存在 VM，无需重新查询）；`@AppStorage("xhan_doc_sidebar_mode")` 持久化。macOS 侧栏改为**常驻**（去掉 `!tocItems.isEmpty`），目录空时给「本文档没有标题」空态。iOS 不动。
- **影响文件**：`DocumentListView.swift`（`DocSidebarMode`、`tocSidebar` 拆分、`tocOutline`/`documentNavList`）、`DocumentFullView` 加 `@ObservedObject viewModel`。
- **后续**：同日分两轮改版（分段控件 → 单行菜单；纯标题行 → 两行带时间·标签），以 `docs/design/document-sidebar-modes/design.md` 的 SM-3/SM-7 与文末后续变更为准。
- **详见**：`docs/design/document-sidebar-modes/design.md`（本文档 §298/§313 的 `tocSidebar (220)` 仍在，只是内部多了模式）。
