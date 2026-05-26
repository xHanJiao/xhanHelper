# 消息调试面板

## 背景

随着使用时间增长，向量检索资源（记忆、笔记、历史消息）和主题数量持续积累。上下文工程中如何利用这些资源、效果如何，需要监测和评估手段。目前后端已有 `PromptTrace`（写入 `message.extra`）和 `message.request_json`，但前端没有相应的调试面板来查看这些数据。

## 目标

建设一个消息级调试面板，让用户能逐轮查看：

1. 用户消息对应的系统提示词（markdown 渲染）
2. 发送给 LLM 的完整 request_json（语法高亮）
3. 系统提示词中使用了哪些资源（主题/记忆/笔记/历史消息），以及查询出的、选中的、丢弃的条目及其分数

从而为上下文工程的参数调优（阈值、条数、权重）提供数据依据。

## 交互设计

| 决策点 | 决议 |
|--------|------|
| 入口 | 大屏模式下，topbar 深浅色切换旁边加调试开关 |
| 开关状态 | localStorage 持久化 |
| 开关效果 | 用户消息旁边显示「调试」图标 |
| 点击后 | 跳转到独立页面 `/admin/message-debug/:msgId` |
| 详情展示 | 选中 assistant 消息后，tabs 切换：系统提示词 / request_json / 资源取舍明细 |
| 资源明细 | 表格形式（后续可升级为漏斗图） |

## API 设计

采用懒加载策略，避免一次性返回数十 KB 的 `request_json`。

### `GET /api/admin/messages/:id/debug`

返回锚点用户消息 + 该轮所有消息（从该用户消息到下一个用户消息之间），不含重量级字段。

```json
{
  "anchor_msg": { "id": "...", "role": "user", "content": "...", "created_at": "..." },
  "round_messages": [
    { "id": "...", "role": "assistant", "content": "...", "tool_calls_json": "...", "created_at": "..." },
    { "id": "...", "role": "tool", "content": "...", "tool_call_id": "...", "created_at": "..." }
  ]
}
```

### `GET /api/admin/messages/:id/debug-detail?field=request_json|extra`

返回单条消息的重量级字段。

```json
{
  "id": "...",
  "field": "request_json",
  "value": "{\"model\":\"deepseek-chat\",\"messages\":[...]}"
}
```

## 检索架构重构

### 拆分 retrievalProvider

原来记忆、笔记、历史消息打包为一个 `retrievalProvider`，通过 `Retriever` 聚合，trace 中合并为一条 "retrieval" 记录。

现将 `Retriever` 移除，改为三个独立 provider，各自直接调用 `ContextSource.Fetch()`：

| Provider | Source | trace name | 默认 score | 默认阈值 | 默认条数 |
|----------|--------|------------|-----------|---------|---------|
| `memoryProvider` | `MemorySource` | memory | 0.75 | 0.5 | 8 |
| `noteProvider` | `NoteSource` | note | 0.65 | 0.5 | 5 |
| `historyProvider` | `HistorySource` | history | 0.55 | 0.8 | 5 |

三个 provider 返回的 `ScoredItem` 在 `Build()` 中统一按 score 降序排列，主题（score ≥ 0.85）天然排在前面。

### 历史消息注入

原先 `HistorySource` 仅在 `message_search` 工具中按需使用，拆分后自动注入每轮 system prompt。通过高阈值（0.8）控制仅注入高度相关的历史对话。

### near-miss 追踪

`ContextSource.Fetch()` 返回值扩展为 `(selected []ContextPacket, nearMiss []NearMissItem, error)`，provider 可将未达标的候选记录到 trace 中，支撑面板的「查出了哪些、去掉了哪些」需求。

### 主题 trace bug 修复

`themeProvider.buildThemeTrace()` 已正确构建全量 trace（含 `vector_below_threshold`、`other_active`），但匹配成功时未将 trace_items 附加到 `ScoredItem.Meta`。修复：在第一个 ScoredItem 的 Meta 中带入全量 trace。

## 可配置化

### config.yaml 扩展

`retrieval` 配置段中，Memory / Note / History 的 `SourceWeights` 增加 `threshold` 和 `limit` 字段：

```yaml
retrieval:
  freshness_decay: 0.02
  memory:
    vector_weight: 0.5
    keyword_weight: 0.3
    freshness_weight: 0.2
    freshness_decay: 0.0
    threshold: 0.5
    limit: 8
  note:
    vector_weight: 0.5
    keyword_weight: 0.3
    freshness_weight: 0.0
    freshness_decay: 0
    threshold: 0.5
    limit: 5
  history:
    vector_weight: 0.6
    keyword_weight: 0.3
    freshness_weight: 0.1
    freshness_decay: 0.005
    threshold: 0.8
    limit: 5
```

### Config 结构体变更

`SourceWeights` 增加 `Threshold float64` 和 `Limit int` 字段。`PromptConfig` 的默认值从 `DefaultPromptConfig()` 生成，运行时从 `config.yaml` 读取。

## 实施顺序

1. ✅ 拆分 retrievalProvider 为三个独立 provider（已完成）
2. ✅ HistorySource 注入 system prompt（已完成）
3. ✅ 删除 retriever.go（已完成）
4. 可配置化：SourceWeights 加 Threshold/Limit，PromptConfig 从 yaml 读取
5. Fetch() 签名改版 + near-miss 追踪
6. 主题 trace bug 修复
7. Debug API 端点
8. 前端面板页面
