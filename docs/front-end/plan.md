# Web 前端方案

> 日期: 2026-05-04 | 更新: 2026-05-09 | 状态: 待开发

## 概述

基于 Vue 3 + Naive UI + Vite 的单页应用（SPA），提供与后端 xhanHelper 一致的聊天、记忆管理、智能体管理功能。通过 nginx 部署静态文件，反向代理 API 到 Go 后端。

## 开发路线图

按依赖关系和交付价值分 4 个 Phase，顺序开发。Phase 2 和 Phase 3 可部分并行。

### Phase 1 — 基础聊天 MVP

目标：用户能登录、对话、看历史、管会话。最薄可用的端到端闭环。

| 内容 | 说明 |
|------|------|
| 项目脚手架 | Vite + Vue 3 + Naive UI + vue-router + pinia |
| `api/client.js` | fetch 封装，Authorization 注入，401 统一拦截 |
| `stores/auth.js` | 登录/注册/登出，token → localStorage 持久化，`storage` 事件多标签页同步 |
| 路由守卫 | `beforeEach`：未登录 → `/login`，已登录访问 `/login` → `/chat` |
| LoginView | 用户名 + 密码表单，登录/注册切换 |
| ChatView（基础版） | 仅处理 `message` / `done` / `error` 三种 SSE 事件 |
| ConversationList | 会话列表加载、新建、重命名、删除 |
| 消息历史 | 进入 `/chat/:id` 调 `messages-v2` 加载，cursor 向上翻页 |
| `composables/useChatSSE.js` | 基础版：SSE 连接 → `parseSSEStream` → 派发 text/done/error |

**交付物**：用户能登录、发起对话、看到流式回复、回看历史、管理会话列表。

### Phase 2 — 丰富聊天体验

目标：SSE 所有事件类型对齐，工具调用和思考过程可视化。

| 内容 | 说明 |
|------|------|
| ThinkingBlock 组件 | `thought_delta` 逐字追加到可折叠区域，`thought_done` 自动折叠 |
| 工具调用卡片 | `tool_call` → 显示 loading 卡片（工具名 + 函数名），`tool_result` → 更新为成功/失败 |
| Plan DAG 可视化 | `plan_node_start/done` 实时更新节点状态，`plan` 事件渲染完整 DAG 图（详见 [plan-dag.md](plan-dag.md)） |
| `status` 事件 | inline badge 显示"已关联 N 条记忆"等状态通知 |
| 深度思考开关 | 顶部 `n-switch` 切换 `deep_mode` |
| 智能体选择器 | `n-select` 加载 `/api/agents` 列表，选中后发消息带 `agent_id` |
| 错误恢复 | 网络断开提示重试、SSE 超时处理、发送中防重复提交 |
| `useChatSSE.js` 补全 | 加入 Phase 2 全部 7 种事件处理 |

### Phase 2 设计决策

> 2026-05-09 讨论确认

| # | 项 | 决策 |
|---|-----|------|
| 1 | ThinkingBlock 折叠行为 | 流式时自动展开，`thought_done` 后自动折叠；历史消息默认折叠 |
| 2 | status 事件展示 | inline badge，插入 AI 消息气泡内，流式过程中实时追加 |
| 3 | 智能体选择器默认值 | 不选（`agent_id` 为空），使用后端默认 System Prompt |
| 4 | 流式中离开页面 | abort SSE + 保留已收部分内容 + 回来时调 `messages-v2` 重拉最新消息覆盖本地 |
| 5 | 历史消息展示 | 思考默认折叠、工具调用折叠为一行摘要（如"已调用 3 个工具"） |
| 6 | MessageBubble 拆分 | 拆成 `ThinkingBlock.vue` + `ToolCallCard.vue` 子组件 |
| 7 | Plan DAG | 手写拓扑分层布局，详见 [plan-dag.md](plan-dag.md) |
| 8 | 重拉机制 | 和 iOS 客户端一致：调 `GET /api/conversations/{id}/messages-v2` 全量替换，不新增后端端点 |

### Phase 3 — 实体管理页面

目标：聊天中创建的数据（记忆/笔记/账单/智能体）都有独立管理页面。可与 Phase 2 并行。

| 内容 | 说明 |
|------|------|
| AgentView | 智能体 CRUD（**须在 Phase 2 智能体选择器之前或同期完成**） |
| MemoryView | 记忆列表、搜索、单条删除、批量删除 |
| NotesView | 笔记列表、搜索、删除 |
| BillsView | 账单列表、日期/分类筛选、CSV 导入导出 |

### Phase 3 设计决策

> 2026-05-09 讨论确认

| # | 项 | 决策 |
|---|-----|------|
| 1 | 账单筛选 | 后端 `GET /api/bills` 加 `start_date`/`end_date`/`category` 查询参数（`ListFiltered` 已支持，补 `limit`/`offset`） |
| 2 | 智能体模型字段 | `n-select` 下拉预设：deepseek-chat / deepseek-reasoner / doubao |
| 3 | 记忆来源展示 | 转换为中文：conversation → 对话提取，csv_import → CSV导入 |
| 4 | CSV导入结果反馈 | `n-message` toast 显示 "导入完成：成功 N 条，跳过 M 条" |
| 5 | 笔记展示方式 | `n-card` 卡片列表（非 n-data-table） |
| 6 | 后端改动 | `BillRepo.ListFiltered` 签名增加 `limit, offset int`；导出调用传 `10000, 0` |

### Phase 4 — 高级定制

目标：开发者/高级用户功能，锦上添花。

| 内容 | 说明 |
|------|------|
| PromptView | 自定义 System Prompt，版本管理 + 回滚 |
| ToolView | 动态工具注册管理 |
| 深色模式 | `n-config-provider` 切换，持久化到 localStorage |
| 设计系统 | 引入 CSS 变量（`--color-primary`、`--radius-card` 等），统一 token 化管理，改一行生效全局 |
| 移动端适配 | 响应式布局（三栏 → 单栏 + 抽屉导航） |

### 依赖关系

```
Phase 1 (聊天 MVP)
    │
    ▼
Phase 2 (丰富聊天体验) ──────┐
    │                        │
    ▼                        ▼
Phase 3 (实体管理页面)    Phase 4 (高级定制)
```

- Phase 2 严格依赖 Phase 1
- Phase 3 可与 Phase 2 并行，但 AgentView 需在 Phase 2 智能体选择器之前完成
- Phase 4 完全独立，可在任意阶段插入

## 技术栈

| 层 | 技术 |
|---|------|
| 框架 | Vue 3 (Composition API) |
| UI 组件库 | Naive UI |
| 构建 | Vite |
| 路由 | vue-router (含路由守卫) |
| 状态管理 | pinia |
| HTTP | fetch (封装，含 401 拦截) |
| 流式 | SSE (ReadableStream) |
| Markdown | markdown-it |
| 样式 | Naive UI 内置 + Tailwind CSS 辅助 |

## 部署架构

```
浏览器 → :443 (nginx)
            /        → /srv/xhan-helper/front-end/dist/  (静态文件, SPA fallback)
            /api/*   → localhost:8088                           (Go 后端, proxy_pass)
            /images/*→ localhost:8088                           (图床公开读, proxy_pass)
```

- nginx 负责前端静态文件，不替换
- Go 后端完全不需要改动
- `vite build` 产物 scp 到服务器即可

nginx 配置示例：

```nginx
server {
    listen 443 ssl;
    server_name helper.xhanx.cn;

    root /srv/xhan-helper/front-end/dist;
    index index.html;

    location /api/ {
        proxy_pass http://127.0.0.1:8088;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;  # SSE 长连接
        proxy_buffering off;
    }

    # 图床公开读：必须反代到 Go，否则落 SPA fallback 回吐首页（见 docs/bugs/031）
    location /images/ {
        proxy_pass http://127.0.0.1:8088;
        proxy_set_header Host $host;
    }

    location / {
        try_files $uri $uri/ /index.html;  # SPA fallback
    }

    # 单图上限 32 MiB，留余量；默认 1M 会让大图上传 413
    client_max_body_size 40M;
}
```

> 真实（含 HTTPS/证书/安全头/超时）以 `server/nginx-helper.conf` 为准，改后需手动装到线上并 reload。

## 目录结构

```
web/
├── package.json
├── vite.config.js
├── .env.development      # VITE_API_BASE=http://localhost:8080
├── .env.production       # VITE_API_BASE=
├── index.html
└── src/
    ├── main.js            # 入口：挂载 app + router + pinia
    ├── App.vue            # 根组件：n-config-provider 深色模式
    ├── api/
    │   └── client.js      # fetch 封装，统一 Authorization + 401 拦截 + error handling
    ├── composables/
    │   └── useChatSSE.js  # SSE 流式对话：连接 → 解析 → 事件派发 → 错误恢复
    ├── stores/
    │   ├── auth.js        # pinia: token, user, login/logout
    │   ├── chat.js        # pinia: 当前会话、消息列表、流式状态、plan 节点
    │   └── conversations.js # pinia: 对话列表
    ├── router/
    │   └── index.js       # 路由定义 + beforeEach 守卫（未登录重定向）
    ├── views/
    │   ├── LoginView.vue  # 登录/注册
    │   ├── ChatView.vue   # 主聊天页（含对话列表 + 聊天区 + plan 可视化）
    │   ├── MemoryView.vue # 记忆列表 + 批量操作
    │   ├── NotesView.vue  # 笔记列表 + 搜索 + 删除
    │   ├── BillsView.vue  # 账单列表 + CSV 导入导出
    │   ├── AgentView.vue  # 智能体管理
    │   ├── PromptView.vue # 自定义 System Prompt 管理
    │   └── ToolView.vue   # 动态工具管理
    └── components/
        ├── ConversationList.vue  # 左侧对话列表（含搜索/删除/重命名）
        ├── MessageBubble.vue     # 消息气泡（含思考过程 + 工具调用展示）
        ├── PlanGraph.vue         # Plan DAG 可视化（本轮工具调用链路）
        └── ThinkingBlock.vue     # 思考模式思维链展示（可折叠）
```

## 路由

| 路径 | 页面 | 说明 | 需认证 |
|------|------|------|--------|
| `/login` | LoginView | 登录/注册 | 否 |
| `/chat` | ChatView | 主聊天，默认页 | 是 |
| `/chat/:id` | ChatView | 进入指定会话 | 是 |
| `/memories` | MemoryView | 记忆列表 + 批量删除 | 是 |
| `/notes` | NotesView | 笔记列表 + 搜索 | 是 |
| `/bills` | BillsView | 账单管理 + CSV 导入导出 | 是 |
| `/agents` | AgentView | 智能体管理 | 是 |
| `/prompts` | PromptView | 自定义 System Prompt（版本化） | 是 |
| `/tools` | ToolView | 动态工具注册管理 | 是 |

### 路由守卫

```js
// router/index.js
router.beforeEach((to, from, next) => {
  const auth = useAuthStore()
  if (to.path !== '/login' && !auth.token) {
    next('/login')
  } else if (to.path === '/login' && auth.token) {
    next('/chat')
  } else {
    next()
  }
})
```

## 状态管理 (Pinia Stores)

### auth.js

```js
{
  state: { token, user, loading }
  actions: { login(username, password), register(...), logout() }
  persist: { token, user } → localStorage  // 多标签页共享
}
```

- `logout()` 调 `POST /api/auth/logout` 后清空 token，跳转 `/login`
- 监听 `storage` 事件：其他标签页登出时本页同步清空 token

### chat.js

```js
{
  state: {
    conversationId,       // 当前会话 ID（null = 新对话）
    agentId,              // 当前选中的智能体
    deepMode,             // 深度思考开关
    messages,             // 已持久化的消息列表（来自 messages-v2）
    streamingMessage,     // 当前流式接收中的 AI 消息（SSE 未完成时）
    planNodes,            // { [nodeId]: { label, status } }  当前 ReAct 轮的工具节点
    planEdges,            // [{ from, to }]
    isStreaming,          // 是否正在接收 SSE
  }
  actions: { ... }
}
```

- `messages` 和 `streamingMessage` 分离：前者是 DB 中已保存的消息（不可变），后者是当前 SSE 轮次的临时消息（实时更新）。`done` 事件时，`streamingMessage` 被固化为 `messages` 的最后一个元素并清空。
- `planNodes` / `planEdges`：收到 `plan_node_start/done` 时更新节点状态，收到 `plan` 事件时整体替换。

### conversations.js

```js
{
  state: { list, loading }
  actions: { fetchList(), rename(id, title), remove(id) }
}
```

- 列表从 `GET /api/conversations` 加载（最近 50 条，无需分页）
- 新会话创建后（收到 `done` 事件带 conversation_id）插入列表头部

## 页面设计

### LoginView

- `n-card` 居中，内含 `n-form`
- 用户名 + 密码 + 登录/注册切换
- 登录成功 → 存 token 到 pinia + localStorage → 跳转 `/chat`
- 注册需后端 `registration_enabled=true`，否则提示不可注册

### ChatView（核心）

布局：

```
┌──────┬──────────────────┬──────────────────────────────────────────┐
│ 导航  │   对话列表         │      聊天区                               │
│      │                  │                                          │
│ 💬   │  + 新建对话        │  🔲 深度思考  [智能体选择 ▼]              │
│ 🧠   │  ┌──────────┐    │  ┌──────────────────────────────────────┐│
│ 🤖   │  │会话1   ✏️🗑️│   │  │ 用户消息气泡 (n-card 蓝色)          ││
│ 📝   │  │会话2       │   │  ├──────────────────────────────────────┤│
│ 📊   │  │...         │   │  │ AI 回复                             ││
│ 🔧   │  │            │   │  │  ├─ 思考过程 (可折叠, thought_delta) ││
│      │  │            │   │  │  ├─ 回复正文 (Markdown)             ││
│      │  │            │   │  │  ├─ 工具调用卡片 (tool_call/result)  ││
│      │  │            │   │  │  └─ 状态提示 (status 事件)          ││
│      │  └────────────┘   │  └──────────────────────────────────────┘│
│      │                  │  ┌──────────────────────────────────────┐│
│      │                  │  │ Plan DAG 可视化 (plan 事件, 可收起)  ││
│      │                  │  └──────────────────────────────────────┘│
│      │                  │  ──────────────────────────────────────── │
│      │                  │  n-input textarea              [发送]     │
└──────┴──────────────────┴──────────────────────────────────────────┘
```

- **导航栏**：`n-layout-sider` 窄模式，icon 按钮切换页面（聊天/记忆/笔记/账单/智能体/提示词/工具）
- **对话列表**：`n-layout-sider` 中等宽度
  - 列表基于 `n-menu` 或自定义 `v-for`
  - 每个会话：标题 + 右侧重命名/删除 icon
  - 最近 50 条，一次性加载（`GET /api/conversations`），无需滚动分页
  - 新会话创建后自动插入列表头部
- **聊天区**：`n-layout-content`
  - 顶部工具栏：
    - `n-switch` 切换深度思考模式（`deep_mode`）
    - `n-select` 选择智能体（`GET /api/agents`），选中后发消息带 `agent_id`
  - 进入 `/chat/:id` 时先调 `GET /api/conversations/:id/messages-v2` 加载历史消息，cursor 分页向上滚动加载更多
  - 消息列表自动滚到底部（新消息到达时），历史翻页时保持滚动位置
  - 每条 AI 消息气泡包含：
    - **思考过程**：`n-collapse` 包裹，收到 `thought_delta` 时逐字追加，收到 `thought_done` 时折叠
    - **回复正文**：Markdown 渲染
    - **工具调用卡片**：`n-card` 嵌套，显示工具名 + 函数名，`tool_call` 时展示为 loading，`tool_result` 时更新为成功/失败状态
  - **Plan 可视化**：收到 `plan_node_start/done` 时构建节点状态，收到 `plan` 事件时渲染完整 DAG 图（节点=工具调用，边=执行顺序）
  - **状态提示**：收到 `status` 事件时以轻量 toast 或 inline badge 显示（如"已关联 3 条记忆"）
  - 输入 `Enter` 发送，`Shift+Enter` 换行。发送时若 `!isStreaming` 才允许发送
  - 发送请求体示例：
    ```json
    {
      "message": "帮我记一笔账",
      "conversation_id": "uuid-xxx",
      "deep_mode": true,
      "agent_id": "agent-uuid",
      "memory_threshold": 0.5,
      "memory_limit": 8
    }
    ```

### MemoryView

- `n-data-table` 展示记忆列表
- 列：内容摘要、来源、时间
- 分页（`n-pagination`）
- 右上角搜索框（keyword 模糊搜索）
- 多选 + 批量删除（`POST /api/memories/batch-delete`）
- 每行右侧删除按钮（`DELETE /api/memories/:id`）
- 支持批量获取（`POST /api/memories/batch`）

### NotesView（新增）

笔记通过聊天中的 `note` 工具创建（带向量检索），此页面提供浏览和管理入口。

- `n-data-table` 或 `n-card` 列表展示笔记
- 每条笔记显示：标题、正文摘要、创建时间
- 搜索框（后端 keyword 模糊搜索，`GET /api/notes?keyword=xxx`）
- 点击展开查看全文
- 每行删除按钮（`DELETE /api/notes/:id`）
- 分页（`n-pagination`）

### BillsView（新增）

账单通过聊天中的 `bookkeeping` 工具创建，此页面提供浏览和 CSV 导入导出入口。

- `n-data-table` 展示账单列表
- 列：项目、金额、一级/二级分类、消费类型、日期、支付方式、归属人、备注
- 筛选：日期范围（`n-date-picker`）、分类下拉、消费类型
- 右上角按钮组：
  - **导入 CSV**：`n-upload` 组件，accept `.csv`，调 `POST /api/bills/import`（Content-Type: text/plain）
  - **导出 CSV**：下载按钮，调 `GET /api/bills/export`，触发浏览器下载
- 分页（`n-pagination`）

### AgentView

- `n-card` 列表展示智能体
- `n-modal` / `n-drawer` 创建/编辑
  - `n-form`：名称、system prompt（textarea）、模型
- 每张 card 右侧编辑/删除按钮

### PromptView

- 列表展示用户自定义的 System Prompt（`GET /api/prompts`）
- 创建：`n-modal` 内含 `n-form`（名称、prompt 文本、类型 flash/pro）
- 每条记录显示当前版本号 + 创建时间
- 操作：编辑（创建新版本）、回滚到指定版本（`POST /api/prompts/:id/rollback`）、删除
- 说明：System Prompt 分为 flash（日常对话）和 pro（深度思考）两种类型，在对应模型下自动启用

### ToolView

- 列表展示动态工具（`GET /api/tools`）
- 每条记录显示名称、描述、schema 预览
- 创建/编辑：`n-modal` 内含 `n-code` 编辑器编辑 schema_json
- 删除（`DELETE /api/tools/:id`）
- 说明：内置工具（bookkeeping/datetime/uuid/calculator/note/agent）不可编辑删除，仅动态工具可管理

## SSE 流式对话

### 事件类型总览

| event | data 格式 | 触发时机 | 前端处理 |
|-------|----------|----------|----------|
| `message` | `{"delta":"好"}` | LLM 回复文本，逐字推送 | `JSON.parse` 后追加到 AI 消息 content |
| `thought_delta` | `"思考内容..."`（纯字符串） | 思考模式思维链，逐字 | 追加到思考块（可折叠） |
| `thought_done` | `""` | 思考完成 | 折叠思考块 |
| `tool_call` | `{"tool":"...","function":"...","args":{...}}` | 检测到工具调用 | 显示工具卡片（loading） |
| `tool_result` | `{"success":true,"data":{...}}` | 工具执行完成 | 更新卡片状态 |
| `plan_node_start` | `{"id":"tool.func","label":"...","status":"running"}` | 工具开始 | Plan DAG 节点 running |
| `plan_node_done` | `{"id":"tool.func","label":"...","status":"done"}` | 工具完成 | 节点 done/failed |
| `plan` | `{"nodes":[...],"edges":[...]}` | 本轮工具汇总 | 渲染完整 DAG |
| `status` | `{"message":"已关联向量记忆","count":3}` | 后台通知 | inline badge |
| `done` | `{"conversation_id":"...","message_id":"...","title":"...","memory_ids":[...],"note_ids":[...],"error":"..."}` | 本轮结束 | 固化消息、更新会话 ID |
| `error` | `"错误信息"`（纯字符串） | 异常 | toast + 终止流式 |

### `done` 事件完整字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `conversation_id` | string | 是 | 会话 ID |
| `message_id` | string | 否 | 本次生成的 assistant 消息 ID |
| `memory_ids` | string[] | 否 | 本次关联的记忆 ID 列表 |
| `note_ids` | string[] | 否 | 本次关联的笔记 ID 列表 |
| `error` | string | 否 | 错误信息（如有） |
| `title` | string | 否 | 会话标题（仅当会话是新建且标题为"新对话"时自动生成） |

**示例**：
```json
{
  "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
  "message_id": "123e4567-e89b-12d3-a456-426614174000",
  "memory_ids": ["mem-1", "mem-2"],
  "note_ids": ["note-1"],
  "title": "记账对话"
}
```

### useChatSSE.js — 统一 SSE 处理

```js
// composables/useChatSSE.js
import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'

export function useChatSSE() {
  const chat = useChatStore()
  const convs = useConversationsStore()
  let abortController = null

  async function sendMessage(message, { conversationId, deepMode, agentId }) {
    if (chat.isStreaming) return  // 流式中不允许发送
    abortController = new AbortController()

    chat.isStreaming = true
    chat.streamingMessage = { role: 'assistant', content: '', reasoning: '', toolCalls: [] }
    chat.planNodes = {}
    chat.planEdges = []

    try {
      const response = await fetch('/api/chat', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${auth.token}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          message,
          conversation_id: conversationId || undefined,
          deep_mode: deepMode,
          agent_id: agentId || undefined,
          memory_threshold: 0.5,
          memory_limit: 8
        }),
        signal: abortController.signal
      })

      if (!response.ok) {
        if (response.status === 401) throw new AuthError('token 过期')
        throw new Error(`HTTP ${response.status}`)
      }

      await parseSSEStream(response)
    } catch (err) {
      if (err.name === 'AbortError') return
      if (err instanceof AuthError) {
        auth.logout()  // 自动跳转登录页
        return
      }
      // 网络错误：提示用户重试
      chat.streamingMessage.error = err.message
    } finally {
      chat.isStreaming = false
    }
  }

  async function parseSSEStream(response) {
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let currentEvent = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop()  // 保留不完整的行

      for (const line of lines) {
        if (line.startsWith('event: ')) {
          currentEvent = line.slice(7).trim()
        } else if (line.startsWith('data: ')) {
          const raw = line.slice(6)
          dispatchEvent(currentEvent, raw)
        }
        // 忽略注释行（:开头）和空行
      }
    }
  }

  function dispatchEvent(event, rawData) {
    switch (event) {
      case 'message': {
        // data 是 JSON: {"delta":"..."}
        const parsed = JSON.parse(rawData)
        chat.streamingMessage.content += parsed.delta
        break
      }
      case 'thought_delta':
        // data 是纯字符串
        chat.streamingMessage.reasoning += rawData
        break
      case 'thought_done':
        chat.streamingMessage.reasoningDone = true
        break
      case 'tool_call': {
        const tc = JSON.parse(rawData)
        chat.streamingMessage.toolCalls.push({ ...tc, status: 'running', result: null })
        break
      }
      case 'tool_result': {
        const tr = JSON.parse(rawData)
        // 匹配最后一个 running 的 tool call
        const last = chat.streamingMessage.toolCalls.findLast(tc => tc.status === 'running')
        if (last) {
          last.status = tr.success ? 'done' : 'failed'
          last.result = tr
        }
        break
      }
      case 'plan_node_start': {
        const node = JSON.parse(rawData)
        chat.planNodes[node.id] = { label: node.label, status: 'running' }
        break
      }
      case 'plan_node_done': {
        const node = JSON.parse(rawData)
        chat.planNodes[node.id] = { label: node.label, status: node.status }
        break
      }
      case 'plan': {
        const graph = JSON.parse(rawData)
        chat.planNodes = {}
        for (const n of graph.nodes || []) {
          chat.planNodes[n.id] = { label: n.label, status: n.status }
        }
        chat.planEdges = graph.edges || []
        break
      }
      case 'status': {
        const s = JSON.parse(rawData)
        chat.statusMessage = s.message
        break
      }
      case 'done': {
        const d = JSON.parse(rawData)
        // 固化流式消息 → 存入 messages 列表
        const finalMsg = {
          ...chat.streamingMessage,
          messageId: d.message_id
        }
        chat.messages.push(finalMsg)
        chat.streamingMessage = null

        // 新会话：设置 conversation_id + 插入列表头部
        if (!chat.conversationId && d.conversation_id) {
          chat.conversationId = d.conversation_id
          convs.prepend({ id: d.conversation_id, title: d.title || '新对话' })
        }
        if (d.title) {
          convs.updateTitle(d.conversation_id, d.title)
        }
        break
      }
      case 'error':
        // rawData 是纯字符串
        chat.error = rawData
        break
    }
  }

  function cancel() {
    abortController?.abort()
  }

  return { sendMessage, cancel }
}
```

> 注意：`status` 事件的 data 格式是 JSON `{"message":"...","count":N}`，不是纯字符串。`error` 事件的 data 是纯字符串。

### Plan 可视化

> 详细方案见 [plan-dag.md](plan-dag.md)

当 Agent 一次调用多个工具（≥2）时，后端推送 `plan_node_start/done` + 最终 `plan` 事件。前端维护 `chat.planNodes`，在 ChatView 底部渲染：

```
┌─────────────────────────┐
│ 本轮执行计划              │
│                         │
│  query_bills  ✓ done    │
│       │                 │
│  create_bill  ✓ done    │
│       │                 │
│  update_bill  ⏳ running │
└─────────────────────────┘
```

- 节点颜色：pending 灰 / running 蓝（带 pulse 动画）/ done 绿 / failed 红
- 可由 `PlanGraph.vue` 组件渲染，支持手动收起/展开
- 单工具调用不展示 DAG

## HTTP 客户端封装

```js
// api/client.js
import { useAuthStore } from '@/stores/auth'

const BASE = import.meta.env.VITE_API_BASE || ''

async function request(path, options = {}) {
  const auth = useAuthStore()
  const headers = { ...options.headers }

  if (auth.token) {
    headers['Authorization'] = `Bearer ${auth.token}`
  }

  const res = await fetch(BASE + path, { ...options, headers })

  if (res.status === 401) {
    auth.logout()  // token 过期 → 清空 → 跳转 /login
    throw new Error('未登录或 token 已过期')
  }

  return res
}

export const api = {
  get: (path) => request(path).then(r => r.json()),
  post: (path, body) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }).then(r => r.json()),
  postRaw: (path, body, contentType = 'text/plain') => request(path, {
    method: 'POST',
    headers: { 'Content-Type': contentType },
    body
  }).then(r => r.json()),
  patch: (path, body) => request(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }).then(r => r.json()),
  del: (path) => request(path, { method: 'DELETE' }).then(r => r.json()),
  // SSE 流式请求不使用 .json()，直接返回 response
  stream: (path, body) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  })
}
```

- 所有非流式请求统一经过 `request()`，自动注入 Authorization header
- 401 响应统一拦截，触发 `auth.logout()` → 清空 localStorage → 跳转 `/login`
- 多标签页同步：`auth.js` 监听 `window.addEventListener('storage', ...)`，另一个标签页登出时当前页同步清空

## 与后端 API 映射

### 认证
| 前端功能 | 后端 API |
|---------|----------|
| 登录 | `POST /api/auth/login` |
| 注册 | `POST /api/auth/register` |
| 登出 | `POST /api/auth/logout` |

### 对话
| 前端功能 | 后端 API |
|---------|----------|
| 聊天（SSE） | `POST /api/chat` |
| 对话列表 | `GET /api/conversations` |
| 消息历史（offset） | `GET /api/conversations/:id/messages` |
| 消息历史（cursor，推荐） | `GET /api/conversations/:id/messages-v2` |
| 删除单条消息 | `DELETE /api/conversations/:id/messages/:msgId` |
| 重命名会话 | `PATCH /api/conversations/:id` |
| 删除会话 | `DELETE /api/conversations/:id` |

### 账单
| 前端功能 | 后端 API |
|---------|----------|
| 账单列表 | `GET /api/bills` |
| 导出 CSV | `GET /api/bills/export` |
| 导入 CSV | `POST /api/bills/import` |

### 笔记
| 前端功能 | 后端 API |
|---------|----------|
| 笔记列表 | `GET /api/notes` |
| 批量获取 | `POST /api/notes/batch` |
| 删除笔记 | `DELETE /api/notes/:id` |

### 记忆
| 前端功能 | 后端 API |
|---------|----------|
| 记忆列表 | `GET /api/memories` |
| 批量获取 | `POST /api/memories/batch` |
| 删除记忆 | `DELETE /api/memories/:id` |
| 批量删除 | `POST /api/memories/batch-delete` |

### 智能体
| 前端功能 | 后端 API |
|---------|----------|
| 智能体列表 | `GET /api/agents` |
| 智能体详情 | `GET /api/agents/:id` |
| 创建智能体 | `POST /api/agents` |
| 编辑智能体 | `PATCH /api/agents/:id` |
| 删除智能体 | `DELETE /api/agents/:id` |

### 动态工具
| 前端功能 | 后端 API |
|---------|----------|
| 工具列表 | `GET /api/tools` |
| 创建工具 | `POST /api/tools` |
| 编辑工具 | `PATCH /api/tools/:id` |
| 删除工具 | `DELETE /api/tools/:id` |

### System Prompt
| 前端功能 | 后端 API |
|---------|----------|
| Prompt 列表 | `GET /api/prompts` |
| 创建 Prompt | `POST /api/prompts` |
| 回滚版本 | `POST /api/prompts/:id/rollback` |
| 删除 Prompt | `DELETE /api/prompts/:id` |

## 错误处理体系

| 场景 | 处理方式 |
|------|---------|
| 401 未认证 | `api/client.js` 统一拦截 → `auth.logout()` → 跳转 `/login` |
| 网络断开 | SSE `fetch` 抛 `TypeError` → `catch` 中设置 `chat.error = '网络连接失败，请重试'` → 发送按钮恢复可点击 |
| SSE 超时 | nginx `proxy_read_timeout 300s`，超时后浏览器端 `reader.read()` 返回 `done=true` → 视为正常结束 |
| 服务端 500 | SSE `done` 事件带 `error` 字段 → 解析后 toast |
| 多标签页同步 | `auth.js` 监听 `storage` 事件，其他标签登出 → 当前页同步清空 |
| 发送中重复提交 | `chat.isStreaming` 为 `true` 时禁用发送按钮 |

## 关键技术点

### 深色模式

```vue
<n-config-provider :theme="theme">
  <n-message-provider>
    <router-view />
  </n-message-provider>
</n-config-provider>
```

`pinia` 存 `theme` 状态，切换时 `document.documentElement.classList.toggle('dark')`，持久化到 localStorage。

### 环境切换

```js
// .env.development
VITE_API_BASE=http://localhost:8080

// .env.production
VITE_API_BASE=

// api/client.js
const BASE = import.meta.env.VITE_API_BASE || ''
```

## 开发流程

```bash
cd web
npm install
npm run dev        # 本地开发，代理到 localhost:8080
npm run build      # 生产构建 → dist/
```

vite.config.js：

```js
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080'
    }
  }
})
```

## 部署

```bash
npm run build
scp -r dist/ user@server:/srv/xhan-helper/web/
```

`make deploy` 增加一行 `scp -r web/dist/`。
