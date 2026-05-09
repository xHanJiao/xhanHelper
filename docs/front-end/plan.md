# Web 前端方案

> 日期: 2026-05-04 | 更新: 2026-05-09 | 状态: 待开发

## 概述

基于 Vue 3 + Naive UI + Vite 的单页应用（SPA），提供与后端 xhanHelper 一致的聊天、记忆管理、智能体管理功能。通过 nginx 部署静态文件，反向代理 API 到 Go 后端。

## 技术栈

| 层 | 技术 |
|---|------|
| 框架 | Vue 3 (Composition API) |
| UI 组件库 | Naive UI |
| 构建 | Vite |
| 路由 | vue-router |
| 状态管理 | pinia |
| HTTP | fetch (封装) |
| 流式 | SSE (ReadableStream) |
| Markdown | markdown-it |
| 样式 | Naive UI 内置 + Tailwind CSS 辅助 |

## 部署架构

```
浏览器 → :80 (nginx)
            /        → /srv/xhan-helper/web/dist/  (静态文件, SPA fallback)
            /api/*   → localhost:8088                     (Go 后端, proxy_pass)
```

- nginx 负责前端静态文件，不替换
- Go 后端完全不需要改动
- `vite build` 产物 scp 到服务器即可

nginx 配置示例：

```nginx
server {
    listen 80;
    server_name _;

    root /srv/xhan-helper/web/dist;
    index index.html;

    location /api/ {
        proxy_pass http://127.0.0.1:8088;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;  # SSE 长连接
        proxy_buffering off;
    }

    location / {
        try_files $uri $uri/ /index.html;  # SPA fallback
    }
}
```

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
    │   └── client.js      # fetch 封装，统一 Authorization + error handling
    ├── stores/
    │   └── auth.js        # pinia: token, user, login/logout
    ├── router/
    │   └── index.js       # 路由定义
    ├── views/
    │   ├── LoginView.vue  # 登录/注册
    │   ├── ChatView.vue   # 主聊天页（含对话列表 + 聊天区 + plan 可视化）
    │   ├── MemoryView.vue # 记忆列表 + 批量操作
    │   ├── AgentView.vue  # 智能体管理
    │   ├── PromptView.vue # 自定义 System Prompt 管理
    │   └── ToolView.vue   # 动态工具管理
    └── components/
        ├── ConversationList.vue  # 左侧对话列表
        ├── MessageBubble.vue     # 消息气泡（含思考过程 + 工具调用展示）
        ├── PlanGraph.vue         # Plan DAG 可视化（本轮工具调用链路）
        └── ThinkingBlock.vue     # 思考模式思维链展示（可折叠）
```

## 路由

| 路径 | 页面 | 说明 |
|------|------|------|
| `/login` | LoginView | 登录/注册 |
| `/chat` | ChatView | 主聊天，默认页 |
| `/chat/:id` | ChatView | 进入指定会话 |
| `/memories` | MemoryView | 记忆列表 + 批量删除 |
| `/agents` | AgentView | 智能体管理 |
| `/prompts` | PromptView | 自定义 System Prompt（版本化） |
| `/tools` | ToolView | 动态工具注册管理 |

## 页面设计

### LoginView

- `n-card` 居中，内含 `n-form`
- 用户名 + 密码 + 登录/注册切换
- 登录成功 → 存 token 到 pinia + localstorage → 跳转 `/chat`

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
│ 🔧   │  │...         │   │  │ AI 回复                             ││
│      │  │            │   │  │  ├─ 思考过程 (可折叠, thought_delta) ││
│      │  │            │   │  │  ├─ 回复正文 (Markdown)             ││
│      │  │            │   │  │  ├─ 工具调用卡片 (tool_call/result)  ││
│      │  │            │   │  │  └─ 状态提示 (status 事件)          ││
│      │  └────────────┘   │  └──────────────────────────────────────┘│
│      │                  │  ┌──────────────────────────────────────┐│
│ 📝   │                  │  │ Plan DAG 可视化 (plan 事件, 可收起)  ││
│      │                  │  └──────────────────────────────────────┘│
│      │                  │  ──────────────────────────────────────── │
│      │                  │  n-input textarea              [发送]     │
└──────┴──────────────────┴──────────────────────────────────────────┘
```

- **导航栏**：`n-layout-sider` 窄模式，icon 按钮切换页面（聊天/记忆/智能体/提示词/工具）
- **对话列表**：`n-layout-sider` 中等宽度
  - 列表基于 `n-menu` 或自定义 `v-for`
  - 每个会话：标题 + 右侧重命名/删除 icon
  - 支持滚动加载（传 cursor 到 `/api/conversations`）
- **聊天区**：`n-layout-content`
  - 顶部工具栏：
    - `n-switch` 切换深度思考模式（`deep_mode`）
    - `n-select` 选择智能体（`GET /api/agents`），选中后发消息带 `agent_id`
  - 消息列表自动滚到底部
  - 每条 AI 消息气泡包含：
    - **思考过程**：`n-collapse` 包裹，收到 `thought_delta` 时逐字追加，收到 `thought_done` 时折叠
    - **回复正文**：Markdown 渲染
    - **工具调用卡片**：`n-card` 嵌套，显示工具名 + 函数名，`tool_call` 时展示为 loading，`tool_result` 时更新为成功/失败状态
  - **Plan 可视化**：收到 `plan_node_start/done` 时构建节点状态，收到 `plan` 事件时渲染完整 DAG 图（节点=工具调用，边=执行顺序）
  - **状态提示**：收到 `status` 事件时以轻量 toast 或 inline badge 显示（如"已关联 3 条记忆"）
  - 输入 `Enter` 发送，`Shift+Enter` 换行
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

### AgentView

- `n-card` 列表展示智能体
- `n-modal` / `n-drawer` 创建/编辑
  - `n-form`：名称、system prompt（textarea）、模型
- 每张 card 右侧编辑/删除按钮

### PromptView（新增）

- 列表展示用户自定义的 System Prompt（`GET /api/prompts`）
- 创建：`n-modal` 内含 `n-form`（名称、prompt 文本、类型 flash/pro）
- 每条记录显示当前版本号 + 创建时间
- 操作：编辑（创建新版本）、回滚到指定版本（`POST /api/prompts/:id/rollback`）、删除
- 说明：System Prompt 分为 flash（日常对话）和 pro（深度思考）两种类型，在对应模型下自动启用

### ToolView（新增）

- 列表展示动态工具（`GET /api/tools`）
- 每条记录显示名称、描述、schema 预览
- 创建/编辑：`n-modal` 内含 `n-code` 编辑器编辑 schema_json
- 删除（`DELETE /api/tools/:id`）
- 说明：内置工具（bookkeeping/datetime/uuid/calculator/note/agent）不可编辑删除，仅动态工具可管理

## SSE 事件处理

### 事件类型一览

| event | data 格式 | 触发时机 | 前端处理 |
|-------|----------|----------|----------|
| `message` | `{"delta":"好"}` | LLM 回复文本，逐字推送 | 追加到当前 AI 消息 content |
| `thought_delta` | `"思考内容..."` | 思考模式思维链，逐字推送 | 追加到思考块（可折叠区域） |
| `thought_done` | `""` | 思考完成 | 折叠思考块，标记完成 |
| `tool_call` | `{"tool":"bookkeeping","function":"create_bill","args":{...}}` | 检测到工具调用 | 显示工具调用卡片（loading 状态） |
| `tool_result` | `{"success":true,"data":{...}}` | 工具执行完成 | 更新卡片状态（成功/失败），显示结果摘要 |
| `plan_node_start` | `{"id":"tool.func","label":"create_bill","status":"running"}` | 工具开始执行 | 在 Plan DAG 中标记节点为 running |
| `plan_node_done` | `{"id":"tool.func","label":"create_bill","status":"done"}` | 工具执行完成 | 标记节点为 done/failed |
| `plan` | `{"nodes":[...],"edges":[...]}` | 本轮所有工具调用汇总 | 渲染完整 DAG 图 |
| `status` | `{"message":"已关联向量记忆","count":3}` | 后台状态通知 | inline badge 或轻量 toast |
| `done` | `{"conversation_id":"...","message_id":"...","title":"...","memory_ids":[...],"note_ids":[...]}` | 本轮对话结束 | 更新会话 ID、标题，清理流式状态 |
| `error` | `"错误信息"` | 异常 | `n-message` error toast |

### SSE 流式聊天实现

```js
const response = await fetch('/api/chat', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({
    message,
    conversation_id: convId,   // 可选，续聊时传入
    deep_mode: deepMode,       // 是否深度思考
    agent_id: agentId,         // 可选，指定智能体
    memory_threshold: 0.5,     // 记忆检索阈值
    memory_limit: 8            // 记忆检索最大条数
  })
})

const reader = response.body.getReader()
const decoder = new TextDecoder()
let buffer = ''
let currentEvent = ''

while (true) {
  const { done, value } = await reader.read()
  if (done) break
  buffer += decoder.decode(value, { stream: true })
  const lines = buffer.split('\n')
  buffer = lines.pop()
  for (const line of lines) {
    if (line.startsWith('event: ')) {
      currentEvent = line.slice(7).trim()
    } else if (line.startsWith('data: ')) {
      handleSSEEvent(currentEvent, line.slice(6))
    }
  }
}
```

### SSE 事件处理逻辑

```js
function handleSSEEvent(event, data) {
  switch (event) {
    case 'message':
      // 追加文本到当前 AI 气泡 content（Markdown 实时渲染）
      appendToCurrentMessage(data.delta)
      break
    case 'thought_delta':
      // 追加到思考块，首次出现时创建可折叠区域
      showThinkingBlock(true)
      appendToThinking(data)
      break
    case 'thought_done':
      // 折叠思考块
      collapseThinkingBlock()
      break
    case 'tool_call': {
      const tc = JSON.parse(data)
      // 在 AI 气泡内显示工具调用卡片（spinner + 工具名.函数名）
      addToolCallCard(tc.tool, tc.function, tc.args, 'running')
      break
    }
    case 'tool_result': {
      const tr = JSON.parse(data)
      // 更新最新 running 状态的卡片为 done/failed
      updateLastToolCard(tr)
      break
    }
    case 'plan_node_start': {
      const node = JSON.parse(data)
      upsertPlanNode(node.id, node.label, 'running')
      break
    }
    case 'plan_node_done': {
      const node = JSON.parse(data)
      upsertPlanNode(node.id, node.label, node.status)
      break
    }
    case 'plan': {
      const graph = JSON.parse(data)
      // 渲染完整 DAG: nodes=[{id,label,status}], edges=[{from,to}]
      renderPlanGraph(graph)
      break
    }
    case 'status': {
      const s = JSON.parse(data)
      showStatusBadge(s.message) // 如 "已关联向量记忆"
      break
    }
    case 'done': {
      const d = JSON.parse(data)
      // 设置 conversation_id、更新标题、清理流式状态
      finalizeMessage(d)
      break
    }
    case 'error':
      showErrorToast(data)
      break
  }
}
```

### Plan 可视化

当 Agent 一次调用多个工具时，后端通过 `plan_node_start` / `plan_node_done` 实时推送每步状态，最后通过 `plan` 事件发送完整 DAG。前端使用轻量 SVG/Canvas 或简单垂直列表渲染：

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
- 超过 2 个工具调用时才显示 DAG（单工具调用不展示）
- 可手动收起/展开

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
| 消息历史（cursor） | `GET /api/conversations/:id/messages-v2` |
| 删除单条消息 | `DELETE /api/conversations/:id/messages/:msgId` |
| 重命名会话 | `PATCH /api/conversations/:id` |
| 删除会话 | `DELETE /api/conversations/:id` |
| 手动提取记忆 | `POST /api/conversations/:id/extract-memories` |

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

## 关键技术点

### 深色模式

```vue
<n-config-provider :theme="theme">
  <n-message-provider>
    <router-view />
  </n-message-provider>
</n-config-provider>
```

`pinia` 存 `theme` 状态，切换时 `document.documentElement.classList.toggle('dark')`，刷新记忆。

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
