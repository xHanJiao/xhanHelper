# LLM 真正异步流式 — 方案

> 日期: 2026-05-08

## 一、现状

```
Orchestrator
  └─ ChatSync()                         ← 阻塞等完整响应（10-30秒）
       └─ HTTP POST /chat/completions   ← stream: false
            └─ 拿到完整 JSON → parseDeepSeekResponse() → Message
                 └─ Orchestrator 解析 text + tool_calls → 逐字推 SSE
```

- `Chat()` 是伪流式：内部调用 `ChatSync()` 拿完整响应，然后拆成逐字符推 channel
- Orchestrator 只用 `ChatSync()`，不用 `Chat()`
- 用户点发送 → 等完整 LLM 输出 → 才看到第一个字

## 二、目标

```
Orchestrator
  └─ Chat()                             ← 立即返回 channel
       └─ HTTP POST /chat/completions   ← stream: true
            └─ SSE 逐 chunk 读 →
                 ├─ delta.content          → text_delta 实时推到客户端
                 ├─ delta.reasoning_content → thought_delta 实时推到客户端
                 └─ 流结束 → 如有 tool_calls → 执行 → 继续 ReAct 下一轮
```

## 三、DeepSeek 流式 API 格式

**请求**：在原有请求体上加 `"stream": true`

**响应**：SSE 格式，`Content-Type: text/event-stream`

```
data: {"id":"...","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"你"}}]}

data: {"id":"...","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"好"}}]}

data: {"id":"...","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

**delta 字段**：

| 字段 | 说明 |
|------|------|
| `delta.content` | 文本增量（可能为空） |
| `delta.reasoning_content` | 思考链增量（仅 thinking 模式） |
| `delta.tool_calls[]` | 工具调用增量（arguments 为分片 JSON） |
| `finish_reason` | `"stop"` / `"tool_calls"` / `"length"` |

## 四、改什么

### 4.1 `internal/llm/deepseek.go`

**新增 `callStreamAPI` 方法**：

```
callStreamAPI(ctx, req, thinking)
  → buildRequest 加 "stream": true
  → HTTP POST
  → 创建 events channel (buffer 64)
  → go goroutine:
       bufio.NewReader(resp.Body) 逐行读
       解析 "data: {...}" →
         delta.content          → StreamEvent{text_delta}
         delta.reasoning_content → StreamEvent{thought_delta}
         delta.tool_calls[]     → 按 index 累积 arguments（分片拼接）
       读到 "data: [DONE]" →
         StreamEvent{done, 携带汇总的 tool_calls}
         close(channel)
  → return channel
```

**重写 `Chat`**：直接调用 `callStreamAPI` 返回 channel

**重写 `ChatSync`**：基于 `Chat` 实现，消费 channel 汇总成完整 `Message`

### 4.2 `internal/agent/orchestrator.go`

**`runReActLoop` 改造**：

```
每轮循环：
  改前: msg, err := client.ChatSync(ctx, req) → 拿到完整 Message → 处理
  改后: events, err := client.Chat(ctx, req)
        遍历 events:
          text_delta    → 直接转发给客户端 SSE（实时）
          thought_delta → 转发
          同时累积 content + reasoning + tool_calls
        channel 关闭后:
          如有 tool_calls → 执行 → 结果追加到 messages → continue 下一轮
          如无 tool_calls → 发 done → return
```

### 4.3 接口不变

`LLMClient` 接口四个方法签名不动：
- `Chat` — 改实现（真流式）
- `ChatSync` — 基于 Chat 实现
- `BuildSystemPrompt` — 不变
- `ModelName` — 不变

## 五、改造范围

| 文件 | 改动 |
|------|------|
| `internal/llm/deepseek.go` | 新增 `callStreamAPI`，重写 `Chat`/`ChatSync` |
| `internal/agent/orchestrator.go` | `runReActLoop` 用 `Chat` 替代 `ChatSync` |
| 其他文件 | 不变 |

## 六、边界情况

- **ctx 取消**：goroutine 内 select ctx.Done()，关闭 resp.Body，清理 channel
- **TCP 分片**：SSE 行可能跨 chunk 到达，用 buffer 拼接不完整行
- **tool_calls 分片拼接**：DeepSeek 流式 tool call 的 arguments 是多次 delta 累积，按 `tool_calls[index]` 合并
- **纯 tool 响应无文字**：某轮 LLM 只输出 tool call 没有 content，delta.content 全程为空，正常
- **done 事件**：携带 chat 结束后汇总的 tool_calls，供 Orchestrator 判断是否需要继续 ReAct
