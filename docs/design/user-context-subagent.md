# user_context 子 Agent：用户信息弹药补充器

## 零、背景与目标

### 0.1 问题

xhanHelper 已积累多层级用户信息：低层对话记录 → 高层记忆、笔记、主题、摘要，外加用户画像稳定层（UserProfileFact，10 字段）。但现有注入体系效果不佳：

1. `unified_search` 覆盖记忆/笔记/摘要/主题/历史五域，但主循环 LLM **多数时候不主动调用**；即便调用，也**只在一轮内使用一次**，缺乏多轮深挖。
2. 主循环三层注入（promptbuilder）每轮注入画像 + 检索上下文，但受 budget 限制，信息不完整。
3. 现有两个 subAgent（`web_explore` 联网、`file_explore` 本地文件）查外部世界，**没有一个专门查用户自身**的子 agent。

### 0.2 方案

新增第三个 subAgent 类型 `user_context`——主循环的**用户信息弹药补充器**：独立上下文 + 多轮深挖 + 结构化汇总，把用户相关信息一次性补足。

与现有两个 subAgent 的本质区别：

| 维度 | web_explore / file_explore | user_context |
|------|---------------------------|--------------|
| 查什么 | 外部世界（网页/本地文件） | 用户自身（画像/记忆/对话/笔记/主题） |
| 数据边界 | **关闭**用户私密数据（子任务不可见） | **全放开**（这是本职） |
| 客户端依赖 | `requiresClient=true`（硬依赖 Mac 客户端） | `requiresClient=false`（纯服务端，Web/客户端都能用） |
| 只读 | 只读 | 只读 |

## 一、触发场景

1. **用户显式要求**：用户说"查我之前的……""结合我上次说的……""我什么时候生日"等涉及自身信息的请求，主 LLM 派 `user_context`。
2. **主动补弹药**：对话开始时，主 LLM 根据当前注入内容判断在聊"过去比较重要的用户信息"且注入不足 → 主动派 `user_context` 深挖。
3. **克制原则**：普通对话、主上下文已能回答、与用户历史无关 → **禁止派**。

触发只靠 prompt 引导（system prompt 注入规则），**无后端硬触发逻辑**。由 `subagent.user_context_enabled` 开关控制是否注入触发规则。

### 1.1 成本风险与兜底

- 纯 prompt 引导、无频率/并发硬约束，task 支持并行 → 若 LLM 误判频繁派发，成本为 Flash 多轮 × 子会话落库 × N 并行。
- **保险丝**：`user_context_enabled` 开关（默认 false）。关闭时既不注入触发规则，主 LLM 仍可手动派 task（描述里含该类型）。
- prompt 规则硬措辞：*"主上下文已能回答 / 与用户历史无关 → 禁止派"*。

## 二、方案设计

### 2.1 新增 `profile` 只读工具（`server/internal/tool/profile.go`）

- 工具名 `profile`，函数 `get`，参数 `fields`（可选 array，空=全部）。
- **`fields` 参数描述动态注入全部字段列表**：`profilefield.ProfileFieldNames()` 拼接（ProfileFields 是 slice，顺序稳定，不破坏前缀缓存）。
- **非法 `fields` 报错**：返回 error 并列出合法字段名（对齐 `ExecuteByName` 模糊纠错风格，LLM 可自纠错）。
- 数据源：`ProfileFactRepo.GetActiveByUser`，按 field 分组返回（field → facts[] + 字段 InjectDesc）。
- `ReadOnlyFuncs` 全只读；`NewProfileTool(factRepo)` 对 nil factRepo panic（接线规范）。
- **注册 + `HideTool("profile")`**：仅 subAgent 可见（webfetch 先例，见 `appinit/tools.go:27`）。画像已通过 resource zone 每轮注入主循环，主循环再查是冗余，且隐藏避免膨胀 catalog。`GetLLMToolsFiltered` 不跳过 hidden 工具，subAgent 路径可用（`registry_hidden_test.go` 已覆盖）。

### 2.2 `subagentSpecs` 加 `user_context` 行（`server/internal/agent/subagent.go`）

```go
"user_context": {
    prompt:          userContextPrompt,
    serverWhitelist: []string{"profile", "unified_search", "datetime", "calculator"},
    maxToolResult:   6000,   // 检索是主力工具，2000 会截断弹药（web_explore 用 2000 因检索只是辅助）
    requiresClient:  false,  // 纯服务端，Web/客户端都能用
}
```

- 无 `clientWhitelist` → `filterClientToolsByFuncWhitelist` 返回 nil，客户端离线/Web 前端不触发"缺本地工具"检查。
- 无 task 工具 → 天然不能套娃（深度限制不触发）。
- `RunSubAgent` 未知类型错误信息补 `user_context`。

### 2.3 `userContextPrompt` 角色 prompt

- 能力：`profile`（画像稳定层，字段列表见工具定义）、`unified_search`（记忆/笔记/摘要/主题/历史五域，语义+关键词）、`datetime`/`calculator` 辅助。
- 工作准则：先画像后历史、多轮深挖、独立检索并行、**数据边界全放开**（本职）、只读。
- **画像为空是有效结论，禁止编造**。
- 输出 `<user_context_result>`（summary / findings 含来源域标注 / open_questions）；明确"输出交主 agent 汇总，不直接面向用户"。
- 篇幅克制：子循环固定 Flash、≤10 轮收敛，引导精炼不堆字。

### 2.4 `task` 工具更新（`server/internal/tool/task.go`）

- `subagent_type` 枚举加 `user_context`（注明纯服务端、无需客户端）。
- `Description()` / `Catalog()` 补 user_context 场景（用户自身信息/历史回忆/画像相关深挖）。

### 2.5 触发指引（独立 provider + config 开关）

- 新建 `userContextTriggerProvider`（**不动** `corePrinciplesProvider`），无 SectionTitle，追加在行为准则后。
- 规则要点：用户问自身信息/回忆历史/画像相关且主上下文注入不足 → 派 `user_context` 深挖后综合回答；主上下文已能回答 / 与用户历史无关 / 普通对话 → 禁止派。
- `config.go`：`SubAgentConfig` 加 `UserContextEnabled bool`（`yaml:"user_context_enabled"`），默认 false；**`merge()` 补 SubAgent 分支**（bool 按 `MemoryAutoExtract` plain assign 模式）。
- `conf/config.yaml` 加 `user_context_enabled: false`。
- `NewPromptBuilder` 加开关参数，builder_test.go 5+ 调用点同步；appinit/agents.go 传入 `cfg.SubAgent.UserContextEnabled`。
- 前端/Swift 零改动（已核实：Swift `SubagentPanel.swift` 无类型硬编码；Web 前端无 subagent 事件处理，只显示 task.run toast）。

## 三、改动清单

### 新增文件

| 文件 | 说明 |
|------|------|
| `server/internal/tool/profile.go` | profile 只读工具 |
| `server/internal/tool/profile_test.go` | 单测 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `server/internal/appinit/tools.go` | 注册 profile + `HideTool("profile")` |
| `server/internal/agent/subagent.go` | `userContextPrompt` + `subagentSpecs["user_context"]` + 未知类型错误信息 |
| `server/internal/tool/task.go` | subagent_type 枚举 + Description/Catalog |
| `server/internal/config/config.go` | SubAgentConfig 加 UserContextEnabled + merge 补 SubAgent 分支 |
| `server/conf/config.yaml` | 加 `user_context_enabled: false` |
| `server/internal/agent/promptbuilder/static.go` | 新增 `userContextTriggerProvider` |
| `server/internal/agent/promptbuilder/builder.go` | NewPromptBuilder 加开关参数 |
| `server/internal/appinit/agents.go` | 传 `cfg.SubAgent.UserContextEnabled` |
| `server/internal/agent/subagent_test.go` | dispatch 表 3 类型 + user_context 用例 |

## 四、测试

| 文件 | 用例 |
|------|------|
| `tool/profile_test.go` | 分组输出、fields 过滤、非法 fields 报错、空数据、nil panic |
| `subagent_test.go` | `TestSubAgentSpecs_DispatchTable` 改 3 类型 + user_context 断言（requiresClient==false、白名单含 profile+unified_search、maxToolResult>2000）；`TestBuildSubAgentSystemPrompt` 加 user_context；新增 `TestRunSubAgent_UserContext_NoClientRequired`（无 ClientTools 成功） |
| `promptbuilder` | 开关开/关时触发规则注入/不注入 |

## 五、验收

- `go build ./...` + `go test ./...` 全绿（轻量级验收：新增工具非主链路改动）。
- 真机聊天验证「查我之前的……」能派 `user_context` 子任务；开关关闭时无触发规则注入。

## 六、明确不做

- 主→子上下文透传（用户决策：子任务独立重查，重新检索更全）。
- 调低主循环现有注入量（保持现状 + 补齐，不互替）。
- 后端管线硬触发（纯 prompt 引导）。
- profile 工具写操作（纯只读；画像写入仍走 ProfileWorker 唯一通道）。
- ask_user / 客户端依赖。
