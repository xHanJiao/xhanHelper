# ~~P6-1 — DeepSeek 思考模式接入（已被取代）~~

> ⚠️ 本方案已被 `design/p6-tool-based-switching.md` 取代。请以最终方案为准。

> 日期: 2026-05-02 | 基于 DeepSeek API 文档 (thinking_mode / tool_calls / 多轮对话)

---

## 一、现状分析

### 1.1 当前实现

| 项目 | 当前值 |
|------|--------|
| 模型 | `deepseek-v4-flash`（单一模型） |
| API 端点 | `https://api.deepseek.com/chat/completions` |
| 请求参数 | `model` + `messages` + `tools` |
| 响应解析 | 仅 `content` + `tool_calls` |
| 消息结构 | `map[string]string{Role, Content}` |
| 工具调用 | OpenAI 兼容格式，tool name 用 `__` 拼接：`bookkeeping__create_bill` |

### 1.2 与 DeepSeek 文档的差距

| 对比项 | 当前 | 思考模式要求 |
|--------|------|------------|
| 模型 | `deepseek-v4-flash` | 强模型 `deepseek-v4-pro`（支持思维链） |
| 请求参数 | 无 thinking | `reasoning_effort="high"` + `extra_body={"thinking":{"type":"enabled"}}` |
| 响应字段 | `content` | `reasoning_content`（思维链）+ `content`（最终回答） |
| 消息结构 | `map[string]string` | 需承载 `reasoning_content`、`tool_calls`、`tool_call_id` |
| tool 结果回传 | 无 `tool_call_id` | **必须**带 `tool_call_id` |
| 思维链回传 | 不存在 | 调用过工具的轮次，后续所有请求**必须**回传 `reasoning_content`，否则 API 400 |

---

## 二、目标

1. 接入 DeepSeek 思考模式，强模型 (`deepseek-v4-pro`) 支持思维链 + 原生工具调用
2. 实现强弱模型自动切换 + 手动 `/think` 触发
3. 客户端展示思维链内容

---

## 三、架构改动

### 3.1 数据模型升级

**Message 从 `map[string]string` 升级为结构体：**

```go
// internal/llm/adapter.go

type Message struct {
    Role             string     // system | user | assistant | tool
    Content          string     // 消息正文
    ReasoningContent string     // 思维链内容（DeepSeek 思考模式）
    ToolCalls        []ToolCall // 原生 tool_calls（assistant 消息携带）
    ToolCallID       string     // tool 消息的调用 ID
}

type ToolCall struct {
    ID       string
    Function ToolCallFunction
}

type ToolCallFunction struct {
    Name      string
    Arguments string // JSON string
}
```

**ChatRequest 新增字段：**

```go
type ChatRequest struct {
    SystemPrompt string
    Messages     []Message
    Tools        []ToolDefinition
    Thinking     bool // 是否启用思考模式
    Effort       string // "high" / "max"
}
```

### 3.2 deepseek.go 改动

**buildBody 改动：**

```go
func (c *DeepSeekClient) buildBody(req *ChatRequest) io.Reader {
    messages := make([]map[string]interface{}, 0)

    // system message
    messages = append(messages, map[string]interface{}{
        "role": "system", "content": req.SystemPrompt,
    })

    // 历史消息：按角色正确拼接
    for _, m := range req.Messages {
        msg := map[string]interface{}{"role": m.Role}
        if m.Content != "" {
            msg["content"] = m.Content
        }
        if m.ReasoningContent != "" {
            msg["reasoning_content"] = m.ReasoningContent
        }
        if len(m.ToolCalls) > 0 {
            // 转换 ToolCall 为 API 格式
            tcs := make([]map[string]interface{}, len(m.ToolCalls))
            for i, tc := range m.ToolCalls {
                tcs[i] = map[string]interface{}{
                    "id":   tc.ID,
                    "type": "function",
                    "function": map[string]interface{}{
                        "name":      tc.Function.Name,
                        "arguments": tc.Function.Arguments,
                    },
                }
            }
            msg["tool_calls"] = tcs
        }
        if m.ToolCallID != "" {
            msg["tool_call_id"] = m.ToolCallID
        }
        messages = append(messages, msg)
    }

    payload := map[string]interface{}{
        "model":    c.model,
        "messages": messages,
    }

    // 思考模式参数
    if req.Thinking {
        payload["reasoning_effort"] = "high"
        payload["thinking"] = map[string]string{"type": "enabled"}
    }

    // tools
    if len(req.Tools) > 0 {
        tools := make([]map[string]interface{}, 0)
        for _, t := range req.Tools {
            for _, fn := range t.Functions {
                tools = append(tools, map[string]interface{}{
                    "type": "function",
                    "function": map[string]interface{}{
                        "name":        t.Name + "__" + fn.Name,
                        "description": fn.Description,
                        "parameters":  fn.Parameters,
                    },
                })
            }
        }
        payload["tools"] = tools
    }

    b, _ := json.Marshal(payload)
    return bytes.NewReader(b)
}
```

**parseDeepSeekResponse 改动：**

```go
// 新增返回值: (content, reasoning_content, tool_calls, error)
func parseDeepSeekResponse(body []byte) (string, string, []ToolCall, error) {
    var result struct {
        Choices []struct {
            Message struct {
                Content          string `json:"content"`
                ReasoningContent string `json:"reasoning_content"`
                ToolCalls        []struct {
                    ID       string `json:"id"`
                    Function struct {
                        Name      string `json:"name"`
                        Arguments string `json:"arguments"`
                    } `json:"function"`
                } `json:"tool_calls"`
            } `json:"message"`
        } `json:"choices"`
        Error *struct {
            Message string `json:"message"`
        } `json:"error"`
    }
    // ... 解析并返回
}
```

**ChatSync 改为返回完整 Message：**

```go
// 原来返回 string，现在返回 Message（含 reasoning_content）
func (c *DeepSeekClient) ChatSync(ctx context.Context, req *ChatRequest) (Message, error)
```

### 3.3 orchestrator.go 强弱切换

```
用户消息到达
  │
  ├─ 以 /think 开头?
  │   └─ 是 → 启用思考模式，模型=strong
  │
  └─ 否 → 使用 weak 模型（非思考模式）
        │
        ├─ LLM 返回
        │   ├─ 包含 plan.execute → 升级，用 strong 重跑
        │   ├─ tool_calls ≥ 3     → 升级，用 strong 重跑
        │   └─ 其余              → 弱模型回复直接输出
        │
        └─ 升级后用 strong 重跑同一请求
```

**核心逻辑：**

```go
func (o *Orchestrator) decideModel(msg string) (thinking bool, model string) {
    if strings.HasPrefix(msg, "/think") {
        return true, o.strongModel
    }
    return false, o.weakModel
}

func (o *Orchestrator) shouldEscalate(messages []llm.Message, lastMsg llm.Message) bool {
    // 条件1: 触发了 plan.execute
    if strings.Contains(lastMsg.Content, "plan__execute") {
        return true
    }
    // 条件2: 多个 tool_call
    if len(lastMsg.ToolCalls) >= 3 {
        return true
    }
    // 条件3: 用户消息很长（长文分析）
    lastUser := getLastUserMessage(messages)
    if len([]rune(lastUser.Content)) > 500 {
        return true
    }
    return false
}
```

### 3.4 思维链上下文正确回传

**关键规则（来自 API 文档）：**

> 调用过工具的轮次，在后续**所有**请求中，必须完整回传 `reasoning_content` 给 API，否则返回 400 错误。

**实现：**

```
Turn 内:
  assistant(有 tool_calls, 有 reasoning_content) → tool result → assistant(有 tool_calls, 有 reasoning_content) → ...
  最后 assistant(无 tool_calls, 内容是最终回复)

下次请求:
  该 Turn 内所有 assistant 消息的 reasoning_content 都要带在 messages 里
```

我们在 orchestrator 的 messages 管理中自然保留即可——每轮 assistant 消息都存完整字段（含 `reasoning_content`、`tool_calls`），后续请求直接拼接，无需特殊处理。

### 3.5 客户端展示思维链

**新增 SSE 事件类型 `thought`：**

```
event: thought
data: {"delta":"用户想查杭州天气，我需要先获取日期..."}

event: thought_done
data: {}

event: message
data: {"delta":"杭州明天的天气是..."}
```

后端在流式场景下将 `reasoning_content` 以 `thought` 事件推送，客户端可用折叠/灰色文字展示。

---

## 四、变动文件清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/llm/adapter.go` | 改 | Message + ToolCall 结构体；ChatRequest 加 Thinking/Effort |
| `internal/llm/deepseek.go` | 改 | buildBody 加 thinking 参数；parse 加 reasoning_content；ChatSync 返回 Message |
| `internal/llm/factory.go` | 改 | 支持传入 model 名；或 NewClient 支持多模型 |
| `internal/agent/orchestrator.go` | 改 | 强弱切换决策；/think 前缀解析；Message 升级适配 |
| `internal/config/config.go` | 改 | 加 StrongModel / WeakModel 字段 |
| `conf/config.dev.yaml` | 改 | 配置两个模型名 |
| `client/.../ChatViewModel.swift` | 改 | 新增 thought 事件解析 |

---

## 五、实施顺序

| 步骤 | 内容 | 依赖 |
|------|------|------|
| 1 | adapter.go: Message 结构体升级 | 无 |
| 2 | deepseek.go: buildBody + parse 改造 | 步骤1 |
| 3 | config: StrongModel / WeakModel | 无 |
| 4 | factory.go: NewClient 支持多模型/动态切换 | 步骤3 |
| 5 | orchestrator.go: 强弱切换逻辑 | 步骤2,4 |
| 6 | 客户端: thought 事件展示 | 步骤2 |
| 7 | 测试: make test 全绿 | 全部 |
