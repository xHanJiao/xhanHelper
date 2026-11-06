# M1 · 骨架、视觉、移动端

> 前置：`00-overview.md`
> 目标：先把「外壳」换成手机优先 + macOS 视觉，再在稳定的壳里加功能。
> 预估：10–16h
> 实现状态（2026-11-01）：骨架、macOS token、会话下拉、手机底部 Tab、PWA 基础版已落地；`ConversationList` 旧侧栏已删除。输入框已补齐图片链路与计划/无痕（见 `02-*.md` 的 M2a/M2c）。M1 还剩逐页视觉回归和真机键盘/安全区验证。

## 一、范围

- 桌面顶栏 + 聊天主区；手机底部 Tab + 全屏内容页
- macOS token 全站替换（浅/深）
- 会话下拉：搜索、置顶、归档、新建
- 修复非管理员看不到历史
- 移动端安全区、`dvh`、键盘、弹窗、表格/代码横向滚动
- 基础 PWA

不做：右栏常驻、拖拽、多 tab、设置页细节（M3）、文档批注（M3）。

## 二、现状与要改的文件

| 文件 | 现状 | 改法 |
|---|---|---|
| `front-end/src/App.vue` | 左侧 280 侧栏 + 抽屉 | 换成 `AppShell`：无侧栏；主区 + 手机底部 Tab |
| `front-end/src/components/ConversationList.vue` | 侧栏四段折叠，历史包在 `v-if="isAdmin"` | 下线；由 `ConversationSwitcher` 取代，历史对所有登录用户可见 |
| `front-end/src/components/PageTopBar.vue` | 有 `title` prop 但不渲染 | 改成顶栏：标题、返回、会话下拉、功能菜单、右侧插槽（2026-11-02 起：列表页的桌面顶栏改由 `PageHeader` + `TopBarActions` 承担，本组件只服务聊天页与详情/管理页） |
| `front-end/src/composables/useMobile.js` | 每个组件各挂一个 resize 监听；断点 768 | 单例 store；断点改 1100；提供 `isPhone`/`isDesktop` |
| `front-end/src/style.css` | Slate + Indigo token | 换成 macOS token；保留语义变量名，减少页面改动 |
| `front-end/src/router/index.js` | 路由无 meta 布局 | 加 `meta.layout = 'shell' | 'auth' | 'share' | 'admin'` |
| 所有 `views/*.vue` | 根节点 `height: 100vh`、24px padding | 改为 `100dvh`、`min-height: 0`、响应式 padding |
| `front-end/index.html` | 普通 viewport | `viewport-fit=cover`、PWA meta、主题色 |

## 三、目标结构

```
front-end/src/
├── App.vue                       # 只做 NConfigProvider + AppShell
├── layouts/
│   └── AppShell.vue              # flex 壳 + 手机底部 Tab + 管理后台手机拦截
├── components/
│   ├── PageTopBar.vue            # 聊天页与详情/管理页的顶栏（列表页见 PageHeader + TopBarActions）
│   ├── ConversationSwitcher.vue  # 当前会话 + 搜索 + 列表 + 新建 + 置顶/归档
│   ├── BottomTabs.vue            # 对话/灵阅/文档/我的
│   ├── FunctionMenu.vue          # 桌面「功能」菜单
│   └── EmptyState.vue            # 统一空态（M2 抽）
├── stores/ui.js                  # isDesktop/isPhone、功能菜单开关（M2 抽）
└── style.css                     # macOS token + reset + safe-area + 正文样式
```

页面组件保持独立，不在 M1 里重写业务；先让它们在新壳里能正常滚动、能返回。

## 四、桌面 shell

- 顶栏高 52px，透明底，内容不铺底色（窗口底由 `body` 的暖米底 + 静态色块负责）。
- 对话页顶栏传 `alignCard`，桌面端内容与聊天卡片同宽（`max(24px, calc((100% - 1080px)/2))`）；其他通栏管理页保持贴边。
- 左侧：会话下拉胶囊（左端 `＋` 新建、右侧当前标题 + `▾`）。整个胶囊是一个容器，内部两个按钮（HTML 不允许 button 套 button）；`＋` 只建会话不展开面板。
- 右侧：`功能`、主题切换、头像菜单。
- 主区：居中聊天卡片，`max-width: 1080`（正文列 960），白霜 + 0.5px 描边 + 外投影，圆角 14。
- 聊天卡片顶部可以向上溢出包住顶栏一点（macOS 的 `cardTopRise`），Web 用负 margin 做近似即可；如果影响滚动，就放弃，保持普通卡片。
- 功能菜单是轻量 popover，不是常驻右栏：灵阅 / 文档 / 图片 / 账单 / 记忆 / 主题 / 智能体 / 管理（管理员）/ 设置。
- 管理后台路由保持全屏，`meta.layout = 'admin'`，带返回聊天。

## 五、手机 shell

- 顶栏高 48px，`padding-top: env(safe-area-inset-top)`。**手机不显示深浅色切换按钮**（主题切换在「设置」页；内嵌 WebView 本来也隐藏），顶栏右侧只留智能体胶囊。两个胶囊（会话 / 智能体）都有最大宽度（`calc(50vw - 20px)` 封顶，不越过顶栏中线）并单行省略，避免窄屏互相压盖（实测 390px：会话胶囊右边缘 189、智能体胶囊左边缘 206，中间留 17px；360/320 同样各留 12px）。
- iOS Safari 聚焦字号 <16px 的输入控件会自动放大整页：手机端所有输入控件字号保底 16px（`style.css` 末尾的 iOS 专用规则，聊天输入框取 `max(16px, --chat-font-size)`），双指缩放不受影响。
- 主区 `overflow-y: auto`，底部给输入区和 Tab 留空间。
- 底部 Tab 高 56px + `env(safe-area-inset-bottom)`；4 项：对话 / 灵阅 / 文档 / 我的。底栏是不透明 `--bottom-chrome`（与画布实底同色，见「PWA 底部白缝」），不再用半透明 + `backdrop-filter`。
- 聊天页输入区在 Tab 上方；键盘弹起时隐藏 Tab，输入区贴键盘上沿（`useKeyboardInset` 监听 `visualViewport` 写 `--keyboard-inset`）。
- 内容页（灵阅/文档/我的）用全屏路由，底部 Tab 保持可见；二级详情页隐藏 Tab，只留返回。
- 顶栏「返回」：手机维持 Tab 页不显示（Tab 已能切过去），桌面除聊天外一律显示；列表页固定回聊天（`meta.backToChat`），二级页（设置等）和详情页回上一页、无历史兜底 `/chat`。按钮文字 `white-space: nowrap`，窄屏不会被拆成上下两行。

### 安全区与键盘

```css
:root {
  --safe-top: env(safe-area-inset-top, 0px);
  --safe-bottom: env(safe-area-inset-bottom, 0px);
}
html, body, #app { height: 100%; }
.app-shell { height: calc(100dvh - var(--keyboard-inset, 0px)); }
```

- `index.html` 必须加 `viewport-fit=cover`。
- 输入区 `padding-bottom: max(12px, var(--safe-bottom))`。
- 监听 `visualViewport.resize`，把 `--keyboard-inset` 设为 `window.innerHeight - visualViewport.height - visualViewport.offsetTop`；输入区 `bottom: var(--keyboard-inset)`。
- 不用 `100vh`；所有 `height: 100vh` 改 `100dvh`。
- **PWA 顶部模糊/白条**：iOS 26 起，主屏 PWA 的状态栏区域会出现一条白色/发糊的带子（iOS 26.1 上是公认的回归）。两个成因分别处理：
  - **取色**：iOS 26 不认 `theme-color`，顶栏取色读的是根元素的 `background-color`，**不认 `background-image`**。`body` 的背景是 `--app-bg-image`（渐变=图片），拿不到实底就回退白色 —— 所以 `body` 必须显式补一条实底（现在是 `background-color: var(--bottom-chrome)`，同一层也负责底部回弹，见下一条）。
  - **底部橡皮筋白缝**：页面到底后再往上拖，iOS 会整体回弹，露出的是**画布实底**，渐变图片铺不进去 —— 原来实底取 `--color-bg`（浅色 `#F7EEDD`），而底栏脚下实际是 `#F9F5EC`，回弹时就会闪出一条白缝。现在 `html` / `body` 的实底与底栏统一用 `--bottom-chrome`（浅 `#F9F5EC`、深 `#1E1F21`，实测复合值），并加 `overscroll-behavior-y: none`（Safari 16+ 能直接拦掉；拦不下的机型靠颜色兜住）。**改底部渐变或底栏背景要重采这个值。**
  - **色块颜色**：iOS 拿 `.pwa-blur-killer` 的 `background-color` 涂状态栏，必须和「页面顶部中央（距顶 4px）」的实测像素一致，否则会出现一条明显的接缝。当前值 `--pwa-top-color`：浅色 `#E7E6D4`、深色 `#494C43`（**改背景渐变后要重新采一次**）。页面顶部是横向渐变，单色块无法逐点匹配，左右两端理论上仍有 ≤10/255 的差；要彻底消除得把页面顶部 ~70px 也压成同色。
  - **系统模糊**：WebKit 里 `_shouldHideTopScrollPocket == _hasVisibleColorExtensionView(Top)`，即上端存在「可见的 fixed 元素」时**不画模糊**，并拿它的 `background-color` 涂状态栏。判定：采样点在上端中央距顶 4px；元素或祖先须 `fixed`/`sticky`；宽高 >10px 且宽度 ≥ 视口 90%；`opacity < 0.1` 弃用；颜色读 computed style 而非渲染像素。据此在 `AppShell.vue` 放 `.pwa-blur-killer`：`height: 11px` + `z-index: 2147483647` + `background-clip: text`（空元素 → 一个像素都不画）。
  - 该机制被 `_obscuredInsets.top > 0` 门控：`black-translucent` 那种 edge-to-edge PWA 里整条链路是死的，任何元素都无效 —— 所以 `apple-mobile-web-app-status-bar-style` 不能是 `black-translucent`。
  - 已试过并**确认无效/更丑**的方案：把 `--safe-top` 加宽让开模糊带（顶部会露出一块白）、实心色块遮罩（伪元素与真实元素都试过）。

## 六、会话下拉

- 顶部胶囊显示当前会话标题；未选会话显示「新对话」。
- 点开下拉：
  - 搜索框（本地过滤 + 回车拉服务端搜索，若接口支持）
  - 「＋ 新对话」
  - 会话列表：标题、时间、当前项打勾、hover 出「置顶 / 归档」
  - 「加载更多」
  - 底部「已归档会话」入口
- 非管理员也必须看到自己的历史：去掉 `v-if="isAdmin"`，只保留管理组的管理员判断。
- 手机下拉用全屏 sheet（从顶部滑下），桌面用 popover。

## 七、视觉 token 落地

> UI 已确认：用户消息用**气泡**（浅色淡绿底、深色淡绿底），不是 macOS 的纯文本；桌面功能入口用顶栏菜单；手机输入区常驻贴底。

1. 先把 `style.css` 的变量值换掉，变量名不变，避免全站改引用。
2. 再补一组新变量：`--card`、`--input`、`--tool`、`--user-text`、`--shadow-card`。
3. Naive UI overrides 用同一组值：
   - `primaryColor` / hover / pressed 用 `--app-accent`；
   - `bodyColor` 用 `--bg`；
   - `cardColor` 用半透明白；
   - `textColorBase` 用 `--text`。
4. 删除硬编码色：`ChatView.vue` 的 `rgba(91,106,191,.85)`、`PlanGraph.vue` 的 `#2080f0` 一套、`ConversationList.vue` 的 `stroke="#666"`。
5. 分享页强制浅色保持不变，但 token 跟着换。

## 八、PWA 基础

- `front-end/public/manifest.webmanifest`：
  - `name`、`short_name`、`start_url: "/"`、`display: "standalone"`、`background_color`/`theme_color`、`icons` 192/512。
- `index.html`：
  - `<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">`
  - `<meta name="apple-mobile-web-app-capable" content="yes">`
  - `<meta name="apple-mobile-web-app-status-bar-style" content="default">`
  - `<link rel="manifest" href="/manifest.webmanifest">`
  - `<link rel="apple-touch-icon" href="/icons/icon-180.png">`
- 图标：`public/icons/icon-{180,192,512}.png` 来自海豚源图（2048×2048）：去掉水印、四角补成全出血正方形蓝底，圆角交给系统裁；标签页 `favicon-32/64.png` 用同一张图裁到海豚主体（原图缩到 16px 看不清）。
- **不加 service worker**，避免缓存旧版本和 iOS 的怪问题。

## 九、路由改造

新增/调整：

```
/chat/:id?            shell
/artifacts            shell（手机 Tab 灵阅）
/artifacts/:id        shell，详情页保留 Tab
/documents            shell（手机 Tab 文档）
/documents/:id        shell，详情页隐藏 Tab
/images               shell
/bills                shell
/memories             shell
/themes               shell
/agents               shell
/me                   shell（手机「我的」）
/settings             shell（M3）
/admin*               admin，仅桌面
/s/:token             share，无壳
```

- `router.beforeEach` 里：`/admin*` 在手机宽度下跳 `/me?admin=desktop-only` 或显示提示页。
- 底栏显隐的唯一判据是路由 `meta.hideTabs`（`AppShell.vue` 的渲染条件），规则是「这页还属不属于某个一级 tab 的内容」：
  - **属于 → 保留 Tab**：`/artifacts/:id`（灵阅详情是「灵阅」tab 里的内容）。
  - **不属于 → `hideTabs: true`**：挂在对话或「我的」下面的工具页与全屏页 —— `/memories` `/notes` `/bills` `/agents` `/prompts` `/tools` `/summaries` `/themes` `/images` `/settings`。
  - **已知例外**：`/documents/:id` 同属「文档」tab，但它底部有 `bottom: max(14px, --safe-bottom)` 的悬浮工具条（`DocumentReadView.vue` 的 `.mobile-bar`），放开 Tab 会被压住。要改先把这个 bottom 抬到 `--bottom-tabs-height` 之上，在此之前维持 `hideTabs`。
- 在详情页（如 `/artifacts/:id`）点已经激活的 tab 会回到它的根路径（列表），沿用 iOS 惯例，**不是 bug**。这条只在 `BottomTabs.go()` 里没有特例的 tab 上成立。

## 十、迁移顺序

1. `tokens.css` + `base.css`，先让全站换色但不改布局。
2. `App.vue` → `AppShell`，桌面顶栏 + 主区；暂时保留旧侧栏作为「历史」按钮。
3. `ConversationSwitcher` 取代旧侧栏；修复非管理员历史。
4. `BottomTabs` + `AppShell`；旧抽屉下线。
5. 逐页改 `100vh` → `100dvh`、`padding`、`overflow`。
6. 弹窗宽度自适应、表格/代码/图片溢出修复。
7. PWA manifest/meta/图标。
8. 管理后台手机拦截。
9. 全站截图自查（见下）。

## 十一、验收

- 手机 390×844：
  - 底部 Tab 不压手势条；聊天输入区不被键盘遮住；
  - 打开灵阅/文档/我的，Tab 状态正确；
  - 弹窗不横向溢出；表格/代码可横滑；正文可长按选中。
- 桌面 1440×900：
  - 顶栏会话下拉可用；聊天卡片居中；功能菜单能进各页；
  - 管理后台入口出现；手机宽度下不出现。
- 非管理员账号能看到自己的历史会话。
- `npm run build` 通过；无 `100vh` 残留（`rg "100vh" front-end/src` 应为 0，分享页/打印除外）。

## 十二、风险

- M1 是全局布局改造，容易把各页面的滚动/高度搞坏；先改壳、再逐页修，不要一次重写所有页面。
- `useMobile` 改成单例后，注意初始化时机（`App.vue` 挂载前 `window` 已存在）。
- iOS 键盘行为差异大，必须真机验；headless Chrome 只能看静态布局。

## 后续变更

- 2026-11-03：灵阅详情 `/artifacts/:id` 放开底部 Tab（用户要求：其他一级 tab 激活时底栏都不消失，保持一致）。去掉该路由的 `meta.hideTabs`，并删掉 `BottomTabs` 里恒为 false 的 `isHidden` 守卫（判据只在 `AppShell` 一处）。影响 `front-end/src/router/index.js`、`front-end/src/components/BottomTabs.vue`。文档阅读页 `/documents/:id` 维持隐藏，原因见第九节。
- 2026-11-06：PWA 会话列表不再陈旧（bug 见 `docs/bugs/046-pwa-conversation-list-stale.md`）。① 顶栏 `＋` 改为先 `POST /api/conversations` 再 `push('/chat/:id')`，不再靠首条消息懒创建（流式中切 Tab/锁屏断流会丢 `conversation_id`）；② 会话下拉每次打开都 `fetchList()`，原来只在列表为空时拉一次，PWA 挂起几天就几天不刷新；③ 新建逻辑收口到 `stores/conversations.js` 的 `create()`。影响 `front-end/src/components/{PageTopBar,ConversationSwitcher}.vue`、`front-end/src/stores/conversations.js`、`front-end/src/views/AgentView.vue`。代价：点了＋不发消息会留下一条空「新对话」。
- 2026-11-06（用户要求）：手机顶栏去掉深浅色切换按钮（主题改在「设置」页切）；会话胶囊 `max-width: min(340px, 100%)` + 标题 `flex: 0 1 auto`，`TopBarBtn` 的 pill 变体 `max-width: 170px` 且文字单行省略 —— 修掉窄屏下会话胶囊溢出、盖住旁边 `＋` 的错位。影响 `front-end/src/components/{TopBarActions,ConversationSwitcher,TopBarBtn}.vue`。
- 2026-11-06（用户反馈）：修手机端「使劲往上拖，底栏和屏幕底之间出现白缝」。根因是 iOS 橡皮筋回弹露出画布实底（渐变图片铺不进去），实底 `--color-bg` 与底栏脚下的实际色差一档。新增 `--bottom-chrome`（浅 `#F9F5EC` / 深 `#1E1F21`），`html`/`body` 实底与 `.bottom-tabs` 背景统一用它（底栏改为不透明，去掉无实际效果的 `backdrop-filter`），并加 `overscroll-behavior-y: none`。影响 `front-end/src/style.css`、`front-end/src/components/BottomTabs.vue`。
- 2026-11-06（用户要求）：`＋` 收进会话胶囊左端（桌面 + 手机同形），不再是胶囊旁边的独立按钮。`ConversationSwitcher` 的外框从 `<button>` 改为 `<div>`，内部分「＋ 新建」与「标题 + ▾ 展开」两个按钮；新建逻辑只剩 `createConversation()` 一处，`PageTopBar` 删掉 `newChat()` / `creating` / 两个无用 import。`＋` 是主题色淡底（12%）+ 主题色描边/图标的圆圈，悬停加深到 22%；胶囊悬停背景改用 `--color-surface`（否则和圆圈同色，圆会消失）。两个胶囊统一 `calc(50vw - 20px)` 封顶 —— 谁都不许越过顶栏中线，超长一律省略号。影响 `front-end/src/components/{ConversationSwitcher,PageTopBar,TopBarBtn}.vue`。

