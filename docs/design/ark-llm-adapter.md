# Ark LLM 适配 — 火山方舟 DeepSeek 接入

## 背景

正式聊天 LLM 从官方 DeepSeek API（`api.deepseek.com`）切换到火山方舟（Ark，
`ark.cn-beijing.volces.com/api/v3`）托管的 DeepSeek 模型。方舟是通用多模型平台，
其 Chat API 与官方 DeepSeek 有少量差异，需要一个独立 client 适配，同时保持：

1. **deepseek.go 一字不动**——官方 DeepSeek 路径行为完全不变，可随时切回。
2. **方舟 client 可扩展**——照 doubao.go 的独立文件模式，未来方舟平台的其他模型
   （doubao-seed 系列等）可复用/派生。

依据：火山方舟《对话(Chat) API》文档（用户提供 PDF）。

## 差异对照（PDF 确认）

### 完全兼容、无需适配

| 项 | 说明 |
|----|------|
| 端点 | `https://ark.cn-beijing.volces.com/api/v3/chat/completions` + `Authorization: Bearer <API Key>`，现有 `baseURL + "/chat/completions"` 拼接结构可复用 |
| model | `deepseek-v4-flash` / `deepseek-v4-pro` 是方舟合法 Model ID（PDF 明确列出），现有配置模型名不变 |
| messages | system/user/assistant/tool + `tool_calls` + `tool_call_id` + `reasoning_content` 结构一致 |
| thinking | `{"type": "enabled"/"disabled"}` 一致（方舟多一个 `auto`，用不上） |
| tools / tool_choice | OpenAI function calling 格式一致 |
| SSE | `data:` 行 + `[DONE]`，`choices[].delta` 结构一致 |

### 需要适配的差异

| # | 差异 | 说明 |
|---|------|------|
| 1 | **`encrypted_content`** | 流式 `delta` 新增字段：思考内容加密原文。在思考内容输出完成后、正式应答前，以独立 chunk 返回（该 chunk 的 content/reasoning 均为空）。PDF 要求「支持的模型调用时必须回传，保证模型推理效果」。当前代码完全不处理 |
| 2 | **流式默认不带 usage** | PDF 示例流式 chunk 中 `usage: null`。需请求侧 `stream_options.include_usage: true`，方舟才会在 `[DONE]` 前额外返回一个 usage chunk（choices 为空数组）。不加则 llmcost 成本统计全记 0 |
| 3 | **`user_id` 请求参数** | 方舟参数表未列出 `user_id`（官方 DeepSeek 支持）。新 client 不发 |
| 4 | **max_tokens 语义** | 方舟 `max_tokens` 默认 4096、取值范围随模型；另提供 `max_completion_tokens`（含思维链），两者不可同时设置。现有 `default_max_tokens: 30000` 是否超限需真机验证 |

### 明确忽略（用户指示：用不到的平台参数不关注）

`frequency_penalty` / `logit_bias` / `logprobs` / `top_logprobs` / `top_p` /
`name` / `strict` / `tool_choice` / `service_status` / `moderation_hit_type` /
`encrypted_content` 之外的响应字段。

## 架构决策

**新建独立 `ark` provider client（`llm/ark.go`），复用现有 LLMClient 接口与装饰器栈，零改动 deepseek.go。**

- `doubao.go` 先例：完全独立的 client 文件，同包内复用 `StatusError`、
  `sendStreamEvent`、共享类型。
- 装饰器栈（`monitor.WrapLLMClient` / `llmcost.WrapLLMClient` / `llm.WrapRetry`）
  已确认 provider 无关（`internal/app/init_llm.go`），无需改动。
- 双模型：`NewClient`（flash，日常对话）与 `NewStrongClient`（pro，深思考）
  由 `factory.go` 按 `provider: ark` 分发。

### 为什么单 client 类型

Flash/Pro 的差异只有 model 名与 thinking 默认值，而 thinking 已由 orchestrator
管理（`orchestrator.go:1024`：pro 模式强制 `Thinking=true`，flash 为 nil）。
client 侧仅直通 `req.Thinking`，不引入第二个类型。

## encrypted_content 全链路

照 reasoning_content 现有链路镜像（该链路已被证明必须：工具调用轮与上下文缓存
回传见 `orchestrator.go:1955` 注释）：

```
Ark SSE delta.encrypted_content
  → llm.StreamEvent.EncryptedContent（done 事件携带，照 Usage 模式）
    → orchestrator.finalizeRound → llm.Message.EncryptedContent
      ├─ ReAct 循环内：state.msgs 内存直传（自动）
      └─ saveAssistantMsg → model.Message.encrypted_content 列
            └─ 跨请求：BuildMessagesForFlash 从 DB 回传 → ark 请求体 assistant 消息
```

要点：

- 流式侧：`delta.encrypted_content` 出现在独立 chunk（content/reasoning 为空），
  ark 解析器单独累加，随 done 事件输出。
- sync 侧：非流式响应 `message.encrypted_content` 同理解析（后台 worker 的
  ChatSync 路径）。
- DB 列 `json:"-"`：加密原文是服务器↔模型回传数据，不暴露给前端历史接口。
- `buildMessages`（deepseek.go）不动；ark.go 自带消息构建（含
  `encrypted_content`/`reasoning_content` 回传）。

## 改动清单

### 1. `server/internal/llm/ark.go`（新建，~280 行）

```go
type ArkClient struct {
    apiKey     string
    baseURL    string
    model      string
    httpClient *http.Client
}
func NewArkClient(cfg config.LLMConfig, model string) *ArkClient
```

实现 `llm.LLMClient` 接口，自带（不依赖 deepseek.go）：

| 成员 | 说明 |
|------|------|
| `setHeaders` | Content-Type + Bearer（抄 doubao） |
| `buildMessages` | 含 `encrypted_content` / `reasoning_content` / `tool_calls` / `tool_call_id` |
| `buildStreamBody` / `buildSyncBody` | `model/messages/stream`、`stream_options: {"include_usage": true}`（仅 stream）、`thinking`（直通 req.Thinking）、`temperature`、`max_tokens`；**不发 user_id** |
| `readSSEStream` | 捕获 content/reasoning/encrypted/tool_calls/usage；`text_delta`/`thought_delta` 事件 + done 事件（ToolCalls/Usage/**EncryptedContent**/RequestJSON/ResponseJSON）；usage chunk 的 choices 为空数组需兼容 |
| `callSyncAPI` | 非流式，解析 `message.reasoning_content` + `message.encrypted_content` + usage |
| `chatWithRawJSON` | 照 deepseek.go：改 stream=false，用自有解析器 |
| `BuildSystemPrompt` | 照 FlashClient 版（native FC 工具说明注入） |
| `Chat` / `ChatSync` / `ChatWithRawJSON` / `ModelName` | 直通 |

复用（同包共享，不改 deepseek.go）：`StatusError`、`sendStreamEvent`。

**错误处理**：非 200 一律 `&StatusError{Code, Body}`（`retry.go` 的
`isRetryableError` 依赖此类型做 429/5xx 重试）。

### 2. `server/internal/llm/adapter.go`（纯加法）

```go
type Message struct {
    ...
    EncryptedContent string `json:"encrypted_content,omitempty"`
}

type StreamEvent struct {
    ...
    EncryptedContent string `json:"-"`
}
```

### 3. `server/internal/llm/factory.go`

- `NewClient`：`case "ark": return NewArkClient(cfg, cfg.Model), nil`
- `NewStrongClient`：`case "ark": model := cfg.StrongModel; 空则回退 cfg.Model; return NewArkClient(cfg, model), nil`

### 4. `server/internal/agent/orchestrator.go`（加法）

- `newAssistantMessage`（:1210）：+1 行 `EncryptedContent: se.EncryptedContent`
  （se 已是参数，签名不变）
- `saveAssistantMsg`（:1552 旁）：镜像 ReasoningContent 落库
- `BuildMessagesForFlash`（:1958 旁）：镜像 ReasoningContent 回传

### 5. `server/internal/agent/subagent.go`（加法）

`SaveAssistantMsg`（:258 旁）：镜像 ReasoningContent 落库。

### 6. `server/internal/model/message.go`（加法）

```go
EncryptedContent *string `gorm:"type:text;column:encrypted_content" json:"-"`
```

### 7. `server/migrations/026_add_message_encrypted_content.sql`（新建）

```sql
ALTER TABLE messages ADD COLUMN encrypted_content TEXT NULL;
```

MySQL prod 用；SQLite dev/test 靠 GORM AutoMigrate 自动加列。

### 8. `server/internal/config/config.go`

`LLMConfig.Provider` 注释：`doubao | deepseek | ark`。

### 9. `server/conf/config.yaml`

```yaml
llm:
  api_key: ${LLM_API_KEY}   # 换方舟 API Key
  provider: ark
  model: deepseek-v4-flash
  strong_model: deepseek-v4-pro
  base_url: https://ark.cn-beijing.volces.com/api/v3
```

`llm_bg`（硅基流动）与 `embedding`（已用方舟 base URL，无 `/api/v3`）不动。

## 验证

### 自动化

1. `go build ./...` + `go test ./...` 全绿。
2. `ark_test.go` 单测：SSE 解析（encrypted_content 独立 chunk + include_usage
   的 usage chunk + tool_calls 增量拼接）。

### 真机（验收关键点）

1. **flash 普通聊天**：文本正常流式、无思维链、llmcost 落账（usage 非 0）。
2. **pro 深思考 + 工具调用**：thought_delta 流正常、工具执行正常、
   `max_tokens: 30000` 未被拒（若 4xx 报参数超限则调配置）。
3. **多轮**：同会话第二轮请求体（debug_persist_json 或日志）中 assistant
   消息带 `encrypted_content` 与 `reasoning_content`。
4. 若 flash 实测出现思维链（方舟默认 auto）→ ark.go 加 1 行显式
   `thinking: {"type": "disabled"}`。

## 明确砍掉 / 延后（YAGNI）

| 项 | 何时加 |
|----|--------|
| usage 缓存字段 fallback（`prompt_tokens_details.cached_tokens`） | 真机抓一次 usage JSON 确认结构后再加 3 行 |
| `reasoning_effort` 透传 | 全库无人 set 此字段，有调用方再加 |
| `max_completion_tokens` | pro 思考模式实测被截断时 |
| `chunk_include_usage` | 不需要逐块用量 |
| 老会话重放缺 encrypted_content 的处理 | PDF 为软性要求不硬报错，接受切后首轮质量略降 |
| `chat.go` 死代码 `buildLLMMessages`、replay_handler | 不碰 |
