# Bug #004 — Deep Mode reasoning_content 未持久化

> 日期: 2026-05-03 | 状态: 已修复，待测试

---

## 问题

Pro 模型（思考模式）的 `reasoning_content` 和 `tool_calls` 在对话结束后丢失，后续请求无法回传 → API 400 错误。

## 根因

三个缺口：

1. **model.Message 缺 `ReasoningContent` 字段**  
   `ToolCallsJSON` 和 `ToolCallID` 已经有了，但 `ReasoningContent` 没有

2. **`buildLLMMessages` 只读 Role+Content**  
   忽略了 ToolCallsJSON、ToolCallID、ReasoningContent

3. **chat.go done 时只存了 `fullContent`**  
   Pro 模式下中间轮次的 assistant(tool_calls=true) 和 tool 消息都没存

4. **职责错位** — 消息保存放在 controller 层，但 controller 只能看到 SSE 事件流，看不到 orchestrator 内部的完整消息列表

## 修复方案（已实施）

**核心思路：消息保存职责从 controller 下沉到 orchestrator。**

| 步骤 | 文件 | 内容 |
|------|------|------|
| 1 | `model/message.go` | 加 `ReasoningContent *string` 字段 |
| 2 | `agent/orchestrator.go` | 接收 msgRepo + convRepo；在 ReAct 循环中实时保存 user 消息、assistant 消息（含 reasoning_content + tool_calls_json）、tool 消息；done 事件直接带 conversation_id + message_id |
| 3 | `controller/chat.go` | 删除消息保存逻辑；`buildLLMMessages` 还原 ToolCallsJSON→[]ToolCall、ReasoningContent、ToolCallID；done 时只做 title 合并 |
| 4 | `cmd/main.go` | 传 msgRepo + convRepo 给 agent.New() |

**存档流程变为：**

```
orchestrator.Run()
  ├── 保存 user 消息（含内容）
  ├── LLM 返回 → 保存 assistant 消息（含 reasoning_content + tool_calls_json）
  ├── 工具执行 → 保存 tool 消息（含 tool_call_id）
  ├── LLM 返回 → 保存 assistant 消息（最终回复）
  └── 发 done 事件 → controller 直接转发
```

下次加载时 `buildLLMMessages` 完整还原所有字段，Pro 的 reasoning_content 自然参与上下文拼接。

## 影响范围

DeepSeek 思考模式（Pro）+ 调用工具的场景。Flash 模式不受影响。
