# 对话重放对比工具

## 背景

目前对策略参数（检索上下文注入、工具结果截断长度、检索阈值/条数等）的调整完全依赖主观感受，无法客观判断「改好了还是改坏了」。在建设大规模评测体系之前，先做一个最小版本的重放对比工具。

核心场景：同一个用户问题序列，用配置 A 和配置 B 各跑一遍，并排对比结果。

## 目标

1. 以调试面板中选中的一条用户消息为入口，携带该消息之前的 N 条历史消息作为上下文，用两组配置分别重放，对比结果
2. 支持切换两组配置（system prompt 变体、截断参数、检索参数等），分别运行对比
3. 并排展示两组的 tool_calls 和 LLM 回复，肉眼判断优劣
4. 使用独立测试环境（SQLite），不污染生产数据

## 非目标

- 不涉及自动化评分或标注数据
- 不做 CI 集成或回归测试
- 不修改生产数据库

## 交互设计

| 决策点 | 决议 |
|--------|------|
| 入口 | 调试面板内，选中一条用户消息后，旁边增加「对比重放」按钮 |
| 消息范围 | 以选中的用户消息为测试输入，携带该消息之前的 N 条历史消息作为上下文（N 可配置，默认 10） |
| 配置面板 | 两组配置并排，可切换的参数：检索开/关、截断长度、检索阈值/条数、模型（flash/pro） |
| 高级编辑 | 配置 A/B 各自有一个「编辑请求」按钮，弹出一个可编辑的 JSON 编辑器，展示最终发给 LLM 的完整 messages 数组（含 system prompt），用户可直接修改后保存，重放时以修改后的内容为准 |
| 执行 | 点击「运行对比」，两组各自创建新的 conversation，用同一份消息序列跑 ReAct 循环 |
| 结果展示 | 左右两栏并排，展示 assistant tool_calls → tool_result → assistant reply |
| diff 标记 | 两栏 tool_calls 不一致时高亮标记 |

## API 设计

### `GET /api/admin/messages/:id/replay-context?history_count=10`

返回锚点用户消息 + 前置历史消息，作为对比重放的输入预览。

```json
{
  "anchor_message": { "id": "...", "content": "...", "created_at": "..." },
  "history": [
    { "role": "user", "content": "..." },
    { "role": "assistant", "content": "..." },
    { "role": "user", "content": "..." }
  ],
  "total_history_messages": 10
}
```

### `POST /api/admin/replay/preview`

预览重放时发给 LLM 的完整请求（messages 数组 + system prompt），供用户检查或手动编辑后再提交对比。

```json
// Request
{
  "anchor_message_id": "xxx",
  "history_count": 10,
  "skip_retrieval": false,
  "max_tool_result_chars": 2000
}

// Response
{
  "anchor_message": { "id": "...", "content": "..." },
  "system_prompt": "## 核心原则\n...",
  "messages": [
    { "role": "system", "content": "..." },
    { "role": "user", "content": "你好" },
    { "role": "assistant", "content": "..." },
    { "role": "user", "content": "帮我查一下昨天的开销" }
  ],
  "history_count": 10
}
```

### `POST /api/admin/replay/compare`

选中一条用户消息，携带其之前的 N 条历史作为上下文，用两组配置分别重放。

```json
{
  "anchor_message_id": "xxx",
  "history_count": 10,
  "config_a": {
    "skip_retrieval": false,
    "max_tool_result_chars": 2000,
    "memory_limit": 8,
    "memory_threshold": 0.5,
    "deep_mode": false,
    "override_messages": null
  },
  "config_b": {
    "skip_retrieval": true,
    "max_tool_result_chars": 500,
    "memory_limit": 5,
    "memory_threshold": 0.7,
    "deep_mode": false,
    "override_messages": null
  }
}
```

`override_messages` 为 null 时使用自动构建的消息；非 null 时直接替换整个 messages 数组（含 system 消息），跳过自动检索和 prompt 构建。

响应为 SSE 流式，事件类型：

| event | 说明 |
|-------|------|
| `start` | 重放开始，携带锚点消息和前置历史预览 |
| `replay_a` | 配置 A 的流式事件，内含原始 SSE 事件（text_delta / tool_call / tool_result 等） |
| `replay_b` | 配置 B 的流式事件，格式同 replay_a |
| `done` | 两组都完成，携带完整对比结果（两组各自的 tool_calls 列表 + 最终回复） |
| `error` | 异常 |

`replay_a` / `replay_b` 事件体：

```json
{
  "side": "a",
  "event": {
    "type": "tool_call",
    "content": "{\"tool\":\"bookkeeping\",\"function\":\"query\",...}"
  }
}
```


## 核心逻辑

### ReplayOrchestrator

复用现有 `Orchestrator`，但：

1. 每个重放对话创建**新的 conversation**（`replay-` 前缀），不写入原对话
2. **独立 SQLite 数据库**：replay 工具启动时打开一个独立的 SQLite 文件（如 `data/replay.db`），与主库（MySQL / 生产 SQLite）完全隔离。重放的 conversation、message、tool_result 都写入此独立 DB
3. 两组配置创建两个独立的 `Orchestrator` 实例（不同 `RunRequest` 参数），共用同一个 replay SQLite
4. 两组**串行执行**（先 A 后 B），避免触发 DeepSeek API 并发 QPS 限制
5. replay DB 可随时通过管理接口清理（删除特定会话或全部清空）

### 为什么不用 `APP_ENV` 切换

`APP_ENV` 切换会改变整个应用的数据库连接，无法在生产环境局部使用。replay 工具通过 `gorm.Open(sqlite.Open("data/replay.db"))` 直接打开第二条数据库连接，与主库互不干扰。

### 伪代码

```go
func (h *ReplayHandler) Compare(c *gin.Context) {
    var req ReplayCompareRequest
    c.BindJSON(&req)

    // 1. 获取锚点消息 + 前置历史消息
    anchorMsg := h.msgRepo.GetByID(ctx, req.AnchorMessageID)
    history := h.msgRepo.GetHistoryBefore(ctx, anchorMsg.ConversationID, anchorMsg.CreatedAt, req.HistoryCount)

    // 2. 组装消息序列：历史 + 锚点用户消息
    msgs := buildMessages(history, anchorMsg)

    // 3. 创建两个新对话
    convA := h.convRepo.Create(ctx, "replay-A-"+req.AnchorMessageID)
    convB := h.convRepo.Create(ctx, "replay-B-"+req.AnchorMessageID)

    // 4. 串行执行（避免触发 LLM API 并发 QPS 限制）
    resultA := runReplay(h.orch, req.ConfigA, convA, msgs)
    resultB := runReplay(h.orch, req.ConfigB, convB, msgs)

    // 5. 推送对比结果
    c.SSEvent("done", compareResults(resultA, resultB))
}
```

## 可对比的配置项

| 配置项 | 说明 |
|--------|------|
| `skip_retrieval` | 是否注入检索上下文到 system prompt |
| `max_tool_result_chars` | 工具结果截断长度 |
| `memory_limit` | 记忆检索最大条数 |
| `memory_threshold` | 记忆检索相关度阈值 |
| `note_limit` | 笔记检索最大条数 |
| `note_threshold` | 笔记检索相关度阈值 |
| `history_limit` | 历史消息检索最大条数 |
| `history_threshold` | 历史消息检索相关度阈值 |
| `deep_mode` | 是否使用 Pro 模型 |
| `temperature` | LLM 温度参数 |
| `max_iterations` | ReAct 最大轮次 |

## 实施顺序

1. API：`GET /api/admin/conversations/:id/user-messages`
2. `ReplayOrchestrator`：复用 Orchestrator，支持独立 conversation + 双实例并发
3. API：`POST /api/admin/replay/compare`（SSE）
4. 前端：对话选择 → 配置面板 → 并排结果展示
