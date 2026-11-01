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
| `front-end/src/components/PageTopBar.vue` | 有 `title` prop 但不渲染 | 改成唯一顶栏：标题、返回、会话下拉、功能菜单、右侧插槽 |
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
│   ├── PageTopBar.vue            # 唯一顶栏：标题/返回/会话下拉/功能菜单/右侧插槽
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
- 左侧：会话下拉胶囊（当前标题 + `▾`），旁边 `＋` 新建。
- 右侧：`功能`、主题切换、头像菜单。
- 主区：居中聊天卡片，`max-width: 760`，白霜 + 0.5px 描边 + 外投影，圆角 14。
- 聊天卡片顶部可以向上溢出包住顶栏一点（macOS 的 `cardTopRise`），Web 用负 margin 做近似即可；如果影响滚动，就放弃，保持普通卡片。
- 功能菜单是轻量 popover，不是常驻右栏：灵阅 / 文档 / 图片 / 账单 / 记忆 / 主题 / 智能体 / 管理（管理员）/ 设置。
- 管理后台路由保持全屏，`meta.layout = 'admin'`，带返回聊天。

## 五、手机 shell

- 顶栏高 48px，`padding-top: env(safe-area-inset-top)`。
- 主区 `overflow-y: auto`，底部给输入区和 Tab 留空间。
- 底部 Tab 高 56px + `env(safe-area-inset-bottom)`；4 项：对话 / 灵阅 / 文档 / 我的。
- 聊天页输入区在 Tab 上方；键盘弹起时隐藏 Tab，输入区贴键盘上沿（用 `visualViewport` 或 `dvh` 兜底）。
- 内容页（灵阅/文档/我的）用全屏路由，底部 Tab 保持可见；二级详情页隐藏 Tab，只留返回。

### 安全区与键盘

```css
:root {
  --safe-top: env(safe-area-inset-top, 0px);
  --safe-bottom: env(safe-area-inset-bottom, 0px);
}
html, body, #app { height: 100%; }
.app-shell { height: 100dvh; }
```

- `index.html` 必须加 `viewport-fit=cover`。
- 输入区 `padding-bottom: max(12px, var(--safe-bottom))`。
- 监听 `visualViewport.resize`，把 `--keyboard-inset` 设为 `window.innerHeight - visualViewport.height - visualViewport.offsetTop`；输入区 `bottom: var(--keyboard-inset)`。
- 不用 `100vh`；所有 `height: 100vh` 改 `100dvh`。

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
- 图标：用现有 `favicon.svg` 生成 180/192/512 PNG；暖米底 + 墨绿字/图形。
- **不加 service worker**，避免缓存旧版本和 iOS 的怪问题。

## 九、路由改造

新增/调整：

```
/chat/:id?            shell
/artifacts            shell（手机 Tab 灵阅）
/artifacts/:id        shell，详情页隐藏 Tab
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
- 详情页 `meta.hideTabs = true`，手机隐藏底部 Tab。

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
