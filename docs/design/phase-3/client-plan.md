# Phase 3 — 客户端：记忆可见 + 智能体

> 日期: 2026-05-04 | 状态: 待开发

## 概述

在现有 iOS 客户端上增加记忆列表和智能体管理功能，打通记忆→智能体→聊天的完整链路。

---

## 布局改动

### 当前

```
ContentView
├── ConversationListView → ChatView
└── 底部: 对话 | 设置
```

### 目标

```
ContentView (TabView, 4 tabs)
├── 对话  → ConversationListView → ChatView
├── 记忆  → MemoryListView
├── 智能体 → AgentListView
└── 设置  → SettingsView
```

---

## 页面设计

### 1. 记忆列表 (MemoryListView)

| 组件 | 说明 |
|------|------|
| 数据源 | `GET /api/memories?limit=20&offset=0&keyword=&source=` |
| 列表 | 每个 cell: 内容摘要（2行截断）、来源标签、时间 |
| 搜索 | 顶部搜索栏，keyword 模糊搜索 |
| 删除 | 滑动删除 → `DELETE /api/memories/:id` |
| 空状态 | "还没有记忆，去聊天页面和 AI 聊聊吧" |
| 分页 | 滚动到底部自动加载更多 |

### 2. 智能体列表 (AgentListView)

| 组件 | 说明 |
|------|------|
| 数据源 | `GET /api/agents?limit=50` |
| 列表 | 每个 cell: 名称、system_prompt 摘要（1行截断） |
| 创建 | 右上角 `+` → 半屏 sheet: 名称(单行 TextField) + system_prompt(多行 TextEditor) + 字数提示 |
| 编辑 | 点击 cell → 同创建半屏 sheet，预填数据 |
| 删除 | 滑动删除 → `DELETE /api/agents/:id` |
| 开始对话 | 每行右侧聊天气泡 icon → 带 `agent_id` 跳转 ChatView，创建新会话 |
| 空状态 | "还没有智能体，点击右上角创建" |

### 3. 智能体与对话的关联

**AgentListView → ChatView**：
- 点击聊天气泡 → 设置 `pendingAgentID`
- ChatView 加载后检测 `pendingAgentID` → `sendMessage` 时传 `agent_id`
- 会话创建后清除 `pendingAgentID`

**对话列表透出智能体信息**：
- 后端 `GET /api/conversations` 返回时，若 `agent_id` 非空，附带 `agent_name`
- `Conversation` 模型加 `agentName` 字段
- `ConversationListView` 每个 cell 中，如果是智能体会话，标题下方显示灰色小字"🤖 智能体名称"

**聊天页透出智能体信息**：
- `ChatView` 导航标题下方，智能体会话显示副标题"🤖 智能体名称"
- `done` 事件返回的 JSON 中附带 `agent_name`

### 4. 回复中透出引用的记忆

**后端流程**：
- `Retriever.Retrieve()` 已返回 `(text, memIDs)`
- `buildSysPrompt` → `Run` → `runReActLoop` 传递 memIDs
- 保存 user message 时写入 `MemoryIDs` 字段（JSON 数组）
- `done` 事件附带 `memory_ids:["id1","id2"]`

**客户端交互**：
- AI 消息气泡下方，如果本次回复引用了记忆，显示小按钮"🧠 基于 X 条回忆"
- 点击弹出 sheet / popover，列出这几条记忆的内容摘要
- 列表项只读展示，不做编辑

---

## 改动清单

### 后端

| 操作 | 文件 | 说明 |
|------|------|------|
| 修改 | `model/message.go` | 加 `MemoryIDs *string` 字段 |
| 修改 | `agent/orchestrator.go` | buildSysPrompt/Run/runReActLoop 传递 memIDs，存到 user message |
| 修改 | `controller/chat.go` | `done` 事件 JSON 附带 `memory_ids` |
| 修改 | `controller/conversations.go` | `listConversations` 返回时 JOIN agents 表带 `agent_name` |
| 修改 | `migrations/001_init.sql` | messages 表加 `memory_ids` 列 |

### 客户端 (SwiftUI)

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `Views/MemoryListView.swift` | 记忆列表 |
| 新增 | `ViewModels/MemoryViewModel.swift` | 记忆数据加载 |
| 新增 | `Views/AgentListView.swift` | 智能体列表 + 创建/编辑 sheet |
| 新增 | `ViewModels/AgentViewModel.swift` | 智能体 CRUD |
| 新增 | `Views/MemoryPopupView.swift` | 消息下方弹出记忆摘要 |
| 修改 | `Views/ContentView.swift` | TabView 替换原有导航 |
| 修改 | `Models/Conversation.swift` | 加 `agentName` 字段 |
| 修改 | `Views/ConversationListView.swift` | cell 显示智能体标记 |
| 修改 | `Views/ChatView.swift` | 接收 pendingAgentID，标题副标题，记忆按钮 |
| 修改 | `Services/APIClient.swift` | 加 memory/agent API 方法 |

---

## API 调用汇总

| 前端操作 | HTTP |
|---------|------|
| 记忆列表 | `GET /api/memories` |
| 删除记忆 | `DELETE /api/memories/:id` |
| 智能体列表 | `GET /api/agents` |
| 创建智能体 | `POST /api/agents` |
| 更新智能体 | `PATCH /api/agents/:id` |
| 删除智能体 | `DELETE /api/agents/:id` |
| 带 agent 建会话 | `POST /api/chat {agent_id}` |
| 对话列表(带agent名) | `GET /api/conversations` |
