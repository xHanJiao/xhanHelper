# P6-1 — DeepSeek 思考模式接入 + 强弱模型切换 最终方案

> 日期: 2026-05-02 | 状态: 方案已确认

---

## 一、方案演进

```
原始想法: 弱模型先上 → 规则检测 → 升级重跑
    ↓
第一版: 强模型作为 weak model 的 tool (deep_think)
    ↓
发现: reasoning_content 传了就传了，非思考模式 API 只是忽略，不会 400
    ↓
最终: 纯手动 toggle，无自动检测，无嵌套 orchestrator
```

**关键发现（来自 API 文档）：**
- 非思考模式收到 `reasoning_content` → API 忽略，不报错
- 思考模式收到含工具调用的消息 → 必须带 `reasoning_content`，否则 400
- 结论：**只要 Message 始终保留 `reasoning_content`，自由切换无风险**

---

## 二、最终架构

```
客户端 toggle OFF (Auto)           客户端 toggle ON (Deep)
  ┌──────────┐                       ┌──────────┐
  │ FlashClient │                     │ ProClient  │
  │ 非思考模式   │                     │ 思考模式    │
  │ 便宜快速     │                     │ 深度分析    │
  └──────────┘                       └──────────┘
       │                                    │
       └───────── 共用工具 ─────────────────┘
            bookkeeping / datetime / calculator / uuid / plan
```

**切换规则：**
- 用户打开 toggle → 后续消息走 ProClient（思考模式）
- 用户关闭 toggle → 后续消息走 FlashClient（非思考模式）
- 切换无缝——同一会话，同一消息历史，`reasoning_content` 始终保留

---

## 三、具体改动

### 3.1 Message 结构体升级

```go
// internal/llm/adapter.go

type Message struct {
    Role             string     // system | user | assistant | tool
    Content          string     // 消息正文
    ReasoningContent string     // 思维链内容（始终保留）
    ToolCalls        []ToolCall // 原生 tool_calls
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

### 3.2 FlashClient（非思考模式）

跟现在的 `deepseek.go` 基本一样，只改 buildBody 使用新 Message 结构体，`reasoning_content` 正常传（API 忽略）。

```
buildBody 不加 thinking 参数
parse 只解析 content + tool_calls
```

### 3.3 ProClient（思考模式）

```
buildBody 加 reasoning_effort="high" + thinking:enabled
parse 解析 content + reasoning_content + tool_calls
ChatSync 返回 Message（含 reasoning_content）
```

### 3.4 orchestrator 改动

```
用户消息 → 看当前 toggle 状态
  → Flash → orchestrate(FlashClient)
  → Pro  → orchestrate(ProClient)
```

无嵌套、无拦截、无升级判断。就是一个 if/else 选 client。

### 3.5 流式推送

Pro 模式下新增 `thought_delta` 事件，逐段推送 `reasoning_content`：

```
event: thought_delta
data: {"delta":"用户想分析1-5月餐饮趋势..."}

event: message
data: {"delta":"1-5月餐饮分析显示..."}
```

### 3.6 客户端改动

- ChatView 加 toggle（右上角图标或设置面板）
- PlanModalView 展示双层 plan（支持嵌套节点）
- 解析 `thought_delta` 事件并展示

---

## 四、文件改动清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/llm/adapter.go` | 改 | Message 升级为结构体 |
| `internal/llm/deepseek.go` | 改 | 拆出 FlashClient / ProClient |
| `internal/agent/orchestrator.go` | 改 | toggle 选择 client |
| `internal/config/config.go` | 改 | StrongModel / WeakModel |
| `conf/config.dev.yaml` | 改 | 两个模型名 |
| `client/.../ChatView.swift` | 改 | 深度模式 toggle |
| `client/.../ChatViewModel.swift` | 改 | thought_delta 解析 |

---

## 五、实施顺序

| 步骤 | 内容 | 说明 |
|------|------|------|
| 1 | Message 结构体升级 | adapter.go，所有引用处适配 |
| 2 | ProClient 实现 | buildBody + parse + reasoning_content |
| 3 | FlashClient 适配 | 适配新 Message 结构体 |
| 4 | orchestrator | 根据 toggle 选 client |
| 5 | config | 两个模型名 |
| 6 | 客户端 toggle + thought | UI |
| 7 | make test | 全绿 |
