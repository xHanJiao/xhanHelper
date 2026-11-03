# M3 · 文档阅读器、只读批注、设置与会话能力

> 前置：`01-m1-shell-mobile.md`、`02-m2-chat-artifacts-images.md`
> 目标：补齐内容深度，让 Web 不只是聊天。
> 预估：15–22h

## 一、范围

- 文档完整阅读器：目录、正文、分享、下载、图片 Lightbox
- 只读批注：批注列表 + 点击定位 + 临时高亮
- 设置页：外观、字号、账号、已归档会话、关于
- 会话能力：置顶/归档恢复、无痕、plan 模式 + 计划便签
- 灵阅增强：标签筛选、搜索、多选删除
- 可选：图片库 `/images`

不做：划线创建、AI 批注回复线程、文档编辑、灵阅富文本编辑。

## 二、文档阅读器 `/documents/:id`

### 布局

桌面：

```
┌──────────────────────────────────────────────┐
│ 顶栏：返回 · 标题 · 分享 · 下载 · 字号        │
├──────────────┬───────────────────────────────┤
│ 目录 220px   │ 正文 728px（居中）             │
│              │                               │
└──────────────┴───────────────────────────────┘
```

手机：

```
┌──────────────────────────┐
│ 顶栏：返回 · 标题 · ⋯    │
├──────────────────────────┤
│ 正文（全宽，18px padding）│
├──────────────────────────┤
│ 目录抽屉（点按钮打开）    │
└──────────────────────────┘
```

### 功能

- `GET /api/documents/:id` 拿完整文档（含 `content`、`tags`、`updated_at`）。
- 正文用 `MarkdownRenderer`；标题、表格、代码、图片沿用 M2 样式。
- 目录：从 markdown 的 `h1–h3` 生成；桌面左栏，手机底部抽屉；点击滚动到标题。
- 图片：远程图片 / `image:<id>` 走 `AuthImage`；点击 Lightbox。
- 分享：`GET /api/documents/:id/share`，没有就 `POST`；复制 `origin + url`。
- 下载：`.md` Blob 下载；导出 PDF 可复用现有 `useExportPDF`。
- 字号：`--doc-font-scale`，桌面 100–125%，手机 100–120%；存 localStorage。
- 返回：优先 `router.back()`，没有历史回 `/documents`。

### 批注（只读）

数据：`GET /api/documents/:id/annotations?limit=200&offset=0`，返回 `annotationItem`：

```json
{
  "id": "...",
  "quote": "...",
  "note": "...",
  "start_offset": 123,
  "end_offset": 130,
  "orphaned": false,
  "quote_rewritten": false,
  "reply_count": 2,
  "last_reply": { "role": "assistant", "content": "..." }
}
```

> `start_offset/end_offset` 是**源文 rune 偏移**，和渲染后的 DOM 位置不是一回事。

Web 只读方案（简化）：

1. 批注列表按 `start_offset` 排序，显示 `quote` + `note` + 回复数。
2. 点击条目：在正文渲染后的 DOM 里按 `quote` 文本查找第一个匹配（用 `TreeWalker` 遍历文本节点），找到后：
   - `scrollIntoView({ block: 'center' })`
   - 用 `CSS Custom Highlight API` 或临时 `<mark class="anno-flash">` 包一层，1.2s 后移除。
   - 不做精确 range 划线，避免 rune offset 与 markdown 渲染位置的映射错误。
3. `orphaned` 显示「原文已改写」徽标；`quote_rewritten` 显示「原文有改动」。
4. 只读：不提供新建、回复、删除按钮。

为什么不用精确 offset：源文是 markdown，渲染后 DOM 多了标题/列表/代码结构，rune offset 不能直接映射；先按 quote 搜，够用且不会错位。将来要做划线创建时再引入「渲染后文本偏移 → DOM range」的映射。

### 目录与批注布局

- 桌面：左侧 220px 目录、中间 728px 正文、右侧 320px 批注（和 macOS 一致）。窗口窄于 1200px 时右侧批注收起，顶部按钮打开抽屉。
- 手机：正文全宽；底部浮动条「目录 / 批注 / 字号」，分别开抽屉。
- 批注面板只读，不出现新建/回复输入框。

## 三、设置页 `/settings`

分组：

1. **外观**
   - 浅色 / 深色 / 跟随系统（存 `xhanhelper_theme`：`light|dark|system`，未设置默认 `system`；内嵌 macOS WebView 恒跟随系统；`index.html` 首帧脚本同步，避免闪烁）。手机顶栏另有深浅快捷切换按钮（`TopBarActions` 的主题按钮），点一下直接切 `light`/`dark`，「跟随系统」仍只在设置页选
   - 字号：小 / 标准 / 大（13.5 / 15 / 16.5px，写 `--chat-font-size`，**只作用于聊天气泡**；存 `chat.fontScale`）。文档阅读器字号独立（`doc.fontScale`），两者不合并
2. **账号**
   - 用户名、修改密码（复用现有弹窗）、退出登录
3. **会话**
   - 已归档会话：分页（50 条/页，显示接口 `total`，底部「加载更多」）；恢复后同步 `total`，当前页空且仍有剩余时自动补一页
   - 无痕会话说明（不默认开启）
4. **关于**
   - 版本（构建期注入 `package.json` version + git 短 hash + 脏标记 `-dirty`；`0.0.0` 时只显示 hash）、前端构建模式、手机端 PWA 安装提示

管理后台入口：仅桌面端显示「管理」分组，链接 `/admin`、`/admin/users`、`/admin/eval`。

## 四、会话能力

### 置顶 / 归档 / 恢复

- 会话下拉行 hover 出「置顶 / 归档」；置顶用 `PATCH /api/conversations/:id`（若没有 pin 字段，先做前端本地置顶，后端有字段再接）。
- 归档 = `DELETE /api/conversations/:id`（现有语义是软删/归档）。
- 恢复：`POST /api/conversations/:id/restore`。
- 「已归档会话」放设置页，分页加载。

> 先确认 `Conversation` 是否有 `pinned` 字段；没有就 M3 只做归档/恢复，置顶后置。

### 无痕会话

- 输入区 pill：`ephemeral`。
- 新会话首条消息带 `ephemeral: true`；已有会话 `PATCH /api/conversations/:id/ephemeral`。
- 顶栏显示 🎭 徽标；列表里无痕会话有标记。
- 销毁由服务端 `session_end` 延迟任务负责；前端只负责标记和提示。
- 默认会话禁止无痕（与 macOS 一致）。

### plan 模式

- 输入区 pill：`mode`，切换 `work/plan`，`PATCH /api/conversations/:id/mode`。
- plan 模式：不执行写工具（服务端已限制），前端输入区用橙色描边提示。
- 计划便签：`GET /api/conversations/:id/plans` 拿 goal + steps；输入区上方小卡片，显示完成度，可收起。
- **不做** `PlanGraph`（macOS 和 Web 都是死代码）。

## 五、灵阅增强

- 标签筛选：`GET /api/artifacts/tags`，顶部 chip；点击筛选列表。
- 搜索：前端按标题/摘要过滤；服务端有 keyword 参数就用。
- 多选删除：卡片勾选圈 + 页头「已选 N / 取消 / 删除」；`POST /api/artifacts/batch-delete`，确认文案说明「不可恢复、已分享链接立即失效」。
- 标签词云：可后置；先做 chip，不做词云。
- 缓存：Web 不需要 ETag 本地缓存；用浏览器 HTTP 缓存 + 内存缓存即可。

## 六、图片库 `/images`（可选）

- `GET /api/images?limit=&offset=` 网格。
- 每张显示缩略图、大小、来源、使用中标记。
- 上传、删除（引用中二次确认）。
- 手机从「我的 → 图片」进入；桌面从功能菜单进入。

## 七、组件与文件

```
views/
├── DocumentReaderView.vue
├── SettingsView.vue
├── ImagesView.vue
└── MeView.vue                 # 手机「我的」聚合
components/
├── document/
│   ├── DocToc.vue
│   ├── AnnotationList.vue
│   └── DocToolbar.vue
├── settings/
│   ├── SettingsCard.vue       # 对齐 macOS SettingsCard
│   └── SettingsRow.vue
└── chat/
    ├── ModePill.vue
    ├── EphemeralPill.vue
    └── PlanNote.vue
stores/
├── settings.js                # 主题/字号/偏好
└── plans.js                   # 当前会话计划
```

## 八、后端接口清单

| 能力 | 接口 | 状态 |
|---|---|---|
| 文档详情 | `GET /api/documents/:id` | 已有 |
| 文档分享 | `GET/POST /api/documents/:id/share` | 已有 |
| 批注列表 | `GET /api/documents/:id/annotations` | 已有 |
| 会话模式 | `PATCH /api/conversations/:id/mode` | 已有 |
| 无痕 | `PATCH /api/conversations/:id/ephemeral` | 已有 |
| 计划 | `GET /api/conversations/:id/plans` | 已有 |
| 归档恢复 | `POST /api/conversations/:id/restore` | 已有 |
| 灵阅批量删 | `POST /api/artifacts/batch-delete` | 已有 |
| 图片库 | `GET /api/images` | 已有 |

M3 基本不需要新增后端接口；主要是前端接线。

## 九、验收

- 文档页：桌面目录可跳、手机目录抽屉可用；图片 Lightbox；分享链接能打开。
- 批注：列表显示；点击能滚到原文并临时高亮；失活/改写有徽标；不出现编辑/回复入口。
- 设置：主题三选一生效且持久化；字号生效；已归档会话能恢复；退出登录正常。
- 无痕：新建后顶栏有徽标；会话列表有标记；服务端销毁后列表移除。
- plan 模式：切换后输入区有提示；计划便签显示步骤和完成度。
- 手机访问 `/admin` 显示「请用桌面端打开」。

## 十、风险

- 批注定位用 quote 搜索会有重复文本命中第一个的问题；先接受，后续做精确映射。
- 无痕会话的销毁是服务端延迟任务，前端不要自己删本地数据，否则会和服务端不一致。
- plan 模式的服务端只读白名单要确认 Web 工具都在其中；否则会出现「UI 说只读，实际能写」。
- 设置页会碰主题 store 现有「按时间自动切」逻辑：已改为默认跟随系统，并同步 `front-end/index.html` 首帧脚本（`system` 与分享页浅色都按同一规则解析），避免 FOUC。
