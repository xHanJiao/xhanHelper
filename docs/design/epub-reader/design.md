# EPUB 阅读器（一期）— 纯客户端 · 看 + 批注 + 事件

> 状态：**UI 已确认（2026-11-14，chrome 常驻），待开发**
> 范围：macOS 客户端（`client/xHelper`）。**服务端零改动**（唯一例外是文档：附表 A 登记事件）。
> 前置阅读：`docs/design/document-highlight-annotation/design.md`（阅读器模板与批注绘制的由来）、`docs/design/activity-events/`（埋点纪律）。
> 结构演示：`mockup-chrome.html`（HTML，**只用于判结构/变体，不作为像素依据**；最终以 SwiftUI 原型与真机为准）。

```
估算（开工）：6 面 / 3 缝（2 需探针）/ 2 次真机往返 / 1 个待拍决定 · ~38–48h（面×15 粗算，系统性偏高）
估算（定稿）：10 面 / 4 缝（3 需探针）/ 3 次真机往返 / 0 个待拍决定 · ~22–28h（≈330–420 轮 × 4min；翻页探针已过，按含翻页实现计）
```

---

## 一、背景与范围

现在只能读服务端存下来的 Markdown 文档（agent 写的或用户导入的），读不了电子书。目标：

1. **一期**：本地导入 EPUB、按章阅读、划线/批注、记住读到哪、把阅读行为上报到活动埋点。
2. **二期**：agent 通过客户端工具读/搜书、引用书中段落、朗读、划词翻译（见 §十二）。

**一期不做**：书内语义检索、跨设备同步、批注聚类/跨书主题、AI 批注回复线程、iOS、DRM、固定版式书、PDF/漫画书。

### 为什么是纯客户端

| 原因 | 说明 |
|---|---|
| 书只读且不变 | 服务端批注那套三级重定位（`document_anchor.go`）是为“正文会被 agent 改写”准备的；书不需要。客户端 `locate()` 现找即可 |
| 不碰文档实体 | 书不塞进 `Document`，不用新 API、新表、新权限，也不污染文档列表/agent 的 `document` 工具 |
| 图片免图床 | 阅读器 CSP 已放行 `data:`，插图直接内联；封面就是本地文件 |
| 成本 | 服务端改动为零；EPUB 解析用 Go 写也一样是这堆活，放在 Swift 只是换了语言 |

**代价（必须接受）**：书和批注只在本机；换机/重装不可恢复（除非开了本地备份）；agent 只有在你开着 Mac 客户端时才能访问（二期走客户端工具桥）。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| E1 | 平台 | ✅ 仅 macOS。阅读器模板本来就 `#if os(macOS)`；iOS 这一期不接 |
| E2 | 服务端 | ✅ **零代码改动**：书、批注、进度全本机；事件走已有 `/api/events/batch` |
| E3 | 书源 | ✅ 本地 `.epub` 文件；只支持 **reflowable**（可重排版）书 |
| E4 | 原文件 | ✅ 保留 `source.epub`（重导入/取证），与解包目录并存 |
| E5 | 存储位置 | ✅ `LocalPaths.dataRoot/Books/<bookID>/`（dev/prod 分开；本地备份自动带上） |
| E6 | 渲染 | ✅ **直接渲染原书 XHTML**，不转 Markdown；解析交给 JS `DOMParser`（Swift 不写 HTML 解析器） |
| E7 | 章节 | ✅ 一屏一章，按 OPF `spine` 顺序；TOC 来自 EPUB2 NCX / EPUB3 nav |
| E8 | 批注 | ✅ 本地 `annotations.json`，**只存 quote + 前后文**；无偏移、无 `orphaned`、无 AI 线程 |
| E9 | 进度 | ✅ 本机 `meta.json`：章节序号 + 章内扁平偏移；打开即恢复 |
| E10 | 入口 | ✅ 首页九宫格：**「书架」换掉「备忘」**；`BrowsePanel.reminders` 枚举保留（问候行仍可进） |
| E11 | 模板 | ✅ 复用 `DocumentReaderView.swift` 里那一份模板与 JS，加 `book` 模式；**不复制** |
| E12 | 拒绝 | ✅ DRM（`META-INF/encryption.xml`）、固定版式（`rendition:layout: pre-paginated`）明确拒绝并说明 |
| E13 | 图片 | ✅ 单章内联为 data URL；单图 ≤ 8MB、单章合计 ≤ 24MB，超限跳过并在章末提示 |
| E14 | 事件 | ✅ 4 个新 type（导入/阅读的起止）+ 复用 `user.action.saved/deleted`；先登记附表 A 再实现 |
| E15 | 阅读页外壳 | ✅ **chrome 常驻**（顶部那行 + 底部进度条一直在）；不做自动隐藏、不做沉浸浮层 |
| E16 | 阅读设置 | ✅ Aa 弹层：翻页方式 / 字号 / 行距 / 段距 / 页宽 / 字体；**全局生效**（不按书记忆） |
| E17 | 阅读模式 | ✅ 上下滚动 + 左右翻页两种；横向翻页探针**已通过**（6/6，见 §5.7），进一期 |
| E18 | 进度显示 | ✅ 有章节：「第 N/M 章」+ 细条；无章节：**只给条**（数字仅悬停 tooltip）；翻页模式**不显示页码** |
| E19 | 背景 / 划线色 | ✅ 背景跟随 app 浅/深主题；划线保持主题绿单色，多色二期再做 |
| E20 | 无章节 | ✅ 判定只看 spine：**spine 只有 1 项 → 无章节书**（隐章号/目录/章末「下一章」，进度按偏移走）。目录只覆盖 1 章**不**降级 —— 坏 NCX 只列 Part I 时会把后续章藏掉（评审 P1） |

---

## 三、数据与目录

```
dataRoot/Books/
  <bookID>/                        # UUID
    meta.json
    source.epub
    cover.jpg                      # 可选（OPF cover-image / meta[name=cover]）
    content/                       # unzip 原样解包（章节 xhtml、图片、字体、css 全留）
    annotations.json
```

```swift
struct BookMeta: Codable {
    var id: String
    var title: String
    var author: String?
    var language: String?
    var chapterCount: Int
    var chapters: [BookChapter]      // 按 spine 顺序
    var toc: [BookTOCItem]           // 扁平化后的目录（含层级）
    var coverFile: String?           // "cover.jpg" / "cover.png"（保留原格式，不强制转 jpg）
    var sourceBytes: Int
    var sourceSHA256: String         // 原文件哈希：导入查重（实现时补的字段，原稿漏了）
    var importedAt: Date
    var lastReadAt: Date?
    var progress: BookProgress?
}

struct BookChapter: Codable {
    var index: Int
    var href: String                 // content/ 内相对路径（已归一，不含 ../）
    var title: String                // TOC 标题 → 首个 h1/h2 → "第 N 章"
    var charCount: Int               // 近似字数（去标签），只用于进度加权
}

struct BookTOCItem: Codable { var title: String; var chapterIndex: Int; var depth: Int }

struct BookProgress: Codable {
    var chapterIndex: Int
    var offset: Int                  // 当前章扁平文本偏移（rune/utf16 由 JS 口径决定，见 §七）
    var updatedAt: Date
}

struct BookAnnotation: Codable, Identifiable {
    var id: String                   // UUID
    var chapterIndex: Int
    var quote: String                // 选区文本（空白已归一，与阅读器 fold() 同口径）
    var prefix: String               // 选区前 ≤60 字
    var suffix: String               // 选区后 ≤60 字
    var note: String                 // 空 = 纯划线
    var createdAt: Date
    var updatedAt: Date
}
```

规则：

- **只保留目录扫描**：启动时遍历 `Books/*/meta.json`，不建索引文件（避免两份数据不同步）。书不多，IO 可以忽略。
- 写 `meta.json` / `annotations.json` 一律**原子写**（临时文件 + `replaceItemAt`），避免中断留下半截 JSON。
- 删除书 = 删目录；不设回收站（原文件本来就是用户自己的，可重新导入）。
- 导入前按 `source.epub` 的 SHA256 查重：命中就提示「《书名》已导入」，不重复建目录。

---

## 四、导入管线

```
NSOpenPanel 选 .epub
  → 建 Books/<uuid>/，复制 source.epub
  → /usr/bin/unzip -o -q -d content/ source.epub
  → 解析 META-INF/container.xml → 根 OPF 路径
  → 解析 OPF：metadata / manifest / spine / cover
  → 解析 TOC：EPUB3 nav（manifest 里 properties 含 nav）或 EPUB2 NCX（spine@toc）
  → 章节标题映射：TOC href（去 #fragment）→ spine index；缺失回落首个 h1/h2
  → 估算每章字数（去标签，进度加权用）
  → 抽封面
  → 写 meta.json
  → 完成（书架出现，按 lastReadAt/importedAt 排序）
```

要点：

- **解包用系统 `unzip`**（`Process`；`LocalBackup` 已有同类用法）。不自己写 zip 解析。
- `container.xml`、`.opf`、`.ncx` 用 `XMLParser`。EPUB3 的 nav 是 XHTML，可能含 `&nbsp;` 等未声明实体 —— 解析前做一次实体归一（换成数字实体），失败则退回 NCX；再没有就只用 spine 顺序 + 内文标题。
- 路径归一：所有 href 相对 OPF 所在目录解析，再相对 `content/` 归一（`OEBPS/Text/ch1.xhtml`），**拒绝逃出 `content/` 的路径**。
- 失败分类（**卡片 = 徽章 + 一行原因**；完整解释进 tooltip / 详情，不把长句压在封面上）：

  | 情况 | 徽章 | 一行原因 | 完整解释（tooltip） |
  |---|---|---|---|
  | `META-INF/encryption.xml` 存在 | DRM 保护 | 内容已加密，无法读取 | 这本 EPUB 受 DRM 保护，正文被加密，阅读器无法解密。请导入未加密的版本。 |
  | `rendition:layout: pre-paginated` | 固定版式 | 暂不支持按页排版的电子书 | 这本书每页按像素排版（常见于画册/漫画）。当前阅读器只支持可重排版的文字书。 |
  | unzip 非 0 / container 缺失 / 无 OPF | 文件无效 | 不是有效的 EPUB，或文件已损坏 | 文件缺少 EPUB 必需的 container.xml / OPF，可能已损坏或后缀名不对。 |
  | 无 spine / 章节全空 | 没有正文 | 解包成功，但解析不到章节 | 能解包，但 spine 为空或章节文件缺失，没有可读内容。 |
  | 单章 > 20MB / 全书 > 200MB | 文件过大 | 超过 200 MB，暂不支持 | 这本书体积超出导入上限（200 MB）。 |
  | 写盘失败 | 导入失败 | 无法写入本地存储，请检查磁盘空间 | 解包或写入 `Books/` 失败，检查磁盘剩余空间与目录权限。 |

- **不解析正文 HTML**：正文章节保持原样落盘，读的时候交给 JS。省掉一个 Swift HTML 解析器，也躲开 `&nbsp;`/未闭合标签这些坑。

导入是纯本地操作，不产生网络请求；只有事件上报走现有的后台补投队列。

---

## 五、渲染与阅读器改造

### 5.1 复用边界

`DocumentReaderView.swift` 里的模板（`private let template = #"""..."""#`）与 JS 是唯一一份。加 `book` 模式，**不改**这些既有能力：选区工具条、划线绘制（`setAnnotations` / `redrawAnnotations` / `wrapSpan`）、点划线定位（`annotationRect` / `scrollToAnnotation`）、查找、字号、排版锚点（`captureAnchor` / `restoreAnchor`）、`flatten` / `locate` / `harden`。

### 5.2 模板新增

```js
var MODE = __MODE__;              // "doc" | "book"
var bookPayload = __BOOK_JSON__;  // book 模式：章节载荷；doc 模式：null
```

`bookPayload` 结构（Swift 组装，`jsString` 转义）：

```json
{
  "index": 2,
  "title": "第三章 谁动了我的奶酪",
  "html": "<body> 的原样 innerHTML </body>",
  "baseDir": "OEBPS/Text/",
  "images": { "OEBPS/Images/fig1.jpg": "data:image/jpeg;base64,..." },
  "links": { "OEBPS/Text/ch4.xhtml": 3 },
  "total": 12
}
```

book 模式的启动流程（新函数 `renderBook(payload)`，doc 模式仍走 `render(md, images)`）：

1. `DOMParser().parseFromString(payload.html, 'text/html')` → 取 `body`
2. 剥掉 `script/style/link/meta/base/iframe/object/embed/form` 和所有 `on*` 属性、`style` 属性
3. 解析 `img[src]`（相对 `baseDir` 归一）→ 命中 `images` 就换 data URL；没命中就移除并留空 alt
4. 解析 `a[href]`：`#fragment` 保留（章内脚注跳转）；命中 `links` 就设 `data-book-chapter` 并阻止默认导航（点击 post `bookLink`）；其余外链 → post `openExternal`
5. `content.innerHTML = body.innerHTML` → `harden(content)` → `redrawAnnotations()`
6. 端口：`setChapter(payload)`（切章不重载页面，保留 JS 状态与字体设置）

新增接口（挂到现有 `reader` 对象上）：

| 接口 | 用途 |
|---|---|
| `setChapter(payload)` | 切章；替换 `#content` 后自动重画批注 |
| `position()` | `{offset, length}`：**当前页首**字符在扁平文本中的下标（滚动模式取视口顶部，翻页模式取当前栏首） |
| `restorePosition(offset)` | 把该字符带到页首：滚动模式滚 `window`，翻页模式滚到对应栏 |
| `setTypo(opts)` | 字号 / 行距 / 段距 / 页宽 / 字体（CSS 变量，不重载） |
| `setReadingMode(mode)` | `scroll` / `paged`；切换后按当前 offset 恢复位置 |
| `turnPage(delta)` | 翻页模式翻一栏；滚动模式 no-op（Swift 用空格滚一屏）。**已经在头/尾再翻 = 换节**（见 §5.7） |

Swift 侧新增消息：`bookLink`（切章）、`openExternal`（系统浏览器）、`tapZone`（`left` / `center` / `right`，翻页模式用；带 `hasSelection` / `onInteractive` 标记，避免抢走选字和点链接）。进度不新增消息：沿用现有 `scroll`，由 Swift 节流后调 `position()`。

### 5.3 章节标题

模板顶部 `doc-title` 显示**章节标题**，`doc-tags` 显示「书名 · 作者 · 第 N/M 章」。正文里如果章节 XHTML 没有以 `h1` 开头，就补一个 `<h1 class="book-chapter-title">`，避免正文无标题。

章末追加一条「继续 ›」（纯 HTML，`data-book-chapter`），读完直接点，不用回目录。**不是必须点**：到边界再翻/再滚一下就自动进下一节（§5.7），按钮留作备用入口。无章节的书（E20）不追加这一条，`doc-tags` 里也不出现章号。

### 5.4 样式

沿用模板现有 CSS 与设计 token（`--fs` / `--text` / `--accent`），只补两条 book 专用规则：

- 正文图片：`#content img { max-width: 100%; height: auto; border-radius: 8px; }`
- 章节标题：`h1.book-chapter-title { margin-top: 0; }`

**不引入书的 CSS**：出版社样式（缩进、字体、行高、字号）一律丢弃，保持 app 一致的排版。这是有意为之——否则每本书看起来都不一样，字号设置也会被覆盖。

### 5.5 与文档阅读器的关系

模板是共用的 → 改 JS 必须两个模式都验（`scripts/check-reader-html.sh` 加 book 断言）。`ReaderHTML` 从 `private` 放宽到模块内可见，新增 `ReaderHTML.buildBook(...)`；模板仍留在 `DocumentReaderView.swift`，`reader-template.py` 的抠取路径不动，只补两个新占位符的默认值。

### 5.6 排版设置（全局）

CSS 变量全部落在 `html` 上（`:root`），默认值等于现状，所以文档阅读器不受影响：

| 变量 | 默认 | Aa 弹层取值 |
|---|---|---|
| `--fs` | 20.06px（`FontSizeManager` 驱动） | 70%–150%，步进 5%（复用现有字号逻辑，`⌘+/−` 同步） |
| `--lh` | 1.78 | 紧凑 1.55 / 标准 1.78 / 宽松 2.0 |
| `--para` | 11px | 紧凑 6 / 标准 11 / 宽松 18（px，按 `--fs` 定死即可） |
| `--measure` | 728px | 窄 640 / 标准 728 / 宽 860（**正文列宽**：上下滚动是居中列的宽；左右翻页一栏 = 这一列，两种模式版面一致，见 §5.7） |
| `--ff` | 系统栈 | 系统（`-apple-system, PingFang SC…`）/ 衬线（`Songti SC, STSong, serif`） |

- 设置模型 `BookReadingSettings`（`ObservableObject` + `UserDefaults`，全局一份）：`mode`、`lineHeight`、`paragraphGap`、`measure`、`fontFamily`；字号仍归 `FontSizeManager`。
- 弹层 `BookReadingSettingsPopover` 挂在底部阅读栏的 Aa 按钮上；改任一项立刻 `setTypo` / `setReadingMode`，不重载页面。
- 出版社 CSS 依然丢弃；行距/段距由我们统一控制。

### 5.7 横向翻页（探针先行）

**实现形态**：CSS 多栏 + 横向滚动容器（**已实测通过**，见文末探针结果）。

```
.page(滚动模式)        → window 纵向滚动（现状）
.page(翻页模式)        → .page {                     /* 外框 = 列宽 + 左右各 28px 呼吸位 */
                          height: 100%;
                          padding: 0 28px;
                          max-width: min(calc(var(--measure, 728px) + 56px), 100%);
                          margin: 0 auto;
                        }
                        #content {
                          width: 100%;            /* 内容盒 = 一栏 = 页宽 */
                          height: <可视高>;         /* 顶/底 chrome 之外的高度 */
                          margin: 4px 0 0;
                          column-width: 100000px; /* 超大值 → 栏数恒为 1、栏宽 = 容器内容盒宽，
                                                     正文继续向右排成多栏 */
                          column-gap: 64px;
                          column-fill: auto;
                          overflow-x: auto;        /* #content 自己就是 pager，不另包一层 */
                          overflow-y: hidden;
                        }
                        #content img { max-width: 100%; max-height: 100%; object-fit: contain; }
```

- **两种模式列宽必须一致**：`.page` 与 `html.book #content` 用同一套几何（`measure + 56`，居中，28px 呼吸位），切模式版面不跳。内边距放 `.page` 而不是 `#content`：后者是横向滚动容器，左右 padding 只作用于首尾栏，中间每页仍贴边。
- 一页的滚动步长 = 容器内容盒宽 + 64；第 N 栏的 `scrollLeft` = `(N-1) × (栏宽 + 64)`。
- **位置只能是整页**：`turnPage` 按页号算（`round(scrollLeft / 步长) ± 1`），不算像素累加；`restorePosition` / `scrollToAnchor` / 查找命中之后统一走 `snapPage()` 吸附到页边界，否则会停在两栏中间（左右各截一半）。动画途中调 `snapPage()` = 直接落到目标页。
- **翻页有补间动画**（rAF，220ms，ease-out，`prefers-reduced-motion` 时直接跳）。不用 `scrollTo({behavior:'smooth'})`：WebKit 的平滑滚动时长/缓动不可控，也拿不到「目标页」，吸附与探针都没法判断。
- 滚轮/触控板**一次手势翻一页**（`wheel` 里挡掉原生滚动；同一手势的连续事件按 150ms 内视为同一次，另有 350ms 冷却挡惯性）。
- 图片不约束会被分栏切成横跨整条滚动区的一条（实测 2312×631），必须 `max-height: 100% + object-fit: contain`，否则图被静默裁掉。
- 不做的：仿真书页卷曲、双页对开、缩放。

需要改的既有函数（只在 `paged` 分支生效，doc 模式不受影响）：

| 函数 | 翻页模式 |
|---|---|
| `scrollToCurrent`（查找） | 命中 `rect.left` 在视口外 → `content.scrollLeft += rect.left - 页边距`，再 `snapPage()` 吸附到页边界 |
| `captureAnchor` / `restoreAnchor` / `setFontSize` | 不用像素锚点，改用 `position()` → 重排 → `restorePosition()`（末尾同样吸附） |
| `restoreScrollY` | 由 `restorePosition` 取代 |
| 滚轮 / 触控板 | 一次手势翻一页（非 passive 挡掉原生滚动；150ms 内视为同一手势，350ms 冷却挡惯性）；纵向、横向手势都翻页 |
| 点击分区 | 左 28% 上一页 / 右 28% 下一页，中间不拦截（JS 判断无选区、非链接/划线才翻） |
| 节边界 | 已经是末页再翻 → 自动进下一节；第一页再往前翻 → 回**上一节的末页**。判据是「这一下能不能真的挪动接近一整页」，不是「位置是否一字不差等于算出来的 max」—— 末页位置常差几像素（`scrollWidth/clientWidth` 取整、引擎夹取），用等值比较会永远以为还能挪一点点，反复挪 0.7px 而永不换节。滚动模式同理：到底/到顶再多滚一下或再按方向键。换节途中（新正文未到）不再举手，一次拖动只换一次 |
| 图片 / 代码块 | `break-inside: avoid`；图片按上面的 `max-height` 收进一栏 |

**探针（2026-11-14 实测：八条全过，翻页进一期）**

- 脚本：`client/xHelper/scripts/paged-reader-probe.sh`（页面用 `reader-template.py` 从 Swift 源码现抠，样式/划线 JS 与线上同一份；**不内联覆盖栏宽**，测的就是模板 CSS）+ `paged-reader-probe.swift`（离屏真 WKWebView）。
- 结果（`./scripts/paged-reader-probe.sh` 直接可复跑）：

  | 断言 | 实测 |
  |---|---|
  | 0 一屏一栏 | `clientWidth 728（期望 728）`；翻页前后各取样 20 个字符偏移，第 1 页 ≤594 / 第 2 页 ≥704，**不重叠** |
  | 1 多栏生成 | `scrollWidth 3104 / clientWidth 728`，≈3.9 页（列高 671） |
  | 2 跨栏划线 | 正文文本不变；`mark` 文本 == quote；矩形横跨 792px（> 栏宽 728） |
  | 3 跨栏选区 | 120 字选中；折叠空白后与 quote 一致；矩形横跨 792px |
  | 4 查找滚到命中栏 | 命中初始在视口外 → `scrollLeft 2376` → 落在视口内 |
  | 5 字号变化回同一句 | 20→26px 后按 offset 594 恢复，同一字落在当前栏内（翻页模式只能对齐「栏」，栏内纵向位置由重排决定） |
  | 6 大图不撑破 | 不约束时被切成分栏长条；`max-height + object-fit: contain` 后 728×663 收进一栏 |
  | 7 翻页动画 + 停在整页 | 翻完 `scrollLeft` 没瞬间到位（有补间）；`snapPage()` 后落 `0/792/1584` 整页；从半页起点翻也落整页 |

- 结论：**方案成立**，不启用「滚动 + 空格翻屏」回退。真机仍需复验手感（触控板/滚轮/点击分区）。

---

## 六、批注

### 6.1 创建 / 编辑 / 删除（复用 JS，接本地 store）

JS 现有的 `create` / `update` / `delete` 消息原样用。`BookReaderController` 收到后写本地 `annotations.json`，再把整份列表 `setAnnotations` 回去（与文档阅读器“服务端为准、不做乐观更新”同构：**本地文件为准**）。

- 创建：`quote`/`prefix`/`suffix` 由 JS `selectionInfo` 给（与文档一致），落 `chapterIndex`。
- 纯划线（`note == ""`）与划线 + 批注是同一实体（沿用文档的 D3）。
- 配额：单条 quote ≤ 500 字、批注 ≤ 2000 字、单本 ≤ 2000 条（照抄文档的合理性，防手滑）。

### 6.2 绘制

`setAnnotations` 收到全量批注；JS 的 `redrawAnnotations` 本来就会跳过当前 DOM 里找不到的 quote。所以**跨章批注直接全量喂**即可，当前章只画能命中的那些。

多章出现同一 quote 不是问题：只画当前 DOM 里的命中；消歧靠 `prefix`/`suffix`（与文档同一套 `locate`）。

### 6.3 面板与定位

- 左键点划线 → `focus` 消息 → 右侧面板滚动到卡片并闪烁（复用 `AnnotationFocus` 的模式）。
- 点面板卡片：若不在当前章，先 `setChapter(该章)`，再 `scrollToAnnotation(id)`（`ready` 后重试一次）。
- 失活：书不变，正常不会失活。万一 `locate` 失败（比如转换时改了正文），**只在面板里灰态显示、不画线**，提供「删除」；不实现手动重挂。
- 排序：按 `chapterIndex, createdAt`。

### 6.4 面板不复用 `AnnotationPanel`

`AnnotationPanel` 绑死了 `DocumentAnnotation` + AI 回复线程（`replies` / `onReply` / `busy`）。一期书不需要 AI 线程，新建 `BookAnnotationPanel`（~120 行）：波浪线 quote 预览、批注正文、时间、编辑/删除、按章分组。配色复用 `AnnotationHighlightColor`，卡片样式对齐文档面板。

二期若要做“批注下 AI 回答”，再把两个面板抽成泛型，不在这一期预支。

---

## 七、阅读进度

- **唯一口径 = 字符偏移**（不再以章数为分母）。导入时为每章算一个近似字数（去标签，只用于加权画条），存进 `meta.json`；非当前章的字数不会在阅读时重新计算。
- **全书进度** = `(前面各章字数 + 当前章 offset) / 全书字数`。
  - 有章节：标题显示「第 N/M 章」，条按全书进度画。
  - 无章节（spine = 1）：隐章号；条照常；**数字不常驻**，只在悬停进度条时出 tooltip（`42%`）。章末「下一章」、目录按钮、上一章/下一章一起隐藏。目录覆盖率不参与判定（见 E20）。
- **记录时机**：JS 已有的 `scroll` 消息（Swift 侧节流 2s）→ `reader.position()`；切章、切翻页方式、离开阅读页、app 失焦时各存一次。
- **恢复**：打开书 → `setChapter(上次章)` → `restorePosition(offset)`（渲染完成后调用）；翻页模式按同一 offset 找到对应栏。
- **偏移口径**：JS 扁平文本的 UTF-16 下标（`String.length`）。书不变 → 同一章的下标稳定；字号/窗口/翻页方式变化都不影响（`restorePosition` 按字符定位，不按像素）。
- **不显示页码**：翻页模式也不显示「第 2 页 / 共 9 页」——页数随字号改变，是假精度；位置由进度条表达。
- 进度只在本机；不上报服务端状态（事件的 `progress_pct` 只是粗粒度统计快照）。

---

## 八、事件

先登记 `附表 A · 事件字典`（本次同批提交文档），实现时同步 `ActivityEventTypes.known`。

| type | 形态 | 必备 attrs | 说明 |
|---|---|---|---|
| `book.import.started` / `book.import.ended` | 区间配对 | `bytes`, `format="epub"`；结束带 `chapters`, `outcome`, `error_type`, `book_id` | 导入成功率/耗时 |
| `book.read.started` / `book.read.ended` | 区间配对 | `book_id`, `chapter`, `progress_pct`；结束带 `outcome` | 阅读时长 = 配对区间 |
| `user.action.saved` / `user.action.deleted` | 点 | `target_kind="book_annotation"`, `target_id`, `book_id` | 划线/批注的建、改、删；不新增 type |

约定：

- 阅读区间的结束点：离开阅读页、切到别的面板、app `didResignActive`。回到前台且仍在阅读页则开新区间。
- 真正崩溃时区间悬挂 —— 由活动埋点既有的闭合规则兜底，客户端不补假结束事件。
- `book_id` 目前不在服务端资源归一白名单（只进 `attrs`），不影响落库与统计；以后要按书聚合再加。
- 事件采集开关仍由 `ActivityClient.isEnabled` 唯一决定（本地后端或云端能力探测），书能读、事件不能发是允许的降级。

---

## 九、UI 方案

> 结构已定（2026-11-14）。客户端是原生 SwiftUI，按项目规范**不用 HTML mockup 定像素**：下一阶段用真实 SwiftUI 的 `#Preview`（必要时 SwiftPM `ImageRenderer` 出图）复核像素与手感，确认后才写业务逻辑。

### 9.1 首页九宫格

- `homePanels = [.bills, .books, .artifacts, .documents, .events]`（去掉 `.reminders`，加 `.books`；浏览器卡仍在最前）。
- 书架卡：主信息「N 本」；次信息「最近在读 · 书名」；左下三行最近三本（书名 + 章号）。
- `.reminders` 仍可进：问候行整行点击 → 备忘面板（现有行为）；设置里的内容入口也保留。
- 卡片视觉沿用 `HomeEntryCard` + `sidebar_books` 图集资源（新增一个 imageset，风格对齐现有五张）。

### 9.2 书架页（`BrowsePanel.books`）

- 页头：返回 / 「书架」/ 导入（`tray.and.arrow.down`）/ 刷新；导入中按钮变成 `ProgressView`。
- 内容：**封面网格**（按面板宽度 2–3 列；封面 3:4、圆角 10、0.5pt 描边），卡下书名（2 行）+ 作者（1 行）+ 细进度条；hover 右上角「⋯」（重新导入 / 在 Finder 中显示 / 删除）。
- 拖拽：`.epub` 拖到书架页任意位置即导入（`onDrop` + 全页高亮描边）；与按钮走同一条导入管线。
- 空态：一本书都没有时，居中「还没有书」+「导入 EPUB」按钮 + 灰色说明「支持可重排版的 .epub · 也可以直接把文件拖进来」。
- 导入中：网格前插一张「导入中」卡（书名 + 步骤：解包 / 解析目录 / 写入），完成自动消失。
- 失败：同一位置变红字卡片，保留「重试 / 移除」；DRM/固定版式的文案见 §四。
- 排序：最近在读优先，其次导入时间倒序；不做手动排序。
- 搜索：一期不做（书一般不多）；需要时再加页头搜索框过滤书名/作者。

### 9.3 阅读页（书详情）

三栏 + 常驻 chrome：

```
┌────────────────────────────────────────────────────────┐
│ ‹书架 │ 书名   第 3/10 章  ‹ ›  目录  查找  批注          │ topbar（常驻）
├────────────────────────────────────────────────────────┤
│ [章节栏 220 / 收起 32] │ [正文] │ [批注 320 / 收起 32]  │
├────────────────────────────────────────────────────────┤
│ 2.2 收入与消费  ━━━━━●────────  [目录] [划线] [Aa]      │ bottombar（常驻）
└────────────────────────────────────────────────────────┘
```

- **外壳常驻**（E15）：顶部那行 + 底部阅读栏一直在，不做自动隐藏、不做沉浸浮层。
- 顶部：返回书架 / 书名（截断）/ 「第 N/M 章」/ 上一章、下一章 / 目录 / 查找 / 批注。无章节的书隐掉章号、目录、上一/下一章。
- 底部：章节名（无章节时显示书名）/ 可拖进度条（悬停出「第 3/10 章 · 42%」，无章节时只出百分比）/ 目录、划线、Aa 三个按钮。
- 目录栏：章节列表（序号 + 标题，层级缩进），当前章 accent 底色 + 加粗；滚到当前章。
- 正文：模板渲染，列宽由 Aa 里的「页宽」控制（默认 728）；章末「下一章 ›」。
- 批注栏：`BookAnnotationPanel`，按章分组；空态「选中文字即可划线」。
- 窄面板兜底：沿用 `docTocHardFloor` / `docAnnotHardFloor` 的“先收目录、再收批注”规则。
- 默认状态：**目录收起、批注收起**，正文优先（右栏 840 宽时正文 776px）；展开态记住（`@AppStorage("xhan_book_toc_open")` / `"xhan_book_annot_open"`）。
- 阅读模式：Aa 里切「上下滚动 / 左右翻页」。翻页模式左右各 28% 为翻页热区（悬停出方向箭头），中间 44% 不拦截点击；**不显示页码**。

### 9.4 批注交互

- 选区工具条：划线 / 批注 / 复制（现有三个动作，不加新按钮）。
- 左键划线 → 面板定位；右键划线 → 编辑批注 / 复制原文 / 删除划线（与文档一致）。
- 不提供“让 AI 回答”（一期无 AI 线程）。

### 9.5 快捷键

| 键 | 动作 |
|---|---|
| ⌘F | 本节查找（现有） |
| ← / → / ↑ / ↓ | 左右翻页：翻一栏（四键都认）；上下滚动：`↑/↓` 逐行、`←/→` 翻一屏 |
| 到边界再按方向键 | 进下一节 / 回上一节末页 |
| 空格 / PageDown / PageUp | 滚动模式翻一屏；在底部/顶部同样触发换节 |
| ⌥⌘← / ⌥⌘→ / ⌥⌘↑ / ⌥⌘↓ | 上一节 / 下一节**开头**（跳转，不是接着读）；正文里由模板 JS 接，SwiftUI 那套只负责焦点不在正文时 |
| ⌘⇧A | 批注面板开关 |
| Esc | 关浮层 / 关查找 |

### 9.6 已定

**已定（2026-11-14）**

1. 书架页用**封面网格**（2–3 列自适应），不用列表行。
2. 打开书时**目录 + 批注都收起**，正文优先；展开态按 `@AppStorage` 记住。
3. 进度：有章节「第 N/M 章 + 细条」；无章节**只给条**（悬停出百分比）；翻页模式不显示页码。
4. **拖拽 `.epub` 到书架页**即导入，同时保留页头导入按钮。
5. 阅读页是**右栏面板态**：书架 → 书在同一面板内两级返回（与文档详情一致），切走面板不保留阅读页（进度已存，回来恢复）。不进右栏 tab。
6. 外壳**常驻**（E15）；阅读设置做成 Aa 弹层、全局生效（E16）；背景跟随 app 主题；划线一期单色（E19）。
7. 横向翻页探针已过（§5.7，6/6），一期含翻页模式；真机只复核手感（触控板/滚轮/点击分区）。

**像素复核**：假数据原型 `client/xHelper/xHelper/Shared/Views/Books/BookUIPrototype.swift` 按本节的“常驻 chrome + 底栏 + Aa”更新后，在 Xcode 画布确认留白/卡片尺寸/栏宽，再落真接线。

---

## 十、改动清单

### 新增

| 文件 | 内容 |
|---|---|
| `Shared/Services/Epub/BookModels.swift` | `BookMeta`/`BookChapter`/`BookTOCItem`/`BookProgress`/`BookAnnotation`/`BookImportError`（徽章+原因+详释）/`EpubImportStep` |
| `Shared/Services/Epub/EpubArchive.swift` | `unzip` 封装、路径归一与逃逸校验、解包后总量、SHA256 |
| `Shared/Services/Epub/EpubPackage.swift` | container/OPF/NCX/nav 解析、spine↔TOC 映射、元数据、封面、近似字数、`HTMLText.bodyHTML` |
| `Shared/Services/Epub/BookStore.swift` | 目录扫描、meta/批注/进度的原子读写、导入/删除/查重、`chapterHTML` |
| `Shared/Services/BookActivity.swift` | 导入区间 / 阅读区间 / 批注 `user.action.*` 事件 |
| `Shared/ViewModels/BookViewModel.swift` | 书架列表、导入队列与失败卡、删除（`shared` 单例） |
| `Shared/ViewModels/BookReaderController.swift` | 阅读状态、进度口径、本地批注、事件、JS 桥；同文件含 `BookReadingSettings`（UserDefaults 全局存） |
| `Shared/Views/BookshelfView.swift` | 书架页（封面网格、拖拽导入、空态/失败/导入中） |
| `Shared/Views/Books/BookReaderView.swift` | WebView 宿主 + 常驻 chrome + 目录 + 查找栏 + Aa 弹层 |
| `Shared/Views/Books/BookAnnotationPanel.swift` | 本地批注面板（按章分组、内联编辑、删除确认） |
| `Shared/Views/Books/BookUIPrototype.swift` | 假数据视觉基线原型（`#if DEBUG`，不进发行包） |
| `xHelperMac/Assets.xcassets/sidebar_books.imageset` | 首页卡图 |
| `client/xHelper/scripts/paged-reader-probe.swift` + `.sh` | 横向翻页探针（真 WKWebView，6 条） |
| `client/xHelper/scripts/book-reader-probe.sh` | 书阅读器模板探针（真 WKWebView，8 条 × 两种宽度） |
| `Tests/EpubParserTests/` | 解析器纯逻辑单测（fixture 小 epub，25 个） |

### 修改

| 文件 | 改动 |
|---|---|
| `Shared/Views/DocumentReaderView.swift` | 模板加 `MODE`/`HEADER`/`BOOK_JSON` 占位符与 book/paged CSS；`ReaderHTML.buildBook` + `BookTypography`；`setChapter`/`position`/`restorePosition`/`setTypo`/`setReadingMode`/`turnPage`/`rewriteResources`；查找/锚点/滚动的翻页分支；键盘翻屏 |
| `Shared/Views/BrowsePanelView.swift` | `.books` case（icon/help/homePanels/entryCard/isLoading/reload/panelContent/panelHeader/tint）；首页卡替换；书架与阅读页两级；切走面板关书 |
| `Shared/Services/ActivityEventTypes.swift` | 4 个新 type 进白名单 |
| `Shared/Services/LocalPaths.swift` | `booksDir`（`dataRoot/Books`） |
| `client/xHelper/Package.swift` | 解析器纯逻辑靶 + 单测靶 |
| `client/xHelper/scripts/reader-template.py` | 新占位符 + `--book` 模式（doc 模式自检脚本继续可跑） |
| `client/xHelper/scripts/paged-reader-probe.swift` | `PROBE_FN` / `PROBE_WIDTH` 环境变量（书探针复用同一驱动） |

> 原计划里的 `RightSidebarView`/`ContentView` 注入最终没做：书架是本地单例，`BookViewModel.shared`
> 在 `BrowsePanelView` 内部持有即可，省掉两层 macOS-only 参数（那两个文件是双目标编译）。

### 文档

- 本文件 + `mockup-chrome.html`（结构演示）
- `docs/design/activity-events/附表 A · 事件字典.md`（新增「阅读（客户端）」一节）
- 若实现中发现与 `document-highlight-annotation` 描述冲突，回到那份文档尾部追加「后续变更」

---

## 十一、验证

1. **解析器单测**（SwiftPM `swift test`，不碰 Xcode）：自造 3 份 fixture —— EPUB2（NCX）/ EPUB3（nav）/ 带目录嵌套与脚注；断言 spine 顺序、章节标题、相对路径归一、DRM/固定版式拒绝、越界路径拒绝、无章节判定（spine 解析出 1 章 → 无章节）。
2. **模板自检**：book 模式断言落在 `scripts/book-reader-probe.sh`（真 WKWebView，7 条：章节注入、章末按钮不进拍平文本、`position`/`restorePosition` 往返、划线不改正文、改字号回同一句、翻页模式步长、翻页模式划线）；doc 模式仍由 `scripts/check-reader-html.sh`（176 条）。两者共用 `reader-template.py` 从源码现抠模板。选区绘制仍以 `snapshot-reader.sh`（真 WebKit）为准。
3. **翻页探针**（§5.7）：`./scripts/paged-reader-probe.sh` 六条断言（已 6/6 通过，改动模板后必须复跑）。视觉状态可用 `snapshot-reader.sh` 加一个 `paged` job 截图。
4. **真机**（3 次往返）：
   - 导入 3 本真书（技术书/小说/带插图），逐章翻、字号/行距/页宽、查找、划线、批注、重启恢复位置；有章节/无章节各一本；
   - 翻页模式实际读 10 分钟，确认点击分区、键盘、触控板、重排恢复（技术可行性已由探针证明，这里只验手感）；
   - 看 `events.db` 里 4 个 type 的配对与 attrs 正常，改批注产生 `user.action.*`。
5. `swiftc -typecheck`（macOS 目标，带项目真实设置）+ 用户 Xcode 编译；按约定不跑 `xcodebuild`。

---

## 十二、二期（在纯客户端约束下）

**做**

1. 客户端工具 `book.*`（`LocalToolRegistry` 新增 provider）：`list_books` / `outline` / `read_chapter` / `search_text` / `list_annotations` / `current_position` / `highlight_quote`。agent 能查书、翻到某句、给指定段落划重点；服务端零改动（工具桥已具备）。
2. 读书笔记：agent 读本地批注 → `create_document` 生成笔记/摘要（用户主动触发）。
3. 朗读：`AVSpeechSynthesizer` + 句子高亮 + 断点续读（纯本地）。
4. 划词翻译/词典（选区工具条加一键）。
5. 阅读统计：现有活动读取层直接查 `book.read.*`。

**明确不做（这次定了）**：书内语义检索、跨设备同步、批注聚类/跨书主题。将来若要做语义检索，只有两条路：客户端调服务端 embedding，或上传正文建索引 —— 两者都破坏“纯客户端”，到时单独拍。

---

## 十三、风险与已知限制

1. **EPUB 变体**是最大的不确定性：NCX/nav 缺失或错位、`<div>` 当段落、脚注/尾注、`<ruby>`、竖排。用 3 本真书回归；有问题的书要能明确报错，而不是打开一片空白。
2. **模板双模式**：改共享 JS 影响文档阅读器。每次改动必须跑 doc + book 两套断言。
3. **内存**：单章大图内联 data URL，极端章节可能几十 MB；已设单图/单章上限，超限跳过。
4. **备份**：本地备份（Go `internal/backup`）只对 SQLite 本地模式生效，默认关闭；开启后整个数据根会被打包，书会让归档明显变大（`Books/*/content` 目前不排除）。
5. **数据只在本机**：换机/重装会丢书与批注（除备份外无恢复路径）。
6. **agent 依赖客户端在线**：二期能力只在 Mac 客户端运行时可用；这是纯客户端的结构性限制。
7. **选区绘制**是引擎行为，`check-reader-html.sh`（Chrome）测不出来；动到划线/选区时必须跑 `snapshot-reader.sh`。
8. **横向翻页**是模板里改造面最大的部分（现有查找/锚点/滚动全按 `window.scrollY` 写）。技术可行性已由探针 6/6 证明；风险转到实现侧 —— 每改一次模板都要复跑 `paged-reader-probe.sh`，别用体感替代断言。
9. **无章节的单文件大书**：spine 只有一项且几十万字时，整章一次性进 DOM，渲染与查找会变慢。一期接受（真遇到再按顶层块切虚拟节）；导入时若单章 > 30 万字给一条提示。

---

## 后续变更

### 2026-11-14 · 实现期落定的四处收敛（批 2–4）

| 决定 | 原因 | 影响文件 |
|---|---|---|
| 悬停菜单只留「在 Finder 中显示 / 删除」，**不做「重新导入」** | 要重建整条解包+解析管线，属重复代码；`source.epub` 已保留，等真有解析器改进再做 | `Shared/Views/BookshelfView.swift` |
| 底栏进度条**只读**（不拖拽跳转） | 按全书字符权重反解章节+偏移要额外一套映射；目录与上一下一章已覆盖导航 | `Shared/Views/Books/BookReaderView.swift` |
| 查找**只在本章内** | 跨章要预载全书正文；一期书目下章内够用 | `DocumentReaderView.swift`（`reader.find`） |
| 批注面板的 quote 预览用**实线下划线** | SwiftUI 没有波浪下划线原语；正文里的划线仍是波浪线 | `Shared/Views/Books/BookAnnotationPanel.swift` |

另：书内 `<a href>` 指向别的章节时剥掉 href（不做书内导航），图片按章节所在目录重写成绝对 `file://`（CSP `img-src 'self'` 已实测放行同源 file 图片）。

### 2026-11-14 · 真机反馈后修的三处（批 5）

| 问题（用户实测） | 根因 | 修法 | 影响文件 |
|---|---|---|---|
| 目录没有层级、点谁都落在同一处、点完一整列都高亮 | `normalize()` 把 `#frag` 切掉了：30 条目录全是同一 spine 文件的锚点，塌成「2 章」 | TOC 条目保留 `fragment`；点目录走 `scrollToAnchor`；高亮改成「当前读到哪一条」（`collectAnchors` 把锚点换算成拍平偏移，滚动时用偏移选条目） | `EpubArchive`/`EpubPackage`/`BookModels`/模板 JS/`BookReaderController`/`BookReaderView` |
| 目录里一行行大空白 | 分组包装层（`<li><ol>…`）是空标题条目 | 解析时滤掉空标题；层级把最小 depth 归一到 0 | `EpubPackage` |
| 点了目录一直转圈、正文不出来 | `setChapter` 抛异常时没人回 `chapterReady`，Swift 侧 spinner 永不结束 | `setChapter` 包 try/catch，异常也回 `chapterReady{error}`；Swift 收错误就收 spinner + Toast | 模板 JS / `BookReaderController` |
| 老书（旧解析器的 meta.json）不会自动变好 | 目录只写在 `meta.json` | `meta.json` 记解析器版本（`BookStore.currentParserVersion`）；打开时版本落后就后台 `refreshTOC` 重算并写回（不重解压、不动进度与批注），与进度写盘共用一条串行队列 | `BookStore` / `BookReaderController` |

探针同步扩到 9 条：新增「目录锚点：`collectAnchors` 给偏移、`scrollToAnchor` 落在同一句」，900 / 360 两种宽度各跑一遍。

### 2026-11-14 · 批 5 续：正文空白 / 图片不显示

用真书（`写给大家看的设计书` 第 4 版，2 个 spine 文件 + 174 条锚点目录）复现并定位到三个独立问题：

| 现象 | 根因 | 修法 |
|---|---|---|
| 右侧整片空白、也不转圈 | `BookReaderWebView` 没给撑满的 `frame`；WKWebView 是 NSView、无 intrinsic size，在 `ZStack`/`HStack` 里被压成 0 宽 | `readingArea` 补 `.frame(maxWidth: .infinity, maxHeight: .infinity)` |
| 字号/行距/页宽设置全不生效 | 书排版 CSS 全挂在 `html.book` 下，而这个类**从没被加上**（只加了 `paged`） | `applyBookTypography` 启动时 `classList.add('book')`；探针加断言「2b html.book 生效」 |
| 正文里的图全裂（350 张 0 张加载） | `loadHTMLString(_:baseURL:)` 只给 baseURL 那**一层目录**读权限，书里的图在 `content/OEBPS/` 子目录 | 模板落成 `content/.xhan-reader.html`，改用 `loadFileURL(_:allowingReadAccessTo: content/)`；实测 350/350 加载。老文件随书目录一起删 |

另：`#content` 的列宽改成 `max-width: calc(var(--measure) + 56px)` + 左右 28px 内边距；翻页模式宽度 `min(var(--measure), 100%)`、`pageSpan()` 用实际 `clientWidth`（右栏比 728 窄时不横向溢出）。

### 2026-11-14 · 批 6：按目录锚点切「节」（真机反馈「整本一次进 DOM」）

**问题**：阅读单元原先是 spine 文件。排版软件常把整本书导出成**一个** xhtml（本例 322 KB / 8.9 万字 / 1889 段 / 350 图，目录 174 条全靠 `#nav_point_N` 锚点），于是「第 2/2 章」= 整本书，一次插 DOM 要几秒。

**方案（已实施）**

| 环节 | 做法 |
|---|---|
| 切分 | 导入/刷新时解析：`HTMLText.topLevelRanges` 用**标签平衡扫描**（XHTML 必良构，不解析 DOM、不重序列化）取 `<body>` 每个顶层片段的源码区间；锚点落在哪个片段就在那里下刀；第一个锚点之前的内容并进第一节 |
| 兜底 | 单节超 **3 万字**才切；无目录的大书按顶层元素累计到 3 万字切，标题留空 |
| 落库 | `BookMeta.sections: [BookSection]`（`start/end` 是 `<body>` 源码 UTF-16 区间）；只存区间不写切片文件，读取时 `substring` 现切 |
| 进度 | `BookProgress` 加 `sectionIndex`，`offset` 改**节内**偏移；老进度按节字数落到对应节首（一次性，最多偏一节） |
| 批注 | `BookAnnotation.sectionIndex`；划线一期**限本节内**（跨节要等二期） |
| 目录 | `BookTOCItem.sectionIndex`；点目录 = 加载那一节（节首即锚点）。同一节里有多个目录条目时仍按 `scrollToAnchor` 细化 |
| 导航 | 顶栏「上一章/下一章」改为**「上一节/下一节」**（这本书的「上一章」= 封面，没意义）；章末按钮文案「继续 ›」 |
| 查找 | 仍限**当前节**（切节后自动在新节重跑关键词） |

**验证**：真书解析出 175 节（1 个封面 + 174 条目录），区间首尾相接、每节切片非空；`swift test` 472 全过（新增 6 条分节/扫描单测）；书模板探针 10 条 × 两种宽度、doc 176 条、翻页 6 条全过；首屏从「整本 322 KB」变成「一节 ≈ 3.8 k 字」。

### 2026-11-14 · 批 7：进度只能回到「章首」/ 书架卡章号失真

| 现象 | 根因 | 修法 |
|---|---|---|
| 读到第四章、点「继续」、下滑、退出再进 → 回到第四章开头 | ① 打开时用的是书架数组里的**旧 `BookMeta`**（那数组只在点「‹ 书架」时重载）；② 退出时靠异步 `reader.position()` 取位置，WebView 正在拆、取不到就把 offset 写成 `0` | ① `BookReaderController.init` 先 `store.meta(id:)` 读盘，进度/节表/目录一律以磁盘为准；② `pauseReading` 先用内存里的 `pendingOffset` 落盘，再异步求精；③ 取位置失败沿用 `pendingOffset`，**绝不写 0**；④ 切节的异步保存带旧节号，`sendChapter` 失败回滚到真正的上一节 |
| 书架卡恒显示「第 2/2 章」 | 章号按 **spine 文件**编号，而这本书 2 个 spine = 封面 + 整本正文 | 切过节的书（`effectiveSections.count > chapterCount`）显示**当前节对应的目录标题**；普通书仍显示章号；无目录只给百分比。首页书架卡三行 detail 同口径 |
| 切走面板再回来，卡片进度不更新 | 同上「书架数组不重载」 | 离开 `.books` 面板时顺带 `bookVM.load()` |

新增 3 条单测：节表 + 节内进度落盘往返、`sectionHTML` 两段切片拼回等于原文、老 meta 零区间退化为整章。

### 2026-11-14 · 批 8：目录没层级（改编书的 NCX 是平的）

**现象**：目录面板 174 条全部顶格，部分 / 章 / 小节挤成一列。

**根因**：层级信息在源文件里就不存在 —— 这本书的 `toc.ncx` 174 个 `navPoint` 全是同级（没嵌套），连作者自己排的目录页都把「第一部分」和「第 1 章」标成同一个 `toc-level-1`。面板的 `depth * 12` 缩进一直是对的，只是拿到全是 0。

**修法**（`EpubPackage.parse`）

| 环节 | 做法 |
|---|---|
| 来源判定 | NCX / nav 的 `depth` **有变化**（作者编排过）就照用；全平才进入推断 |
| 推断 | `HTMLText.headingLevel(anchor:in:)` 找到锚点所在标签，是 `h1…h6` 就取级别（`h1`→0）。锚点打在 `<p>` / `<a>` / 容器上返回 nil，不猜 |
| 部分下沉 | 标题命中 `第 N 部分` / `Part X` / `上/中/下篇` 时它自己留原层，其后条目整体 +1；遇下一个部分或 `附录/后记/致谢/索引` 重置。只在推断路径生效，作者编排过的目录不动 |
| 封顶 | 最深 3 级（面板 220pt，再深文字没地方放） |

**结果**（你这本书）：部分 0 → 章 1 → `h2` 小节 2 → `h3` 子小节 3；末尾三个附录退出分组回到 0。

**验证**：`swift test` 478 全过（新增 3 条：`headingLevel` 只认标题标签、部分/附录标题识别、平 NCX fixture 推出 `[0,1,2,3,0]`）；`singleFileRichTOC` 的旧断言（编码了「全平」行为）改为 `[0,1,1]`；真书解析出 4 个层级；`parserVersion` 3 → 4，老书打开时自动重算目录（进度与批注不动）。

**评审后收敛（同日）**

- **一次扫描**：`HTMLText.scanBody(_:)` 顺序扫一遍 `<body>` 就同时给出「每个 `id=/name=` 锚点的位置」和「所在标题级别」，取代原先逐锚点全文搜索（174 条目录 × 10MB 级 body ≈ 上百次扫描）。`anchorPosition` 随之删除，`planSections` 与目录层级共用这次扫描（真书 322 KB 全量解析 **0.13s**）。
- **`id` 挂在标题内的 `<a>` 上**（Calibre 常见的 `<h1><a id="x">标题</a></h1>`）也算标题层级 —— 扫描时记着当前所在标题，不看「紧贴 id 前的那个标签」。
- **不带锚点的目录条目**（`content src="ch2.xhtml"`，一 navPoint 一文件的平目录最常见）按该章**首个标题**的级别兜底，不再一律 0。
- **判「作者编排过层级」只看能落到正文的条目**：封面/版权页那一小簇有嵌套，不再让全书退回平层。
- 正则收紧：`Part 1 of the story` 不算分组标题（序号后必须分隔符或行尾）；`Indexing…` 不算索引（英文加词尾边界，中文仍按前缀，「致谢词」算）。
- `refreshStructure` 补全后 `updateCurrentTOC` 会重算 `currentTOCKey`（depth 进了 key），底栏标题与高亮不会锁在旧 key 上 —— 原本已覆盖，无需改动。

### 2026-11-14 · 批 9：拆掉常驻底栏（真机反馈「底下这条占地方」）

底栏原来常驻 46pt，装着「当前节标题 + 全书进度条 + 目录按钮（与顶栏重复）+ Aa 按钮」。

| 处理 | 说明 |
|---|---|
| 删底栏 | `bottomBar` / `progressRail` / `progressTooltip` 全删，正文区多出 46pt |
| 节标题上移 | 顶栏那行改成 `目录条目 → 节标题 → 章标题 → 章号`，`lineLimit(1)` + `layoutPriority(-1)`（窄窗口先截它，不挤右侧按钮），完整文字走 tooltip |
| Aa 上移 | 顶栏最右，紧接批注按钮；弹层锚点从「贴底栏」改为「贴顶栏右下方」（`.topTrailing`，`padding(.top, 44)`） |
| 进度条 | **不要了**。用户拍板：阅读时看不到全书进度可接受，进度只存 `meta.progress` 并显示在书架卡上 |

顺带：顶栏和底栏原来各有一个目录按钮，现在只剩顶栏那个。

### 2026-11-14 · 批 10：顶栏节标题并在书名后 + 设置图标改齿轮

真机反馈：节标题单独浮在顶栏右侧「很奇怪」，`Aa` 图标也不够直观。

| 处理 | 说明 |
|---|---|
| 节标题 | 删掉右侧孤立的 `Text`，改为 `书名 · 节标题` 拼成**一个** `Text`（书名 13pt 半粗、分隔符与节标题 11pt 次级色）。整行一起 `lineLimit(1)` 截断，不会出现「书名截一半、节标题还在」的错位；完整文字走 tooltip |
| 取值 | `目录条目 → 节标题 → 章标题 → 章号`（`headerTitle` 拆成不含书名的 `sectionLabel`）；没有节标题时只显示书名，不留孤零零的「 · 」 |
| 图标 | `textformat.size` → `gearshape`（同一个按钮、同一个弹层、同一份 help 文案） |

取舍：书名特别长时会把节标题一起截掉（优先保书名）。

### 2026-11-14 · 批 11：顶栏 tooltip 统一 + 目录图标 + 删设计备注

真机反馈：图标分不清是什么；Aa 弹层尾部那行说明像内部黑话。

| 处理 | 说明 |
|---|---|
| 目录图标 | `list.bullet` → `sidebar.leading`，与「划线与批注」的四条线图标区分开 |
| tooltip | 统一「动作 + 范围/快捷键」，开关类按状态变文案：`展开目录` / `收起目录`、`展开划线与批注（⌘⇧A）` / `收起…`、`在本节查找（⌘F）`、`阅读设置（字号、行距）` |
| 查找文案 | 「本章查找」→「本节查找」（工具提示 + 输入框占位），与实际「只查当前节」一致 |
| 删备注 | Aa 弹层尾部「背景跟随 app 主题；划线一期单色（多色二期）」整行删除 —— 一期/二期是排期术语，且「没有的控件」不需要解释 |

**补（同日）**：`⌥⌘↑` / `⌥⌘↓` 与 `⌥⌘←` / `⌥⌘→` 等价（上一节 / 下一节）。SwiftUI 一个 view 只能挂一个 `keyboardShortcut`，所以后两个键走顶栏里的隐形按钮（`opacity(0)`），首/末节同样置灰；普通 ↑/↓ 仍留给正文滚动。

### 2026-11-14 · 批 12：翻页严格一页 + 一屏一栏

真机反馈：左右翻页会停在两栏中间（左右各截一半）。根因是有四条路径直接改 `scrollLeft`：`turnPage` 按像素累加（起点半页就一直半页）、`wheel` 的 `+= deltaY`（触控板停在任意处）、`restorePosition` / `scrollToAnchor` 按字符位置滚到视口左缘（切节、改字号、跳目录都会走）。

| 处理 | 说明 |
|---|---|
| 一屏一栏 | `html.paged #content`：`width: 100%` + `column-width: 100000px`（超大值 → 栏数恒为 1、栏宽 = 容器宽）。翻页模式**不再读 `--measure`**，Aa 里的「页宽」置灰并写明「只影响上下滚动」 |
| 按页号翻 | `turnPage(delta)` = `round(scrollLeft / 步长) + delta`，不再累加像素 |
| 吸附 | 新增 `snapPage()`：`restorePosition`、`scrollToAnchor`、查找命中后统一吸附到页边界；末页贴在内容尾部 |
| 滚轮/触控板 | 一次手势翻一页（150ms 内视为同一手势、350ms 冷却挡惯性），纵向与横向手势都翻页；不再让原生横向滚动停在半页 |
| 探针 | `paged-reader-probe.sh` 加两条：`0 一屏一栏（模板 CSS）`、`7 翻页停在整页`（含「从半页起点翻」），当前 8/8 通过 |

影响文件：`client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`（模板 CSS/JS）、`Views/Books/BookReaderView.swift`（Aa 页宽禁用）、`client/xHelper/scripts/paged-reader-probe.sh`。

### 2026-11-14 · 批 12 补：列宽两模式一致 + 翻页动画

真机反馈两件事：① 切「上下滚动 / 左右翻页」版面全变（滚动是居中 728 列，翻页是满宽），且翻页模式两边贴边；② 翻页「咔嚓」硬切，没有过渡。

上面「一屏一栏」那条被这次取代：翻页模式**重新读 `--measure`**，Aa 里的「页宽」恢复可用。

| 处理 | 说明 |
|---|---|
| 几何统一 | `html.paged .page { padding: 0 28px; max-width: min(calc(var(--measure, 728px) + 56px), 100%); margin: 0 auto }`，`#content` 回到 `width: 100%`。与 `html.book #content` 同一套：外框 = 列宽 + 56，居中。窄面板下正文 = 面板宽 - 56，不再贴边 |
| 内边距放 `.page` | 放 `#content` 上只作用于首尾栏，中间每页仍贴边，且会污染 `pageSpan() = clientWidth + 64` |
| 翻页动画 | `animatePage()`：rAF 补间 220ms、ease-out；`prefers-reduced-motion: reduce` 直接跳。动画途中再翻从「目标页」起算，不叠半路位置 |
| 收尾 | `snapPage()` 动画途中调用 = 取消 rAF 并落到目标页；`restorePosition` / `scrollToAnchor` / 查找命中 / `setReadingMode` 先 `settlePage()`，避免 rAF 把刚设好的位置拖回中间 |
| 探针 | `0 一屏一栏` 改成采样断言：视口撒 24 点取字符偏移，翻页前后两页的偏移**不得重叠**（一屏塞两栏就会重叠）；同时验 `clientWidth == min(728, 视口-56)`。`7 翻页动画 + 停在整页`：先验「翻完位置没瞬间到位」= 真有补间，再 `snapPage()` 验落整页。探针不再内联覆盖栏宽，直接测模板 CSS |

影响文件同上，另加 `client/xHelper/scripts/book-reader-probe.sh`（`turnPage` 断言中间插 `snapPage()`）。

### 2026-11-14 · 批 13：到底自动换节（不用再点「继续」）

真机反馈：每一节读完还要手动点「继续」，很难受。根因不是切节慢 —— 按在跑的那本书实测，一整章 316KB 切成 174 节，**一节≈1.8KB**，读盘 + 抠 body + 切片是毫秒级；难受的是「到边界那一刻没人接话」。

| 处理 | 说明 |
|---|---|
| 翻页模式 | `turnPage` 被夹在头/尾（`x === before`）且该方向有相邻节 → `advanceSection(delta)` 发 `nextSection` / `prevSection`。它是键盘、滚轮、点击分区三个入口的唯一漏斗，一处改动三条路径全自动 |
| 滚动模式 | 只在**已经到底/到顶**时把「再多滚一下」当换节手势（`advanceSection`）；复用 150ms 新手势 + 350ms 冷却，惯性事件间隔 ~16ms 永远不会被误判。空格 / PageDown 在底部同理，不再空滚 |
| 反向 | 第一页再往前翻 → 上一节的**末页**（`previousSectionFromEnd()`：offset 给上一节源码区间长度，JS 夹到最后一个字后 `snapPage()` 收到末页），不是首行 |
| 防重复 | JS 侧 `advancing` 锁：举手换节后到新正文到达（`setChapter`）之前不再发，否则一次连滚会连跳两节 |
| 转圈降噪 | `isLoadingChapter` 的 ProgressView 加 0.25s 延迟，快加载根本不出现（自动换节后每节闪一下比手动点更烦） |
| 不做预加载 | 按上面实测数据省不到东西。真要做：`chapterReady` 后后台 `sectionHTML(index + 1)` 存 controller，命中就跳过读盘，约 25 行 —— 等真机量出停顿再加 |
| 「继续 ›」 | 保留，退化为备用入口（滚动模式里它是唯一的可见提示） |

探针：`book-reader-probe.sh` 加两条 —— `10 到底再翻 = 换节`（末页再翻发 `nextSection`、换节途中不重复、第一页再往前发 `prevSection`）、`11 滚动模式到底再滚 = 换节`（没到底不换、到底连滚 5 下只换 1 次、换节途中累计仍 1 次），两种宽度都 12/12 通过。探针把 `window.webkit.messageHandlers.reader` 换成收集器来观察 `post`（没有原生 handler 时 `window.webkit` 不存在，`post` 本来静默丢弃）。

影响文件：`client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`（`advanceSection` / `turnPage` / wheel / keydown / `setChapter` 增 `hasPrev`）、`ViewModels/BookReaderController.swift`（`previousSectionFromEnd`、`prevSection` 消息、`hasPrev` 透传）、`Views/Books/BookReaderView.swift`（转圈延迟）、`client/xHelper/scripts/book-reader-probe.sh`。
### 2026-11-14 · 批 14：修「末页 → 不换节」+ 四个方向键全接

真机反馈：**左边能换节、右边不能**（翻页模式末页按 `→` 没反应），`↑/↓` 在任何模式下都不换节。

根因（`turnPage`）：判断「到头了」用的是 `x === before` —— `x` 由 `scrollWidth - clientWidth`（两个都取整）算出来，末页的真实 `scrollLeft` 常差几像素（取整误差、引擎夹取）。于是 `3168 !== 3165` 成立，它以为还能往右挪 3px，`animatePage` 演 3px 之后再按一下又是同样 3px，永远进不了「到头」分支。往回翻的起点是 `0`，`0` 没有误差，所以 `←` 一直是好的 —— 这就是那个不对称。

改法：**判据换成「这一下能不能挪动接近一整页」（`moved < span/2`）**。翻不动一整页 = 到边界 = 换节。这一条对取整误差、引擎夹取、内容撑大 `scrollWidth` 全都成立，不再赌任何一个像素是否相等。

顺带补齐方向键（原来只有 `←/→` 在翻页模式接了，`↑/↓` 和滚动模式的 `←/→` 都是死的）：

| 键 | 左右翻页 | 上下滚动 |
|---|---|---|
| `←` `↑` | 上一栏 | `←` 上一屏、`↑` 原生逐行 |
| `→` `↓` | 下一栏 | `→` 下一屏、`↓` 原生逐行 |
| 到边界再按 | 换节 | 换节 |

其它两条：

- **`⌥⌘+方向键` 改在模板 JS 里认**：焦点在正文（WKWebView）时 SwiftUI 的 `keyboardShortcut` 不保证收得到，这条路径以前就是靠运气。JS 发 `sectionJump{delta}`，Swift 侧 `jumpSection()` 有 250ms 去重 —— 同一次按键即使两路都响也只跳一节。SwiftUI 那对快捷键保留，负责焦点在目录/查找框里的情况。
- `Shift+方向键` 直接放过（扩选，不翻页）。

探针（`book-reader-probe.sh`，两种宽度各 15 条）：`10 到底再翻`、`10b 小数栏宽 640.5`、`10c 末页差 3px 也算到头`（这条在旧判据下必挂，是真正的回归用例）、`12 方向键（两种模式 × 四个键 + ⌥⌘）`八个组合。

> 探针**复现不出**真机那个几何：离屏页面里 `scrollWidth - clientWidth` 与真实末页差 0px（整数宽、小数宽、加超宽子元素都试过），所以 `10/10b` 在旧代码下也是绿的。这类几何问题只能靠「判据本身不依赖精确像素」来兜，不能靠探针复现。

影响文件：`client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`（`pageMax` / `turnPage` 判据 / keydown 四个方向键 + ⌥⌘）、`ViewModels/BookReaderController.swift`（`jumpSection` 去重 + `sectionJump` 消息）、`Views/Books/BookReaderView.swift`（按钮与快捷键改走 `jumpSection`）、`client/xHelper/scripts/book-reader-probe.sh`。
### 2026-11-14 · 批 15：滚动模式的键盘焦点 + 到底换节改成累计量 + 导航诊断日志

真机反馈：① 上下滚动模式里**鼠标滚轮能滚、键盘完全没反应**；② 到底部时「有时能换节、有时不能」；③ 左右翻页模式没问题。

| 处理 | 说明 |
|---|---|
| 键盘进不来 | 键盘事件只有 WKWebView 是**第一响应者**时才进得了页面。只用滚轮、不点正文的读者从来没把焦点给过正文 —— 症状就是「鼠标能滚、按键毫无反应」，日志里连 `导航 key` 都没有。`BookReaderWebView.updateNSView` 首次进层级时 `makeFirstResponder(webView)` 一次（`Coordinator.didFocus` 保证不跟查找框抢焦点） |
| 到底换节不可靠 | 原来按「新手势」判（150ms 内算同一次）。触控板惯性是连续事件、间隔 ~16ms，**永远凑不出 150ms 空隙** —— 读者一直推也轮不到一次。改成**在顶部/底部继续推的累计量**：累计 ≥120 且距上次换节 ≥600ms 才触发；停手 400ms 重新累计。鼠标一格（~40–100px）两三下就到，触控板惯性尾巴直接触发 |
| 诊断日志 | 模板发 `{type:'diag'}`，Swift 记 `书阅读器·导航 <kind> ...`（`LogBuffer.info`，DEBUG 才有）。`key`（每个方向键/空格，带 cmd/alt/shift/mode）、`chapter`（每节 nx/pv/mode/字数）、`advance`（换节成功，带位置/max/span）、`advance-skip`（没换：lock/nx/pv）。判断法：**没有 `key` 日志 = 事件没进页面（焦点问题）**；有 `key` 但跟 `advance-skip` = 锁或没有相邻节。日志在「设置 → 诊断日志」里看 |

踩坑：诊断函数一开始叫 `report()`，而模板里 `report()` 已经是「发 findResult」的那个函数 —— **同名函数声明后置覆盖**，`find()` 静默不再回报命中数。改名 `navDiag()`。详见 `docs/bugs/067-reader-template-report-shadowing.md`；自查脚本要**两个都跑**（`check-reader-html.sh` 抓到的，`book-reader-probe.sh` 看不出来）。

影响文件：`client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`（`navDiag` / wheel 累计量 / keydown 诊断）、`Views/Books/BookReaderView.swift`（抢焦点）、`ViewModels/BookReaderController.swift`（`diag` 消息、`handleReady` 记第一响应者）。
