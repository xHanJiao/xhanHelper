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
