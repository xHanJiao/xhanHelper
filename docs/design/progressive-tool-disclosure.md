# 渐进式工具定义透出（Progressive Tool Disclosure）

## 背景与动机

当前主循环 native FC 路径每次请求把全部工具的完整函数 schema 塞进请求体 `tools` 字段（`orchestrator.go:922` `GetNativeFCTools()`），单请求 schema token 数千。方案：初始只给 LLM 一个**工具组目录**（组名 + 描述 + 函数名列表，无参数 schema），LLM 需要某组时调用元工具 `expose_tools.enable(["组名"])` 启用，下一轮请求体即携带该组完整 schema。唯一常驻工具 = `expose_tools`。

## 数据调研结论（MySQL 线上 dump，138 会话 / 4707 条工具调用消息）

1. **工具使用整体稀疏**：47 个会话用到工具，共 3993 轮中仅 366 轮（9%）用工具。91% 的轮次零工具调用——这部分目前白背全量 schema。
2. **窗口级（15min gap）重复使用占比**（窗口内第 2+ 次用同工具 = 窗口级暴露省下的往返）：

   | 工具 | 用工具轮 | 窗口内重复 | 省往返% |
   |---|---|---|---|
   | browser | 121 | 95 | **79%** |
   | document | 118 | 88 | **75%** |
   | search | 13 | 7 | 54% |
   | bookkeeping | 124 | 52 | **42%** |
   | note | 115 | 42 | 37% |

3. **结论：暴露粒度 = 会话窗口**（`SessionStartMsgID` 为 key）。browser/document 窗口内持续使用 → 暴露一次覆盖整窗；bookkeeping 窗口内零散使用 → 每窗重新暴露便宜。跨窗口自然失效，无需清理逻辑。
4. 之前讨论过的 per-run（每次用户消息重置）代价 = 每工具轮多 1 次 LLM 往返（延迟）；窗口级已覆盖 79%/75% 高频场景。

## 核心设计

### 1. 暴露注册表（窗口级）

- **持久化**：`conversation.extra` JSON 新增字段（无需迁移，text 列）：
  ```go
  // model/conversation.go
  type ToolExposure struct {
      SessionStartMsgID string   `json:"session_start_msg_id"`
      Tools             []string `json:"tools"` // 排序，字节稳定
  }
  // ConversationExtra 增加 ToolExposure *ToolExposure `json:"tool_exposure,omitempty"`
  ```
- **加载**（run 开头）：窗口 key = `req.Window.SessionStartMsgID`，若 `IsNewSession` 则 = 本条 userMsg.ID（与 `prepareUserMsg` 同逻辑）。读 extra，key 不匹配 → 空集。
- **持久化**（run 结尾，defer）：读-改-写 `UpdateExtra`（复用加载时读到的 `*ConversationExtra`，只改 ToolExposure 字段，不覆盖 used_tool_ids 等）。暴露集为空则跳过写。
- 旧窗口的暴露记录自然失效（key 不匹配），无清理任务。

### 2. 系统提示词工具目录（复用 promptbuilder 架构）

- `tool/registry.go` 新增 `ToolCatalog() string`：遍历 `sortedVisibleToolNames()`（字节稳定），每个工具输出 `- 组名: caveman 描述` + `函数: f1, f2, ...`（函数名取自 `Functions()`）。跳过 `expose_tools` 与 hidden 工具。
- **caveman 描述集中定义在 `tool/catalog.go` 一个 map**（`map[string]string`，缺省回退 `Description()`），不改动各工具原 `Description()`（prompt-eng 模式、seeder embedding 仍用原文）。
- `promptbuilder/static.go`：native FC 分支的 `nativeFCNoticeProvider` 替换为 `toolCatalogProvider{registry}`（builder 已有 registry），render = 使用说明 + `registry.ToolCatalog()` + 原有"纯文本回复、禁 JSON"notice。plan 模式在 `builder.go` 的 switch 中跳过该 provider（同 tool_definitions 处理）。

### 3. 元工具 expose_tools（唯一常驻）

- 新文件 `tool/expose_tools.go`：注册进主 registry，函数 `enable`，参数 `{groups: []string}`，schema 极小。
- `Execute`：解析 args → 调 `ec.Expose(groups)`（nil → 返回"当前上下文不可用"）→ 结果 JSON 返回 enabled/rejected/available 列表。
- `tool/tool.go`：`ExecuteContext` 新增可选回调字段：
  ```go
  Expose func(groups []string) *ExposeResult // nil = 不可用
  // ExposeResult{Enabled, Rejected, Available []string}
  ```
- orchestrator 在 `executeConcurrently` 里注入回调（closure over `state`），校验：`registry.Has && !IsHidden && !toolExcludes && != "expose_tools"`，合法名写入 `state.exposedTools`。`state` 加 `expMutex sync.Mutex`（并发工具调用安全）。
- `registry.go` 补两个小方法：`IsHidden(name string) bool`、`VisibleToolNames() []string`。

### 4. 请求体工具组装（mkLlmChatReq）

```
tools = GetLLMToolsFiltered(排序后的 state.exposedTools)   // 已暴露组，完整 schema
tools = filterToolDefs(tools, toolExcludes)
tools = append(tools, expose_tools 定义)                  // 唯一常驻
tools = append(tools, buildClientToolDefs(...))           // 客户端工具不变
```
删除 `GetNativeFCTools()` 调用。签名改为传 `state *reactState`（拿 exposedTools）。

## 天然兼容项（零改动）

- plan 模式：现有 `Mode != ConvModePlan` 门禁把全部工具（含 expose_tools）隐藏；目录 provider 被 skip。
- finalRound：无 tools，收尾轮不可能调 expose_tools。
- 执行层：`ExecuteByName` 不变——暴露只是可见性过滤，未暴露工具被调仍能执行。
- 名称扁平化 `tool__func`、client tools、prompt-eng 路径（doubao）全量注入逻辑、subagent 白名单路径均不动。
- prompt-eng 模式下 expose_tools 会出现在 GeneratePrompt 列表里，调用是 no-op（无害）。

## 改动文件清单

| 文件 | 改动 |
|---|---|
| `model/conversation.go` | +ToolExposure 结构 + ConversationExtra 字段 |
| `tool/catalog.go`（新） | caveman 描述 map + `Registry.ToolCatalog()` |
| `tool/expose_tools.go`（新） | 元工具实现 + ExposeResult 类型 |
| `tool/tool.go` | ExecuteContext +Expose 字段 |
| `tool/registry.go` | +IsHidden +VisibleToolNames（3-5 行） |
| `promptbuilder/static.go` | native FC notice → toolCatalogProvider（含 registry） |
| `promptbuilder/builder.go` | plan 模式 switch 加 tool_catalog case |
| `agent/orchestrator.go` | reactState+exposedTools/expMutex/convExtra；load/persist exposure；mkLlmChatReq 改组装；executeConcurrently 注入回调；convToucher 接口 +GetExtra/UpdateExtra |
| `appinit/tools.go` | 注册 expose_tools（1 行） |
| 测试 mock（orchestrator_streaming_test.go） | mockConvRepo +GetExtra/UpdateExtra |
| 新测试 | expose_tools 单测；ToolCatalog 顺序/隐藏/跳过 expose_tools；orchestrator 暴露流（round1 expose → round2 请求体含组 schema）+ 持久化 + 新窗口重置 |

## 目录文案草稿（caveman 风格，待实现时微调）

```
可用工具组（函数完整定义按需加载）。需要某组能力时先调用
expose_tools.enable(["组名"])，一次可多组，启用后下一轮即可直接调用其函数。

- bookkeeping: 记账。加/改/查/删账单，月/分类统计，分类选项。函数: create_bill, update_bill, batch_update_bills, cancel_bill, query_bills, get_bill_by_id, get_monthly_stats, get_category_stats, get_category_options, get_owner_options
- datetime: 时间。今天几号、当前时间。函数: get_current_time
- calculator: 计算。四则运算、括号、sqrt/pow/sin/cos。函数: evaluate, evaluate_batch
- note: 笔记。记录/检索长期追踪信息：事实、观点、事件、状态、关系。函数: record_note, semantic_search, query_notes, count_notes, update_note, delete_note, rollback_note, get_note, get_note_versions, list_topics
- document: 文档。写/查/改/删 Markdown 文档，版本回滚。用户明确要求才用。函数: create_document, get_document, update_document, delete_document, rollback_document, get_document_versions, query_documents, list_documents, list_tags
- agent: 智能体。建/查/改自定义 AI 助手，绑定/解绑会话。函数: create_agent, find_agent, list_agents, update_agent, bind_agent, unbind_agent
- reminder: 提醒。设/查/取消待办。函数: create_reminder, list_reminders, search_reminders, cancel_reminder
- get_summary: 对话摘要。按时间/ID 查过去聊了什么。函数: query_by_time, get_summary
- message_search: 历史消息。按时间/ID 精确查。语义/关键词检索走 unified_search。函数: search_by_time, search_by_range, get_by_ids, count_messages
- unified_search: 统一检索。语义/关键词搜记忆、笔记、摘要、事件、历史消息（history 关键词只搜最近 90 天，无命中放宽到 365 天；更早用 message_search 按时间/ID 查，不支持关键词）。函数: semantic_search, keyword_search
- plan: 执行计划。存/读当前会话步骤清单（todo）。函数: save, get
- task: 子任务。派子 agent 独立干活：联网调研 web_explore、本地文件 file_explore。函数: run
```

expose_tools 自身 schema 描述：`启用工具组。传组名列表（见系统提示词工具目录）。启用后下一轮起其函数即可直接调用。`

## 代价与风险

- **每窗口每新组 +1 轮 LLM 往返**（延迟）。数据已证 browser/doc 79%/75% 窗口内重复，平均代价远低于 per-run。
- 模型可能滥用 expose_tools 一次开多组 → 目录说明已约束；即使开多组也只是回到接近现状的 token 水平，不劣化。
- tools 数组随暴露增长变化 → 前缀缓存失效，固有代价。
- conversation.extra 读-改-写有并发覆盖风险（同会话并发请求，现有 used_tool_ids 同款模式），可接受。
- 持久化失败 → 日志 + 降级为 per-run，不阻断。

## 验收

- `go build ./...` + `go test ./...` 全绿；新增暴露流测试。
- 真机验证：新窗口首轮只见 expose_tools → 说"记一笔账" → LLM 先 expose bookkeeping 再 create_bill；同窗口第二条"再记一笔"不重复 expose；15min 后新窗口重新 expose。日志无 `[post_chat_group]` 等报错。
- 回归：plan 模式只可见服务端只读函数 + 客户端 fs 只读三函数（glob/grep/read）；finalRound 无工具；客户端工具照常。

## 不做（YAGNI）

- 函数级细粒度暴露（组级够用）
- 暴露集 LRU/过期清理（窗口 key 自然失效）
- doubao prompt-eng 路径的渐进透出（仍全量注入）
- 配置开关（toolExcludes 已可兜底隐藏工具）
