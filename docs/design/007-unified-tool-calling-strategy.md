# 007 — 统一工具调用策略

## 背景

当前系统有两种工具调用路径：

| 路径 | 使用场景 | 工具传递方式 | LLM 响应 | 工具结果角色 |
|------|----------|------------|---------|------------|
| Native function calling | Pro 模式（DeepSeek reasoner） | API `tools` 参数 | 原生 `tool_calls` | `role: "tool"` |
| Prompt engineering (PE) | Flash 模式（DeepSeek chat / 豆包） | 文本注入 system prompt | JSON `{"reply":"...","tool_calls":[...]}` | `role: "user"` |

### 问题

1. **Pro / Flash 不能来回切换**：消息历史中工具结果的角色不同（`tool` vs `user`），中途切换模型会炸
2. **reasoning_content 回传要求复杂**：DeepSeek 思考模式下，有 tool_call 的轮次必须在后续请求中完整回传 reasoning_content，多轮工具调用时累积携带
3. **Pro 入口已下掉**：因为上述不兼容问题

## 方案演变

### Phase 1（当前方案）：提示词工程 + 统一消息格式

**核心思路**：Pro 退回使用 prompt engineering（不再发 API `tools` 参数），存储层格式统一。

```
聊天入口 → pickClient(deepMode) → ProClient 或 FlashClient
                                       ↓
                              (thinking 可选, but 都不发 tools)
                                       ↓
                              全都走 prompt engineering
                                       ↓
                         parseLLMResponse(content)  ←── 两个客户端走同一解析路径
                                       ↓
                         消息存储：role: "tool" 统一（包括 Flash）
                                       ↓
                         BuildMessages() 发送时:
                          - Pro 客户端：原样发送 role: "tool"
                          - Flash/豆包：role: "tool" → role: "user"
```

**优点**：
- 工具调用方式只有一种路径，Pro/Flash 行为一致，可自由切换
- 不需要处理 native tool calling 下的 reasoning_content 回传复杂性
- thinking 模式照样工作（推理走 `thought_delta`，回复 JSON 走 `text_delta`，互不干扰）
- 现有 `parseLLMResponse()` / `ParseFlashJSON()` / `extractAllVisibleText()` 已验证稳定
- prompt engineering 通用性最强，新模型零成本接入

**缺点**：
- 放弃 native function calling 的可靠性优势（格式保证、参数 schema 验证）
- 工具定义占用 system prompt 空间（通过 tool_retriever 动态检索缓解）
- Pro 模型的工具调用准确率依赖 prompt 质量而非 API 结构保证

**存储变更**：
- Flash 模式的工具结果从 `role: "user"` + `"[工具执行结果] "` 前缀 → `role: "tool"` + `IsInternal: true`（与 Pro 现行方式对齐）
- assistant 消息存储：`content` 保留完整 JSON（含 reply + tool_calls），消息历史 API 中 `cleanMessages()` 提取 reply
- `BuildMessagesForFlash()` 发送时做 role 转换

### Phase 2（未来）：加 Native 策略，形成双策略抽象

当未来需要支持原生 function calling 的模型增多时，引入策略层：

```go
type ToolCallStrategy interface {
    BuildContext(tools []ToolDefinition, sysPrompt string) (prompt string, apiPayload interface{})
    ExtractCalls(msg llm.Message) ([]tool.ToolCall, bool)
    ResultMessageRole() string
}
```

两个实现：
- `PEStrategy` — 现有 prompt engineering 逻辑
- `NativeStrategy` — DeepSeek / OpenAI 兼容的 function calling

```go
// 策略选择（按 provider 能力）
func resolveStrategy(client llm.LLMClient) ToolCallStrategy {
    if nc, ok := client.(NativeCaller); ok && nc.SupportsNativeTools() {
        return &NativeStrategy{client: nc}
    }
    return &PEStrategy{}
}
```

策略层介于 orchestrator 和 LLM 之间。Orchestrator 只调 strategy 接口，不关心底层是 native 还是 PE。

**优点**：
- 加新模型（如 Claude）只加一个 strategy 实现，不改 orchestrator / provider
- 同一策略可被多个 provider 复用
- 独立可测试

## 实施规划

### Phase 1 步骤

1. `orchestrator.go`：去掉 `isProMode` 对工具调用路径的分支选择，统一走 `handleFlashResponse()`
2. `orchestrator.go`：`saveToolMsgWithID` 改为存 `role: "tool"` + `IsInternal: true`（去掉 Flash 专属的 `role: "user"` 逻辑）
3. `orchestrator.go`：`BuildMessagesForFlash` 做 role 转换
4. `cleanMessages()`：统一处理，不再区分 Pro/Flash
5. `deepseek.go`：ProClient 不再发 `tools` 参数（或保留能力但不使用）
6. 重新上线 Deep Mode（Pro 入口）

### 不做的事情

- 不删除 `handleNativeToolCalls()` 逻辑（保留代码，Phase 2 的 NativeStrategy 会用到）
- 不删除 `baseClient.buildStreamBody()` 中的 `tools` 格式化逻辑
- 不做 DB migration（消息格式变更通过 `BuildMessages` 兼容层处理）

## 相关文档

- `docs/design/006-tool-context-and-retrieval.md` — tool_retriever 动态检索机制
- `CLAUDE.md` — 第七节「Tool 系统」
