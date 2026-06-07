# Phase 14 — 原生 Function Calling 切换 & SSE 实时流式优化

## 1. 背景与动机

### 1.1 问题：伪 SSE

当前系统采用 **prompt engineering** 方式实现工具调用：在 system prompt 中注入工具 JSON Schema 文本，要求 LLM 以 JSON 格式输出 `{"reply":"...","tool_calls":[...]}`。

这导致 SSE 是「伪实时」的：

```
LLM 流式输出 → orchestrator 全部收完 → parseJSON → 提取 reply → 打字机效果推到前端
                                      ↑
                                   用户在此阶段盯着空白屏幕
```

只有 `thought_delta`（推理过程）是实时推送的，正文文本必须等完整 JSON 收完才能解析。

### 1.2 根因

JSON 是一种必须完整接收才能解析的格式。LLM 输出的 JSON 文本嵌在 `content` 字段里，后端无法在收完前提取其中的 `reply` 子串。

### 1.3 目标

- 文本回复实现**真正的流式推送**（边收边推）
- 去掉 JSON 壳，不依赖 prompt engineering 解析
- 保持工具调用能力
- 兼容旧会话历史

---

## 2. 方案：切换到原生 Function Calling

DeepSeek API 已支持 OpenAI 兼容的原生 function calling。

### 2.1 核心变化

| | 现在（Prompt Engineering） | 切换后（Native FC） |
|---|---|---|
| 工具定义 | 拼入 system prompt 文本 | 通过 `tools` 参数传 JSON Schema |
| LLM 响应 | content = `{"reply":"...","tool_calls":[...]}` | content = 纯净回复文本，tool_calls = 结构化字段 |
| 文本推送 | 全收完 → 解析 → 推 | **边收 delta 边推** |
| 工具调用 | `ParseLLMResponse()` 从 text 中解析 | API 直接返回 `tool_calls[]`，无需解析 |

### 2.2 流式语义

原生 FC 下，LLM 流式返回中：
- `content` delta → **直接推到前端**（纯文本，无需解析）
- `tool_calls` delta → 在 `readSSEStream` 中累积（现有逻辑已实现 `tcMap`），done 事件时携带完整 tool_calls
- `reasoning_content` delta → 实时推到前端（现有逻辑保持不变）

### 2.3 思考模式 + 工具调用

DeepSeek 支持 thinking + tools 同时开启。关键约束：

> 进行了工具调用的轮次，在后续所有请求中必须完整回传 `reasoning_content`，否则 API 返回 400。

---

## 3. 架构改动

### 3.1 改动总览

```
HTTP Request
  → middleware
  → controller/chat.go              [不改动，SSE 事件格式不变]
     → orchestrator.Run()
        ├── buildLLMMessages()      [★ 改动：旧格式兼容判断]
        ├── mkLlmChatReq()          [★ 改动：Tools 字段不再为 nil]
        ├── runOneRound()           [★ 改动：text_delta 实时推送]
        ├── finalizeRound()         [★ 改动：不再 parseLLMResponse，直接用原生 tool_calls]
        │   └── handleOneRoundResponse() → ParseLLMResponse() [★ 改为解析原生 tool_calls]
        ├── executeConcurrently()   [★ 改动：工具结果以 role=tool 保存]
        └── streamTextChunks()      [★ 删除，不再需要打字机效果]
  → view/render.go                  [不改动]
```

### 3.2 关键不改的部分

- **SSE 事件类型不变**：`text_delta`、`thought_delta`、`tool_call`、`tool_result`、`done`、`error`
- **前端无需改动**：前端收的 `message` 事件里 `{"delta":"..."}` 格式不变
- **工具执行接口不变**：`ExecuteByName(ec, toolName, funcName, args)` 保持不变

---

## 4. 详细设计

### 4.1 Registry：新增扁平化工具导出方法

**文件**：`server/internal/tool/registry.go`

新增方法 `GetNativeFCTools()`，将所有工具的所有函数扁平化为 DeepSeek 原生 FC 格式：

```go
// NativeFCToolDef — 原生 function calling 的单个函数定义
type NativeFCToolDef struct {
    Type     string   `json:"type"`     // 固定 "function"
    Function FuncDef  `json:"function"`
}

func (r *Registry) GetNativeFCTools() []NativeFCToolDef {
    // 遍历所有工具的所有函数，以 "toolName__funcName" 格式扁平化命名
    // 隐藏工具（hiddenNames）和 tool_retriever 自身不导出
}
```

命名规则：`name = toolName + "__" + funcName`（用双下划线分隔）。
反向解析时：按 `__` 拆分为 `(toolName, funcName)`。

示例：
```
tool: bookkeeping, function: create_bill  →  "bookkeeping__create_bill"
tool: note,        function: record_note  →  "note__record_note"
tool: calculator,  function: calculate    →  "calculator__calculate"
```

### 4.2 LLM Adapter：Tools 字段已就绪

**文件**：`server/internal/llm/adapter.go`

`ChatRequest.Tools` 字段和 `ToolDefinition` 结构已存在。`deepseek.go` 中的 `buildStreamBody` 已有序列化 tools 的逻辑（`req.Tools` 非 nil 时构造 JSON）。

**改动**：无需改 adapter 层。只需确保 orchestrator 正确填充 `ChatRequest.Tools`。

注意：`BuildSystemPrompt` 方法不再需要注入工具定义文本。System prompt 中移除 `### 输出格式（必须严格遵守）` 整个 JSON 输出规范段落。

### 4.3 Orchestrator：核心改动

**文件**：`server/internal/agent/orchestrator.go`

#### 4.3.1 mkLlmChatReq — 传入 Tools

```
当前 (line 351):
    Tools: nil,

改为:
    Tools: o.registry.GetNativeFCTools(),
```

#### 4.3.2 原生 FC 的 content 语义变化

**重要**：原生 FC 下，`content` 和 `tool_calls` **可以在同一条消息中共存**。例如：

```
content="我先帮你查一下天气，让我获取今天的日期。" + tool_calls=[get_date]
```

这和 prompt engineering 下强制 `reply=""` 当 tool_calls 非空不同。LLM 会自然地「边说边调工具」。

对 streaming 的影响：
- content delta 先到 → 实时推给用户（用户看到「我先帮你查一下...」）
- tool_calls delta 随后到 → 在 readSSEStream 中累积
- done 事件 → 处理 tool_calls

用户体感：文本立刻出现，不用等工具执行完。

#### 4.3.3 runOneRound — text_delta 实时推送

```
当前 (line 313-317):
    case "text_delta":
        streamContent.WriteString(se.Content)   // 仅累积，不推送

改为:
    case "text_delta":
        streamContent.WriteString(se.Content)   // 仍累积（保存到 DB 用）
        o.sendEvent(events, se)                 // ★ 实时推送到前端
```

#### 4.3.4 finalizeRound — 使用原生 tool_calls

```
当前流程:
    收到 done → ParseLLMResponse(content) → 提取 reply + 解析 tool_calls

改为:
    收到 done → 提取 se.ToolCalls（原生格式）→ 转换为内部 tool.ToolCall
```

新增转换函数：

```go
func nativeToolCallsToInternal(tcs []llm.ToolCall) []tool.ToolCall {
    // 将 OpenAI 格式 tool_calls 转为内部格式
    // 拆分 name 中的 "__" 得到 toolName + funcName
}
```

#### 4.3.5 streamTextChunks — 删除

打字机效果方法 `streamTextChunks`（line 955-990）不再需要。文本已在 `runOneRound` 中实时流式推送，无需事后模拟。

#### 4.3.6 工具执行 — role: tool + tool_call_id

工具执行结果新增保存方式：

```go
// 工具结果消息保存时
msg := model.Message{
    Role:       "tool",           // 新：使用 "tool" role
    ToolCallID: &toolCall.ID,     // 新：关联原生 tool_call ID
    Content:    resultContent,
    // ...
}
```

### 4.4 消息拼接：旧格式兼容

**文件**：`server/internal/agent/orchestrator.go` 中的 `buildLLMMessages` 或等价函数

加载历史消息时，按消息自身字段判断格式：

```go
func buildMessageForLLM(msg model.Message) llm.Message {
    // 新格式：role=tool 且有 tool_call_id
    if msg.Role == "tool" && msg.ToolCallID != nil {
        return llm.Message{
            Role:       "tool",
            Content:    msg.Content,
            ToolCallID: *msg.ToolCallID,
        }
    }
    
    // 新格式：assistant 有原生 tool_calls
    if msg.Role == "assistant" && msg.ToolCallsJSON != nil {
        var tcs []llm.ToolCall
        json.Unmarshal([]byte(*msg.ToolCallsJSON), &tcs)
        return llm.Message{
            Role:             "assistant",
            Content:          msg.Content,          // 纯文本，不是 JSON
            ReasoningContent: ptrToString(msg.ReasoningContent),
            ToolCalls:        tcs,
        }
    }
    
    // 旧格式：assistant 的 content 是 JSON 壳
    if msg.Role == "assistant" && strings.HasPrefix(msg.Content, "{") {
        parsed, _ := ParseLLMResponse(msg.Content)
        if parsed != nil {
            return llm.Message{
                Role:             "assistant",
                Content:          parsed.Reply,       // 提取纯文本
                ReasoningContent: ptrToString(msg.ReasoningContent),
                // 旧格式不传 ToolCalls（无法还原 tool_call_id）
            }
        }
    }
    
    // 旧格式：tool result 伪装成 user 消息
    // 保持 role=user，不传 tool_call_id
    return llm.Message{
        Role:             msg.Role,
        Content:          msg.Content,
        ReasoningContent: ptrToString(msg.ReasoningContent),
    }
}
```

**判断逻辑**：
- `role == "tool"` 且 `tool_call_id` 非空 → 新格式（原生 FC）
- `role == "assistant"` 且 `content` 以 `{` 开头 → 旧格式（JSON 壳），提取 `reply`
- 其他 → 原样保留

### 4.5 reasoning_content 回传规则

根据 DeepSeek 文档要求，原生 FC + thinking 模式下的拼接规则：

```
同一 Turn 内（有工具调用）：
  user₁ → assistant₁(reasoning + tool_calls) → tool_result₁ → assistant₂(reasoning + reply)
  
  所有消息的 reasoning_content 在后续请求中必须原样回传

新 Turn：
  user₂ → ...
  
  前面 Turn 中所有 reasoning_content 仍需保留
```

实现：`llm.Message` 已有 `ReasoningContent` 字段，构建消息时原样填入即可。

---

## 5. 兼容性策略

### 5.1 数据库

Message 表字段已就绪：
- `role` — 已支持 `"tool"` 值
- `tool_call_id` — 可为 NULL（旧消息），新消息填充
- `tool_calls_json` — 旧消息存 prompt-eng 格式或为空，新消息存原生 FC 格式
- `reasoning_content` — 旧消息可能已存，新消息继续存

**无需 DDL 变更**。

### 5.2 旧会话处理

旧会话消息保持现有 role 和 content 不变。加载历史时，上述 `buildMessageForLLM` 逻辑自动判断每条消息的格式，旧消息走旧解析路径，新消息走原生 FC 路径。

### 5.3 降级开关

建议新增配置项，支持热切换：

```yaml
llm:
  use_native_fc: true  # true=原生FC, false=prompt engineering(旧)
```

用于：
- 上线初期快速回退
- A/B 对比测试
- Doubao 等不支持原生 FC 的 provider 自动 fallback

---

## 6. System Prompt 变化

### 6.1 移除的内容

- `### 输出格式（必须严格遵守）` 整个段落
- `{"reply":"...","tool_calls":[...]}` 格式说明
- 工具定义的**函数级别**描述（参数 schema 改由 tools 参数传递）
- `tool_retriever` 相关规则（工具定义已在 tools 中，LLM 可直接看到所有函数签名，不需要 search 再查）

### 6.2 保留的内容

- Layer 1 公共规则（时间、用户 ID 等）
- Layer 2 角色定义
- Layer 3 检索上下文（主题/记忆/笔记/历史）

### 6.3 工具全量发送 vs tool_retriever

**决策：全量发送，废弃 tool_retriever 的检索引导功能。**

工具统计（主 Registry，非隐藏）：

| 工具 | 函数数 |
|------|--------|
| bookkeeping | 8 |
| document | 9 |
| note | 8 |
| mission | 6 |
| calculator | 2 |
| agent | 2 |
| search (_bocha) | 1 |
| search_notes_v2 | 1 |
| search_memories_v2 | 1 |
| message_search | 1 |
| reminder | 1 |
| **合计** | **40** |

加动态工具约 45-50 个函数。每个函数 JSON Schema 约 200-600 字符，估算 **~4,000-5,000 tokens**。

相比保留 tool_retriever 按需加载：
- 全量发送：多 ~4,000-5,000 tokens/请求，但省掉一轮 tool_retriever.search 往返
- DeepSeek prompt caching 会缓存 tools 定义（不常变），实际额外开销有限
- 先跑 B 方案，如果 token 消耗过大再考虑 A

### 6.4 System Prompt 精简

工具定义改为在 `tools` 参数中发送，system prompt 中**不再嵌入工具 schema 文本**。这让 system prompt 大幅缩短，节省 token。

---

## 7. 风险与注意事项

### 7.1 reasoning_content 回传 400

DeepSeek 明确要求：有 tool_calls 的轮次的 reasoning_content 必须在后续请求中回传。**这是之前 400 报错的疑因**。

缓解：消息拼接时严格检查 `ToolCallsJSON` 是否非空，决定是否携带 `reasoning_content`。

### 7.2 tool_call_id 匹配

同一轮中，tool_result 的 `tool_call_id` 必须与 assistant 发出的 `tool_calls[].id` 匹配。单个 assistant 消息可能产生多个 tool_calls，每个需独立回传 tool_result。

缓解：`executeConcurrently` 中为每个 tool_call 生成对应的 tool_result，正确关联 ID。

### 7.3 扁平化命名冲突

使用 `__` 分隔符时，需确保没有函数名本身包含 `__`。检查现有所有 FuncSchema.Name。

缓解：启动时校验，发现冲突直接 panic（fail-fast）。

### 7.4 Doubao / Claude provider

- Doubao 不支持原生 function calling，需保持 prompt engineering 路径
- Claude stub 暂不处理

缓解：`use_native_fc` 配置 + provider 类型判断，非 DeepSeek 自动走旧路径。

---

## 8. 实施步骤

1. **Registry**：新增 `GetNativeFCTools()` 方法，扁平化导出
2. **命名校验**：检查所有 FuncSchema.Name 不含 `__`
3. **Orchestrator**：`mkLlmChatReq` 传 Tools，`runOneRound` 实时推 text_delta
4. **finalizeRound**：解析原生 tool_calls，删除 `streamTextChunks`
5. **消息拼接**：实现 `buildMessageForLLM` 的格式兼容逻辑
6. **工具执行**：结果以 `role: tool` + `tool_call_id` 保存
7. **System Prompt**：移除 JSON 输出规范章节
8. **Schema 审查**：检查所有 FuncSchema.Parameters 符合 DeepSeek 要求
9. **配置**：新增 `use_native_fc` 开关，非 DeepSeek provider 默认 false
10. **测试**：`make test` 全绿 + 手动端到端测试
