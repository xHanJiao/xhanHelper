# 计划模式只读化（Plan Mode Read-Only）

## 背景与现状

计划模式（会话级 `Conversation.Mode = "plan"`）当前是**纯讨论状态**：所有工具一律禁用，不区分读/写。

2026-08-14 做了"激进收紧"，代码注释散落多处：

| 层 | 位置 | 现状 |
|---|---|---|
| prompt-eng 工具定义 | `promptbuilder/builder.go:82` | plan 时整段跳过 `tool_definitions` |
| native FC 工具目录 | `builder.go:84` | 替换为 `planModeNoToolsNotice`（`static.go:71`） |
| native FC 请求体 tools | `orchestrator.go:1049` | plan 时 `tools` 为空 |
| native FC exposure | `orchestrator.go:354,546,578` | plan 时目录/加载/持久化全关 |
| 服务端工具执行 | `tool/registry.go:527` | 一律拦截，不看 `ReadOnlyFuncs` |
| 客户端工具执行 | `orchestrator.go:1795` | 一律拦截，不看 `IsReadOnly` |
| 每轮提示 | `controller/chat.go:39` | `planModeNotice` 告知 LLM"没有任何工具" |

**关键：只读分类基础设施仍在，只是被主动停用。**

- `Tool.ReadOnlyFuncs()`（`tool/tool.go:65`）+ 启动校验 `ValidateReadOnlyFuncs()`（`registry.go:31`，函数名拼错直接 panic）。
- 各内置工具声明齐全且与 `Functions()` 一致。
- 客户端只读索引 `clientToolReadOnlyIndex()`（`client_tools.go:283`）已构建并传入 `executeConcurrently`，但函数体内未使用。
- 客户端上报 `is_read_only`（Swift `LocalToolRegistry.swift:132`）。

因此本设计不是新建机制，而是把现成的只读标记重新接回拦截判断与 prompt 注入。

## 目标

计划模式 = **读放行、写拦截**：允许 LLM 检索/查询用户数据支撑方案讨论，任何产生副作用的操作仍被硬拦截。范围含**服务端内置只读工具** + **客户端本地文件只读工具**（fs 的 `glob`/`grep`/`read`）；客户端其它工具（shell/browser/chart）仍全拦。

## 放行清单（服务端只读函数）

以各工具 `ReadOnlyFuncs()` 声明为准：

| 工具 | 放行函数 |
|---|---|
| unified_search | 全部 |
| message_search / theme_search / get_summary | 全部 |
| get_topic_stats / get_topic_detail / get_bill_stats | 全部 |
| note | semantic_search / get_note / query_notes / count_notes / list_topics / get_note_versions |
| document | get_document / query_documents / list_documents / get_document_versions / list_tags |
| bookkeeping | query_bills / get_bill_by_id / get_monthly_stats / get_category_stats / get_category_options / get_owner_options |
| agent | find_agent / list_agents |
| plan | get |
| datetime / calculator | 全部 |

**不随只读声明放行的特例：**

- `task`（子智能体入口）：虽声明 `AllReadOnly`，但会启动 subAgent，subAgent 可能带浏览器等写工具 → **plan 模式必须拦截**。做法：把 `TaskTool.ReadOnlyFuncs()` 改为返回 `nil`（nil = 全写 = plan 全拦），语义上也更准确（task 触发执行，非只读）。
- `expose_tools`：`ReadOnlyFuncs()` 返回 nil，自然被拦，且 plan 模式不注入渐进暴露机制。

**客户端本地工具**（浏览器/文件系统/画图）：2026-10-31 起只放行 **fs 的 `glob`/`grep`/`read`**（服务端 `planModeClientToolFuncs` 显式白名单，不读客户端 `is_read_only`）。原因：`BrowserToolProvider` 当前把包含 `click/fill/type/press/drag` 在内的几乎所有函数标为 `isReadOnly: true`（历史特例），按标记放行等于计划模式可操作浏览器。其余客户端工具（shell/browser/chart/MCP）维持全拦。

## 改造点

### 1. 执行层（`tool/registry.go` `ExecuteByName`）

plan 时改为按函数粒度判断：

```go
if ec.Mode == model.ConvModePlan {
    if !t.ReadOnlyFuncs()[funcName] {
        return blocked(...) // 文案：本模式仅允许只读操作
    }
}
```

- `ReadOnlyFuncs()` 返回 nil / 空 map 的工具 → 全拦（含 `task`、`expose_tools`、`theme`、`mailbox`）。
- 空 Mode（旧调用方）仍按 work 处理，不误拦。

### 2. 请求层（`agent/orchestrator.go`）

- 新增 `Registry.ReadOnlyToolDefinitions()`：按 `ReadOnlyFuncs()` 过滤出只读函数的 `[]llm.ToolDefinition`，跳过 hidden / `expose_tools` / 无只读函数的工具，字节稳定排序。
- `mkLlmChatReq`（`:1049`）：plan 分支不再留空 `tools`，改为注入只读定义，并同样走 `filterToolDefs(tools, o.toolExcludes)`。plan 模式不注入 `expose_tools`。
- exposure 机制（`:546/:578`）继续在 plan 关闭（只读定义直接下发，无需渐进暴露）。

### 3. Prompt 层（`promptbuilder/`）

- prompt-eng：新增 `Registry.GenerateReadOnlyPrompt()`，plan 时注入只读函数定义（`builder.go:82` 从 `continue` 改为渲染只读定义）。
- native FC：`builder.go:84` 的 `planModeNoToolsNotice` 改为只读短提示（函数定义已随请求体下发，目录段无需再列函数名）。
- `static.go`：更新提示文案常量。

### 4. 文案（`controller/chat.go`）

`planModeNotice` 改为"仅提供只读工具（检索/查询），写操作被禁用"。**必须保留首句前缀 `当前处于计划模式（只读讨论状态）`**——`planNoticeMarker` 靠它做退出提示检测（`chat.go:548`）。

拦截错误文案同步更新（`registry.go`、`orchestrator.go` 客户端拦截处）。

### 5. 客户端展示

Swift `ModePill` tooltip 已写"计划模式 · 只读"，无需功能改动（可选：图标 `eye.slash` → `eye`）。

## 影响与风险

- **前缀缓存**：mode 是会话级、稳定的，切换 mode 本就改变 system prompt；`tools` 数组随 mode 变化不影响同一 mode 内的缓存前缀。
- **只读声明正确性**：依赖 `ValidateReadOnlyFuncs()` 的启动校验（函数名存在性），语义正确性由各工具自行负责；本设计不改动既有声明内容，只新增 `task` 声明收紧。
- **客户端工具**：计划模式仅放行 fs 的 `glob`/`grep`/`read`（白名单在服务端，客户端离线时无定义）；shell/browser 等仍不可用。
- **回滚**：改动集中在拦截判断与 prompt 分支，回滚 = 恢复 plan 全拦分支即可。

## 测试清单

- `tool/registry_plan_mode_test.go`：
  - `PlanModeBlocksRead` → `PlanModeAllowsRead`（只读函数放行）；
  - 保留写函数拦截、`ReadOnlyFuncs=nil` 全拦、work 全放行、空 Mode 兼容；
  - 新增 `task` 在 plan 被拦的用例。
- 新增 `Registry.ReadOnlyToolDefinitions()` / `GenerateReadOnlyPrompt()` 单测：只含只读函数、不含 `task` / `expose_tools` / 写函数。
- `promptbuilder/builder_test.go`：plan 断言改为"含只读工具定义、不含写工具定义"（prompt-eng + native FC 两条路径）。
- `agent/client_tools_test.go`（2026-10-31）：plan 放行 `fs.glob/grep/read`，拦截 `fs.write/edit`、`shell.run`、`browser.*`；plan 请求体只下发 fs 只读三函数。
- 验收：影响主链路 → `go build ./...` + `go test ./...` 全绿（`make test` 已暂时停用，见 `server/Makefile`）。

## 后续变更

- 2026-10-31：计划模式放行客户端 **fs 的 `glob`/`grep`/`read`**，让方案讨论可以直接读本地文件。白名单在服务端 `planModeClientToolFuncs`（不依赖客户端 `is_read_only`），plan 分支直接把这三个函数定义下发到请求体 `tools`；shell/browser/chart/MCP 与 `task` 仍全拦。影响文件：`server/internal/agent/client_tools.go`、`server/internal/agent/orchestrator.go`、`server/internal/controller/chat.go`。
