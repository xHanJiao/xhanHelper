# SubAgent 跨运行续跑（task_id 恢复机制）

## 零、背景与目标

### 0.1 问题

xhanHelper 子任务（web_explore / file_explore / user_context）当前是一次性执行：

1. 预算写死 `explore_max_iterations: 10`（9 轮工具 + 1 轮强制收敛）、`explore_timeout_seconds: 600`。
2. 预算耗尽 → 强制收敛轮逼出结论；超时 → 交部分成果（工具日志摘要）。但任务本身可能没做完（调研只完成一半、页面被墙换源未完等）。
3. 主 LLM 想继续时只能**再次调用 `task` 新建一个子会话**——全新上下文、重新从第 1 轮开始，之前调研到的一半信息全部丢失，只能把已得结论塞进新 prompt 里重跑，重复劳动且费 token。

### 0.2 目标

- 主 LLM 在一次预算耗尽/超时拿到部分结果后，可携带返回的 `<task id="...">` 再次调用 `task`，**续跑同一子会话**：历史上下文完整保留、步数预算重置。
- 不引入新配置项、新依赖、不改 MiniReAct 循环核心逻辑。
- 复用已落库的子会话消息（`subAgentMsgSink` 本就全量写库），不额外存状态。

### 0.3 opencode 对应关系

| opencode | xhanHelper（本方案） |
|----------|----------------------|
| task 工具可选参数 `task_id`（tool/task.ts:47） | `task` 工具新增可选参数 `task_id`（tool/task.go） |
| `sessions.get(task_id)` 命中则复用，否则静默 `create`（task.ts:136-172） | `ConversationRepo.FindByID(task_id)` 命中且校验通过则复用，否则**报错**（偏离 opencode：静默新建会让误用无声重启，报错更诚实） |
| 每次 `prompt()` 是新的 `runLoop`，`step` 从 0 重置，历史从 DB 重载（prompt.ts:1085, 1092） | 每次续跑是新的 `MiniReAct.Run`，`maxIterations` 重新生效，历史从 DB 加载作为 `InitialMessages` |
| `MAX_STEPS_PROMPT` 强制文本总结，要求列剩余工作与建议（runner/max-steps.ts） | 子 agent 强制收敛轮换用"列已完成/未完成"版 `FinalRoundPrompt` |
| 同一 session 并发运行由 `state.ensureRunning` 串行 | Orchestrator 按子会话 ID 加内存互斥锁 |

---

## 一、设计

### 1.1 task 工具新增 `task_id` 参数

`tool/task.go` `Functions()` 的 schema `properties` 增加：

```json
"task_id": {
  "type": "string",
  "description": "可选。继续之前返回的 <task id='...'> 中的任务 ID，复用同一子会话（保留此前全部调研上下文）继续工作；不传则新建子任务。"
}
```

`Execute` 解析后透传 `SubAgentRunOptions.TaskID`。`Description()` 补充一句：预算耗尽拿到部分结果时，可带 `task_id` 续跑同一子任务。

`tool/subagent.go` 的 `SubAgentRunOptions` 增加字段：

```go
TaskID string // 继续的子会话 ID，空 = 新建子会话
```

主 LLM 天然拿得到 ID：task 工具输出已包裹 `<task id="{ConvID}" state="...">`（task.go:146）。

### 1.2 RunSubAgent 复用逻辑（校验失败 → 报错）

`agent/subagent.go` 步骤 4 改造：

```
TaskID == ""         → 照旧新建子会话
TaskID != ""         → ConversationRepo.FindByID(TaskID)，全部满足才复用：
                         1) 存在且 Status 正常
                         2) UserID == 父会话 UserID          （防跨用户）
                         3) ParentID != nil && *ParentID == opts.ParentConvID （防跨主会话）
                         4) Extra.SubAgentType == opts.SubAgentType          （prompt/白名单一致性）
                     任一项不满足 → 报错返回（提示去掉 task_id 重试），不静默新建
```

报错而非静默新建的原因：静默新建会让"看似续跑实则重启"的误用无声发生（如主 LLM 拿错 id、类型填错），浪费一次全新子任务的 token 且结果不可预期。报错走 task 工具既有 hard-error 路径回主 LLM，主 LLM 会自动去掉 `task_id` 重发新任务。

安全说明：跨用户/跨会话的 `task_id` 报错前**绝不加载他人会话数据**，无泄漏面。

深度检查（步骤 1，写死 ≤1 层）不变：续跑复用子会话不改父链，主会话派子会话仍是 depth 1，合法；子会话派生子会话仍被拒。

### 1.3 历史加载与预算（40k 含义）

新增 `loadSubAgentHistory(ctx, convID) ([]llm.Message, error)`：

1. `MessageRepo.ListByConversationRecent(ctx, convID, N)` 取最近 N 条（按时间正序返回，现成方法）。**分页约定**：走现成 limit 查询，一次取最近 N 条即可（子会话消息量有界）。
2. 逐条转 `llm.Message`：
   - `role=user` → `{Role:"user", Content}`
   - `role=assistant` → `{Role:"assistant", Content, ToolCalls: 反序列化 ToolCallsJSON, ReasoningContent/EncryptedContent 带回}`
   - `role=tool` → `{Role:"tool", Content, ToolCallID}`（原生 FC 回放必须带 ToolCallID 与上一条 assistant 的 tool_calls id 对齐）
   - 仅取 `MsgStatusNormal`（软删除天然排除）
3. **总预算截断**：累加 `content` 的 rune 数，从最旧消息开始丢弃，直到总长 ≤ `subagentHistoryBudgetChars`（常量，默认 **40000**）。

**40k = 40,000 字符（rune）**，即从子会话加载进续跑上下文的历史消息 `content` 总长度上限。依据：每条工具结果写入时已被 `maxToolResult` 截断（web_explore=2000、user_context=6000），单条有界；40k ≈ 可保留最近 1~2 轮完整历史（1 轮 ≈ 9 轮工具 × 2~4k + 若干文本）。超出丢弃最旧，保证续跑上下文在模型窗口内。

```go
// ponytail: 固定 40000 rune 启发式上限；若续跑多轮后历史挤占系统/工具定义空间，
// 升级路径是引入 token 级预算或压缩（对齐 opencode compaction），本期不做。
const subagentHistoryBudgetChars = 40000
```

### 1.4 MiniReAct 支持 InitialMessages

`agent/mini_react.go`：

- `MiniReActConfig` 新增 `InitialMessages []llm.Message`。
- `Run` 的初值改为：

```go
msgs := make([]llm.Message, 0, len(m.initialMessages)+1)
msgs = append(msgs, m.initialMessages...) // 为空则照旧
msgs = append(msgs, llm.Message{Role: "user", Content: userMessage})
```

- 无续跑场景 `InitialMessages` 为空 → 行为完全不变（既有调用方 profile_worker 等零影响）。
- `subAgentMsgSink.SaveUserMsg` 照旧把新 prompt 追加进同一子会话 → 续跑历史自然增长，无需额外处理。

### 1.5 预算语义（对齐 opencode）

每次续跑 = 新的 `MiniReAct.Run`：

- `maxIterations`（10）**重新生效**（opencode 每次 prompt 调用 `step` 从 0 重置的对应物）；
- 600s timeout **重新生效**；
- system prompt 预算文案（"最多执行 N 轮/总时长约 T 秒"）逐轮不变，续跑后依旧准确。

无需改循环逻辑。

### 1.6 并发防串台（新引入风险，必须防）

主循环可并发执行多个 `task`（`executeConcurrently`）。续跑引入同一子会话被并发调用的可能（同一轮两个 task 都带同一 `task_id`）→ 两个 MiniReAct 同时写同一会话，消息乱序。

Orchestrator 增加按子会话 ID 的互斥：`sync.Map[string]*sync.Mutex`，在确定 `subConv.ID` 后（新建与复用统一）`Lock` / `defer Unlock`。后到者等先到者跑完，再加载其历史续跑——语义正确。

```go
// ponytail: 进程内存锁，条目随子会话数增长（量级小）；如需跨进程/实例，
// 升级路径是 DB 行锁或状态机（sub_conversation.status），本期不做。
```

### 1.7 强制收敛轮提示词增强（提升续跑质量）

子 agent 场景注入专用 `FinalRoundPrompt`（`RunSubAgent` 构造 MiniReAct 时传 `FinalRoundPrompt`），替换默认 `genericFinalRoundPrompt`：

```
[系统] 本轮已达任务的最大步数限制。请不要再调用任何工具，直接输出最终结果。
你必须同时列出：
- 已完成的工作
- 尚未完成/需继续的事项（供主任务决定是否带 task_id 续跑）
如信息已充分，正常给出最终回答即可，不必提及内部限制。
```

与 web_explore 输出结构 `<open_questions>` 互补：结论正常收，未完成事项单独列出，主 LLM 据此决定是否续跑。

---

## 二、改动文件清单

| 文件 | 改动 |
|------|------|
| `server/internal/tool/subagent.go` | `SubAgentRunOptions` 加 `TaskID string` |
| `server/internal/tool/task.go` | schema 加 `task_id` 参数；Description 补续跑说明；Execute 透传 |
| `server/internal/agent/subagent.go` | RunSubAgent 复用/校验（失败报错）/新建分支；`loadSubAgentHistory`（含 40k 截断）；构造 MiniReAct 传 `InitialMessages` + 子 agent 专用 `FinalRoundPrompt` |
| `server/internal/agent/mini_react.go` | `MiniReActConfig` 加 `InitialMessages`；Run 初值拼接；子 agent 收敛轮提示词常量 |
| `server/internal/agent/orchestrator.go` | 子会话 ID 互斥锁 map + 执行前加锁 |

无新配置项、无新依赖、无 schema 迁移。

## 三、数据流

```
主循环（第 N 轮）
  └─ task(web_explore, prompt, task_id=?)
       ├─ 无 task_id → 新建子会话 conv-A
       ├─ 有 task_id=conv-A 且校验通过 → 复用 conv-A
       └─ 有 task_id 但校验失败（不存在/越权/类型不符）→ 报错，主 LLM 去掉 task_id 重发新任务
             ├─ 加锁 conv-A
             ├─ loadSubAgentHistory(conv-A)   ← 40k 截断
             ├─ MiniReAct{ InitialMessages=历史, FinalRoundPrompt=增强版 }
             │     └─ 9 轮工具 + 1 轮强制收敛（预算重置）
             ├─ 结果（含 partial）包成 <task id="conv-A"> 回主 LLM
             └─ 解锁
  主 LLM：结果不完整 → 带 task_id=conv-A 再调 task → 续跑……
```

## 四、测试计划

**影响主链路（聊天/工具调用/SSE/数据库写入）→ 完整 `make test` + 新增针对性单测。**

| 测试 | 覆盖 |
|------|------|
| `agent/subagent_test.go: TestRunSubAgent_Resume` | 首跑后带 TaskID 续跑：复用同一 ConvID、不新建会话、LLM 收到历史（首 prompt + 新 prompt）、预算重置（轮次重新从 1 计） |
| `agent/subagent_test.go: TestRunSubAgent_ResumeInvalidTaskID` | task_id 不存在 / 跨用户 / 跨父会话 / 类型不符 → 报错"不可复用"，且不新建子会话 |
| `agent/subagent_test.go: TestRunSubAgent_ResumeHistoryBudget` | 超 40k 时最旧消息被丢弃，加载总长 ≤ 预算 |
| `agent/mini_react_test.go: TestMiniReAct_InitialMessages` | InitialMessages 非空时作为首轮 messages；为空时行为不变 |
| `tool/task_test.go` | task_id 参数解析与透传 |

## 五、验收标准

1. `go build ./...` + `go test ./...` 全绿；`make test` 全绿。
2. 真机跑一次：调 task 调研，让其预算耗尽返回部分结果，再带 `task_id` 续跑——面板显示同一子任务继续、上下文延续（后续结论能引用首批调研内容）。
3. 日志无 `[post_chat_group]` 等报错；子会话消息落库完整（含续跑新增消息）。
4. 既有无续跑路径行为零变化（回归）。
