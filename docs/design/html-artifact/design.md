# HTML 产物（聊天场景）

> 日期：2026-10-29
> 状态：**已实现**（2026-10-29，`feat/html-artifact`），代码审查见 `docs/code-review/html-artifact/2026-10-29.md`
> 改版省 token（细粒度节点读写，2026-10-30 设计）：见同目录 `fine-grained-edit.md`
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
    HTML           string  // LONGTEXT，json:"-"（任何列表都带不出去；图片已内联成 data:）
    SourceHTML     string  // LONGTEXT，json:"-"（模型给的原文，图片仍是 image:<id>；html.get 读回用）
    ShareToken     string  // 公开分享凭证（空 = 未分享，见 §9.6）
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
| 改版 | 原地覆盖同一行 | 传 `artifact_id` 就 UPDATE，不新建；照扣一次单轮配额（防模型反复重写） |
| 每会话总量 | 不设硬上限 | 一天两页 ≈ 一年 14MB，且列表不取正文，日常零开销；只影响备份体积。带图页面按图计（一张原图 base64 约 ×1.33） |

**不做内容去重。** 自由生成的 HTML 几乎不可能字节重复，按正文哈希等于写一段永不执行的分支；重试导致的重复由"每轮 ≤3"兜住。"改这一版"造成的堆积交给 §4.2 的原地覆盖——用户明确说"改这页"时是不是新版本是确定语义，不用猜。

**改版 = 原地覆盖（2026-10-29 起；此前写的是「永远不做」）。** `html.create` 接受可选 `artifact_id`：传了就 UPDATE 同一行的 `title/summary/html`，不新建、id 不变、灵阅条数不变。

起因：真机做手账时用户连着改十几版样式，"改一版就是新建一条"让灵阅堆出二十多张同款页面，只能逐条删。新建一版在语义上对，在交互上错。

**改版时 `title` / `summary` 可选**：不传就沿用旧页（见 §8.3），只有正文是必填。

**模型怎么拿到 id、又怎么读到旧内容**：不能假设它记得。同一个工具上有一对只读函数：

- `html.list` — 最近 10 页（`artifact_id` / 标题 / 摘要 / 创建时间，**用户级、不按会话过滤**，换了窗口也查得到）。
- `html.get` — 按 `view` 读取（默认 `outline`）：块清单（nid / 首 60 字 / hash）、单块全文、`<style>`、整页原文。读的是 `artifacts.source_html`（模型给的原文，图片仍是 `image:<id>` 引用），**不是** `html` 列——那列里图片已内联成几 MB 的 `data:`，整页塞回上下文不现实。整页读回上限 128KB。
- `html.patch` — 按 nid / style 做局部修改，不重传整页（改文字、换图、改 CSS 走它，见 `fine-grained-edit.md`）。

工具描述写死改版动作：改文字/换图用 `get(view="outline")` + `patch(ops)`；加/删段落与图片用 `patch(insert / remove)`；改样式用 `get(view="style")` + `patch(style=…)`；只有重排版式、整页换结构才 `get(view="full")` + `create(artifact_id)`。`list` 找 id 这条是 2026-10-29 真机撞出来的：工具原先只有 `create` 一个写入口，换个窗口说「改一下刚才的灵阅」，模型既不知道是哪一页、也读不到旧 HTML，只能新建或反问。

代价（明确接受）：旧版本不再可回溯，翻回三天前的消息看到的是今天的版式；同理已分享的链接会跟着变成新版。能接受的前提是产物定位为**可再生成的成品**而非快照记录。真需要版本历史时再引 `supersedes_id` 链（只显示链条末端那种）。

### 4.3 图片：生成时内联，与源文件解耦

模型**不写 base64**（参数太长，极易写崩），只写引用：

```html
<img src="image:<ImageAsset.ID>" alt="...">
```

服务端在清洗之前解析这个 scheme：按 `id + userID` 查 `image_assets`、读磁盘文件、**压到适合屏幕的尺寸**、转成 `data:image/...;base64,...` 写回 `src`。清洗随后照常跑，`safeURL` 仍然只放行 `#anchor` 与 `data:image/`。

**内联前先压缩**（`internal/util/imgshrink`，2026-10-29 起）：

| 步骤 | 做法 |
|---|---|
| 解码 | jpeg / png / gif（取首帧）/ webp |
| 缩小 | 最长边 → **1600px**（右栏最宽 1200、Retina 2x ≈ 2400，够看）；CatmullRom |
| 重编码 | 有透明像素 → PNG；否则 **JPEG q85** |
| EXIF 方向 | 读 APP1/IFD0 的 `0x0112` 并**烘焙进像素**（Go 标准库不读方向，直接重编码会让竖拍照片躺倒） |
| 缓存 | 压好的副本落盘 `images/inline/<id>.jpg|png`，同一张图第二次内联直接读；原图删除/清理时一并清掉 |
| 保护 | 解码后像素 > 50MP 直接拒绝（防解出巨型位图吃光内存）；已经够小的图**原样返回**（不白掉画质） |

效果：3MB 手机照片 → 300~500KB，12 张约 4~6MB，稳在 8MB 内；页面渲染也快一个量级。**历史产物不重算**（已经存成 `data:` 的页面不会变小）。

**为什么必须内联而不是"引用 id + 渲染时取"**：图片有保留期（被消息引用过 15 天、孤儿 3 天，`storage.image_ttl_days` / `image_orphan_ttl_days`）。内联发生在生成那一刻，页面从此是字节副本，**源图之后被清掉也不影响已生成的页面**。引用型方案则要额外给图片做 pin（新列 + 清理豁免 + 解除时机），页面也不再自包含。

规则：

| 项 | 值 | 说明 |
|---|---|---|
| 模型给的 HTML | ≤ 2MB | 内联前，挡误存 |
| 单张图 | 不设原字节上限 | 内联前压到最长边 1600（见上表）；原图受上传上限 32MB 约束 |
| 单页内联总量 | ≤ 8MB | base64 后；超了报错，提示少放几张 |
| 单页张数 | ≤ 12 张不同图片 | 按 `image:<id>` 去重计数（同一张重复引用算 1 张）；真正兜体积的是 8MB，这条只避免"为一张页读几十个文件" |
| 支持的写法 | 只 `img[src="image:<id>"]` | CSS `background-image: url(image:...)` **命中即报错**（不能指望 `cleanCSS` 静默换成 `none`），见下 |
| 来源 | 该用户自己的任意 `ImageAsset` | 文档配图（public）也能用；owner 不匹配按"不存在"处理 |

失败口径：**任一引用解析失败（已过期 / 不存在 / 越权 / 像素超限 / 超过张数或总量 / 写成了 CSS 背景图）→ 整次 `html.create` 失败**，错误里点名是哪个 id。页面不入库、不扣单轮配额。理由：用户明确要求"把这张图放进去"，静默少一张比报错更糟。

内联成功后对这些 id 调 `MarkReferenced`，把源图保留期续 15 天——便于同一会话隔几天再修订一版；**不改全局 TTL、不加豁免**。页面本身不依赖这一步。

> 同一张图被多页引用时的重复体积 vs 外链 + 调 TTL 的取舍，见 `image-retention.md`（备忘，2026-10-30，未决策）。

模型怎么发现图片：`image.list_images` 增加 `scope`（`turn` = 本轮附件、`all` = 全库分页），结果里直接给 `<img src="image:<id>">` 片段；每条带 `available`（文件还在不在）——迁移/外部删除留下的「死图」会被标 `false`，并在 `hint` 里提示别用。本轮附件的 id 由 orchestrator 通过 `ExecuteContext.UserImageIDs` 带进工具。

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

名字：`html`。四个函数：`create`（写：`title` / `summary` / `html`，外加可选的 `artifact_id`）、`list`（读：最近 10 页的 id/标题/时间）、`get`（按 view 读：outline / block / style / full）、`patch`（局部改：nid / 图片 / CSS，见 `fine-grained-edit.md`）。

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

**改版时不要重写 `title` / `summary`**（2026-10-29 撞出来的）：它们描述的是「这一页是什么」，不是「这次改了什么」。`create` 带 `artifact_id` 时这两个字段**可选**，不传就沿用旧值——默认行为就是对的，不靠模型自觉。只有整页主题真的变了（三页变四页）才传，且仍必须是一句话的整页摘要。

反例（真机出现过）：「同一页加回菜单照片并带点开放大，其余文字沿用上一版。」——灵阅列表会变成一串修改日志。

### 8.4 风格与宽度约束

固定在工具描述里（字节稳定，不破坏 DeepSeek 前缀缓存）：

- 观感：留白、纸感、配色，给一两个正例片段的文字描述（手账只是其中一种，不写死风格）
- **按 760px 宽设计、不做横向滚动**——模型不知道容器多宽（右栏默认 840），不写这条每张都要手动缩放

通用版式（字号下限、行高、最大宽度、底色）不靠模型，由客户端加载时注入基线 CSS 保证，见 §9.3。

### 8.5 改版读取（2026-10-29）

`get` 走 `source_html`（新增列，建页时把模型给的原文存一份）。这样「改版」才是真的改：模型读到的就是它当初写的 `<style>` 和结构，图片保持 `image:<id>` 引用，改完重交一遍由服务端重新内联。

**2026-10-30 起，改版优先走细粒度 `patch`**：`get(view="outline")` 拿 nid/hash → `patch` 改文字 / 换图 / 插入片段 / 删除块；改样式只读写 `<style>`。整页 `get` + `create` 只留给重排版式。契约、校验清单与选型依据见 `fine-grained-edit.md`。

| 项 | 值 |
|---|---|
| `list` 返回条数 | 默认 10，最多 30 |
| `get` 读回上限 | 128KB（超过报错，不截断——截断的 HTML 改完会坏） |
| 跨轮使用 | 历史回放按 `llm.max_tool_result_chars`（20000 字符）截断工具结果，所以读回的 HTML **只在当前轮算数**；下一轮要改就重新 `get`（工具描述已写死） |
| 老页面（没有 `source_html`） | `get` 的非 full 视图与 `patch` 直接拒绝（只能重做）；`view="full"` 退回 `html` 列，带图的基本都会超 128KB |
| `patch` 单次上限 | 50 个块 / CSS 64KB；nid 与 hash 必须匹配（防错位） |

### 8.6 其他

- 描述里写一句"一次只生成一张，需要多张时先问用户"——比硬上限更有效，且不误伤。
- 每轮 ≤3 张；模型 HTML ≤2MB，内联图片后 ≤8MB；单张不设原字节上限（内联前压到最长边 1600，§4.3）。
- 描述里写清配图写法（`<img src="image:<id>">`），并明确"不要自己写 base64"。
- 改版：用户说"再改改/换版式"时传 `artifact_id` 原地覆盖，不要新建（§4.2）。
- 不知道 id 时先调 `html.list`（只读，用户级最近 10 页），别猜、别新建；要动内容先 `html.get`（默认 `outline`，改样式用 `style`，只有重排版式/整页换结构才 `full`）。
- 模型能稳定输出十几 KB，真遇到截断再改成"传文件路径"。
- 只读声明：`list` / `get`（plan 模式可用）；`create` / `patch` 写库。
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

**当时不选"新增浏览面板的一个 case"**：`BrowsePanel` 是 `CaseIterable` 驱动的网格入口（账单/记忆/文档…），加一个 case 会在侧栏首页多出一个永久入口——那时要的是"看当前这一页"，不是"一个内容库"。

**2026-10-29 补充**：产物已经攒成库，用户提出要回看入口，于是新增浏览面板 `灵阅`（见 §9.5）。聊天里的卡片仍然走 tab，两条入口共用同一个 coordinator，行为一致。

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

### 9.5 回看入口：右栏「灵阅」

聊天里的卡片只在消息流里，翻历史要一直往上滚。产物攒多之后需要一个**按时间倒序的内容库**——右栏浏览面板新增一项「灵阅」，替代原来的「笔记」。

笔记的退场是**配置层收口**，不是使用习惯：`conf/config.yaml` 的 `chat.tool_excludes: [note]` 让 `note` 工具对 LLM 彻底不可见（`expose_tools` 的目录与函数定义两层都拦，见 `makeExposeFn`）。否则会出现「模型还能写、Mac 端却没地方看」。历史笔记数据保留，Web 前端 `NotesView.vue` 仍可查。

数据源与消息表无关：

- 列表 `GET /api/artifacts?limit=30&offset=0` → `{items, total}`，`WHERE user_id = ? AND deleted_at IS NULL ORDER BY created_at DESC, id DESC`。
- **只 `Select` 五列**（id/title/summary/conversation_id/created_at）。正文 `html` 不进 SQL、也不进 JSON（`Artifact.HTML` 是 `json:"-"`），沿用 §4.1 的硬规则。
- 详情仍只有 `GET /api/artifacts/:id` 一个正文出口。

界面与交互：

- `PageContainer` + `CardGridView` + `CardConfig`（标题 + 摘要 2 行 + 日期脚注），与文档列表同款观感；不做搜索、不按会话分组。删除见 §9.7（单条 + 多选批量）。
- 点一条 → `ArtifactOpenCoordinator.open(id:title:)` → 右栏开 tab 渲染整页（与聊天卡片同一条路径，同一 id 再点是切回去）。
- 「灵阅」是浏览面板的一种态；离开再回来会重新拉第一页（`ArtifactViewModel.load` 在 `.task` 里），顺带把刚生成的产物顶到最前面。
- 空态：「暂无内容。可在对话中生成页面后回到此处查看。」

生命周期（和消息不是一回事）：

- 普通删除/归档会话只置 `conversation.is_deleted`，**不动 artifacts**。所以作品比聊天记录活得久，历史消息翻不到了灵阅里还在——这是成品该有的行为。
- 无痕会话走 `BeginPurge`（按 conversation 软删）+ `FinishPurge`（物理删），**无痕里生成的作品不会漏进灵阅**（§十一）。
- 内联过图片的产物是自包含字节副本，列表页不碰图片，也不受 `image_ttl_days` 影响。

**为什么不复用文档那种「面板内打开 + 返回」**：那要新写一层面板内导航，而且同一个产物从聊天点开是 tab、从灵阅点开是面板内，行为不一致。tab 方案零新代码，且列表留在面板里可以点回去继续翻。

### 9.6 分享：公开只读链接

需求：把做好的页发给别人看，**对方不需要账号**。

**不新建分享表**：产物与分享是 1:1，`artifacts` 直接加一列 `share_token`（uuid，`json:"-"`）。文档那边用独立表是历史选择，这里照抄只会多一整套 repo。

| 接口 | 说明 |
|---|---|
| `POST /api/artifacts/:id/share` | 生成或返回现有 token（幂等），回 `{token, url}` |
| `DELETE /api/artifacts/:id/share` | 撤销：清空 token，旧链接立即 404；回 `{shared:false, revoked}`（`revoked=false` ＝本来就没分享，客户端据此换文案） |
| `GET /api/share/artifacts/:token` | 公开只读，**直接返回 `text/html`** |

- **为什么直接吐 HTML 而不是像文档那样返回 JSON + Vue 页**：产物本身就是完整自包含文档，前端再渲染一层纯属多写一个页面。
- **为什么挂在 `/api/` 下**：nginx 只转发 `/api/` 与 `/images/`，因此零部署改动（想要 `/a/xxx` 短链才需要动线上配置）。
- 链接**永久有效、可随时撤销**；不做有效期、不做访问密码、不做访问计数。
- **v1 只有 macOS 有分享入口**（产物页右上角浮钮）：iOS 的 sheet 里没有，`HTMLArtifactView` 已按两端都能编译写，补的话是在 `ChatView` 的 sheet `.toolbar` 里放同一个按钮。

安全（两层）：

1. 产物入库时已清洗：`script/iframe/object/embed` 删除、`on*` 属性删除、外链 URL 过滤、图片内联为 `data:`、内嵌 CSP `default-src 'none'`。
2. 公开路由再压一层响应头：`Content-Security-Policy: sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:` + `X-Content-Type-Options: nosniff` + `Referrer-Policy: no-referrer` + `Cache-Control: no-store`。
   - `sandbox` 把页面放进**不透明源**：万一哪天清洗漏了，脚本也碰不到同域 Web 端存在 localStorage 里的 Bearer token。
   - `no-store` 是「可撤销」的前提——允许缓存的话，撤销后浏览器还拿旧副本继续显示。

图片与分享的关系：生成时已内联成字节副本，因此**不依赖图床、不需要登录、不受 `image_ttl_days` 影响**。代价是图片随页面一起公开，且撤销只让 URL 失效——**对方已保存的副本收不回**。

生命周期：无痕会话的产物随会话销、软删的产物被 GORM 默认过滤 → 两种情况下链接都自然 404。

### 9.7 删除：软删、无回收站

- `DELETE /api/artifacts/:id`，按 `user_id` 鉴权，命中 0 行回 404。
- `POST /api/artifacts/batch-delete`，body `{ids:[...]}`（≤100、去重去空，空 400）→ `WHERE user_id = ? AND id IN ?` 软删。越权/已删的 id 静默跳过，返回 `{deleted:n}`——批量操作里一条失效 id 不该让整次失败。
- 软删（`gorm.DeletedAt`）：正文仍留在库里，但列表、详情、分享路由都看不到（GORM 默认过滤）。**不提供回收站**——产物是可再生成的成品，留着恢复入口反而要维护一套「已删」视图。
- 客户端（灵阅列表）：卡片页脚 `CardAction(role: .destructive)` + 二次确认（产物一次性、删了没入口找回）。删除成功后本地 `items` 移除、`total -= 1`；若该产物正开在右栏 tab 里，一并 `closeTab`，不留僵尸 tab。
- **多选批量删除**（2026-10-29，为清理「改版堆出来的那批」）：列表顶部 `选择` → 卡片变勾选态（点卡＝勾选，单条删除按钮隐藏）→ `全选（取消全选） / 已选 N / 删除所选 / 取消`。二次确认共用一个 alert（同一视图挂两个 `.alert` 会互相覆盖）。成功后退出选择态；失败留在选择态，可直接重试。
- **和分享的联动**：token 就在同一行，删掉即失效，无需额外逻辑。
- **聊天里的旧卡片仍在**：点开 404，coordinator 提示「该页面已被删除」（而不是把英文错误抛给用户）。

### 9.8 大页面的加载态

现象：内联 13 张图的页面接近 8MB 上限，点开时「拉 JSON → 解析 → 注入基线 → `loadHTMLString`」全在主线程（`-default-isolation MainActor`），期间**没有任何反馈**，用户看到的就是"点了没反应"。

做法：`ArtifactOpenCoordinator.open` **先开一个骨架 tab**（spinner + 标题），拉到正文后 `updateTabContent` 换成 `HTMLArtifactView`；失败则**关掉骨架 tab** 并 toast 说清原因（留一个永远白屏的 tab 比没有更糟）。

- 骨架走的是右栏既有的 tab 机制（`tabs[i].content` 是 `var`，已有 `updateTabTitle` 同款写法），不新造容器。
- iOS 的 sheet 保持原位（先弹出再显示加载态），无需 tab。
- 顺带收益：点卡片后立刻有视觉反馈，「点了没反应」这类反馈不会再被误判成 bug。

### 9.9 聊天里的卡片按轮次成槽

现象：一轮里既生成了文档（文档卡）又生成了 HTML 页（产物卡），两张卡上下堆着，看不出属于同一轮。

根因：两张卡走两条不同的渲染路 ——

| 卡 | 数据在哪 | 原渲染 |
|---|---|---|
| 文档卡 | 独立的 `local_tool_result` 消息（靠 `anchorUserID` 归位到本轮 user 消息） | 相邻卡片消息合并成横排条 |
| HTML 产物卡 | 助手消息的 `artifacts` 字段 | 塞在消息内部竖排 |

定义：**一轮 = 一条 user 消息到该轮结束**。本轮产生的所有卡片（文档卡 / HTML 卡）进**同一个横排槽**，槽放在**本轮开头**（user 消息之后、回复正文之前）——先给结果再给解释。

实现要点：

- `DisplayRow` 增加 `.cardSlot([ChatCard])`；`ChatCard` 是枚举（`docRef` / `htmlRef`），`ArtifactCardStrip` 本来就是泛型容器，直接吃。
- 归组时按消息顺序走：遇到 user 消息先收上一轮的槽、并把它插到上一轮 user 消息之后；卡片消息不单独成行；普通助手消息自己成一行且把它 `artifacts` 里的卡塞进本轮槽；列表末尾收尾。
- **单张卡保持原样**（渲染整卡，不缩成 240×92）；≥2 张才用横排条。`HTMLArtifactCard` 因此要支持 `width`/`height` 参数，与 `DocumentRefCard` 一致。
- 流式期间卡片到达时槽在原地增长、正文往下让，不会"等整轮结束才蹦出来"。
- 分享多选态**保持逐条**（每张卡各自一行 + 选择圈），不进槽——那个态要求逐卡可点。
- **同一页只留最新那张卡**（2026-10-29）：改版是原地覆盖、id 不变，但卡会挂到新一轮的消息上，历史轮的消息也还挂着同一个 id —— 不去重就会「改一版多一张」。按 `ChatCard.id` 找**最后一次**出现的消息（`ChatCardDedupe.lastOwner`），只在那一条里渲染；旧位置的卡自动不显示，页面本体不受影响。流式期间新卡挂在当前轮，天然就是最新那张。
- iOS 不分组（无右栏、无文档卡），但同样只留最新那张卡（在 `displayRows` 里过滤 `artifactRefs`）。

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
| 同一 id 覆盖正文 | **做**（2026-10-29 拍板，§4.2） | 改版堆积的交互成本高于「快照历史性」；产物是可再生成的成品。代价：旧版不可回溯、分享链接跟新版 |
| 浏览器打开 | **v1 不做** | 见 §9.4 |
| 写入方式 | **同步单行 INSERT** | 见 §4.1；`request_json` 的异步先例不适用 |

## 十二、涉及文件

服务端（新增）：

- `internal/model/artifact.go`（`Artifact` + `ArtifactRef`）
- `internal/service/artifact_repo.go`
- `internal/tool/html_artifact.go` + 测试
- `internal/util/htmlsanitize/sanitize.go`（清洗 + 校验纯函数，含 CSP 注入与 `image:` 内联钩子；不含版式基线）+ 测试
- `internal/util/imgshrink/shrink.go`（内联前压缩：缩放 + EXIF 方向 + 透明转 PNG）+ 测试
- `internal/controller/artifact.go`（`GetArtifact` 正文出口 + `ListArtifacts` 列表 + `DeleteArtifact` + 分享三接口 + 公开只读路由 `ViewSharedArtifact`）+ `router.go`

服务端（改造）：

- `internal/model/artifact.go`（+`ShareToken`，`json:"-"`）

- `internal/model/message.go`（+`artifacts_json`）
- `internal/agent/orchestrator.go`（本轮结束时收集 id 写回消息；本轮图片 id 透传给工具）
- `internal/tool/image.go`（`list_images` 增加 `scope`，返回可直接用的 `<img src="image:<id>">`）
- `internal/service/image_store.go`（`DataURI` 内联出口：owner 校验 + 读文件 + 单图上限）
- `internal/controller/conversations.go`（`getMessages` / `messages-v2` 一次 `IN` 批量展开 title/summary）
- `internal/app/*`（注册 repo + 工具）
- `conf/config.yaml`（`chat.tool_excludes: [note]`：笔记工具退场，见 §9.5）

客户端（新增）：

- `Shared/Views/HTMLArtifactCard.swift`（照 `DocumentRefCard`）
- `Shared/Views/HTMLArtifactView.swift`（WKWebView + 关 JS + 取消导航 + 版式基线注入 + 不透明底 + 分享按钮）
- `Shared/Views/ArtifactLoadingView.swift`（大页面的骨架态：spinner + 标题；失败时直接关掉 tab）
- `Shared/Views/ArtifactListView.swift`（右栏「灵阅」列表：`PageContainer` + `CardGridView` + 单条删除 + 多选批量删除）
- `Shared/ViewModels/ArtifactViewModel.swift`（分页加载，30/页；`delete` / `deleteMany`）
- `Shared/Models/ArtifactRef.swift`（`ArtifactRef` + `ArtifactPayload` + `ArtifactSummary` / `ArtifactListResponse`）
- `Shared/Services/ArtifactOpenCoordinator.swift`（拉正文 + 先开骨架 tab + 换内容 / iOS sheet + 分享）
- `Shared/Views/MessageListView.swift`（`DisplayRow.cardSlot`：按轮次归组卡片）
- `Shared/Extensions/Pasteboard.swift`（复制到剪贴板，macOS/iOS 共用）

客户端（改造）：

- `Shared/Models/Message.swift`（+`artifacts`）
- `Shared/Views/MessageListView.swift`（渲染卡片 + 按 id 去重 + 点击调 coordinator）
- `Shared/Views/HTMLArtifactCard.swift`（支持 `width`/`height`，与 `DocumentRefCard` 一致以便混排）
- `Shared/Views/RightSidebarManager.swift`（+`updateTabContent`）
- `Shared/ViewModels/ChatViewModel.swift`（SSE tool_result 即时插卡）
- `Shared/Views/BrowsePanelView.swift`（`BrowsePanel` 新增 `.artifacts`「灵阅」，替换 `.notes`）
- `Shared/Views/PageHolders.swift`、`ContentView.swift`、`RightSidebarView.swift`（接线 `artifactVM`）
- `Shared/Services/APIClient.swift`（`listArtifacts` / `deleteArtifact` / `deleteArtifacts` / `shareArtifact` / `unshareArtifact`）

前端（改造）：`front-end/src/` 消息卡片最小展示（sandbox iframe）。

## 十三、验收

- `go build ./... && go test ./...` 全绿（含清洗、校验、repo 的针对性单测）
- `swift test` + macOS/iOS `swiftc -typecheck` 0 error
- 真机：发一句"给我做个今天的手账"，出卡片 → 点卡右栏打开 → 页面完整、无横向滚动 → **再点同一张不会开第二份** → 关掉再点能重开 → 切屏/重载卡片还在
- 右栏守卫：点"退出浏览器"时，开着的 artifact 不会被藏起来；浏览器语义的遍历不会把手账 tab 当网页处理
- **触发边界**：问天气、列三件待办、解释一个概念、要一段代码 —— 这四种都**不该**生成页面，确认走 markdown
- **每轮上限**：同轮内要求"给我五种风格"，确认最多落 3 条
- **不取正文**：抓一次消息列表接口的响应，确认里面没有 `html` 字段（`Omit` 生效）
- **灵阅列表**：右栏 →「灵阅」→ 看到历史产物（时间倒序）→ 点一条在右栏开 tab → 切回面板 → 滚到底自动加载第 31 条；抓一次 `/api/artifacts` 响应，确认同样没有 `html` 字段
- **灵阅与消息解耦**：删掉（归档）产出该页的会话，确认灵阅里那条还在、点开正常
- **改版原地覆盖**：生成一页 → 说"把标题改大一点" → 灵阅条数**不变**、点开是**同一页的新版**（不是多一条）；旧轮聊天卡点开也是新版
- **跨会话改版**：新开一个对话说「改一下刚才那个灵阅」→ 模型先调 `html.list` 认出那一页 → `html.get` 读回原文 → 带 `artifact_id` 原地覆盖，灵阅条数不变（这条是 2026-10-29 真机暴露的：只有 create 没有 list/get 时它只能新建或反问）
- **读回带图页面**：`html.get` 一张含内联图片的页，确认返回的是 `image:<id>` 引用、体积是几十 KB 而不是几 MB
- **改版后 tab**：把那页开在右栏，再让模型改它 → 右栏内容**自动变成新版**（不闪骨架、不需要手点刷新）
- **改版不动标题摘要**：让模型"只改样式"，确认灵阅里的标题和摘要**没变**（不会变成「本次改了什么」）
- **改版 id 失效**：删掉页面后让模型"改刚才那页" → 工具报错点名 id，库里**不新增**
- **批量删除**：灵阅点「选择」→ 全选 → 取消勾选要留的两张 → 删除所选 → 确认 → 卡片消失、`total` 对得上、翻页不重复、对应右栏 tab 一并关掉
- **不重复**：生成页面后，正文里不应再出现页面内的同一份内容
- 版式基线：一条模型没写任何样式的 HTML，打开后仍满足最小字号、行高、不横向滚动、有底色
- 恶意样本：存一条带 `<script>alert(1)</script>`、`<iframe src="file:///etc/passwd">`、外链 `<img>` 的，确认剥干净、WebView 控制台无报错、无网络请求
- 截断样本：故意存一条缺 `</html>` 的，确认工具返回错误、库里没有记录
- 带图页面：本轮发一张图 + "把手账加上这张图"，确认页面里是内联 `data:`（不是 `image:` 也没外链），断网也能显示
- 图已过期：把源图删掉后打开已生成的页面，确认图仍在（内联生效）
- 引用失效图：让页面引用一个不存在的 id，确认工具报错点名该 id、库里没有记录、单轮配额没被扣
- 大图压缩：拿一张 4000×3000 的原图内联，确认入库的是 `data:`、最长边 1600、体积显著变小，且竖拍照片不躺倒；再打开同一页确认第二次走缓存（不重复压缩）
- CSS 背景图：页面里写 `url(image:<id>)`，确认工具报错而不是页面静默少图
- 图片张数：一页塞 13 张，确认报错而不去读第 13 个文件
- 内存：连开关五篇，活动监视器里 WebContent 进程数不应随"打开过的篇数"线性增长
- **分享（macOS）**：产物页右上角点分享 → 复制到链接 → 浏览器无痕窗口打开，页面完整、图片在、控制台无报错；`curl -i` 确认 `Content-Security-Policy: sandbox`、`Cache-Control: no-store`
- **分享撤销**：`DELETE .../share` 后同一个链接 404；再点分享得到**新** token（旧链接不会复活）
- **分享与删除**：分享着的一条产物删掉后，链接立刻 404
- **分享与无痕**：无痕会话里生成的页面，会话销毁后链接 404
- **删除**：灵阅删一条 → 弹确认 → 卡片消失、`total` 减一、翻页不重复；若该页开着 tab，tab 一并关掉
- **删除后旧卡片**：回到聊天里点那张旧卡 → 提示「该页面已被删除」，不是英文错误
- **加载态**：用一张内联了多张大图的页面点开，确认**立刻**出现 spinner 骨架，随后换成真页面（不出现"点了没反应"）
- **卡片槽**：一轮里同时生成文档和 HTML 页 → 两张卡在**同一条横排**、且在本轮**开头**（你的消息之后、正文之前）；再发一轮 → 新的一槽；单张卡仍是原样整卡；分享多选态仍能逐条选中
- **同一页只留最新卡**：连改同一页三次 → 聊天流里**只有一张**卡（在最新那轮**开头**），上面的旧卡自动不显示；页面对应的右栏 tab 仍自动变新版
- **流式不跳**：让模型边生成边出卡（先 html.create 再写正文）→ 卡片出现在本轮开头后**不回跳、不闪**，正文在它下面往下长
- **细粒度改版**：改一句话 → 日志里只有 `html.get(view="outline")` + `html.patch`，不出现整页 `create`；改样式只有 `get(view="style")` + `patch(style=…)`（完整剧本见 `fine-grained-edit.md` §七）

## 十四、风险

- **触发过宽**（最大的风险）：目标从"做手账"扩到"任何更好看的内容"后，模型很容易给问天气、列清单也生成一张页。压不住的话，这个功能会从"偶尔惊艳"变成"天天烦人"。靠 §8.1 的正反例压，没有代码层兜底。
- **正文复述**：prompt 只能说"降低概率"，服从度没有实测。真频繁出现再考虑在工具返回后注入一条 `role=tool` 提示。
- **基线 CSS 的覆盖顺序**：必须靠 DOM 顺序（基线在前）保证模型能覆盖，不能靠 `!important`，否则模型想定制会被锁死。
- **截断**：已由校验兜住，但失败率高时白白烧 token（也是改成"传文件路径"的信号）。
- **宽度错配**：模型按 1000px 设计 → 右栏横向滚。靠工具描述里的 760px 约定 + 基线 CSS 兜底。
- **体积**：正文十几 KB 没问题；带图页面按图片体积走。内联前已压到最长边 1600 / JPEG q85，单张通常 300~500KB，单页 ≤12 张、base64 后 ≤8MB（实际多在 3~5MB）。参数不会变长（模型只写 `image:<id>`），但 DB 行会变大——这是"自包含换过期免疫"的代价。压缩的代价只在「某张图第一次被内联」：实测（M1，含解码→转正→缩放→编码）12MP 竖拍约 **800ms**、12MP 无 EXIF 约 470ms、48MP 约 **2.8s**（峰值驻留 ~270MB）；命中缓存后为零，3 张 12MP 竖拍约 2.4s 一次性。已知可优化但本轮未做：把 `scaleDown` 挪到 `applyOrientation` 之前可省 ~43% 时间与 32% 内存；`MaxPixels` 50MP 在 2GB 内存的云机上偏宽。
- **图片不可回溯**：内联保证已生成页面永久有效，但源图若在生成前已被 TTL 清掉就找不回来；CSS 背景图（`url(image:...)`）v1 不支持，只能走 `<img>`。
- **小图原样内联会带 EXIF（含 GPS）**：够小的图走 passthrough（不重编码，保住原画质与动图），它的 APP1 元数据会跟着 `data:` 进页面；产物现在可公开分享（§9.6），等于 GPS 也会随链接出去。大图重编码时元数据已丢。
- **分享会连带公开私有图**（已兑现，见 §9.6）：产物里的图在生成时就内联成 `data:`，所以分享一页带图的手账＝把这些**聊天私有图**一起公开——源图的 TTL、`visibility`、`user_id` 鉴权都不再生效，撤销也只能让 URL 失效（对方已保存的副本收不回）。要不要在分享前剥离图片是取舍：v1 选择接受，因为「图随页走」正是产物自包含的前提。
- **改版会丢历史版本**（2026-10-29 起支持原地覆盖）：同一 id 的正文被改写，翻回三天前的消息看到的是今天的版式；已分享的链接也会跟着变。这是「只留一份」换来的代价，见 §4.2。
- **改版依赖模型记得 `artifact_id`**：上一版 id 在 tool 结果里，长会话被窗口裁掉后模型可能拿不到 → 仍会新建一条。工具描述里已写死"改这一版必须传"，但不是硬保证。要硬保证得做服务端自动归并（同会话同标题覆盖），误伤面太大，不做。
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
- **2026-10-29 · 右栏「灵阅」回看入口**（替代笔记面板）
  - 需求：产物只在消息流里，翻历史要一直往上滚；要一个按时间倒序的回看入口。笔记已不再写入，用户在两者之间选了「替代」。
  - 决定：`BrowsePanel` 用 `artifacts`「灵阅」替换 `notes`；笔记浏览链（`NoteListView` / `NoteViewModel` / `NoteDetailOverlay` / `NotePage`）删除。后端 `/api/notes`、笔记数据、Web 端 `NotesView.vue` 不动。
  - 决定：列表走新接口 `GET /api/artifacts`（30/页，`Select` 五列不含正文）；点开复用 `ArtifactOpenCoordinator` 开右栏 tab，与聊天卡片同一条路径。
  - 决定：不做搜索、按会话分组、删除产物；iOS 与 Web 端暂无入口。
  - 决定：`note` 工具在 `conf/config.yaml` 用 `chat.tool_excludes: [note]` 收口（配置层，不改代码）。理由：Mac 端已无笔记面板，留着工具会出现「写了看不见」；笔记历史数据与 Web 端 `NotesView.vue` 不受影响。
  - 生命周期确认：普通删除/归档会话不动 artifacts（作品比聊天记录长寿）；无痕会话由 `BeginPurge` + `FinishPurge` 连删，不漏进灵阅。
  - 影响文件：`server/.../service/artifact_repo.go`（+`ListByUser`）、`controller/artifact.go`（+`ListArtifacts`）、`controller/router.go`；`client/.../Views/{BrowsePanelView,PageHolders,ArtifactListView,ContentView,RightSidebarView}.swift`、`ViewModels/ArtifactViewModel.swift`（新）、`Models/ArtifactRef.swift`、`Services/APIClient.swift`；删除 `NoteListView.swift` / `NoteViewModel.swift` / `NoteDetailOverlay.swift`。
- **2026-10-29 · 分享 + 删除 + 加载态 + 卡片槽**
  - 需求：① 产物要能发给别人看（对方无账号）；② 灵阅要能删；③ 大页面（13 张内联图）点开后长时间无反馈，像"点了没反应"；④ 一轮里文档卡与 HTML 卡上下堆着，看不出属于同一轮。
  - 决定：分享走 `artifacts.share_token`（1:1，不建新表），公开路由**直接吐 `text/html`**，挂 `/api/share/artifacts/:token`（nginx 零改动）；响应头带 `sandbox` + `no-store`（见 §9.6）。
  - 决定：删除是软删、无回收站；聊天里的旧卡片点开提示「该页面已被删除」（见 §9.7）。
  - 决定：`open()` 先开骨架 tab、拉到正文再换内容，解决"无反馈"（见 §9.8）。
  - 决定：聊天卡片按**轮次**归组到同一横排槽（`DisplayRow.cardSlot`），单张保持整卡、≥2 张才成条，分享多选态仍逐条（见 §9.9）。
  - 影响文件：`server/.../model/artifact.go`、`service/artifact_repo.go`(+测试)、`controller/artifact.go`、`controller/router.go`；`client/.../Views/{HTMLArtifactView,HTMLArtifactCard,ArtifactLoadingView(新),ArtifactListView,MessageListView,RightSidebarManager,ChatView}.swift`、`Services/{APIClient,ArtifactOpenCoordinator}.swift`、`ViewModels/ArtifactViewModel.swift`。
- **2026-10-29 · 内联前压缩（推翻上面「不做压缩」那条）**
  - 起因：真机做手账时，手机照片 3–5MB，一张就撞 4MB 单图上限、三张就撞 8MB 整页上限；勉强生成出来的页面接近 8MB，打开要好几秒（"点了没反应"）。
  - 决定：新增 `internal/util/imgshrink`，`ImageStore.DataURI` 内联前先压——最长边 1600、有透明留 PNG、否则 JPEG q85；**EXIF 方向烘焙进像素**（标准库不读方向，直接重编码会让竖拍照片躺倒）；像素 >50MP 拒绝；够小的图原样返回。
  - 决定：压好的副本落盘 `images/inline/<id>.jpg|png`，同一张图第二次内联直接读；`ImageStore.Delete` 与 `Cleanup` 一并清掉。
  - 决定：**历史产物不重算**（已存成 `data:` 的页面不动）。
  - 决定：`InlineMaxBytes`（单图 4MB 上限）删除；工具描述同步去掉「单张 ≤4MB」。
  - 影响文件：`util/imgshrink/shrink.go`(新)+测试、`service/image_store.go`(+测试)、`tool/html_artifact.go`（描述）、`util/htmlsanitize/sanitize.go`（注释）、`go.mod`（+`golang.org/x/image`）。
- **2026-10-29 · 改版改原地覆盖 + 灵阅多选删除**
  - 起因：真机连着改了十几版手账样式，每轮都新建一条，灵阅里堆出二十多张同款页面，用户只想留两张。设计文档 §14 早就把这条列为「修订会堆卡」，现在到期了。
  - 决定：`html.create` 增加**可选** `artifact_id`——传了就原地覆盖同一行的 `title/summary/html`（id 不变、不新增行），不传才新建。找不到/不属于该用户 → 报错点名，**不静默新建**（静默新建正是堆积来源）。
  - 决定：工具描述写死「用户说再改改/换版式时必须传 artifact_id」，靠模型判断，不是硬保证（长会话里 id 可能被窗口裁掉）。
  - 决定：**推翻**原先的「永远不做同一 id 覆盖正文」（§4.2 / §11）——代价是旧版不可回溯、分享链接跟着变新版，用户已接受。
  - 决定：灵阅加速 `POST /api/artifacts/batch-delete` + 客户端选择态（全选 / 删除所选 N / 取消），用来清理本次堆下的存量（§9.7）。
  - 影响文件：`server/.../service/artifact_repo.go`（+`Update` / `DeleteMany`）+测试、`tool/html_artifact.go`（+`artifact_id`、抽出 `inlineImages`）+测试、`controller/artifact.go`（+`BatchDeleteArtifacts`）、`controller/router.go`；`client/.../Views/ArtifactListView.swift`（选择态）、`ViewModels/ArtifactViewModel.swift`（+`deleteMany`）、`Services/APIClient.swift`（+`deleteArtifacts`）。
- **2026-10-29 · 工具补 `list` + `get`，改版真的能"读旧内容"**
  - 起因：`html` 只有 `create` 一个写入口。用户换个窗口说「改一下刚才的灵阅」，模型既拿不到 id、也读不到旧 HTML，只能新建（继续堆）或反问。这是"能写不能读"的直接后果。
  - 决定：加 `list`（用户级最近 10 页，id/标题/摘要/时间；`limit` 默认 10、上限 30）与 `get`（读回一整页原文），都声明进 `ReadOnlyFuncs`（`{list, get}`），plan 模式也能查。
  - 决定：`model.Artifact` 加 `source_html`（模型给的原文，图片保留 `image:<id>`），`get` 读它而不是 `html` 列——后者图片已内联成几 MB 的 `data:`。`get` 读回上限 128KB，超了报错而不是截断。**该列不加 `not null`**：SQLite 的老库升级不接受「ADD COLUMN NOT NULL 无默认值」，本地版会起不来。
  - 决定：改版流程写进工具描述：`list` 找 id → `get` 拿原文 → 改 → 带 `artifact_id` 调 `create`。
  - 决定：改版后已开着的右栏 tab 由 `ArtifactOpenCoordinator.reloadIfOpen` 静默重拉（原地覆盖不加新 id，不重拉就永远显示旧版）。
  - 影响文件：`server/.../model/artifact.go`（+`source_html`）、`tool/html_artifact.go`（+`list`/`get`、存原文）+测试、`service/artifact_repo.go`（`Update` 改「先 UPDATE，0 行再回查」、+`GetMeta`/`GetForRead`）+测试、`controller/artifact_test.go`（批量删除契约）；`client/.../Services/ArtifactOpenCoordinator.swift`（+`reloadIfOpen`）、`ViewModels/ChatViewModel.swift`（改版后刷新）、`ViewModels/ArtifactViewModel.swift`（按 100 分块）、`Views/ArtifactListView.swift`（勾选圈不挡点击、无障碍标签、选择态裁剪）。
- **2026-10-29 · 改版不再覆盖标题/摘要**
  - 起因：原地覆盖上线后，模型改版时把 `summary` 写成「同一页加回菜单照片并带点开放大，其余文字沿用上一版」——灵阅列表变成修改日志，summary 失去了「这页是什么」的作用。
  - 决定：`create` 的 `title` / `summary` 从必填改为**新建必填、改版可选**；不传就沿用旧页（`repo.GetMeta` 只取三列）。改版只要求 `html`。
  - 决定：描述写死「改版时不要传 title/summary，除非整页主题真的变了」——默认行为就是对的，不靠提示词自觉。
  - 影响文件：`tool/html_artifact.go`（schema/校验/沿用旧值）+测试、`service/artifact_repo.go`（+`GetMeta`）。
- **2026-10-29 · 同一页在聊天流里只留最新那张卡**
  - 起因：原地覆盖上线后，同 id 仍被写进每一轮消息的 `artifacts_json`，客户端又在每轮末尾追加一张卡 → 用户看到「改一版多一张」，且最新那张在最下面、要往上翻才找得到旧的。
  - 决定：聊天流里同一张卡（`ChatCard.id`）按**最后一次出现**收敛，只在最新那条消息里渲染；旧位置的卡不显示，页面本体与灵阅不受影响。选「留最新」而不是「留最早」：改版后卡就在当前轮下面，不用向上翻。
  - 决定：文档卡同规则（同 id 跨轮也只留最新）。分享多选态保持逐条、不过滤。
  - 影响文件：`client/.../LocalArtifacts/LocalArtifact.swift`（+`ChatCardDedupe`，纯逻辑，落在已有 SwiftPM 源里、不必改工程文件）、`Tests/LocalArtifactsCoreTests/ChatCardDedupeTests.swift`、`Views/MessageListView.swift`（`lastCardOwner` + macOS 归组/iOS 逐条过滤）、`docs/design/html-artifact/design.md`（§9.9、§13）。
- **2026-10-29 · 卡片槽从轮末挪回轮首**
  - 起因：§9.9 最初把槽放在本轮末尾，真机观感是「卡片贴在输入框上方、正文说完了才出现」，与改版前「卡片在助手消息上方」的直觉相反。用户：「原来位置挺好的」。
  - 决定：槽仍按轮收（混排不变），但插到**本轮 user 消息之后、正文之前**；流式期间卡片到达时槽原地增长、正文往下让。
  - 影响文件：`client/.../Views/MessageListView.swift`（`displayRows` 的 `roundStart` + `rows.insert`）；`docs/design/html-artifact/design.md`（§9.9、§13）。
- **2026-10-30 · 细粒度编辑（省 token）**
  - 起因：改版整页进出（读 15–30KB + 写 15–30KB），调五次样式就是 20–40k tokens，且每次都在重写没要动的段落、容易改坏格式。
  - 决定：`get` 增加 `outline` / `block` / `style` / `full` 四种视图；新增 `html.patch`（nid + hash 改文字/换图/CSS，后续同日又加 insert / remove：加删段落与图片也走局部）。nid 现场编号、不落库；hash + 乐观锁防错位；rich 块拒绝文本 patch。
  - 决定：服务端校验是硬要求（归属/nid/hash/rich/图片/样式体积/乐观锁，见 `fine-grained-edit.md` §4.5），不只依赖提示词；系统提示常驻「改版动作选择」。
  - 决定：客户端 `patch` 结果与 `create` 一样挂卡并刷新右栏（`reloadIfOpen`）。
  - 影响文件：`server/internal/util/htmlpatch/`（新）+测试、`tool/html_artifact.go`、`service/artifact_repo.go`（+`UpdateIfSourceUnchanged`）+测试、`agent/promptbuilder/static.go` + `builder.go`、`client/.../ViewModels/ChatViewModel.swift`。
- **2026-10-30 · 灵阅卡片的动作收进右上角「⋯」**
  - 起因：卡片页脚要同时放日期、分享、下载、导出、删除，260 宽的三列卡片放不下；且这几个动作在文档卡片上是另一套摆法（页脚 / 顶栏 / 右键各一处）。
  - 决定：卡片右上角加一颗 `⋯`（`CardConfig.menuActions`，见 `docs/design/card-more-menu/design.md`），菜单项 = 分享链接 / 下载 HTML / 导出 PDF / ─── / 删除；页脚不再放删除按钮。选择态传空数组让位给勾选圈。
  - 决定：撤销分享**不进卡片菜单**（灵阅详情页右上那颗浮钮里的「撤销分享」保持原样，详情页本次零改动）。
  - 决定：下载 = 存 `.html` 单文件、导出 = 走 `DocumentPDFExporter.exportHTML`（与文档 PDF 同一条打印分页管线，加载前注入同一份版式基线）。
  - 影响文件：`client/.../Views/CardStyle.swift`、`CardView.swift`、`ArtifactListView.swift`、`HTMLArtifactView.swift`（基线函数放宽为 internal）、`Services/ArtifactOpenCoordinator.swift`（+`copyShareLink` / `downloadHTML` / `exportPDF`）、`DocumentPDFExporter.swift`（+`exportHTML`）。
  - **2026-10-30 补充**：卡片菜单里的「导出 PDF」**只在 Debug（dev）构建出现**（`LocalPaths.isDev`）——大产物的分页/图片解码还没验够，Release 不带；「下载 HTML」照常开放。放开只需删掉调用点的 `if`（见 `docs/design/card-more-menu` §9）。
- **2026-10-30 · 灵阅详情左侧加换篇列表**
  - 起因：灵阅攒成库后，从一篇看另一篇只能回面板再点，还要重新找位置；文档详情早有左侧「文档列表」侧栏。
  - 决定：tab 内容包一层 `ArtifactDetailView`（左 220 侧栏 + 页面），侧栏只有「灵阅列表」一种形态（灵阅是任意 HTML 且 `javaScriptEnabled = false`，没有标题大纲可做）；收起态 / 窄栏自动收 / 当前项钉行 / 滚到底续拉，交互与样式沿用 `document-sidebar-modes`。
  - 决定：点行 = `ArtifactOpenCoordinator.open(id:title:)`（已开切过去、没开新开 tab），**不做原地替换 tab**——避免给 `RightSidebarManager` 加 tab 身份迁移，也保住「两篇并排对照」。
  - 决定：列表数据复用 `ContentView` 那份 `ArtifactViewModel`（coordinator 注入），侧栏在 `items.isEmpty` 时自己补一次 `load()`；骨架 tab 也带侧栏，点开就能连续翻。
  - 决定：`HTMLArtifactView` / `ArtifactLoadingView` 的 `.ignoresSafeArea()` 上移到容器（macOS 在 `ArtifactDetailView`，iOS 在 `ChatView` 的 sheet 补一个），行为不变。
  - 影响文件：`client/.../Views/{ArtifactDetailView(新),SidebarNavRow(新),DocumentListView,HTMLArtifactView,ArtifactLoadingView,ChatView,ContentView,LayoutConstants}.swift`、`Services/ArtifactOpenCoordinator.swift`、`ViewModels/ArtifactViewModel.swift`（`total` 公开读）；详见 `docs/design/artifact-nav-sidebar/design.md`。
