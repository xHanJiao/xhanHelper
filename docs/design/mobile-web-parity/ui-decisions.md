# UI 预览与已确认形态

> 用法：用 Chrome/Safari 打开 `mockup.html`，或直接打开三份单文件预览。
> 页面底部有控制条，可切屏幕、主题、字号、形态；URL 也支持参数，便于截图和讨论。
> 预览不是最终实现，只用来定结构、层级、密度和几个拿不准的形态。

## 一、已确认的 UI 形态（2026-11-01）

| # | 项目 | 结论 |
|---|---|---|
| 1 | 用户消息 | **气泡**：浅色淡绿底 + 圆角，不用 macOS 的纯文本 |
| 2 | 桌面功能入口 | **顶栏「功能」菜单**：popover，选中进全屏页；不做常驻右栏 |
| 3 | 多卡排列 | **单卡 + 横条「＋N」**：单张保持整卡，多张横排、放不下收成窄格 |
| 4 | 卡片条收起 | **8 秒自动收起**：展开后 8 秒回收；hover/focus 时暂停 |
| 5 | 手机「我的」 | **功能格子**：2 列，图片/账单/记忆/主题/智能体/设置 |
| 6 | 灵阅密度 | **单列大卡**：标题 + 摘要 2–3 行 + 标签 + 日期 |
| 7 | 手机输入区 | **常驻贴底、可收起**：卡顶把手点一下收起，右下角「发消息」浮标再展开；键盘弹起时 Tab 隐藏 |
| 8 | 文档布局 | **桌面左目录 + 右批注；手机正文 + 底部「目录/批注/字号」浮动条** |
| 9 | 输入框按钮 | **跟 macOS 对齐**：回形针（选图/粘贴/拖拽）、计划模式、无痕；不做完全信任/模型/精炼/接力 |
| 10 | 上下文占用 | **不做**：输入框左下角不放 token/费用圆环（Web 端价值低） |
| 11 | 图片上传时机 | **选完立刻传**，并发 2；失败单张可重试，未引用的图由服务端 3 天 orphan TTL 清 |

其余备选形态（纯文本用户消息、图标入口、右抽屉、竖叠卡片、列表式我的、双列/列表灵阅、默认收起输入区、顶部目录）只作为对照，不进入实现。

## 二、预览文件

| 文件 | 看什么 |
|---|---|
| `mockup.html` | 预览总入口（含链接与参数） |
| `mockups/01-shell.html` | 桌面顶栏/会话下拉/功能菜单；手机底部 Tab/聊天壳/我的页 |
| `mockups/02-chat.html` | 消息卡片、卡片条、图片条、Markdown、流式态、附件托盘 |
| `mockups/03-content.html` | 灵阅列表/详情、文档阅读器、设置页、只读批注 |

## 三、预览参数

### `01-shell.html`

- `?screen=desktop|mobile-chat|mobile-me`
- `?theme=light|dark`
- `?bubble=bubble|text`
- `?functions=menu|icons|drawer`
- `?me=grid|list`
- `?composer=always|collapsed`
- `?popover=functions|conversations`
- `?chrome=0` 隐藏控制条

### `02-chat.html`

- `?screen=desktop|mobile|compose|lightbox`
- `?theme=light|dark`
- `?bubble=bubble|text`
- `?cards=single|stack|strip`
- `?autocollapse=1|0`
- `?chrome=0`

### `03-content.html`

- `?screen=artifacts|artifact-detail|doc-desktop|doc-mobile|settings`
- `?theme=light|dark`
- `?density=large|compact|rows`
- `?anno=on|off`
- `?chrome=0`

## 四、预览里的假数据

- 会话标题、文档标题、灵阅标题都是示例，不代表最终文案。
- 灵阅详情页里的“手账”是内联 HTML 示例，只用于看容器和留白。
- 图片用 CSS 渐变占位，不引外链。

## 五、后续变更（2026-11-02）

真机验收后的调整（影响 `ArtifactCards.vue` / `PageTopBar.vue` / `ChatView.vue`）：

- 灵阅卡摘要从 3 行收到 **2 行**，卡片固定 96px 高，不再被摘要撑高。
- 顶栏智能体切换去掉 🤖 图标，只留「智能体 ▾ / 名称 ▾」。
- 计划模式 / 无痕：hover 改用 `NTooltip` 即时提示（手机不启用）；点击后的提示不再常驻黄条，改成几秒自动消失的 `nMessage`，模式状态靠 pill 高亮体现。
- 手机输入区恢复 ⤢/⤡ 展开/收起按钮（M1 误加了 `v-if="!isMobile"`）。
- 手机输入区可整块收起：卡顶细把手（tap 收起，默认展开），收起后右下角浮出「发消息」胶囊；收起时顺带 blur 关掉键盘；「↓」按钮按输入卡实际高度定位（贴在卡上沿，展开时隐藏）。
- 去掉用户消息气泡上的调试齿轮（`debugMode` 全局开关在 M1 已移除，齿轮成了关不掉的状态）。单条消息调试入口改挂到消息的右键 / 长按菜单（`NDropdown`），与 macOS 对齐只对**用户消息**、且仅管理员可见，跳 `/admin/message-debug/:id`；手机端不显示（该路由 `adminOnly`，进来只会撞「请在桌面端打开」）。
- 审查后补：三个纯图标 pill 补 `aria-label`；收起态容器加 `inert`（不可聚焦/不播报）；输入框展开时隐藏「↓」按钮（避免压在输入框上）；点「↓」不再强行展开输入框；切会话重置展开态。
- 桌面卡片拉宽：聊天卡 `max-width` 820 → **1080**，正文列 / 输入卡 760 → **960**。原来 1440 宽屏下卡片只占一半、顶栏两侧按钮显得虚浮；手机（≤1099）仍是全宽无卡片。
- 顶栏与卡片齐平：`PageTopBar` 新增 `alignCard`（对话页启用），桌面端 `padding-inline: max(24px, calc((100% - 1080px) / 2))`，会话胶囊左沿 / 头像右沿正好压在卡片描边上；其他通栏管理页不传该属性，行为不变。
- 桌面列表页合并顶栏：原来「56px 全局顶栏（功能/主题/头像）+ 吸顶页头（返回/标题/页面操作）」是上下两条行，看着像两个顶栏。现在把全局入口抽成 `TopBarActions`，由 `PageHeader` 在桌面渲染在标题行右侧（1px 竖线分隔），标题从 28 降到 20px；12 个列表页不再渲染 `PageTopBar`。全局按钮随之跟内容列右缘对齐（1920 屏从 x≈1890 移到 x≈1520），不再贴窗口边。

## 六、文档阅读器（2026-11-02 实施）

影响 `views/DocumentReadView.vue` / `components/media/AuthImage.vue` / `views/DocumentsView.vue`：

- **目录**：markdown-it 只解析一次，给 `h1–h3` 按顺序写 `id="doc-h-N"`，目录与标题共用同一组 id。桌面左栏 220px；`<1100` 隐藏，底部浮条「目录 / Aa」，「目录」开底部抽屉。不做滚动高亮，点击即跳（`scrollIntoView` + `scroll-margin-top`）。
- **字号**：桌面 100 / 110 / 120 / 125%，手机 100 / 110 / 120%；存 `localStorage` 的 `doc.fontScale`，只作用于阅读页。正文基准桌面 15px、手机 16px，标题用 em 跟随缩放。
- **分享**：`utils/document.js` 的 `ensureDocumentShareUrl` 统一处理 —— 已分享就复用，未分享（404，`JSONError` 带 `errorCode` 会抛 `ApiError`）就 `POST` 创建，其它错误照抛；弹窗展示完整链接并复制（`navigator.clipboard` 不可用时退回 `execCommand`）。列表页与阅读页共用；列表页原先只走 clipboard，LAN http 下必失败，此次一并补上。
- **下载 / PDF**：顶栏 `⋯` 菜单放「下载 .md」「导出 PDF」（复用 `useExportPDF`）；手机菜单额外带「分享链接」。
- **状态**：`loading` 初值 true；`load()` 起手清空 doc/toc/html，404 或脏响应走失败页，失败页不保留上一篇的分享/下载入口。
- **重复标题**：正文第一个标题是 `h1` 且与 `doc.title` 相同时跳过（正文与目录都不再出现第二条同名标题）；`##` 开头、后面再写同名 `#` 的文档不去重。
- **图片**：`/images/…` 是公开图（文档配图会被 `promoteDocImages` 升为 public），直接交给 `<img>`，吃浏览器一年缓存；只有历史遗留的 `/api/images/…` 才用 `api.blob`（并校验返回的是 `image/*`）。外链保留原样；点击开 `ImageLightbox`。`AuthImage` 只对 `/api/images/…` 带 Bearer，且只 revoke 自己创建的 URL。
- 正文内链（`data-route`）走 `router.push`；大标题 + 更新时间 + 标签放在纸面顶部。

## 七、只读批注（2026-11-02 实施）

影响 `views/DocumentReadView.vue` / `components/document/AnnotationList.vue`：

- 取数与正文分开：`GET /api/documents/:id/annotations?limit=200&offset=0` 失败只在批注面板里显示「加载失败 + 重试」，正文照常可用；加载成功但没有批注时不显示任何批注入口。
- 布局：桌面 ≥1200px 右栏 320px；1100–1199（非手机但放不下）与手机都把列表放进抽屉（桌面右侧 / 手机底部 60vh）。手机底部浮条变成「目录 / 批注 N / Aa」，顺序固定、不换行。
- 定位不用 `start_offset/end_offset`（源文 rune 偏移对不上渲染后的 DOM）：把正文文本节点拼成一条带位置的字符串，按 macOS `DocumentReaderView.swift` 的 `fold()` 同一套投影（丢 ``* _ ` ~ |`` 五个行内标记 + 空白折叠）后 `indexOf(quote)`，再映射回 `Range`；quote 也先过同一投影。跨 `**粗体**` / `code` / 链接的划线、以及 `user_memories` 这类带标记字符的原文都能命中，投影规则两边要一起改；`scrollIntoView({ block:'center' })` 后用 CSS Custom Highlight API 临时高亮 1.6s（`::highlight` 里 `var()` 解析不出来，颜色写死 token 实值，深浅各一条）。老 Safari 没有该 API 时只滚动、不画高亮。
- 计数用接口 `total`（不是本页条数）；单页 200、上限 500，超出时列表底部提示「仅显示前 N / total 条」。
- 条目只读：`quote`（最多 3 行）+ `note`（最多 4 行）+「N 条回复」+「原文已改写」/「原文有改动」徽标。不展示 `last_reply`，不提供新建/回复/删除/重新定位。
- 找不到 quote（失活批注）只 `message.info` 提示，不报错；切文档/卸载时清空列表、清高亮与定时器。

## 八、灵阅增强（2026-11-02 实施）

影响 `views/ArtifactsView.vue`，后端接口均已存在：

- 标签筛选：`GET /api/artifacts/tags` 取 `{tags:[{name,count}]}`，筛选面板里按次数降序展示前 20 个（带篇数）；请求失败只记 console，列表照常可用。多选按**交集**过滤（`?tags=a,b`，与服务端同口径），最多同时选 10 个，超出提示并拒绝。
- 搜索走服务端 `keyword`（Enter / 清空触发），与标签叠加；筛选面板的「清除」只清标签、保留搜索词。
- 多选删除：页头「选择」进入选择态（列表为空时不显示）；卡片右上角圆形勾选圈，点卡片 = 选中/取消（不跳详情），hover 位移关闭；页头换成「已选 N / 取消 / 删除」。
- 「删除」走 `NPopconfirm`，文案「删除 N 条灵阅？不可恢复，已分享的链接会立即失效」→ `POST /api/artifacts/batch-delete`。单次上限 100（与服务端 `maxBatchDelete` 一致），超出提示不发请求。
- 成功后按选中 id 本地移除、`total` 减实际删除数并退出选择态；「加载更多」新增的条目默认不选中。筛选无结果时给「清除筛选」（清空关键词 + 标签）。

## 九、列表页头部统一（2026-11-02 实施）

影响 `components/PageHeader.vue` / `components/TopBarActions.vue` / `components/PageTopBar.vue` / `composables/useGoBack.js` / `style.css`，以及 Artifacts、Documents、Bills、Memory、Notes、Summary、Theme、Agent、Prompt、Tool、Settings、Me 各页：

- 全部功能列表页用同一套 `PageHeader`：大标题 `.ph-title`（桌面 20px / 手机 24px，700，`letter-spacing: -0.02em`）+ 可选返回、搜索（点开才展开）、筛选（手机底部面板 / 桌面 popover，内容由页面插槽提供）+ 右侧操作插槽。
- 手机端这些页没有 `PageTopBar`，标题直接顶到安全区下；**桌面也只有这一行**——全局入口（`TopBarActions`：功能菜单 / 主题 / 头像）直接并进 `PageHeader` 右侧，用一根 1px 竖线和页面操作分开。此前是「56px 全局顶栏 + 页头」两条独立行，视觉上像两个顶栏。并入后全局按钮跟内容列（1120/760）右缘对齐，不再贴窗口边。
- 滚动容器统一 `.page-scroll`：桌面 `0 16px 28px`，手机 `calc(safe-top + 4px) 12px 20px`。
- 内容宽度统一：功能列表页（灵阅/文档/账单/记忆/笔记/摘要/主题/智能体/Prompt/工具）桌面 **1120px 居中**（`padding: 0 max(16px, calc((100% - 1120px)/2)) 28px`：容器本身保持全宽，宽屏两侧空白处也能滚动；1920 下左右内边距各 400，内容盒就是 1120），由 `.page-scroll` 一处控制；设置/我的用各自 scoped 的 760px 覆盖（同样「容器全宽 + 内边距收窄」的写法），聊天页独立（卡片 1080）。灵阅原先单独写死的 900px 上限已删——那是双列大卡时代的遗留，导致它比兄弟页窄一圈。
- 卡片页列数统一：记忆/笔记/摘要/主题/智能体/Prompt/工具全部 `:cols="isMobile ? 1 : 3"`，断点与应用自身的 1100 同源（不用 Naive 的 `responsive="screen"`：它按窗口的 s=640 / m=1024 算，会在 1024–1099 这段「已是手机壳」的宽度里给出 3 列）。桌面 3 列（1120 下卡 ~363），手机 1 列，与灵阅「≤1099 单列」一致。文档页例外：卡片带正文预览，**恒单列**（`:cols="1"`）。
- 桌面 `PageHeader` 吸顶：`@media (min-width: 1100px)` 下 `.ph` 用 `position: sticky; top: 0` 常驻标题/返回/页面操作/全局按钮（这一行就是桌面唯一的顶栏）。手机不吸顶——安全区和键盘判断复杂，且纵向留白本来就紧。**吸顶条不铺纯色**：页面底是 `background-attachment: fixed` 的四层渐变 + `body::before` 白纱，只铺一层色会跟四周差出一个可见色块，所以把渐变和白纱抽成 `--app-bg-image` / `--app-veil`（深色主题只改 `--app-veil`），`.ph` 用同一套 + `background-attachment: fixed` 复刻，静止和滚动都看不出接缝。`.page-scroll` 同时加 `scroll-padding-top: 64px`，避免键盘 Tab 滚进来的卡片被页头压住（64 是按页头**基础**高度 58 估的；展开搜索行或带筛选胶囊时页头会更高，那时可能仍被压掉一截，暂不为这一个边角上 ResizeObserver）。
- 返回逻辑抽到 `useGoBack`：`meta.backToChat` 的列表页固定回 `/chat`；二级页（设置、管理后台的详情等）按历史回上一页，无历史兜底 `/chat`。`/settings` 是从「我的」/头像菜单进的二级页，**不吃** `backToChat`，返回原来那页。
- 灵阅/文档筛选收进标签面板：灵阅多选取交集，文档单选；生效后在标题下显示可删胶囊、筛选图标带角标。文档标签改用 `GET /api/documents/tags`（默认 audience=finished，与列表同口径），不再从当前页文档里凑。
- 筛选是**草稿式**：面板里的「清除」只清草稿、不发请求，点「查看结果」才提交；标题下胶囊行上的「清除」只清**标签**（搜索词有自己的 ×，行为在灵阅/文档两页一致）。空状态的「清除筛选」才连搜索词一起清。
- 灵阅进入选择态时只隐藏标签胶囊、保留搜索词胶囊（选的是「筛选结果里的 N 条」），并把筛选角标归零；选择态里改搜索会先退出选择，避免重拉列表后 `selectedIds` 指向已不在列表里的条目。
- 手机底部筛选面板是模态：`role="dialog"` + `aria-modal` + Esc 关闭 + 锁背景滚动 + 焦点移入面板/关闭还原到筛选按钮（不做完整 Tab trap）；`filterable` 变 false（如进入选择态）时面板自动关闭，不会退出后自己弹回来。
- 账单：筛选面板放分类 + 日期范围。日期用**两个单日期**选择器（起 / 止），不用 daterange——Naive 的 daterange 面板固定两个月并排约 550px，手机上会横向溢出。支持只选一端（胶囊显示那一端）；两端都选时自动钳制——开始晚于已选的结束就把结束挪到开始（反之亦然），所以「查看结果」永远能提交，不会出现校验失败把面板关掉、草稿丢失的情况。日期参数按**本地年月日**拼串（`ymd()`），不用 `toISOString()`（UTC 会让东八区差一天）。导出保留在桌面标题行。
- 记忆/笔记/摘要：标题 + 搜索；主题/智能体/Prompt/工具：标题 + 新建；设置/我的：只有标题。
- 手机端 `.ph-icon` 38px、`.page-action` 加内边距，撑到接近 44px 的触控区。文档无结果且有筛选时给「清除筛选」，与灵阅对齐。
