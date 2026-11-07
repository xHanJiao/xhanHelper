# HTML 产物 · 细粒度编辑（省 token 的改版路径）

- **状态**：**已实现**（2026-10-30）
- **上位文档**：`docs/design/html-artifact/design.md`（功能全貌、数据表、卡片/右栏/分享）
- **一句话**：改版不再整页读、整页传，而是按「块 / 图片 / 样式」精确定位，只读写要改的那一小块；排版自由度一点不降。

---

## 一、问题

### 1.1 现状

改版目前是一条「整页进出」的路：

```
html.get  → 返回 source_html（模型原文，图片是 image:<id>）
模型改    → 整页重写
html.create(artifact_id) → 覆盖整页
```

`source_html` 的体积（实测）：

| 来源 | 大小 |
|---|---|
| 桌面实验页（10 个） | 4.8 KB – 19.1 KB |
| dev 库「下北泽 · 第一晚的居酒屋」 | 3.6 KB |
| dev 库「HTML 工具回归记录」 | 3.9 KB |
| 更复杂的手账/财务页 | 10–19 KB |

一次改版 = 读一遍 + 写一遍，**8–30 KB 在上下文里进出**（约 3–8k tokens）。
调五次样式就是 **20–40k tokens 的纯改版开销**，而且：

- 历史回放对 `role=tool` 按 20000 字符截断（`truncateToolResult`），大页跨轮又得重新 `get`；
- 模型每次都在重写没要改的段落，**改错/改丢格式的概率随页长上升**；
- 页面越精致（CSS 越长），改一句文字越贵。

### 1.2 根因

`source_html` 是一个**不可寻址的整体**。模型要改第 3 段，只能拿到整页、改完再还回整页——没有任何「只要这一段」的接口。

### 1.3 目标 / 非目标

**目标**

- 改一段文字、换一张图：读写都 < 1 KB
- 改颜色/字体/间距：只读写 `<style>`，正文不动
- 排版自由度不变（页面仍是模型写的自由 HTML）
- 页面渲染、分享、灵阅、卡片**零改动**

**非目标**

- 不做「内容 JSON + 模板」的块模型（原因见 §3）
- 不改客户端、不改 `artifacts` 表结构
- 不做多人协同编辑、不做版本历史
- 不做样式规则的逐条编辑（改 CSS 仍是整段替换）

---

## 二、候选方案

| # | 方案 | 核心 | 结论 |
|---|---|---|---|
| A | 模板 + 内容 JSON（块模型） | 内容存结构化块，模板存骨架/CSS | 否决（表达力不够，见 §3） |
| B | **细粒度节点读写** | 整页仍是 HTML，但按 nid / style 精确定位 | **选定** |
| C | 内容/样式两字段（不拆块） | `content` 存纯文本、`style` 存 CSS | 否决（同 A，且模板仍要表达结构） |
| D | 让模型产出 diff / 查找替换 | 模型给 `old → new` | 否决（定位不可靠，见 §3.3） |

---

## 三、选型依据（基于现有页面）

### 3.1 现有页面长什么样

对桌面 10 个实验页做结构统计（`html.parser` 深度 + 版式 API）：

| 页面 | 大小 | DOM 深度 | flex/grid 处数 | `grid-template-columns` |
|---|---|---|---|---|
| daybook-minimal | 4.8 KB | 6 | 4 | 6 |
| daybook-collage | 7.0 KB | 7 | 3 | 2 |
| daybook-night | 7.2 KB | 7 | 5 | 6 |
| daybook-driving | 9.3 KB | 8 | 7 | 8 |
| tokyo-daybook | 11.0 KB | 8 | 7 | 3 |
| tokyo-daybook-riso | 10.2 KB | 8 | 7 | 4 |
| tokyo-daybook-bullet | 8.0 KB | 9 | 3 | 3 |
| fin-2026-10 | 14.9 KB | 8 | 14 | 10 |
| fin-2026-10-interactive | 17.3 KB | 7 | 17 | 9 |
| ai-ui-paths | 9.3 KB | 9 | 6 | 2 |

实际用到的结构单元：

- 页头（kicker / 大标题 / 日期 meta）、导语段（比正文大一档）
- 图片 + 图注 + **纯 CSS `:target` 放大层**
- 多栏 / 卡片网格（`display:grid`，每页 2–10 处）
- 时间线（竖线 + 时间戳 + 标题 + 正文 + 状态点）
- 提示框 / 引用块、标签 / 内联高亮（`span.tag`）
- 有序/无序列表、表格（财务页是 `div` 拼的）
- 页脚

dev 库里的两页新格式同样如此：一个是时间线（`.tl` / `.item`），一个是手账（`figure` + `.rows` + `.cols` + 放大层）。

### 3.2 为什么否决 A（块模型）

要把上面这些搬进「内容数组 + 模板」，块类型至少要覆盖：

```
header(kicker,title,meta) / lede / section(h?, blocks) / paragraph
figure(image,caption,zoom) / list / table / quote|box / timeline(items)
columns(n, [blocks]) / tag / divider / footer
```

而且 `columns`、`section` 必须支持嵌套——否则双栏/卡片做不了。

代价：

1. **等于重写一个排版 DSL**：块渲染器 + 嵌套规则 + CSS 约定，约 400 行起，且以后每加一种玩法都要加块类型；
2. **模型写 HTML 的强项被浪费**：现在这些页好看，正因为模型能自由用 `div` + CSS；
3. 块模型对「把照片斜贴、卡片错落、时间线带状态点」这类版式表达力不足，强行塞会做出「填空题」风格；
4. 内容/样式分离的收益，B 也能拿到，且更省（B 的 patch 请求比一份内容 JSON 还小）。

### 3.3 为什么否决 D（diff / 查找替换）

- 模型写 `old/new` 时要做 HTML 转义、空白匹配，命中率不可靠；
- 原文里重复段落会替换错位置；
- 为了让模型写出准确的 diff，仍然得先给它整页上下文——**token 一点没省**。

### 3.4 为什么选 B

- 页面仍是自由 HTML，现有观感/版式零损失；
- 改一段文字：`outline`（1 KB）+ `ops`（几百字节），比块模型的一份内容 JSON 更小；
- 不动表结构、不动客户端、不迁移数据；
- 老页面同样适用（只要有 `source_html`）；
- 实现面窄：一个 HTML 解析/定位包 + 三个动作 + 校验。

---

## 四、方案 B 具体设计

### 4.1 数据模型：不变

`artifacts` 表继续用：

- `html`：成品（模板 + 内容 + 图片已内联），给客户端/分享；
- `source_html`：模型原文（图片是 `image:<id>`），细粒度编辑的唯一操作对象；
- `title` / `summary`：工具可改，走 `patch(title=… / summary=…)`（2026-11-07 起）：不传＝不动，纯元数据修改不重写正文；
- `share_token` / `deleted_at`：不变。

**新增列？没有。**

### 4.2 节点编号（nid）

服务端每次处理时按 DOM 文档顺序现场编号，**不落库**：

| 类型 | 编号 | 判定 |
|---|---|---|
| 文本块 | `t1, t2, …` | `h1–h6 / p / li / td / th / figcaption / blockquote / pre / dt / dd / caption`，且直接子节点只有文本与内联元素（`span/a/b/i/em/strong/code/br`） |
| 图片 | `img1, img2, …` | `<img>` |

每个 nid 附带：

- `hash`：`sha1(tag + "\x00" + 文本)` 前 8 位（图片用 `src` 参与哈希），**防错位**；
- `rich`：块内是否含内联元素（`true` 表示有高亮/链接/code）。

`get` 与 `patch` 必须共用同一个遍历函数，保证编号一致。

### 4.3 工具契约

工具还是 `html`，动作还是三个（`create` / `list` / `get`）+ 新增 `patch`。

#### get

```json
{ "artifact_id": "…", "view": "outline" }           // 默认
{ "artifact_id": "…", "view": "block", "nid": "t3" } // 单块全文
{ "artifact_id": "…", "view": "style" }              // <style> 内容
{ "artifact_id": "…", "view": "full" }               // 整页原文（逃生口）
```

`outline` 返回示例（1–3 KB）：

```json
{
  "blocks": [
    {"nid": "t1", "tag": "h1", "class": "title", "text": "下北泽 · 第一晚的居酒屋", "chars": 13, "hash": "a1b2c3d4"},
    {"nid": "t2", "tag": "p",  "class": "sec lead", "text": "落地第一天，和小吴约在下北泽，坐进一家居酒屋…", "chars": 96, "truncated": true, "hash": "9f8e7d6c", "rich": true},
    {"nid": "img1", "tag": "img", "class": "photo", "image": "image:d20ca32d…", "alt": "居酒屋手写菜单", "hash": "77aa88bb"}
  ],
  "style_bytes": 5210,
  "page_bytes": 3609,
  "tip": "改文字用 html.patch(nid, text)，不要重传整页"
}
```

- 文本块只给首 60 字（`text` 超长时加 `"truncated": true`），拿全文走 `view=block`；
- 图片块给 `image:<id>`；`data:` 直写的图**只给** `"legacy": true` + `"image_bytes"`，**绝不回传 base64**（一张 3–5MB 的图会把 outline 变成几 MB）。
- 每块原样带 `class`（没有就不给，超 80 字截断并附 `"class_truncated": true`——截断了别整段照抄，抄到半截类名对不上）：间距、配色都写在 class 上，模型 `insert` 时要照抄相邻块的它，否则插进来的是裸标签、紧贴上一段。

#### patch

```json
{
  "artifact_id": "…",
  "ops": [
    {"nid": "t3", "hash": "a1b2c3d4", "text": "新的一段文字"},
    {"nid": "img2", "hash": "9f8e7d6c", "image": "<新 image id>", "alt": "新的说明"}
  ],
  "insert": [
    {"after": "t3", "hash": "a1b2c3d4",
     "html": "<figure><img src=\"image:abc\" alt=\"菜单\"><figcaption>今晚的菜单</figcaption></figure>"}
  ],
  "remove": [
    {"nid": "img2", "hash": "77aa88bb"}
  ],
  "style": "…<style> 里的 CSS…",
  "style_hash": "11223344",
  "title": "新标题",
  "summary": "新的一句话摘要",
  "tags": "手账,旅行"
}
```

- `ops` / `insert` / `remove` / `style` / `title` / `summary` / `tags` 至少给一个；
- `title` / `summary`（可选，2026-11-07）：只改元数据，不传＝沿用旧值；trim 后不允许空串或超过 80 / 200 字。可以和块编辑放在同一次请求里（同一条 UPDATE 落库，仍带 `source_html` 乐观锁）；只改元数据时不重写正文（仍会读一次页面行做归属校验、取旧值），**老页面（没有 `source_html`）也能改**；
- `tags`（可选，2026-10-31）：整套替换标签，1–5 个，空串拒绝；不参与块编辑的 hash 校验，也不触发正文重新清洗（只改 `tags` 列）；
- `insert`：插到 `after` 块的后面；`html` 是**块级片段**（figure/div/p/img…），图片写 `image:<id>`，服务端统一清洗 + 内联；单次 ≤10 段；
- `remove`：删掉整块；单次 ≤20 块。删完从该块的父节点向上**收缩变空的容器**（判据见 §4.5「空容器」）：删图不留空相框 + 孤立图注，删光单元格不留 `<table>` 骨架；同一 figure 里还有别的图就不动；
- 同一块不能既在 `ops` 又在 `remove`；`insert.after` 也不能是被删的块；
- 全部校验通过才写入（**事务性**），任何一个失败整次拒绝、不动库；
- 写完重新走 `htmlsanitize` + `inlineImages`，同时更新 `html` 与 `source_html`；
- 返回只回摘要：`{"artifact_id", "title", "summary", "tags", "updated": ["t3", "img2"], "inserted": 1, "removed": 1, "style_updated": true, "page_bytes": 3620}`。`updated` 只列**内容被替换**的块（`ops` 的 nid）；删除块有自己的计数（`removed`），不再混进 `updated`——删掉的块连 nid 都不存在了，混进去只会让调用方误判「哪块只是改了字」。
  带 `title`/`summary`/`tags` 是给客户端的：patch 后同 id 的卡要按「只留最新」收敛到本轮，摘要与标签得跟着更新。

#### create

保持现状。带 `artifact_id` 仍是「整页重写」的逃生口（重排版式、整页换结构），不做限制。

### 4.4 服务端处理流程

```
patch
 → 取 source_html（空 = 老页面；有块/样式改动才报错，纯元数据照常写）
 → 解析/编辑（仅当有 ops / insert / remove / style）
 → 有正文改动时：清洗 → inlineImages → 乐观锁 UPDATE（WHERE source_html = 旧值），0 行 → 「页面已变」
 → 纯元数据（title / summary / tags）：一条 UPDATE 只写这几列，不读整页、不重算 html
 → MarkReferenced（被用到的图续期，仅正文改动时）
 → 返回摘要
```

### 4.5 校验清单（硬要求）

**身份与存在性**

- `user_id` 归属校验；软删页面视为不存在；
- `source_html` 为空（这次改动前生成的老页面）+ 有 `ops/insert/remove/style` → 明确报「这页是早期格式，只能重做」；只改 `title`/`summary`/`tags` 不受影响。

**nid / hash**

- `nid` 必须匹配 `^(t|img)\d+$` 且存在于本次编号表；
- `hash` 必须与实际内容一致（页面被另一端改过 → 不一致 → 拒绝，提示重新 `get`）；
- `rich: true` 的文本块**拒绝** `text` patch（会压平内联高亮/链接），错误里提示「整页重写或先去掉内联格式」；
- 同一请求里重复 nid → 拒绝（避免后写覆盖前写）；
- `insert.after` 必须存在、hash 匹配、且不在 `remove` 里；`remove.nid` 必须存在且 hash 匹配；
- 同一 nid 不能同时出现在 `ops` 和 `remove`（删和改一次做不了，提示分两次）；
- 插入片段：非空、≤64KB、顶层只允许块级标签（`script/style/iframe/meta/link/form` 直接拒绝；散文本也拒绝）；
- 片段**子树里**出现会被安全清洗删掉的标签（`script/iframe/object/embed/form/input/…`，与 `htmlsanitize.dropTags` 同一组）→ 直接拒，文案说明「会被安全清洗删掉」。否则「插入成功」其实是内容被清洗没了，模型会以为插进去了；
- 插入片段顶层的 `tr/td/th/thead/tbody/tfoot/col/colgroup/caption` → 明确报「表格行列不能单独插入，请整张 `<table>` 一起插」（这些标签在 `<div>` 上下文里会被 HTML5 静默丢弃，不单独拦会报成「散文本」误导模型）；
- **锚点落在容器之后**：锚点是 `ul/ol/dl/picture` 的**直接子块**时，落点一律抬到容器**之后**（容器兄弟层级）——锚在列表项上插段落，落在整个列表之后，不会产出 `<ul><p>`。`figure` 只在**锚点是 `figcaption`/`caption`** 时抬（插在图注之后会变成「图注后面还有内容」，非法）；锚在照片上照旧插在 `figure` 内，方便「照片下面加一句说明」并沿用 figure 的内部样式。布局用的 `div` 不抬（卡片里加段落就该留在卡片里）。
- **锚点在表格结构里**：`insert.after` 的父节点是 `table/thead/tbody/tfoot/tr` → 拒绝（`ErrBadAnchor`）。锚在 `td/th/caption` 上插块级片段会写成 `<tr><td>x</td><p>…</p></tr>`，浏览器 foster-parent 把内容挤出表格、位置和样式静默跳走（而 `outline` 看起来一切正常）。文案指出两条出路：加段落锚在**整张 `<table>` 之后的块**上，加行/加格只能整页重写；
- **空容器收缩**（`remove` 之后）：从父节点向上，逐层删掉「已经只剩残渣」的容器，**停在 `<body>`（body/html 永不删）**。两档判据：
  - 删节点时顺手清掉相邻的纯空白文本节点，源码不留空白行（`get(full)` 读回来仍是原缩进）；
  - 通用：子树里没有 `img/picture/svg/video/audio/canvas/iframe/embed/object/hr`，也没有 `figcaption`/`caption` 之外的非空文本；
  - **图注按语义标记认，不按「有没有 nid」放宽**：`figcaption` / `caption` 标签，以及 **class 带 `cap` 词根**（`.cap` / `.caption` / `.cap-note`）且本身不是语义块的元素，里面的**文字**不算内容（跟着图一起收）；但子树里的**图片/视频仍算内容**——图注里放一张小图时，删主图不能把它一起带走。其余文字一律算内容——模型写在 `<div>` 里的正文不会因为删图被带走（见 §五）；
- 数量上限：`ops ≤50`、`insert ≤10`、`remove ≤20`。

**内容**

- `text` 长度 ≤ 8000 字；空字符串允许（清空该块文字）；
- `alt` ≤ 200 字；
- 单次 `ops` ≤ 50 条；
- `image` 必须是 `image:<uuid>` 且属于当前用户（`data:` 一律拒绝）。

**样式**

- `style` 长度 ≤ 64 KB；
- 保存前必须过 `htmlsanitize` 的 CSS 清洗（去外链 `url()` 等）；
- `style_hash` 与当前 `<style>` 内容一致（防错位）。

**元数据**

- `title` / `summary` 传了就 trim；trim 后为空 → 拒绝（这两列没有「清空」语义，不想改就别传）；
- `title` ≤ 80 字、`summary` ≤ 200 字（与 `create` 同一组常量）；
- 与块编辑同一次请求时，跟正文并进同一条 `UPDATE`（仍带 `source_html` 乐观锁）；只有元数据时走单条 `UPDATE`，不校验 hash。

**结果**

- 应用后整页仍须满足 `MaxInlineBytes`（8 MB）；
- `UPDATE ... WHERE source_html = <旧值>` 影响 0 行 → 「页面已变，请重新 get」（乐观锁，防并发覆盖）。

**鉴权与配额**

- `patch` 不计入「每轮 ≤3 张」配额（它不是新页面）；
- 不新增限流；写库失败按现有错误处理规范向上返回并记录。

### 4.6 错误文案（模型据此自愈）

| 情况 | 文案 |
|---|---|
| 页面已变 | `页面已变（另一处改过），请重新 html.get 拿新 nid` |
| nid 不存在 | `没有这个块 t9；先用 html.get(view="outline") 看现有块` |
| rich 块 | `t2 含高亮/链接等内联格式，patch 会丢格式；要改它请 view="full" 后整页重写` |
| 图片失效 | `图片 <id> 内联失败（可能已过期或已删）：换一张或整页去掉这张图` |
| 老页面 | `这页是早期格式（没有原文），只能重新生成` |

### 4.7 提示词工程（三层）

**第一层：系统提示（常驻，不依赖渐进暴露）**

> 修改 HTML 页面时按改动大小选动作，不要默认重传整页：
> - 改文字、数字、换图 → `html.get(view="outline")` 找 nid → `html.patch(ops)`
> - 加一段/加一张图 → `html.patch(insert)`；删一段/删一张图 → `html.patch(remove)`
> - 改颜色、字体、间距 → `html.get(view="style")` → `html.patch(style=…)`
> - 只有重排版式、整页换结构才 `html.get(view="full")` + `create(artifact_id)`
>
> 反例（禁止）：为改一句话读回整页 20KB 再整页重传——慢、贵，还容易改坏没要动的段落。

**第二层：工具描述**——贴「动作选择表」，并写死：

> 改单个文字节点**必须**用 `html.patch`；用 `create` 整页重写会丢掉其他块的格式。
> `patch` 报「页面已变」时重新 `get`，不要拿旧 nid 硬试。

**第三层：工具返回**

- `get(outline)` 结果带 `tip`，并写死「div 里的文字没有 nid（只认标题/段落/列表/表格单元格等语义块），改它请 `view="full"` 整页重写」——模型每次看 outline 都会看到；
- `patch` 的描述里同样写一句（防止模型不读 tip）：只有语义块有 nid，别拿不存在的 nid 反复试；
- `patch` 带了 `insert` / `remove` 时回一句「块编号已重排，继续改请重新 `get(view="outline")`」——hash 是按内容算的、没变，变的是「第几块」；
- 锚点规则写进描述 + tip：**加在页面末尾就锚在页脚之前那块**（锚在 `<footer>` 内部的块上会插进页脚里，样式跟着页脚走）；**别锚在 `td/th` 上**（会插出 `<tr><td>x</td><p>…</p></tr>` 这种非法结构，浏览器重解析后位置会跳）——这条服务端也硬拒（§4.5 `ErrBadAnchor`），提示词只负责让模型少撞一次；
- 插入片段的 `class` **照抄相邻块**（`get(outline)` 每块都带 `class`）——间距/配色都在 class 上，不抄就是紧贴上一段的裸标签；
- `create` 改版成功且整页 > 10 KB 时附 `tip: 下次改文字用 html.patch`；
- `patch` 结果只回摘要，不回整页。

---

## 五、边界与已知限制

- **只有「重排版式」走整页**：增删段落/图片用 `insert` / `remove`；换版式、整页重构才用 `get(full)` + `create(artifact_id)`；
- 表格只能改单元格文字，不能增删行列（行列属于结构重排；整张 `<table>` 可以插）；
- **rich 块不能 patch 文本**：含内联高亮/链接的段落要整页改（以后的扩展点是「按内联片段编辑」）；
- `style` 是整段替换，不能只改一条 CSS 规则；页面原本没有 `<style>` 时，`get(style)` 回空 hash，带 `style_hash=""` 的 `patch(style=…)` 会**新建**一段（不必整页重写）；
- nid 不跨版本：`get` 之后页面被改过，旧 nid/hash 失效；
- 图片只能是 `image:<id>` 引用，早期页面里的 `data:` 图片不支持 patch（outline/block 只给 `legacy + image_bytes`，**不回传 base64**）；
- **`div` 段落 / 单元格不参与 patch**：`textTags` 只收语义块级元素（h1–h6/p/li/td/th/figcaption/blockquote/dt/dd/caption）。现有页面里用 `<div>` 拼的表格、时间线标签只能整页改——刻意不做，`div` 多为布局容器，放开会误伤结构。**已在 `get(outline)` 的 tip 与 `patch` 描述里写死**，模型不会拿着不存在的 nid 反复试；
- **`pre` 不参与 patch**：缩进是内容，归一化会压平；
- **多段 `<style>` 只认第一段**（`get(style)` 与 `patch(style)` 都是）：模型正常只写一段，多段属异常；
- **图注跟着图走**：写成 `<figcaption>` / `<caption>` 或 class 带 `cap`（`.cap` / `.caption` / `.cap-note`）的说明文字，删图时会跟整个相框一起收掉（它们没有 nid，模型删不掉）；但图注里的**图片/视频**算内容，会保住相框。其余文字（哪怕写在 `<div>` 里、没有 nid）都算内容，删图不会带走它；想保住图注就写成 `<p>` / `<h3>`（有 nid），相框会保留、模型自己再 `remove`；
- **`remove` 允许删单个 `td/th/caption`**：会得到列数不齐的表（HTML 仍合法、位置不跳），和 `insert` 侧「表格里不许插」不对称。清空单元格建议把 `text` 改成空串；删行/删列属于结构重排，请整页重写。暂不放宽成硬拒（删表头多余的列是真实需求）；
- **删掉某格/某卡里唯一的内容，那一格/那张卡会一起消失**：`<td><p>甲</p></td>` 删掉 `甲` 就是整格没了（表格会少一列），`<div class="cell"><p>甲</p></div>` 同理。卡片网格上这是想要的（删卡即整张卡消失）；要只清空文字而不删格，得整页改；
- **删到整页不足 200 字节会被拦**：`patch` 会回「删完这页就空了：至少留一块，或用 create 重建整页」；
- **`insert` 不幂等**：同一次 insert 重复提交会插两份（响应丢了 + 模型重试才会踩到）；
- **表格是最后一个块时没法往后追加**：`table` 没有 nid、不能当锚点，而 `insert` 只支持「锚在某块之后」；这种页面只能整页重写（描述里已写明）；
- **把块删光后没有锚点可用了**：只能 `get(full)` + `create` 重建；
- **结构改动后 nid 会顺移**：删掉一块，后面所有块的编号前移（hash 是按内容算的、不变，变的是 nid→hash 的配对），所以 `insert`/`remove` 之后必须重新 `get(outline)`；
- **patch 会把整页图片重新内联**：源图超过 15 天保留期且期间没续期的，改一句话也会因为「图片已过期」整页失败（错误点名 id）。这是沿用现有内联管线的结果，暂不做「从成品 data: 复用」的优化——真频繁触发再做。

---

## 六、兼容

- 老页面（无 `source_html`）：`get` / 有正文改动的 `patch` 明确报「只能重做」，不回退读 `html`（读回来也会超限）；`patch(title=… / summary=… / tags=…)` 不依赖原文，照常可用（2026-11-07 起）；
- 客户端、分享、灵阅、卡片槽、无痕销毁：**零改动**；
- 表结构不变，不需要迁移。

---

## 七、验收剧本

1. **改一段文字**：对已生成的页说「把第二段改短一点」→ 日志只有 `html.get(view="outline")` + `html.patch`，**不出现整页 create**；右栏 tab 自动变新版。
2. **换一张图**：说「把这张照片换掉」→ `patch` 图片 nid；页面图片更新，其余段落一字不变。
3. **改样式**：说「背景换成深色」→ `get(style)` + `patch(style)`；正文块 hash 全部不变。
4. **删掉一节/一张图**：说「第三部分不要了」→ `get(outline)` + `patch(remove)`；删图时内层相框与外层 `.fig`（含胶带、div 写的图注）一起消失，其余段落不动；同一相框里还有别的图时只删那一张。
5. **加一张图**：说「把这张照片加到第二段后面」→ `get(outline)` + `patch(insert)`；插入的片段走清洗 + 内联，图片显示正常。
6. **整页重排**：说「把三栏改成两栏」→ 走 `get(full)` + `create` 整页（允许且合理）。
7. **防错位**：另一端先改一版，再用旧 nid patch → 报「页面已变」，库不动。
8. **大页**：19 KB 的页改一句 → `patch` 前后上下文占用 < 2 KB（对比当前 8 KB+）。
9. **老页面**：对无 `source_html` 的页 `get` → 明确报「早期格式，只能重做」。
10. **删空表格**：把一张表的单元格全删 → `<table>` 骨架一起消失，不留空行。
11. **加到末尾**：说「最后加一节总结」→ 锚在页脚之前那块，新内容落在正文末尾、页脚**之前**（锚在页脚内部的块上会插进页脚里）。

---

## 八、风险

- **模型不守规矩**：仍然整页读+整页写。影响只是「没省到」，功能不坏；靠三层提示词 + 验收剧本 1 观察，必要时把 `create(artifact_id)` 的整页重写加一条「改动很小请用 patch」的提示（不做硬拒，避免卡死正常改版）。
- **nid 错位**：由 hash + 乐观锁兜住；最坏情况是多一次 `get`。
- **rich 块被拒**：含内联标记的段落只能整页改，可能多花一次 token；这是「不静默丢格式」的代价。
- **CSS 整段替换**：改一个小颜色也要传 5 KB 左右；等真嫌大再做「按规则改」。
- **样式清洗**：模型写的 CSS 经过 `htmlsanitize` 的 CSS 清洗，可能和预期有出入（现有行为，不是新增风险）。
- **跨轮拿旧副本整页重写**：乐观锁只覆盖单次请求的读→写；历史里那份 `get(full)` 在第一次 `patch` 后就过期。由 `docs/design/tool-result-history/design.md` 的「回放时把 `get(full)` 换成 stub（当轮仍是全文）」兜住，模型下一轮只能重新 `get(outline)`。

---

## 九、工作量

- 面：**2**（HTML 解析/编号/patch 应用；工具契约与三层提示词）
- 缝：**1**（模型是否真的先用 `outline`——只能真机剧本验）
- 真机往返：**1 次**
- 待拍决定：**0**（块类型、占位符这些已随块模型一起否决）
- 估算：**5–7 小时**

---

## 十、文件影响

| 文件 | 改动 |
|---|---|
| `server/internal/util/htmlpatch/`（新包） | nid 遍历/哈希、文本与图片替换、insert/remove、style 提取替换 + 测试 |
| `server/internal/tool/html_artifact.go` | `get` 增加 `view`；新增 `patch`；工具描述与返回提示 |
| `server/internal/tool/html_artifact_test.go` | patch 成功/校验失败/并发/老页面用例 |
| `server/internal/agent/promptbuilder/` | 系统提示加「改版动作选择」一段 |
| `docs/design/html-artifact/design.md` | §8 增加指向本文的一行；§16 记一条后续变更 |
| 客户端 | `ViewModels/ChatViewModel.swift`：`patch` 结果与 `create` 一样挂卡（同 id 就地更新摘要）+ `reloadIfOpen` 刷新右栏；其余不改 |

---

## 十一、后续变更

- **2026-10-30 · 实现落地**
  - `util/htmlpatch`：Scan / Apply / Style / ReplaceStyle；错误用哨兵值（`ErrHashMismatch` 等），工具层翻成模型能自愈的中文文案。
  - `service/artifact_repo.go`：新增 `UpdateIfSourceUnchanged`（`WHERE source_html = 旧值` 的乐观锁）。
  - `tool/html_artifact.go`：`get` 支持四种 view；新增 `patch`；单次 50 op / CSS 64KB；patch 不计入每轮 3 张配额。
  - 客户端：`ChatViewModel` 的 tool_result 分支同时认 `patch`，结果带 title/summary，挂卡 + `reloadIfOpen`。
  - 系统提示：`promptbuilder` 新增常驻「HTML 页面改版」段（不依赖渐进暴露）。
- **2026-10-30 · 初版设计定稿**
  - 选型：否决「模板 + 内容 JSON」块模型（现有 10 个实验页用到时间线、多栏卡片、标签、放大层等结构，块模型需要 12+ 块类型与嵌套，等于重写排版 DSL 且牺牲观感），采用细粒度节点读写。
  - 决定：nid 现场编号、不落库；`hash` + 乐观锁防错位；`rich` 块拒绝文本 patch。
  - 决定：服务端校验是硬要求（见 §4.5），不只依赖提示词。
- **2026-10-30 · 增删块也走局部（insert / remove）**
  - 起因：patch 只能替换内容，加图/删图仍要整页重写（15–30KB）。手账里删一张照片并不罕见。
  - 决定：`patch` 增加 `insert`（锚点 `after` + 块级 HTML 片段）与 `remove`（删整块）。片段由模型给，class 与页面原有样式一致；服务端只做清洗、内联、位置校验。
  - 决定：删图片时若 figure 只剩这张图（+图注），连 figure 一起删，不留空相框；同一块不能既改又删（分两次）。
  - 决定：提示词三层同步——系统提示、`patch` 描述、`get(outline)` 的 tip 都写明「加/删块用 patch，不要整页重传」。
  - 影响文件：`util/htmlpatch/`（+`ApplyEdit` / `InsertOp` / `RemoveOp`）+测试、`tool/html_artifact.go`（patch 参数与校验）+测试、`agent/promptbuilder/static.go`。
- **2026-10-30 · 删块后收缩空容器（复审第二轮 + 真机反馈）**
  - 起因：① 复审 P1——同一 `<figure>` 里删一张图会连带删掉另一张（`figureOnlyHasImage` 只判「有没有非图内容」，另一张 img 不算内容）；② 真机反馈——`div` 相框删图后留空相框 + 孤立图注、表格单元格删光后留 `<table>` 骨架。
  - 决定：三处同一根因，用一条机制修：`remove` 之后从父节点向上收缩「完全空」的容器（无媒体元素、无图注/说明之外的非空文本），停在 `<body>`。删掉 `figureOnlyHasImage` / `enclosingFigure`。
  - 决定：`insertTags` 去掉 `tr/td/th/thead/tbody` 死条目（`<div>` 上下文里会被丢弃），并对表格族首标签单独报「表格行列不能单独插入，请整张 `<table>` 一起插」。
  - 决定：锚点提示——末尾锚在页脚块上（落在其后）、别锚在 `td/th` 上；插入片段复用页面已有 class。
  - 决定：结构改动后回一句「块编号已重排，重新 get」，不直接把新 outline 塞进响应（省 1–3KB，改完就收工时不吃亏）。
  - 影响文件：`util/htmlpatch/htmlpatch.go`（-`figureOnlyHasImage` +`pruneEmptyAncestors` / `isEmptyish` / `rejectTableParts`）+测试、`tool/html_artifact.go`（描述、tip、`patchResult`）+测试。
- **2026-10-30 · 收缩判据放宽 + body 守卫（复审第三/四轮）**
  - 修：`pruneEmptyAncestors` 在「被删块直接挂 body 下且 body 因此变空」时会把 `<body>` 摘掉（与文档「停在 body」不符）→ 加 body/html 守卫。
  - 修：真机复现「删图后外层 `.fig` + 悬空胶带 + 无图可配的图注留在页面上」——原有判据只认「完全空」，`.cap`（div 文字）让外层永不为空，而 `.cap` 没有 nid、模型删不掉 → 删图时放宽为「没有画面、也没有可寻址文本块」即收缩。
  - 修：`rejectTableParts` 只看首个开始标签，`<div class="row"><tr>…</tr></div>` 能漏过去 → 改成扫全片段 + 跟踪是否在 `<table>` 内。
  - 修：`patch` 删空整页时不再报「HTML 太短，不像完整页面」，改报「删完这页就空了：至少留一块，或用 create 重建」（`htmlsanitize.ErrTooShort`）。
  - 影响文件：`util/htmlpatch/htmlpatch.go`（`content` / `contentOf` / `residue` 取代 `isEmptyish`、`rejectTableParts`）+测试、`util/htmlsanitize/sanitize.go`（`ErrTooShort`）、`tool/html_artifact.go`（删除路径文案）、本文件。
- **2026-10-30 · outline 带 class + 删除不留空白行**
  - 起因：真机反馈两处小瑕疵——① 删块后源码里留空行（肉眼无害，但攒起来、`get(full)` 也难读）；② `insert` 进来的块紧贴上一段、没有间距，因为间距写在原结构的 class 上，而模型手里根本没有 class 可抄（要自己从 `get(style)` 的 CSS 反推 class 名）。
  - 决定：`get(outline)` / `get(block)` 每块原样带 `class`（无则不给，超 80 字截断）；tip 从「复用页面已有 class」改成可执行的「照抄相邻块的 class」。
  - 决定：不做服务端自动复制锚点 class（class 是语义标签，`lead`/`photo` 套到别的块上会同时套错样式）。
  - 决定：删块（含收缩容器）时清掉相邻的纯空白文本节点。
  - 影响文件：`util/htmlpatch/htmlpatch.go`（`Block.Class` + `dropAdjacentSpace`）+测试、`tool/html_artifact.go`（`blockJSON` + tip）+测试。
- **2026-10-30 · 终盘复审收尾（描述与文档去陈旧表述）**
  - 修：`create` / `get` / `list` 三处工具描述与 `design.md` 两处仍写着「只有增删块、换版式才整页重写」，会抵消省 token 的效果 → 统一成「增删块走 `patch(insert/remove)`，只有重排版式才整页」。
  - 修：`htmlpatch.Apply` 生产代码零调用（只有测试在用）→ 删掉，测试改用 `ApplyEdit`。
  - 修：`patch` 删空整页的文案补工具层用例（`TestHTMLArtifactRemoveLastBlockSaysPageWouldBeEmpty`）。
  - 修：`class` 截断附 `class_truncated`，并补用例。
  - 影响文件：`util/htmlpatch/htmlpatch.go` + 测试、`tool/html_artifact.go` + 测试、`design.md`、本文件。
- **2026-10-30 · 跨轮旧副本**：工具结果历史回放的 `get(full)` stub 已实现在 `agent/history_stub.go`（见 `docs/design/tool-result-history/design.md`），本文件 §八 补一条交叉引用。
- **2026-10-30 · 锚点侧校验（第五轮复审 P1）**
  - 起因：真机回归里 `insert.after` 指向表格单元格时服务端成功返回，把 `<p>` 写进 `<tr>`；浏览器 foster-parent 把内容挤出表格、位置跳走，而紧接着的 `outline` 把那个 `<p>` 当普通段落，**看起来完全正常**。前四轮只补了片段侧（`rejectTableParts`），漏了锚点侧。
  - 决定：`insert` 校验段加「锚点父节点 ∈ {table,thead,tbody,tfoot,tr} → `ErrBadAnchor`」，文案给出两条出路（锚到整张 table 之后的块 / 加行加格整页重写）。只拦表格族：`<ul>` 里塞 `<p>` 之类位置不乱，不做内容模型推导。
  - 决定：不做已落库坏页的自动修复（读取侧不重写结构），只能 `view="full"` + `create` 重建。
  - 影响文件：`util/htmlpatch/htmlpatch.go`（`tableContextTags` + `ErrBadAnchor` + insert 校验）+测试、`tool/html_artifact.go`（`patchErrorMessage`）+测试、本文件。
- **2026-10-30 · 真机第六轮（落点 / 页脚 / updated）**
  - 起因：真机回归三处 —— ① 锚在页脚前一块（图注）上，段落直接落进 `<figure>`，页面看着正常但结构错；② 文档与描述说「锚在页脚块上会插进页脚里」，实测落在页脚之后，行为是对的、文案是错的；③ `updated` 语义混乱：把 `remove` 的 nid 也列进去、纯 `insert` 却是空数组，调用方判断不出「哪块只是改了字」。
  - 决定：锚点是 `figure/ul/ol/dl/picture` 的直接子块时，落点抬到该容器**之后**（容器兄弟层级）；布局 `div` 不抬（卡片里加段落要留在卡片里）。锚在表格结构里仍直接拒绝。
  - 决定：页脚锚点文案改正——锚在页脚块上，新内容落在页脚之后。
  - 决定：`updated` 只列 `ops` 命中的 nid；删除看 `removed` 计数，插入看 `inserted`。
  - 影响文件：`util/htmlpatch/htmlpatch.go`（`tightContainers` + `insertPlan.target`）+测试、`tool/html_artifact.go`（描述、`get(outline)` tip）+测试、本文件。
- **2026-10-30 · 真机第七轮（收缩判据 / 首次样式 / 表格删除）**
  - 起因：① 删图时容器里模型写在 `<div>` 里的正文被一起收走（旧判据「没有可寻址文本块就算残渣」）；② 页面原本没有 `<style>` 时改样式死锁——`get(style)` 报错、`patch(style)` 又硬要 `style_hash`；③ 删单个 `td` 被当成漏洞报上来；④ 「单页最多 12 张」被理解成 `<img>` 标签数。
  - 决定：删图收缩不再按「有没有 nid」放宽，改成按语义标记认图注（`figcaption`/`caption` 标签 + class 含 `cap` 词根的非语义块），其余文字一律算内容。
  - 决定：没有 `<style>` 的页允许 `patch(style=…, style_hash="")` 新建一段；`get(style)` 回空 CSS + 空 hash + 提示，不再报错。
  - 决定：`remove` 单个 `td/th/caption` 保持允许（清空单元格用 `text=""`，删行列走整页），写进 §五 已知限制；不改硬拒。
  - 澄清：12 张上限按不同 `image:<id>` 计，描述与 `design.md` 表改为「不同图片」。
  - 影响文件：`util/htmlpatch/htmlpatch.go`（`captionLike` + `residue` 简化 + `Style`/`ReplaceStyle` 新建分支）+测试、`tool/html_artifact.go`（getStyle / patch 校验 / 描述）+测试、`design.md`、本文件。
- **2026-10-30 · 审查 §七 收尾（图注媒体 / figure 抬升 / 片段危险标签）**
  - 修：收缩判据递归进图注子树——图注里的**文字**仍不算内容，但图片/视频算，删主图不再连带删掉图注里的图（与第二轮「删一张连带删另一张」同源）。
  - 修：`figure` 的抬升按锚点类型细分——只有锚在 `figcaption`/`caption` 上才抬出去；锚在照片上留在 figure 内（figure 允许 flow content，figcaption 仍保持首/尾）。`ul/ol/dl/picture` 照旧一律抬。
  - 修：插入片段子树里出现会被清洗的标签（`script/iframe/object/form/…`）直接拒，不再「插入成功但内容被清洗没了」。
  - 修：`insertPlan.after` 死字段删除；`ErrNoStyle` 改名 `ErrNoHead`（`Style()` 无样式已改为返回空）；幂等重放连 `updated` 一起清空。
  - 改：页脚文案改回「锚在页脚之前那块」——锚在 `<footer>` 内部的块上会插进页脚（`TestInsertInsideFooterStaysInside` 钉住）；描述补一句「表格是最后一个块时只能整页重写」。
  - 影响文件：`util/htmlpatch/htmlpatch.go`（`contentOf` 递归、`liftAlways`、`dangerousTags`、`insertPlan`、`ErrNoHead`）+测试、`tool/html_artifact.go`（描述、幂等分支）+测试、`tool/image.go` + `service/image_store.go`（`available`）、本文件。
- **2026-10-31 · patch 支持改标签**
  - 加：`patch(tags=…)` 整套替换标签（1–5 个，空串拒绝）；不传＝不动。只改标签时不重传整页、不触发重新清洗（`newSrc == src` 分支直接 `SetTags`）；无标签老页改正文会返回补标签的 `tip`。
  - 影响文件：`tool/html_artifact.go`（patch 的 `tags` 参数、`validateArtifactTags`、`SetTags` 分支）+测试、`service/artifact_repo.go`（+`SetTags`）、`design.md`、本文件。
- **2026-11-07 · patch 支持改标题/摘要（工具与灵阅对齐）**
  - 起因：`patch` 只有 `tags` 能单独改，标题/摘要想改一句也必须 `get(full)` + `create` 整页重传；灵阅只有 LLM 能写，等于这两列改起来代价极大。同时模型在工具目录里看不到 `html` 就是「灵阅」，用户说「改下灵阅那篇的摘要」时对不上号。
  - 决定：`patch` 加可选 `title` / `summary`，不传＝不动，trim 后空串拒绝，长度沿用 80 / 200。与块编辑可同时给（同一条 `UPDATE` + `source_html` 乐观锁）；纯元数据走单条 `UPDATE`（`SetTags` 扩成 `SetMeta`），不重写正文，老页面也能改。
  - 决定：`Catalog().ShortDesc` 与工具 `Description` 明确写「灵阅就是它」，`create` 描述补「只想改标题/摘要用 patch，不用整页重传」。
  - 影响文件：`tool/html_artifact.go`（`patch` 参数/校验/结果、`Catalog`/`Description`/`create` 描述）+测试、`service/artifact_repo.go`（`SetTags` → `SetMeta`、`UpdateIfSourceUnchanged` 加 title/summary）+测试、`design.md`、本文件。
