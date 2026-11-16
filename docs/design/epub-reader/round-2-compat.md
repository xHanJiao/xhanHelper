# EPUB 阅读器 · 第二轮：语义兼容与阅读体验（批 16–18）

- 日期：2026-11-15
- 状态：方案定稿，待实施
- 主文档：`docs/design/epub-reader/design.md`

估算（开工）：16 面 / 4 缝（0 条需先建探针）/ 3 次真机往返 / 1 个待拍决定 · ~9–13h
估算（定稿）：25 面 / 3 缝（0 条需先建探针）/ 4 次真机往返 / 0 个待拍决定 · ~10–15h

> 口径见 `~/.config/opencode/skill/ai-estimation`。探针已存在（`book-reader-probe.sh` / `check-reader-html.sh` / `paged-reader-probe.sh` / `snapshot-reader.sh`），所以缝只算真机才能判的部分。

---

## 一、背景与范围

第一轮把「能读」做完了：解包、目录、分节、翻页、划线、进度。但渲染层只做了「去壳 + 统一排版」，EPUB 语义基本没接。第二轮按三批补齐：

| 批次 | 主题 | 目标 |
|---|---|---|
| 16 | 阅读语义 | 脚注/尾注、书内链接、外链、内联图片、`srcset`、破图、`audio/video`、基础标签样式 |
| 17 | 导入兼容 | 字体混淆误判、封面页书、扉页标题、解析健壮性、`lang`、结构重算后的批注/进度迁移 |
| 18 | 阅读体验 | 目录滚到当前/折叠、全书百分比、宽表、书签 |

**明确不做**（沿用第一轮结论）：竖排、RTL、固定版式/漫画、出版社 CSS 全量接管、隐藏内容还原、跨节划线/跨节查找、MathML、TTS。

**为什么不做引擎替换**：Readium Swift 只支持 iOS（维护者 2026-04 明确无 macOS 计划）；foliate-js 能用在 WKWebView，但它会换掉分页/批注/进度模型（iframe + CFI），等于重写阅读器层。第二轮只抄它的**脚注识别规则**（MIT），不引入渲染器。

---

## 二、已拍板的决定

| # | 决定 | 取值 |
|---|---|---|
| D1 | 注释弹层形态 | **底部卡片**（宽度跟正文列，贴底 16pt，max-height 38vh） |
| D2 | 跨文件尾注 | **一起做**；用 `noteFile` 读文件 → JS 取 `#id` → 弹层，不跳节 |
| D3 | 书签入口 | 侧栏「目录 / 书签」双 tab；顶栏书签按钮 + `⌘D` 添加/取消 |
| D4 | 外链 | 点击**直接**打开系统浏览器，只放行 http/https/mailto |
| D5 | 目录默认态 | 全展开；折叠状态不跨会话持久化 |
| D6 | 宽表 | 探针不过就降级为「滚动模式横滚 + 翻页模式缩字号」 |

---

## 三、批 16：阅读语义

### 16.1 脚注识别（移植 foliate-js `footnotes.js` 的规则）

参考实现：<https://github.com/johnfactotum/foliate-js/blob/main/footnotes.js>（MIT）。只抄判定表，不抄它的 iframe 渲染。

```
引用类型 refTypes  = biblioref / glossref / noteref
引用角色 refRoles  = doc-biblioref / doc-glossref / doc-noteref
目标类型           = biblioentry / definition / endnote / rearnote / footnote / note
无类型兜底 maybe() = 链接自身是 <sup>、唯一子元素是 <sup>、或父元素是 <sup>
                    且不是 backlink（backlink / doc-backlink）
隐藏规则 hidden    = 目标是 <aside> 且类型是 footnote
```

**本地适配（关键）**：foliate 用 XHTML 解析器，`getAttributeNS('http://www.idpf.org/2007/ops','type')` 能拿到 `epub:type`；我们的正文是 `innerHTML` 进 HTML 文档，`epub:type` 只是**字面属性名**。判定必须写成：

```js
function epubTypes(el) {
  var raw = (el.getAttribute && el.getAttribute('epub:type'))
         || (el.getAttributeNS && el.getAttributeNS('http://www.idpf.org/2007/ops', 'type'))
         || '';
  return raw.split(/\s+/).filter(Boolean);
}
```

- 属性值要按空白切分（`epub:type="footnote footnote-1"`）。
- `role` 同样按空白切分。
- **不要依赖 `getComputedStyle` 的 `vertical-align`**：出版社 CSS 在检测前已被剥掉，只能靠 `<sup>` / 父元素 / 类型判断。
- `maybe()` 只在 `href` 以 `#` 或「相对 xhtml + fragment」时启用；外部链接不参与。
- `maybe()` 命中的目标还必须「像注释」：带注释类型，或位于 `.duokan-footnote-content` / 尾注容器里；否则按普通锚点处理，避免把 `<sup><a href="#s1">1</a></sup>` 这种普通引用弹成注释。
- 只隐藏 `aside[epub:type~=footnote]`。**不要隐藏 `li` / `section` 尾注**——尾注页本身还要能读。
- 隐藏方式：加 `.book-note`（`display:none !important`），**不移除 DOM**，保证 `anchorElement` / 目录锚点仍能找到它。

### 16.2 注释弹层

DOM（模板里常驻，doc 模式不显示）：

```html
<div id="note-scrim" class="ui" hidden></div>
<div id="note-pop" class="ui" role="dialog" aria-label="注释" hidden>
  <div class="note-head"><span>注</span><button id="note-close">×</button></div>
  <div class="note-body"></div>
</div>
```

行为：

- 点注标 → 取目标内容 → `harden` → 塞 `.note-body` → 显示；scrim 在卡片下方铺满，点击关闭（`.ui`，不触发翻页）。
- 关闭路径：scrim、×、Esc、滚动、翻页（`turnPage`）、换节（`applyChapter`）、切阅读模式（`setReadingMode`）、换字体（`setTypo` 后重排会让锚点漂移）。
- 注释内容：`aside` 里的 `ol > li` 每个转一段；`li`/`div` 取 `innerHTML`；空的显示「（这条注释是空的）」。
- 注释里的 `<a>` 一律剥掉（弹层里点链接没有明确语义），图片先按注释所在目录重写。
- 卡片风格：实色纸底 + 1px 描边 + 阴影，**不用 backdrop-filter**（WKWebView 与非不透明窗叠加不可靠）。浅色 `rgba(255,255,255,.96)`，深色 `rgba(28,28,30,.96)`，文字用 `--text`。
- 字号 `calc(var(--fs) * 0.9)`，跟随 Aa 设置。
- 注标：图片型图标收成 `width:1em;height:1em;vertical-align:-.12em`，`alt` 改为「注」；文字型保留原数字。`role="button"`，`aria-label="注释"`。

### 16.3 `noteFile`：同文件跨节 / 跨文件

问题：大章按节切片后，当前 DOM 只包含本节；`href="#fn1"` 的目标可能在同文件的其他节，或另一个文件（常见 `notes.xhtml`，还可能 `linear="no"` 被排除在 `chapters` 外）。

协议：

```
JS  → Swift: { type: 'noteFile', href: '#fn1' | 'notes.xhtml#en-1', frag: 'en-1' }
Swift → JS: reader.showExternalNote(html, dir, frag)
```

Swift 侧：

1. `href` 为空或以 `#` 开头 → 用**当前章节的 href**；否则以当前章节目录为 base，`EpubArchive.normalize` 归一。
2. 拒绝 `content/` 之外的路径；扩展名只收 `xhtml` / `html` / `htm`；文件 ≤ 5MB（超限 toast）。
3. 用 `BookStore.chapterHTML` 读（自带 UTF-8 → 嗅探回退）；失败 `log` + toast。
4. 读文件**不查 `chapters`**，所以 `linear="no"` 的尾注文件也能读。
5. `noteRequestToken`（UUID）：连点两个注标时只认最后一次响应。

JS 侧：

1. `DOMParser().parseFromString(html, 'text/html')`；
2. 按注文件目录重写相对图片（`rewriteResources(root, dir)` 重构成可复用）；
3. 剥 `style` 属性；`harden`；
4. `getElementById(frag)`（找不到再按 `name`），取内容 → 卡片；
5. 找不到 → toast「这条注释没找到」。

### 16.4 书内链接与外链

`rewriteResources` 现在把非 `#` 的 `href` 全删（`DocumentReaderView.swift:2138`），而且 `Coordinator.decidePolicyFor` 取消一切导航（`BookReaderView.swift:566`）。所以链接只能走 JS 消息：

| 链接 | 处理 |
|---|---|
| `#frag`（同节） | `scrollToAnchor(frag)`；找不到再按 16.3 读当前章整文件 |
| 相对 `*.xhtml[#frag]` | 保留 `data-book-href`；点击 → `bookLink` → Swift 按「当前章目录 + 归一」找章节 → `loadSection(anchor:)` |
| `http(s):` / `mailto:` | 保留 `data-external`；点击 → `openExternal` → Swift 校验 scheme（白名单）→ `NSWorkspace.open` |
| 其他（`file:`、`javascript:`、`data:`、未知） | 删 href，保留文字 |

- 点击分发顺序：`.fn-ref` → 外链 → 书内链接 → 图片 → 划线/翻页分区。全部 `preventDefault` 后 `return`。
- 翻页模式的点击分区已经识别 `a`（`onWidget`），不会抢。
- 外链失败：日志 + toast；不打印完整 URL（只记 scheme + host）。

### 16.5 内联图片

现状 `html.book #content img { display: block; margin: 18px auto; }` 把所有图片块级化，注标、行内符号全被顶成单独一行。

新规则：

- 默认 `display: inline`，`max-width: 100%`，`height: auto`。
- 加 `.fig` 才 block + 居中：
  - 在 `figure` 里的主图；
  - 父元素（`p`/`div`/`section`/`li`）除图片、`br`、空白之外没有别的内容。
- `.fn-ref` 里的图永远 inline。
- 内联 `<svg>`：`max-width: 100%; height: auto`；`figure` 里的 svg 同 `.fig`。
- 注标图标、行内公式图、场景分隔符都走这条。

### 16.6 小件

| 项 | 做法 |
|---|---|
| `srcset` | 逐个候选重写（按 `,\s` 切，跳过 `data:`；保留 `2x`/`480w` 描述符）；`picture > source` 同样处理 |
| 破图 | 注入后挂 `error`，并检查 `img.complete && naturalWidth === 0`；注标失败直接隐藏，正文图显示「图片加载失败」占位 |
| `audio/video` | `harden` 里连同 `source/track` 一起移除（CSP 也放不了，留着只是空壳） |
| 基础标签 | `ruby`（`rt` 0.5em + 行高 1）、`figcaption`（0.88em 次级色居中）、`dl/dt/dd` |
| `<html lang>` | 放批 17，跟 `BookMeta.language` 一起 |

### 16.7 探针

`book-reader-probe.sh` 新增一本假章节（Duokan + 标准 aside + 文字注标 + 图片注标 + 跨文件注标 + 外链 + 书内链接 + 内联图 + 独占图 + ruby），断言：

1. 注标识别数正确；`.book-note` 存在且 `display:none`
2. 注释文字 `reader.find(...)` 命中 0（`flatten` 已跳过）
3. 点注标 → `#note-pop` 显示、内容正确
4. 点注标不产生 `image` 消息（不弹大图）
5. Esc / 滚动 / 翻页 → 卡片收起
6. 图片注标 `getComputedStyle(img).display === 'inline'`
7. 独占图 `display === 'block'`（`.fig`）
8. 跨文件注标发 `noteFile` 消息
9. 外链发 `openExternal`，且 `javascript:` 不发
10. 书内链接发 `bookLink`；同节锚点走 `scrollToAnchor`
11. `srcset` 全部变成 `file:` 绝对地址

`check-reader-html.sh`（doc 模式 176 条）必须继续全绿——所有新逻辑用 `BOOK` 守卫，所有新 CSS 挂在 `html.book` 下。

---

## 四、批 17：导入兼容（parser v5）

### 17.1 DRM 判定

现状：`META-INF/encryption.xml` 存在就拒（`EpubPackage.swift:29-32`），把**字体混淆**误判成 DRM。

新规则：

1. `META-INF/license.lcpl` 存在 → DRM（LCP）。
2. 解析 `encryption.xml` 的每个 `<EncryptedData>`：`EncryptionMethod@Algorithm` + `CipherReference@URI`。
3. 只放行这两种算法（大小写不敏感、允许末尾斜杠差异）：
   - `http://www.idpf.org/2008/embedding`（IDPF）
   - `http://ns.adobe.com/pdf/enc#RC`（Adobe）
4. **任一条目算法不认识、或缺 URI、或 XML 解析失败 → 继续拒**（保守）。
5. 空清单（`<encryption/>`）→ 放行。

测试 fixture 必须改：现有 `SampleBook.drm()` 用的是 `<encryption/>`，改后会被放行；换成带 AES 条目的清单，并新增「字体混淆可导入」用例。

### 17.2 封面页书

没有 manifest `cover-image` / `<meta name="cover">` 时：

1. EPUB2 `<guide><reference type="cover" href="...">`
2. spine 里 href 含 `cover` 的 XHTML
3. href 含 `titlepage` 的 XHTML

从候选 XHTML 抓第一张：`<img src>` 或 `<image xlink:href|href>`；按该文件目录归一；只收 `jpg/jpeg/png/gif/webp`；文件必须存在。多个候选时优先**图片文件名含 `cover`**，否则取第一个命中。
（原计划的「其次声明尺寸最大」实现时省了：封面页里通常只有一张图，多张时文件名比 `width/height` 属性可靠，属性常缺或写错。）

**只对新导入生效**：`refreshStructure` 不更新 `coverFile`，老书不会自动补封面（要补就得重导）。

### 17.3 扉页/版权页标题

`EpubPackage.parse` 生成章节标题时，无 h1/h2、且目录没覆盖到，才按 href 关键词兜底：

| href 含 | 标题 |
|---|---|
| `cover` | 封面 |
| `titlepage` / `title_page` | 扉页 |
| `copyright` | 版权信息 |
| `colophon` | 版权页 |
| `toc` / `nav` | 目录 |
| 其他 | 第 N 章 |

**不删章节、不重排**——章节下标一变，进度和批注全错位。

### 17.4 解析健壮性

| 问题 | 修法 |
|---|---|
| 整本包在多层 `<div>` 里，切节静默失效 | `planSections` 改成「把**每个**超限的 wrapper 片段逐层展开」，封顶 8 轮。只要不是纯文本就继续下探（单层下探救不了两层容器，真机测试书踩到过）；兄弟片段（章首 `<h1>`）保持原样，`innerPieces` 的偏移本来就加 `innerStart` |
| `<xhtml:p>` / `<xhtml:h1>` 等前缀 | `scanBody` / `topLevelRanges` 的标签名统一取 `split(":").last`；`voidElements`、标题级别、`<xhtml:br>` 都走这个判断；`firstHeading` 正则允许前缀 |
| `<script>/<style>` 里的 `<` 被当标签 | `topLevelRanges` 扫到 `script|style` 就跳到闭合标签，不计内部文本；`scanBody` 已有类似逻辑，抽成共用或各写一份 |
| nav 不是 UTF-8 | nav 改成先 `readText(url)`（UTF-8 → `usedEncoding` 嗅探）再解析；**转成 String 后必须去掉/重写 XML 声明**（源文件可能声明 `encoding="utf-16"`，重新按 UTF-8 编码后声明不匹配会让 `XMLParser` 直接失败）；失败仍退回 NCX |

### 17.5 `<html lang>`

`ReaderHTML.buildBook(typography:language:)` 新增 `language` 参数，白名单校验 `^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`，不合法回退 `zh`。doc 模式保持 `zh`。

### 17.6 结构重算后的批注/进度迁移

v5 可能改变节边界（wrapper/script/prefix）。`refreshStructure` 目前只重算结构，不动批注；`annotations.json` 里存的是 `sectionIndex`，边界一变就可能画不出。

在 `BookStore.refreshStructure` 里做（新旧结构都在手上，且与进度写盘同一条串行队列）：

> 实现说明：`BookAnnotation` 没有节内偏移（只有 quote/prefix/suffix），批注按「所在节的**起点**」换算章内偏移；进度有 `offset`，换算精确。



1. 用**旧** `effectiveSections` 把每条批注/进度换算成「章内字数偏移」：`前面各节 charCount 之和 + offset`（`BookStructureMigration`）；
2. 在新 `effectiveSections` 里找包含该偏移的节，写回 `sectionIndex`（进度连节内偏移一起重算）；
3. 批注的 `chapterIndex` 不一致 → 说明章节结构也变了（不该发生），保留原值并日志；
4. 持久化批注（`saveAnnotations`）；进度并进 `save(meta)`；读书器侧刷新完重读一次批注；
5. 日志：`进度迁移：第 N 节/偏移 X → 第 M 节/偏移 Y`、`批注迁移 N/M 条`。

无变化时是 no-op，成本很低，但能挡住「升级后划线全没了」。

### 17.7 测试

- `swift test`：字体混淆可导入 / AES 仍拒 / 坏 XML 拒 / 空清单放行 / 封面页书 / 三层 wrapper / 前缀标签 / script 里的 `<p>` / nav UTF-16
- 真机：打开《富士日记》触发 v5，确认目录、进度、划线都在；打开《写给大家看的设计书》确认 175 节不变或批注已迁移。

---

## 五、批 18：阅读体验

### 18.1 目录：滚到当前 + 折叠

- `ScrollView` 包 `ScrollViewReader`，每行 `.id(row.id)`；面板 `onAppear` 后下一拍 `scrollTo(currentTOCKey, anchor: .center)`。
- 打开前先展开当前条的**所有祖先**（flat 列表按 depth 回溯）。
- 折叠：`@State collapsed: Set<String>`；渲染时按 depth 跳过被折叠子树：
  ```
  var hiddenDepth: Int? = nil
  for row in allRows {
    if let d = hiddenDepth, row.depth > d { continue }
    hiddenDepth = nil
    append(row)
    if collapsed.contains(row.id), hasChildren(row) { hiddenDepth = row.depth }
  }
  ```
- 有子项的行显示 chevron（独立 Button，不触发跳转）；展开时旋转 90°。
- 默认全展开；状态不持久化（D5）。

### 18.2 全书百分比

- 顶栏 `.help` 追加「全书 42%」；目录头显示「目录 · 174 条 · 42%」。
- 数据用 `controller.bookProgress`（已有，随进度防抖更新），不加新 chrome。

### 18.3 宽表

- `applyChapter` 后给每个 `table` 包一层 `.table-scroll`。
- CSS：`max-width:100%; overflow-x:auto; margin:16px 0; break-inside:avoid`；内层 `table { margin:0 }`。
- 探针：翻页模式下 `wrapper.scrollWidth > wrapper.clientWidth` 且 `pageSpan()` 不变、`turnPage` 仍是一页。
- 不过就降级：只在滚动模式横滚；翻页模式 `table { font-size: .82em }`。

### 18.4 书签

模型（`BookModels.swift`）：

```swift
struct BookBookmark: Codable, Identifiable, Equatable {
    var id: String            // UUID
    var chapterIndex: Int
    var sectionIndex: Int
    var offset: Int           // 节内拍平偏移
    var quote: String         // 当前页首附近 60 字
    var createdAt: Date
}
```

存储：`Books/<id>/bookmarks.json`，复用 `BookStore` 的 `decode`/`writeJSON`（`.atomic`）。
- 缺失 = 空列表；**损坏 = 抛错**，UI 提示且本次不写（镜像 annotations 的防覆盖规则）。

交互：
- 顶栏 `bookmark` / `bookmark.fill` 按钮 + `⌘D`：当前 `pendingOffset` 同节 ±40 字内已有书签 → 删；否则加。quote 由新 JS `reader.snippet(offset, 60)` 取。
- 侧栏两个 tab：「目录 / 书签」。书签行按书内顺序（sectionIndex, offset）排，显示 quote + 节标题，× 删除，点行跳转（`loadSection(sectionIndex, offset:)`）。
- 加载时校验 `book.section(at: sectionIndex)?.chapterIndex == chapterIndex`，不一致就丢弃 + 日志（不猜）。
- 保存失败：日志 + toast；不静默。
- 书删掉 → 连同 `bookmarks.json` 一起删（目录级删除，已有行为）。

### 18.5 测试

`book-reader-probe` 加宽表断言；`snapshot-reader` 看卡片/目录/书签面板；typecheck。

---

## 六、风险登记

### P0（实现时必须一起做）

| # | 风险 | 处置 |
|---|---|---|
| 1 | v5 重算后批注/进度错节，划线静默消失 | 17.6 的章内偏移重映射 |
| 2 | `noteFile` 路径逃逸读任意文件 | `normalize` + 必须在 `content/` 内 + 扩展名/大小上限 |
| 3 | 注文件 HTML 注入 | `DOMParser` 只取目标 + `harden` + 剥 style；CSP 兜底 |
| 4 | `openExternal` scheme 逃逸 | 只放行 http/https/mailto；`URL` 解析后校验 |
| 5 | `bookmarks.json` 损坏被空列表覆盖 | 缺失=空、损坏=抛错且禁写 |

### P1（大概率会咬）

| # | 风险 | 处置 |
|---|---|---|
| 6 | 连点两个注标，晚到的响应覆盖弹层 | `noteRequestToken` |
| 7 | 注释目标在同文件的其他节 | href 为空/`#` 时读当前章整文件 |
| 8 | `linear="no"` 尾注不在 chapters | noteFile 走路径读，不查 chapters |
| 9 | `epub:type` 是字面属性 | 双读法（16.1） |
| 10 | 注标被灯箱抢 | `.fn-ref` 守卫 + `alt="注"` |
| 11 | 注释文字仍进 flatten | skip `.book-note` + probe `find=0` |
| 12 | 无类型 sup 链接误判 | 目标必须注释型/在注释容器里 |
| 13 | 注文件里的相对图片失效 | 先按注文件目录重写再取内容 |
| 14 | 弹层与翻页/滚动互抢 | scrim + 统一关闭路径 |
| 15 | 宽表破坏分栏 | 探针断言 + 降级开关 |
| 16 | v5 首开大书变慢 | 后台重算；失败保留旧 meta；日志 |
| 17 | DRM 规则过宽 | 任一条目不认识就拒；换掉现有空清单测试 |
| 18 | 封面误抓 logo | 文件名/尺寸优先级；只对新导入生效 |
| 19 | 共享模板回归 doc 模式 | `BOOK` 守卫 + `check-reader-html` 176 条 |
| 20 | 目录折叠/滚动 SwiftUI 时序 | 两拍滚动；chevron 独立 Button |
| 21 | 书签 offset 失效 | 加载时校验 chapterIndex，失配丢弃 |
| 22 | `⌘D` 与查找框 | 真机确认，必要时按焦点禁用 |

### P2（记日志即可）

空注释 / 重复 id / 目标缺失；注文件 GBK/UTF-16（`chapterHTML` 已嗅探）；注文件超大；`srcset` 里的 data URI；破图/音视频空壳；ruby 行高；百分比滞后 1–2s；多窗口并发写书签（现有 annotations 同样问题）。

---

## 七、验证

### 自动

| 批次 | 命令 |
|---|---|
| 16 | `book-reader-probe.sh`（两宽度，新增 11 条）+ `check-reader-html.sh` 176 + `paged-reader-probe.sh` 8 + `snapshot-reader.sh` + `swiftc -typecheck` |
| 17 | `swift test`（新增 fixture）+ `swiftc -typecheck`；打开旧书验证重算 |
| 18 | `book-reader-probe.sh`（宽表）+ `snapshot-reader.sh` + `swiftc -typecheck` |

### 真机（一轮打包）

1. 360pt / 728pt 底部卡片（浅/深）
2. 注标 1em 大小与基线
3. 内联图 / 独占图 / 封面 SVG
4. 跨文件尾注（测试书）
5. 外链打开系统浏览器
6. 宽表翻页
7. 目录折叠 + 打开滚到当前
8. 书签加/删/跳 + `⌘D`
9. v5 首开《富士日记》后划线都在
10. 字体混淆书能导入

### 测试书

由 `client/xHelper/scripts/make-test-epubs.py` 生成（输出默认 `~/Downloads/xhan-epub-tests/`）：

| 文件 | 覆盖 |
|---|---|
| `reader-test-main.epub` | Duokan 脚注、标准 aside、文字/图片注标、同文件跨节注释、跨文件 `linear=no` 尾注、外链、书内链接、内联/独占图、ruby、宽表、隐藏内容、`audio` 空壳 |
| `reader-test-cover-only.epub` | 只有 `cover.xhtml`（无 manifest cover-image） |
| `reader-test-font-obfuscation.epub` | 字体混淆 `encryption.xml`（当前应被拒，批 17 后可导入） |
| `reader-test-parser-edge.epub` | nav UTF-16、三层 wrapper、`<xhtml:p>`、script 含 `<p>`、大章切节 |

---

## 八、诊断日志

- `navDiag('notes', {total, hidden, unresolved})`——每章一次
- `noteFile`：字节数、耗时、失败原因
- `bookLink`：目标章节/节
- `openExternal`：scheme + host（不打印完整 URL）
- `refreshStructure` / 重映射：批注 N 条、进度 M 条
- 书签：加载/保存失败原因

统一走 `LogBuffer`，DEBUG 可见；用户可见失败再配 toast。

---

## 九、回滚

- 三批独立提交（`feat(client)`），互不依赖；批 16 可单独回退，注释恢复显示，批注不受影响。
- 批 17 的 `parserVersion = 5`：旧代码读到 5 不会重算（`5 < 4` 为假），meta 字段未变，回滚安全。
- `bookmarks.json` 旧代码忽略，不影响其它功能。
- 批 16 隐藏注释会让进度 offset 小幅前移；回滚后 offset 回到原口径，最多偏一句话。

---

## 十、影响文件

- `client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`（模板：脚注/弹层/链接/图片/srcset/宽表/CSS）
- `client/xHelper/xHelper/Shared/ViewModels/BookReaderController.swift`（`noteFile`/`bookLink`/`openExternal`/书签/重映射/诊断）
- `client/xHelper/xHelper/Shared/Views/Books/BookReaderView.swift`（书签按钮、目录 tab/折叠、百分比）
- `client/xHelper/xHelper/Shared/Services/Epub/EpubPackage.swift`（DRM/封面/标题/健壮性/lang）
- `client/xHelper/xHelper/Shared/Services/Epub/BookStore.swift`（封面/书签/重算日志）
- `client/xHelper/xHelper/Shared/Services/Epub/BookModels.swift`（`BookBookmark`）
- `client/xHelper/scripts/book-reader-probe.sh`、`scripts/make-test-epubs.py`
- `docs/design/epub-reader/design.md`（后续变更指针）、本文档

---

## 十一、实施记录

### 2026-11-16 · 批 16 已落地

| 改动 | 位置 |
|---|---|
| 脚注识别（`epub:type`/role 双读 + `<sup>` 兜底 + 目标必须像注释）、`aside` 隐藏、注标标记 | `DocumentReaderView.swift` 模板：`prepareNotes` / `isNoteRef` / `isNoteTarget` |
| 底部注释卡片 + scrim；Esc / 滚动 / 翻页 / 换节 / 切模式 / 遮罩关闭 | 模板 CSS + `#note-pop` / `#note-scrim` + `hideNote` |
| 跨文件 / 同文件跨节：JS 发 `noteFile`，Swift 读文件回传，JS 取 `#frag` | 模板 `showExternalNote` + `BookReaderController.handleNoteFile`（令牌、路径归一、5MB 上限） |
| 书内链接 / 外链 | `bookLink` / `openExternal`；Swift 按当前章目录归一，外链只放行 http/https/mailto |
| 内联图 / 独占图 / 注标 1em | `classifyImages` + CSS `.fig` + `.fn-ref img` |
| `srcset` / 音视频 / 破图 / ruby / figcaption / dl | `rewriteResources` + `watchImages` + `harden` + CSS |
| 页内锚点 | 点击 `a[href^="#"]` → `scrollToAnchor`（此前被 `decidePolicyFor` 取消导航，点了没反应） |

验证（未提交）：

- `book-reader-probe.sh` **22 条 × 2 宽度全绿**（新增 7 条：注标识别/隐藏、卡片打开、三种关闭、跨文件 `noteFile`、链接分发、内联/独占图、srcset/音视频）
- `check-reader-html.sh` 176 条 PASS（doc 模式未回归）
- `paged-reader-probe.sh` 8 条 PASS
- `snapshot-reader.sh` 完成
- `swiftc -typecheck -D DEBUG` 0 error

已知未覆盖 / 后续：

- `handleNoteFile` 的**真实读文件渲染**只有单测级路径检查，探针只断言到 `noteFile` 消息；需要真机导入 `reader-test-main.epub` 看卡片内容与跨文件尾注。
- 卡片观感（360pt / 深色 / 字号）没有截图验证，按设计先用实色底 + 描边。
- `#note-scrim` 是透明遮罩，确认不会挡住顶层 SwiftUI chrome（它只在 WebView 内）。
- 批 17（DRM/封面/解析/迁移）与批 18（目录/书签/宽表）未开始。

### 2026-11-16 · 批 17 已落地（parser v5）

| 改动 | 位置 |
|---|---|
| DRM 判定：`license.lcpl` 直接拒；`encryption.xml` 逐条看算法，只放行 IDPF / Adobe 字体混淆；缺 URI / 未知算法 / 坏 XML 仍拒；空清单放行 | `EpubPackage.parse` + `XMLScan.encryptionIsDRM` |
| 封面页书：manifest 没封面时，guide cover → spine 里 href 含 `cover` → 含 `titlepage` 的 XHTML 抓首图，优先图片名含 cover | `EpubPackage.coverFromPage` + `HTMLText.firstImageSrc` |
| 扉页/版权页标题：无 h1/h2 时按 href 兜底（封面/扉页/版权信息/版权页/目录，其余「第 N 章」） | `HTMLText.chapterTitle` |
| wrapper 切节：把每个超限片段逐层展开（不再只认「顶层只有一个片段」），封顶 8 轮 | `HTMLText.planSections` + `innerPieces` |
| 前缀标签：`<xhtml:p>` / `<xhtml:h1>` / `<xhtml:br/>` 一律按本地名处理 | `HTMLText.localTag`（scanBody / topLevelRanges / headingLevel / firstHeading 正则） |
| `<script>` / `<style>` 里的裸 `<`：整段跳过，不再切错片段 | `HTMLText.topLevelRanges` |
| nav 非 UTF-8：先 `readText`（UTF-8 → 嗅探）再解析，并去掉 XML 声明（声明写 utf-16 但重编码成 UTF-8 会让 XMLParser 直接失败） | `XMLScan.navTOC(_ text:)` |
| `<html lang>` 用书的 `dc:language`（BCP47 形状白名单，不合法回退 `zh`） | `ReaderHTML.buildBook(typography:language:)` |
| 结构重算后的批注/进度迁移（章内字数偏移口径） | `BookStructureMigration` + `BookStore.refreshStructure` / `migrateAnnotations` |

验证：

- `swift test`：**490 条全绿**（新增 11 条：字体混淆两种算法 / 空清单 / 坏 XML / 封面页书 / 前置页标题 / 两层 wrapper / 前缀标签 / script 裸 `<` / nav UTF-16 / 迁移纯函数 / 刷新后批注+进度迁移）
- 本机 4 本测试书实跑解析：`font-obfuscation` 能导入；`cover-only` 抽到封面且标题为「封面」；`parser-edge` 目录恢复（nav UTF-16）+ 正文切成 6 节；`main` 的大章 `big.xhtml` 由 1 节变成 8 节（锚点全部对得上）
- `book-reader-probe.sh` 22 × 2、`check-reader-html.sh` 176、`paged-reader-probe.sh` 8 全绿；`swiftc -typecheck -D DEBUG` 0 error

已知未覆盖 / 后续：

- 真实书库里「老书打开触发 v5」没有真机跑过：本地旧书的批注是否都在、进度是否落回同页，需要用户拿真实书验一次。
- 字体混淆只放行不解密：阅读器用系统字体，混淆字体不会被使用，属预期。
- `refreshStructure` 不补封面（设计如此）：老书要新封面只能重导（重导会丢批注）。

