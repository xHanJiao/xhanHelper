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
- 去掉用户消息气泡上的调试齿轮（`debugMode` 全局开关在 M1 已移除，齿轮成了关不掉的状态）；单条消息调试入口暂时只有管理后台内部可跳，若需要可后续挂到右键/长按菜单。
- 审查后补：三个纯图标 pill 补 `aria-label`；收起态容器加 `inert`（不可聚焦/不播报）；输入框展开时隐藏「↓」按钮（避免压在输入框上）；点「↓」不再强行展开输入框；切会话重置展开态。
