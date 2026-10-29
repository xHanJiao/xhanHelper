# HTML 产物（聊天场景）

> 日期：2026-10-29
> 状态：**已实现**（2026-10-29，`feat/html-artifact`），代码审查见 `docs/code-review/html-artifact/2026-10-29.md`
> 来源：2026-10-29 讨论纪要（`~/Desktop/HTML产物展示机制-讨论纪要-20261029.md`）
> 前情：已经用模型自由生成验证过效果（四版手账风格），卡在落地——目前 HTML 只是输出到本地目录，demo 可用，不能实际使用。

## 一、要解决什么

聊天里的展示形式太死板（markdown 正文 + 有限的几种卡片），而模型直接写 HTML 能把版面做得很活。这条路的价值就在"每次自由生成"，所以不引入任何内容协议、不上固定模板。

**目标是"任何内容都能被排得更易读"，不是某个特定品类。** 手账只是最早的验证案例；多列对比、时间线、一页纸总结、图文混排、仪表盘式汇总都算同一件事。

一条判断标准贯穿全文：**排版本身承载信息时才做页面**。文字本身就是内容的，留在 markdown 里（细则见 §8.1）。

范围：**只做聊天场景**，文档场景暂缓。

## 二、不做什么

| 不做 | 原因 |
|---|---|
| A2UI 类展现协议 | 要复用的是渲染容器，不是内容协议 |
| 固定模板 | 会把"视觉自由生成"这个核心优势废掉。风格用 prompt 约束 |
| 强制数据槽位 / facts 结构化 | 搜索从 HTML 抽文本、跨篇读快照、复现靠快照，三件事都不需要槽位 |
| nginx 静态目录托管 | 浏览器导航带不了 Bearer，只能退化成 cookie 会话或"URL 即凭证"；且改了静态目录，DB 就不再是唯一源 |
| 消息卡片里嵌 WKWebView | 一嵌就把高度回传、滚动冲突、实例池全搬回来。**渲染放右栏，不放消息流** |
| 临时文件 + 默认浏览器 | v1 不做。去掉之后没有"唯一会真执行 JS 的路径"、没有两份渲染的偏差、也没有 iOS 上 `NSWorkspace` 缺失的问题 |

## 三、结论先行

1. **正文存 DB**，一条记录一个字段，跟着会话走。删除、备份、导出、换设备都不用额外处理。
2. **聊天里只是一张引用卡**：标题 + 一行摘要 + 整卡可点，没有"打开"按钮。
3. **点开在右栏新开一个 tab 用 WKWebView 渲染**，JS 关掉。不落文件、不交给外部浏览器。

## 四、存储

```go
// internal/model/artifact.go
type Artifact struct {
    ID             string  // UUID
    UserID         string
    ConversationID string // 必须挂：无痕会话销毁要连带清
    Title          string
    Summary        string
    HTML           string  // LONGTEXT，json:"-"（任何列表都带不出去）
    CreatedAt      time.Time
    DeletedAt      gorm.DeletedAt
}
```

- 资源（图片）base64 内联进 HTML，做成**自包含单文件**（细则见 §4.3）。代价体积涨约 1/3；视频扛不住，v1 不收。
- 消息表**不存正文**，只存引用（见 §五）。

### 4.1 写入 / 读取的 IO 口径

一页十几到三十 KB，一个会话撑死几百 KB——**这个量级不是 IO 风险**。真正要守的是下面四条，别为了省一次 INSERT 引入异步。

| 规则 | 原因 |
|---|---|
| **同步写**，不做异步队列 | 工具返回值必须带 id，模型下一轮要用、客户端可能立刻点开。异步会开一个"返回了 id 但行没落地"的窗口，用户点得快就是 404。要兜住得预生成 id + 进程内 pending map，为省一次单行 INSERT 不值。**`request_json` 那个异步先例不适用**：它是调试审计数据、且在整条消息行落库之后才提交，落后无所谓 |
| **消息列表查询必须 `Omit("html")`**（或换不含正文的 DTO） | GORM 默认 `SELECT *`。一旦列表把正文带出来，每次打开会话就是 N 条 × 几十 KB 的持续性开销。这是本方案最容易被写错、且后果最严重的一处 |
| 正文只在 `GET /api/artifacts/:id` 取 | 同上，唯一的正文出口 |
| 不建全文索引 / 不做 embedding | 纪要里"不强约束槽位"的前提：搜索走别处，不碰 `html` |

无痕销毁时**软删阶段只改 `deleted_at` 一列**，LONGTEXT（存在溢出页）不会被重写，几乎零 IO；物理删在 Stage B 随批进行。

### 4.2 容量与去重

| 限制 | 值 | 理由 |
|---|---|---|
| 每轮最多 | 3 张 | 防模型循环/重试刷屏；同时覆盖"给我三种风格"这种合理请求 |
| 单条大小 | 模型 HTML ≤2MB；内联图片后 ≤8MB | 挡误存；图片单独限（§4.3） |
| 每会话总量 | 不设硬上限 | 一天两页 ≈ 一年 14MB，且列表不取正文，日常零开销；只影响备份体积。带图页面按图计（一张原图 base64 约 ×1.33） |

**不做内容去重。** 自由生成的 HTML 几乎不可能字节重复，按正文哈希等于写一段永不执行的分支；重试导致的重复由"每轮 ≤3"兜住。真正的重复问题属于修订闭环（v2），那时用户明确说"改这一版"，模型带 `artifact_id` 进来，是不是新版本是确定的语义，不用猜。

**永远不做"同一 id 覆盖正文"。** 快照类内容被改写会让历史消息失去历史性——翻回三天前那条消息，看到的却是今天的版式。要做修订只能新建记录（v2 用 `supersedes_id` 串起来，渲染时只显示链条末端）。

### 4.3 图片：生成时内联，与源文件解耦

模型**不写 base64**（参数太长，极易写崩），只写引用：

```html
<img src="image:<ImageAsset.ID>" alt="...">
```

服务端在清洗之前解析这个 scheme：按 `id + userID` 查 `image_assets`、读磁盘文件、转成 `data:image/...;base64,...` 写回 `src`。清洗随后照常跑，`safeURL` 仍然只放行 `#anchor` 与 `data:image/`。

**为什么必须内联而不是"引用 id + 渲染时取"**：图片有保留期（被消息引用过 15 天、孤儿 3 天，`storage.image_ttl_days` / `image_orphan_ttl_days`）。内联发生在生成那一刻，页面从此是字节副本，**源图之后被清掉也不影响已生成的页面**。引用型方案则要额外给图片做 pin（新列 + 清理豁免 + 解除时机），页面也不再自包含。

规则：

| 项 | 值 | 说明 |
|---|---|---|
| 模型给的 HTML | ≤ 2MB | 内联前，挡误存 |
| 单张图原字节 | ≤ 4MB | 超过报错，不做压缩（压缩要引 x/image + 处理 EXIF 方向，v1 不值得） |
| 单页内联总量 | ≤ 8MB | base64 后；超了报错，提示少放几张 |
| 单页张数 | ≤ 12 | 真正兜体积的是 8MB；这条只避免"为一张页读几十个文件" |
| 支持的写法 | 只 `img[src="image:<id>"]` | CSS `background-image: url(image:...)` **命中即报错**（不能指望 `cleanCSS` 静默换成 `none`），见下 |
| 来源 | 该用户自己的任意 `ImageAsset` | 文档配图（public）也能用；owner 不匹配按"不存在"处理 |

失败口径：**任一引用解析失败（已过期 / 不存在 / 越权 / 超过 4MB / 超过张数或总量 / 写成了 CSS 背景图）→ 整次 `html.create` 失败**，错误里点名是哪个 id。页面不入库、不扣单轮配额。理由：用户明确要求"把这张图放进去"，静默少一张比报错更糟。

内联成功后对这些 id 调 `MarkReferenced`，把源图保留期续 15 天——便于同一会话隔几天再修订一版；**不改全局 TTL、不加豁免**。页面本身不依赖这一步。

模型怎么发现图片：`image.list_images` 增加 `scope`（`turn` = 本轮附件、`all` = 全库分页），结果里直接给 `<img src="image:<id>">` 片段。本轮附件的 id 由 orchestrator 通过 `ExecuteContext.UserImageIDs` 带进工具。

## 五、卡片：数据源跟消息走，不跟设备走

仓库里已有一套「工具结果 → 特殊展示」的管线（`docs/design/local-artifacts/`），但它存的是**设备本地**的胶囊数据，跨设备/Web 都看不到。这套 HTML 产物的正文既然在 DB，卡片的**数据源就该跟消息走**。

- 助手消息加一个 `artifacts_json` 字段（沿用 `tool_calls_json` 的写法），存 `[{"id": "...", "type": "html"}]`，**只存 id 和类型**，标题/摘要不冗余。
- 消息列表接口返回时，按本页消息里的 artifact id **一次 `IN` 查询**批量展开成 `{id, title, summary}`（不要逐条查）。
- 客户端只按消息字段渲染卡片，位置天然就在这条回复里，不需要 `LocalArtifactMerge.reposition` 那套"按位置找最近的 assistant"的判定。

**渲染照抄现成观感**：`DocumentRefCard` 就是目标形态（标题 13.5 semibold + 摘要 11.5 muted 最多 3 行 + 整卡可点、无外框、240×92），宽度/高度/内边距取 `ArtifactCardMetrics`，别另写一套常量。新写一个 `HTMLArtifactCard`，行为从"打开文档面板"换成"右栏开 tab"。

竖向叠、不进横向卡片条。一条回复产出多张就先叠着，等真出现"一次五张"再谈分组。

**`summary` 的分量比看起来重**：它是用户在聊天流里唯一的判断依据，决定要不要点开。要求是一句话说清"这一页讲了什么"，不许"以下是对比""见下图"这种废话（约束写进工具描述，见 §8.3）。

## 六、生成 + 展示时序

```
模型调 html 工具
  → 服务端清洗 + 校验（§七）
      ├─ 不合格 → 工具返回错误文本，不落库，模型自己重写
      └─ 合格   → 建 artifacts 记录 → tool_result 返回 {artifact_id, title, summary}
  → SSE tool_result 带这三个字段，客户端立刻插一张卡（先于正文，位置同现有工具结果）
  → 本轮结束时，assistant 消息落库，把本轮成功产出的 id 写进 artifacts_json
  → SSE done 后消息被真实消息替换：卡片按 artifact_id 幂等去重，只渲染一张
  → 用户点卡 → GET /api/artifacts/:id → 右栏 openTab（§九）
```

去重是必须的：SSE 期插的那张和 done 后消息字段里带的是同一张，按 `artifact_id` 判等即可。

## 七、入库前：清洗 + 校验

### 7.1 清洗

因为渲染侧会关掉 JS，清洁标准可以降一档，但**不能降到零**：

| 威胁 | 是否还成立 | 处理 |
|---|---|---|
| `<script>`、`on*`、`javascript:` | 关 JS 后失效 | 顺手删掉（一行，留着也是脏数据） |
| `<iframe>` / `<object>` / `<embed>` | **仍然有效**，file:// 同源很松，能框本机文件进页面 | **必须删** |
| 外链 `<img>` / `<link>` / CSS `url()` | 仍然发请求（泄漏 IP、引荐） | 删 + 靠 CSP 兜底 |
| `<img src="image:<id>">` | 是本站私有图床引用，页面本身取不到 | 清洗前换成 `data:` URI（§4.3）；换不成直接失败 |
| `<meta http-equiv="refresh">` | 仍然会跳转 | 删 |

所以入库时做五件事：先把 `image:` 引用内联成 `data:` URI（§4.3）、删 `script/iframe/object/embed/link/meta`、删 `on*` 属性与 `javascript:`、剥外链 `src`/`href`、在 `<head>` 注入

```html
<meta http-equiv="Content-Security-Policy"
      content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">
```

**两道闸，都不省**：清洗挡的是"没有 JS 也会发生"的事（iframe、外链请求）；`javaScriptEnabled = false` 挡的是"清洗漏了"的情况。没有浏览器兜底之后，安全口径只剩这一套，不再有两份标准要维护。

### 7.2 校验（比清洗更容易出事）

项目全链路没有 `finish_reason == "length"` 处理，`max_tokens` 触顶是**静默硬截断**，而一页 HTML 十几 KB 正好在这个区间。入库前必须判：

- HTML 能被解析（不要求严格 XHTML，至少标签栈能配上）
- 末尾有 `</html>`（或明显的完整收尾）
- 长度过下限（挡空壳）

不合格 → 返回错误给模型重写，**绝不把半张 HTML 存进库**。否则症状是"打开只有上半页"，而且没人知道是截断。

清洗和校验都是纯函数，走单测。

## 八、工具 v1

名字：`html`。三个参数：`title` / `summary` / `html`。

### 8.1 什么时候该用（工具描述里必须写死）

这是整个功能最大的风险点：触发太宽，聊天流会被卡片塞满，而且每张都要点开才知道是什么。

| 用 | 不用 |
|---|---|
| 排版本身承载信息：多列对比、时间线、一页纸总结、图文混排、仪表盘式汇总 | 纯文字、代码、短列表、单张表、需要复制内容的时候（HTML 里的字复制出来体验差） |
| 内容量大、需要独立滚动阅读 | 内容少于 200 字——markdown 就够 |

- 描述里**反例比正例管用**：明确写"问天气、列清单、解释概念 → 直接用 markdown"。
- 拿不准时不用。宁可少生成一张，不要多生成一张没人点的卡。

### 8.2 正文不要复述页面内容

调完工具后，正文只写页面里没有的东西：结论、推荐、下一步。

```
[卡片：三方案对比 / 按成本、维护性、迁移难度横向对比]

结论是方案 B：成本只高一点，但迁移最难的那部分它已经解决了。
```

而不是把同一份对比再用 markdown 列一遍。三层各管什么：

| 层 | 内容 | 谁写 |
|---|---|---|
| 正文 | 结论、推荐、下一步 | 模型自由写 |
| 卡片 | 一句摘要（"这一页讲了什么"） | 工具的 `summary` |
| 页面 | 全部内容本身 | 工具的 `html` |

规则写成工具描述里的一句话即可。做不到也不会坏，只是啰嗦——**不要为此写代码去比对/截断正文**，容易误伤。

### 8.3 summary 怎么写

一句话说清这一页讲了什么，作为用户决定"点不点"的唯一依据。禁止"以下是对比""详见下图""为你整理了…"这类零信息表述。

### 8.4 风格与宽度约束

固定在工具描述里（字节稳定，不破坏 DeepSeek 前缀缓存）：

- 观感：留白、纸感、配色，给一两个正例片段的文字描述（手账只是其中一种，不写死风格）
- **按 760px 宽设计、不做横向滚动**——模型不知道容器多宽（右栏默认 840），不写这条每张都要手动缩放

通用版式（字号下限、行高、最大宽度、底色）不靠模型，由客户端加载时注入基线 CSS 保证，见 §9.3。

### 8.5 其他

- 描述里写一句"一次只生成一张，需要多张时先问用户"——比硬上限更有效，且不误伤。
- 每轮 ≤3 张；模型 HTML ≤2MB，内联图片后 ≤8MB，单图 ≤4MB（§4.2 / §4.3）。
- 描述里写清配图写法（`<img src="image:<id>">`），并明确"不要自己写 base64"。
- 模型能稳定输出十几 KB，真遇到截断再改成"传文件路径"。
- 只读声明：无（写库）。
- 失败即失败，不静默降级。

## 九、打开：右栏 tab + WKWebView

不用新造容器，右栏已有 tab 基建（`RightSidebarTab` + `RightSidebarManager.openTab`）：

```swift
rightSidebar.openTab(
    id: "artifact:\(artifact.id)",     // 以 id 为键 → 再点同一张是切回去，不开第二份
    title: artifact.title,
    icon: "doc.richtext",
    content: AnyView(HTMLArtifactView(html: artifact.html))
)
```

### 9.1 为什么是右栏而不是消息流 / 外部浏览器

- 消息流里嵌 WKWebView 才会有高度回传、滚动冲突那堆问题；右栏是独立滚动容器，天然没有。
- **不挤占聊天区**：页在右栏，聊天流里只有一张卡，滚动互不干扰。
- 宽度够：右栏默认 840、上限 1200，可拖。页面按 728–800 排正合适。
- **和文档面板的关系**：文档走 browse panel（`browsePanelSelection = .documents`），artifact 走 tab。两者能各占一处、切来切去，但共享右栏这一块地方。
- 不落文件 → 没有 Caches 被清、没有 `file://` 进浏览器历史、没有"每次覆盖写"。
- iOS 不再是问题：没有 `NSWorkspace` 也无所谓，同一个 WKWebView 组件，iOS 用 sheet 代替 tab。

**接入位置定为 `tabs` 池**（不再单开一个右栏模式）：`openTab` 一行就能拿到 tab 条、关闭、返回、切回已有 tab 这些配套，而独立字段/独立模式得自己把这些重画一遍。代价是两个已知毛病，各补一处守卫即可：

- `BrowserToolProvider.performCloseAll` 里 `activeTabId = nil` → **经实现时核对不用改**：那是 provider 自己的字段，不是 `rs.activeTabId`；`RightSidebarManager.closeTab` 关掉网页 tab 时会保留仍存在的 tab（含产物页），所以产物不会被藏掉。
- 遍历 `manager.tabs` 的浏览器语义（进浏览器、关页回落、保活、右键菜单、拖分屏）按 `id.hasPrefix("artifact:")` 过滤掉这一类。全仓此类引用约 11 处，集中在 `RightSidebarView`；实现时收紧成三处真正会踩到的：`enterBrowser`、`performCloseScoped` / `performCloseTab` 的回落、`demoteAgentTab` 的回落。

**不选"新增浏览面板的一个 case"**：`BrowsePanel` 是 `CaseIterable` 驱动的网格入口（账单/记忆/文档…），加一个 case 会在侧栏首页多出一个永久入口"HTML 页面"——而我们要的是"看当前这一页"，不是"一个内容库"。

### 9.2 WKWebView 配置

```swift
prefs.javaScriptEnabled = false              // 引擎层面禁 JS，清洗漏了也不执行
prefs.javaScriptCanOpenWindowsAutomatically = false
webView.navigationDelegate = self             // 非 about:blank 导航一律 cancel
webView.loadHTMLString(html, baseURL: nil)    // 页面自包含，不需要 baseURL
```

内存上有三条硬规矩（右栏已有"非激活 tab 摘除视图层级"的机制，照它走就够）：

- **不进 `keptAliveTabIds`**。那个名单是给视频/音频续播的。手账这类页面切走就销毁，切回来重新 `loadHTMLString`，无 JS 无网络，重载代价可以忽略。常驻的代价是每篇一个 WebContent 进程（几十 MB 量级）。
- **WKWebView 用 `@StateObject` 持有**，别塞进 `RightSidebarTab.content` 的构造参数——后者会让实例跟着 tab 结构体一直活着。
- **不要全局缓存 HTML 正文**。要缓存就放 tab/选择项里（几百 KB 字符串可接受），别做"最近 N 篇"的 map。

其他：

- 底下**垫一层不透明底**，别让页面直接压在照片底图上（见 `docs/bugs/030` 那类灰闪，文档读者已在右栏跑 WKWebView，照它的做法）。
- 加载失败要有错误态（别白屏），走 `LogBuffer.error` + Toast。

### 9.3 版式基线 CSS（客户端加载时注入）

模型不一定每次都写到"好读"，所以加载时在模型自带的 `<style>` **之前**注入一小段基线（DOM 顺序在前，模型总能覆盖）：

- **正文列交给顶层容器**，`body` 只管背景：`body > *{max-width:min(760px,100%);margin-left:auto;margin-right:auto}`。
  不能把 `max-width` 写在 `body` 上——模型普遍会写 `body{margin:0}` 顶掉居中；body 被限宽还会让模型的桌面底色只铺 760，侧栏一宽就露出白边（2026-10-29 实测两页都是这样）。
- `min(760px, 100%)`：侧栏窄于 760 时让写死 `width:720px` 的页面收缩，而不是被 `overflow-x:hidden` 裁掉。
- `line-height: 1.6`、正文最小 15px（挡 12px 密密麻麻的表格）
- 表格 `max-width: 100%` + `overflow-x: hidden`（挡横向滚动）
- 页面自带不透明底色，**不跟随 app 主题**（不透明是为了不直接压在照片底图上；页内配色由模型决定）
- 系统字体栈 `-apple-system, "PingFang SC"`

**注入位置在客户端（`HTMLArtifactView`），不写进库**：基线是表现层，写进库就没法回溯——改一次版式要把所有历史页面重新生成。存储的 HTML 只有清洗结果 + CSP（服务端契约），基线随 app 版本走。

这是整个方案里性价比最高的十几行：内容千变，基线不变。

### 9.4 不做"在浏览器打开"

v1 不提供这个入口。省下的不只是几行代码：清洗标准只剩一套、没有两份渲染的偏差、没有抢焦点/切 Space、iOS 不用再研究 `SFSafariViewController` 能不能开 `file://`。

真需要的两个场景各有替代：调样式在 Xcode 的 WKWebView 里看，存档/发人用 `WKWebView.createPDF`（后续再加）。

## 十、Web 前端

Vue 侧也会收到 `artifacts` 字段，v1 最低要求是**不炸 + 能看**：渲染同样的标题 + 摘要卡，点击在 `<iframe sandbox="" srcdoc="...">` 里展开（空 `sandbox` 等效于关 JS + 禁同源，和 WKWebView 那条线等价）。做不做随你，但字段解析必须容错。

## 十一、已定决策（原待定）

| 决策 | 结论 | 依据 |
|---|---|---|
| 点开时取正文 | **每次拉最新**，不做会话内缓存 | artifact 不可变（改就是新记录），缓存拿到的字节永远一样，只多一套失效逻辑 |
| 无痕会话销毁 | **跟两段式节拍**：Stage A 只改 `deleted_at`（不碰正文，几乎零 IO），Stage B 按 `conversation_id` 随批物理删 | 复用 `ephemeral_destroyer` 的既有节奏；Stage A 直接物理删会在 Stage B 失败时留下坏链 |
| 无痕是否连带删 | **删 artifact，但不删文档** | artifact 是那条消息的身体；文档是用户级资产，`BeginPurge` 现在也不清它。这个区别别让人照文档的逻辑处理 |
| 接入位置 | **`tabs` 池** + 两处守卫 | 见 §9.1；独立模式要自己重画 tab 条，不划算 |
| 内容去重 | **不做** | 自由生成的 HTML 几乎不可能字节重复，按正文哈希等于写一段永不执行的分支；重试由"每轮 ≤3"兜住 |
| 同一 id 覆盖正文 | **永远不做** | 快照被改写会让历史消息失去历史性。修订只能新建记录（v2 用 `supersedes_id`） |
| 浏览器打开 | **v1 不做** | 见 §9.4 |
| 写入方式 | **同步单行 INSERT** | 见 §4.1；`request_json` 的异步先例不适用 |

## 十二、涉及文件

服务端（新增）：

- `internal/model/artifact.go`（`Artifact` + `ArtifactRef`）
- `internal/service/artifact_repo.go`
- `internal/tool/html_artifact.go` + 测试
- `internal/util/htmlsanitize/sanitize.go`（清洗 + 校验纯函数，含 CSP 注入与 `image:` 内联钩子；不含版式基线）+ 测试
- `internal/controller/artifact.go` + `router.go` 一行

服务端（改造）：

- `internal/model/message.go`（+`artifacts_json`）
- `internal/agent/orchestrator.go`（本轮结束时收集 id 写回消息；本轮图片 id 透传给工具）
- `internal/tool/image.go`（`list_images` 增加 `scope`，返回可直接用的 `<img src="image:<id>">`）
- `internal/service/image_store.go`（`DataURI` 内联出口：owner 校验 + 读文件 + 单图上限）
- `internal/controller/conversations.go`（`getMessages` / `messages-v2` 一次 `IN` 批量展开 title/summary）
- `internal/app/*`（注册 repo + 工具）

客户端（新增）：

- `Shared/Views/HTMLArtifactCard.swift`（照 `DocumentRefCard`）
- `Shared/Views/HTMLArtifactView.swift`（WKWebView + 关 JS + 取消导航 + 版式基线注入 + 不透明底）
- `Shared/Models/ArtifactRef.swift`（`ArtifactRef` + `ArtifactPayload`）
- `Shared/Services/ArtifactOpenCoordinator.swift`（拉正文 + 打开右栏 / iOS sheet）

客户端（改造）：

- `Shared/Models/Message.swift`（+`artifacts`）
- `Shared/Views/MessageListView.swift`（渲染卡片 + 按 id 去重 + 点击调 coordinator）
- `Shared/ViewModels/ChatViewModel.swift`（SSE tool_result 即时插卡）

前端（改造）：`front-end/src/` 消息卡片最小展示（sandbox iframe）。

## 十三、验收

- `go build ./... && go test ./...` 全绿（含清洗、校验、repo 的针对性单测）
- `swift test` + macOS/iOS `swiftc -typecheck` 0 error
- 真机：发一句"给我做个今天的手账"，出卡片 → 点卡右栏打开 → 页面完整、无横向滚动 → **再点同一张不会开第二份** → 关掉再点能重开 → 切屏/重载卡片还在
- 右栏守卫：点"退出浏览器"时，开着的 artifact 不会被藏起来；浏览器语义的遍历不会把手账 tab 当网页处理
- **触发边界**：问天气、列三件待办、解释一个概念、要一段代码 —— 这四种都**不该**生成页面，确认走 markdown
- **每轮上限**：同轮内要求"给我五种风格"，确认最多落 3 条
- **不取正文**：抓一次消息列表接口的响应，确认里面没有 `html` 字段（`Omit` 生效）
- **不重复**：生成页面后，正文里不应再出现页面内的同一份内容
- 版式基线：一条模型没写任何样式的 HTML，打开后仍满足最小字号、行高、不横向滚动、有底色
- 恶意样本：存一条带 `<script>alert(1)</script>`、`<iframe src="file:///etc/passwd">`、外链 `<img>` 的，确认剥干净、WebView 控制台无报错、无网络请求
- 截断样本：故意存一条缺 `</html>` 的，确认工具返回错误、库里没有记录
- 带图页面：本轮发一张图 + "把手账加上这张图"，确认页面里是内联 `data:`（不是 `image:` 也没外链），断网也能显示
- 图已过期：把源图删掉后打开已生成的页面，确认图仍在（内联生效）
- 引用失效图：让页面引用一个不存在的 id，确认工具报错点名该 id、库里没有记录、单轮配额没被扣
- 超大图：拿一张 >4MB 的图内联，确认报错而不是入库
- CSS 背景图：页面里写 `url(image:<id>)`，确认工具报错而不是页面静默少图
- 图片张数：一页塞 13 张，确认报错而不去读第 13 个文件
- 内存：连开关五篇，活动监视器里 WebContent 进程数不应随"打开过的篇数"线性增长

## 十四、风险

- **触发过宽**（最大的风险）：目标从"做手账"扩到"任何更好看的内容"后，模型很容易给问天气、列清单也生成一张页。压不住的话，这个功能会从"偶尔惊艳"变成"天天烦人"。靠 §8.1 的正反例压，没有代码层兜底。
- **正文复述**：prompt 只能说"降低概率"，服从度没有实测。真频繁出现再考虑在工具返回后注入一条 `role=tool` 提示。
- **基线 CSS 的覆盖顺序**：必须靠 DOM 顺序（基线在前）保证模型能覆盖，不能靠 `!important`，否则模型想定制会被锁死。
- **截断**：已由校验兜住，但失败率高时白白烧 token（也是改成"传文件路径"的信号）。
- **宽度错配**：模型按 1000px 设计 → 右栏横向滚。靠工具描述里的 760px 约定 + 基线 CSS 兜底。
- **体积**：正文十几 KB 没问题；带图页面按图片体积走（base64 ×1.33），单图 ≤4MB、单页 ≤8MB。参数不会变长（模型只写 `image:<id>`），但 DB 行会变大——这是"自包含换过期免疫"的代价。
- **图片不可回溯**：内联保证已生成页面永久有效，但源图若在生成前已被 TTL 清掉就找不回来；CSS 背景图（`url(image:...)`）v1 不支持，只能走 `<img>`。
- **分享会连带公开私有图**：artifact 目前只有鉴权接口（`router.go` 里没有公开分享路由），所以内联的聊天私有图不会外泄。**哪天加 artifact 分享，内联进正文的私有图会跟着一起公开**，届时要么分享前剥离图片，要么接受这条。
- **修订会堆卡**：v1 没有"改这一版"的入口，用户说"再改改"等于重新生成一条新回复 + 一条新 artifact。旧的不算过期内容（它是上一轮的真实产物），但连改五版就是五张长得很像的卡。存储无所谓（100KB 量级），烦的是聊天流。解法在 v2：`artifact_id` 入参 + `supersedes_id` + 渲染只显示链条末端。
- **分享**：消息分享（`MessageShare`）会把卡片带出去，但正文需要鉴权取不到 → 打开是空。v1 要么分享时隐藏卡片，要么把 HTML 一起打包（未定，倾向隐藏）。
- **修订闭环**：主模型没有眼睛，用户说"这版太满"它只拿到 id，只能靠文字描述瞎改。v1 只支持"带 id 重新生成一版"，不做局部改；把修订交给带风格库上下文的子 agent 是 v2 的事。

## 十五、一句话

先把"能显示、能打开、不会崩"跑通，再谈生成得多好；先把"什么时候不该生成"跑通，再谈生成得多勤。

## 十六、后续变更

- **2026-10-29 · 代码审查后的修正**（审查报告 `docs/code-review/html-artifact/2026-10-29.md`）
  - `Message.artifacts` 改 Optional + `artifactRefs` 计算属性：服务端 `omitempty` 会把空数组整个省掉，合成的 `Decodable` 不用属性默认值兜缺 key，否则所有消息列表解码失败。
  - `html.create` 的配额改为**校验/清洗通过后**再扣：截断是最常见的失败，连撞三次不该让整轮报废。
  - `artifacts_json` 只存 `{id,type}`，标题摘要读时由 `attachArtifacts` 一次 `IN` 展开（原实现多存了一份，已删）。
  - 右栏浏览器语义按 `artifact:` 前缀过滤：`enterBrowser` 不再把产物 tab 当目标；关页回落不再抢走产物视图。`performCloseAll` 经核对无需改动（见 §9.1）。
  - 补 `navigationDelegate`（非 `about:` 导航一律 cancel）+ 加载失败提示（不再纯白屏）。
  - iOS 补上 `onOpenArtifact` 接线（此前 sheet 是死代码）。
  - CSP 增加 `font-src data:`（内联 base64 字体不再静默失效）；`<style>` 改为清洗所有文本子节点。
  - 删掉 `ArtifactRepo.SoftDeleteByConversations` / `DeleteByConversations` 两个死方法（删除逻辑在 `ConversationRepo.BeginPurge` / `FinishPurge`）。
  - 影响文件：`client/.../Message.swift`、`MessageListView.swift`、`ChatViewModel.swift`、`ContentView.swift`、`HTMLArtifactView.swift`、`ArtifactOpenCoordinator.swift`、`RightSidebarManager.swift`、`BrowserToolProvider.swift`；`server/.../html_artifact.go`、`orchestrator.go`、`conversations.go`、`artifact_repo.go`、`htmlsanitize/sanitize.go`。
- **2026-10-29 · 版式随右栏宽度自适应**（用户反馈：侧栏一宽一窄页面就破相）
  - 根因：基线把 `max-width:760px;margin:0 auto` 写在 `body` 上，模型普遍写 `body{margin:0}` 直接顶掉居中；body 被限宽后模型的桌面底色只铺 760，侧栏一宽就露白边；侧栏窄于 760 时模型写死的 `width:720px` 又被 `overflow-x:hidden` 裁掉。
  - 决定：`body` 只当背景层，正文列交给顶层容器 `body > *{max-width:min(760px,100%)}`；宽了纸张居中、底色铺满，窄了整页收缩不裁字。
  - 决定：**基线改到客户端加载时注入**（服务端只存清洗结果 + CSP）。理由：基线是表现层，写进库没法回溯，改一次版式要把所有历史页面重新生成。
  - 工具描述补「body 只放底色，容器用 width:100%;max-width:760px」。
  - 影响文件：`Shared/Views/HTMLArtifactView.swift`、`server/.../htmlsanitize/sanitize.go`（+测试）、`server/.../tool/html_artifact.go`。
- **2026-10-29 · 页面支持图片（生成时内联）**
  - 需求：手账里放用户上传过的图 / 本条会话发的图；担心源图过保留期被清导致页面丢图。
  - 决定：模型只写 `<img src="image:<id>">`，服务端在清洗前换成 `data:` URI。**内联发生在生成那一刻，页面从此与源文件解耦**，源图之后被 TTL 清掉也不影响已生成的页面（比"引用 id + 渲染时取 + pin 保活"少一套机制，也保住自包含）。
  - 决定：**不做压缩**。单图 >4MB、单页内联 >8MB 直接报错。压缩要引 `golang.org/x/image` 并处理 EXIF 方向（iPhone 竖拍会横躺），v1 不值得。
  - 决定：任一引用失败（已过期/越权/超大）整次生成失败并点名 id，不静默少图；页面不入库、不扣单轮配额。
  - 内联成功后对这些图 `MarkReferenced`（保留期续 15 天，方便后续修订），**不改全局 TTL、不加豁免**。
  - `image.list_images` 增加 `scope`（`turn` / `all`）并返回 `<img>` 片段；本轮附件 id 由 `ExecuteContext.UserImageIDs` 提供。
  - 不支持 CSS `background-image: url(image:...)`（`cleanCSS` 仍会清成 `none`），v1 只能走 `<img>`。
  - 影响文件：`server/.../util/htmlsanitize/sanitize.go`（+测试）、`service/image_store.go`、`tool/html_artifact.go`（+测试）、`tool/image.go`、`tool/tool.go`、`agent/orchestrator.go`、`appinit/tools.go`。客户端无需改动（`data:` 本来就被允许）。
- **2026-10-29 · 第三轮审查修正**（审查报告 `docs/code-review/html-artifact/2026-10-29.md` §九）
  - 内联加**张数与累计字节闸**（≤12 张 / ≤8MB），在 `resolve` 闭包里就地判：原先只在整页 Render 之后判，模型引用几十张大图时会白读几十个文件、压几百 MB base64 再报错。
  - CSS 背景图从"被静默清掉"改为**报错点名**（`url(image:...)` 扫 `<style>` 与 `style` 属性），工具描述同步写明只能放 `<img>`。
  - `DataURI` 先 `os.Stat` 再读：上传上限 32MB，不再为判 4MB 把整个文件读进内存。
  - 删掉没有生产调用方的 `htmlsanitize.Process`（生产只走 `ProcessWithImages`），测试同步。
  - 修正「源图被清理」用例的恒真断言：改为删除后**重新查库**再断言。
  - **已知上限**：8MB 总量 ÷（4MB×1.33）≈ 1.5 张满配图，两张 4MB 原图仍会失败。真机撞得多时优先调 `MaxInlineBytes`，不加压缩。
  - 影响文件：`util/htmlsanitize/sanitize.go`（+测试）、`service/image_store.go`（+测试）、`tool/html_artifact.go`（+测试）。
