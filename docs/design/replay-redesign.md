# 对比重放工具重构

## 问题

1. **对比方式错误**：当前 compare 把 A/B 两侧各跑一遍完整 ReAct 循环（两次 LLM 调用链），A 侧用历史 request_json 重放，B 侧用新配置。正确做法：A 侧直接展示 DB 中已存储的原 assistant 响应，只跑 B 侧。
2. **检索细节不可见**：真正跑 compare 时，B 侧实际用了什么检索结果、各项打分、过滤情况，用户完全看不到。

## 设计决策

| # | 决策点 | 结论 |
|---|--------|------|
| 1 | A 侧数据来源 | 从 DB 捞消息链，不跑 LLM |
| 2 | A 侧展示内容 | 完整链路：每轮 assistant 的 reasoning + content + tool_calls + 工具返回结果（和 B 侧结构对齐） |
| 3 | A 侧边界 | 从锚点 user msg 开始，到下一条 user msg 之前的所有 assistant + tool 消息 |
| 4 | B 侧检索溯源 | B 侧结果旁加可折叠「检索溯源」区块，列出每条内容的来源、打分、过滤结果 |
| 5 | SSE → 同步 | 纯同步 JSON 接口，不再用 SSE 流 |
| 6 | B 侧 system prompt 来源 | 用户可选：基于历史 request_json 的 system prompt 或基于当前实时 build |
| 7 | Preview 步骤 | 保留，两步走：先 preview 看 system prompt → 再 compare |

## API 变更

### `POST /api/admin/replay/preview`（保留，调整）

Request 不变，但只针对 B 侧配置构建 system prompt（去掉 ConfigA 相关逻辑）。

### `POST /api/admin/replay/compare`（重构）

**旧**：SSE 流式，A/B 两侧各跑一次 ReAct → 流式输出结果

**新**：同步 JSON 接口

Request：
```json
{
  "anchor_message_id": "msg_xxx",
  "history_count": 5,
  "config_b": {
    "skip_retrieval": false,
    "deep_mode": false,
    "max_tool_result_chars": 2000,
    "max_iterations": 8,
    "memory": { "threshold": 0.5, "limit": 8 },
    "note": { "threshold": 0.5, "limit": 5 },
    "history": { "threshold": 0.8, "limit": 5 },
    "theme": { "threshold": 0.5, "limit": 10 },
    "temperature": null,
    "override_system_prompt": "可选，用户编辑后的 system prompt"
  },
  "system_prompt_source": "historical"  // "historical" | "live"
}
```

Response：
```json
{
  "anchor_message": { "id": "...", "content": "...", "role": "user", ... },
  "history_messages": [ ... ],
  "side_a": {
    "messages": [
      { "role": "assistant", "content": "...", "reasoning": "...", "tool_calls": [...], ... },
      { "role": "tool", "tool_call_id": "...", "content": "...", ... },
      ...
    ],
    "system_prompt": "当时实际使用的 system prompt（从 request_json 提取）",
    "model_params": { "temperature": 0.6, "model": "deepseek-v4-flash" }
  },
  "side_b": {
    "result": {
      "tool_calls": [...],
      "reply": "...",
      "reasoning": ["..."]
    },
    "retrieval_trace": {
      "memory": [{ "content": "...", "score": 0.72, "discarded": false, "discard_reason": "" }],
      "note": [...],
      "history": [...],
      "theme": [...]
    },
    "system_prompt": "B 侧实际使用的 system prompt"
  }
}
```

## 后端改动

### `replay/runner.go`
- 保留 `Run()`（B 侧用），新增 Run 方法返回 retrieval trace
- 删除 `RunFromRequestJSON()`（不再需要）
- `collectEvents()` 保留

### `controller/replay_handler.go`
- `PreviewReplay`：去掉 ConfigA 相关逻辑，只做 B 侧 preview
- `CompareReplay`：重写为同步接口
  - 从 DB 捞 A 侧消息链（anchor → 下一条 user msg 之前）
  - 从 A 侧 request_json 提取当时 system prompt + model params
  - 根据 `system_prompt_source` 决定 B 侧 system prompt 起点
  - 调用 `runner.Run()` 跑 B 侧
  - 返回完整 JSON

### `service/message_repo.go`（如需）
- 可能新增方法：获取某条消息之后、下一条 user 消息之前的连续 assistant/tool 消息链

## 前端改动

### `ReplayCompareView.vue`
- 去掉 EventSource / SSE 相关逻辑
- 改用 `fetch` POST → 一次性拿到结果
- A 侧渲染消息链（和 B 侧结构对齐）
- B 侧新增检索溯源区块（可折叠）
- A 侧展示当时 system prompt（只读）
- B 侧展示实际使用的 system prompt

### `MessageDebugView.vue`
- 「对比重放」入口逻辑不变

## 删除项

- `runner.RunFromRequestJSON()` 方法
- A 侧 ConfigA 相关前端 UI
- SSE 事件流逻辑（`replay_done` / `done` 等事件）
