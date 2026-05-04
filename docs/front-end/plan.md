# Web 前端方案

> 日期: 2026-05-04 | 状态: 待开发

## 概述

基于 Vue 3 + Naive UI + Vite 的单页应用（SPA），提供与后端 xhanHelper 一致的聊天、记忆管理、智能体管理功能。通过 nginx 部署静态文件，反向代理 API 到 Go 后端。

---

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

---

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

---

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
    │   ├── ChatView.vue   # 主聊天页（含对话列表 + 聊天区）
    │   ├── MemoryView.vue # 记忆列表
    │   └── AgentView.vue  # 智能体管理
    └── components/
        ├── ConversationList.vue  # 左侧对话列表
        └── MessageBubble.vue     # 消息气泡
```

---

## 路由

| 路径 | 页面 | 说明 |
|------|------|------|
| `/login` | LoginView | 登录/注册 |
| `/chat` | ChatView | 主聊天，默认页 |
| `/chat/:id` | ChatView | 进入指定会话 |
| `/memories` | MemoryView | 记忆列表 |
| `/agents` | AgentView | 智能体管理 |

---

## 页面设计

### LoginView

- `n-card` 居中，内含 `n-form`
- 用户名 + 密码 + 登录/注册切换
- 登录成功 → 存 token 到 pinia + localstorage → 跳转 `/chat`

### ChatView（核心）

布局：

```
┌──────┬──────────────────┬──────────────────────────┐
│ 导航  │   对话列表         │      聊天区               │
│      │                  │                          │
│ 💬   │  + 新建对话        │  消息气泡（v-for）         │
│ 🧠   │  ┌──────────┐    │    - 用户: n-card 蓝色   │
│ 🤖   │  │会话1   ✏️🗑️│   │    - AI:   n-card 灰色   │
│      │  │会话2       │   │    - Markdown 渲染       │
│      │  │...         │   │                          │
│      │  └──────────┘    │  ────────────────────    │
│      │                  │  n-input textarea [发送]   │
└──────┴──────────────────┴──────────────────────────┘
```

- **导航栏**：`n-layout-sider` 窄模式，icon 按钮切换页面
- **对话列表**：`n-layout-sider` 中等宽度
  - 列表基于 `n-menu` 或自定义 `v-for`
  - 每个会话：标题 + 右侧重命名/删除 icon
  - 支持滚动加载（传 offset 到 API）
- **聊天区**：`n-layout-content`
  - 消息列表自动滚到底部
  - SSE 流式：`fetch` + `ReadableStream`，逐 delta 追加到 AI 消息
  - 输入 `Enter` 发送，`Shift+Enter` 换行
  - 顶部 `n-switch` 切换 deep_mode
  - 顶部 `n-select` 选择智能体（`GET /api/agents`）

### MemoryView

- `n-data-table` 展示记忆列表
- 列：内容摘要、来源、时间
- 分页（`n-pagination`）
- 右上角搜索框（keyword 模糊搜索）
- 每行右侧删除按钮

### AgentView

- `n-card` 列表展示智能体
- `n-modal` / `n-drawer` 创建/编辑
  - `n-form`：名称、system prompt（textarea）、模型
- 每张 card 右侧编辑/删除按钮

---

## 关键技术点

### SSE 流式聊天

```js
const response = await fetch('/api/chat', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ message, conversation_id, deep_mode, agent_id })
})

const reader = response.body.getReader()
const decoder = new TextDecoder()
let buffer = ''

while (true) {
  const { done, value } = await reader.read()
  if (done) break
  buffer += decoder.decode(value, { stream: true })
  // 按 \n 分割解析 SSE 行
  const lines = buffer.split('\n')
  buffer = lines.pop()
  for (const line of lines) {
    if (line.startsWith('event: ')) currentEvent = line.slice(7)
    else if (line.startsWith('data: ')) handleEvent(currentEvent, line.slice(6))
  }
}
```

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

---

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

---

## 部署

```bash
npm run build
scp -r dist/ user@server:/srv/xhan-helper/web/
```

`make deploy` 增加一行 `scp -r web/dist/`。

---

## 与后端 API 映射

| 前端功能 | 后端 API |
|---------|----------|
| 登录 | `POST /api/auth/login` |
| 注册 | `POST /api/auth/register` |
| 聊天（SSE） | `POST /api/chat` |
| 对话列表 | `GET /api/conversations` |
| 消息历史 | `GET /api/conversations/:id/messages` |
| 重命名会话 | `PATCH /api/conversations/:id` |
| 删除会话 | `DELETE /api/conversations/:id` |
| 记忆列表 | `GET /api/memories` |
| 删除记忆 | `DELETE /api/memories/:id` |
| 智能体 CRUD | `POST/GET/PATCH/DELETE /api/agents` |
