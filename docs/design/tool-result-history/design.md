# 工具结果的历史回放策略（读时 stub，不落残片）

- **状态**：已实现（2026-10-30，`agent/history_stub.go`）
- **关联**：`docs/bugs/042-tool-result-no-persist-budget.md`（问题记录）、`docs/design/html-artifact/fine-grained-edit.md`（为什么 html 的「重读」已经不贵）
- **一句话**：工具结果全文照常落库；只在「拼历史」和「下发客户端」两个**读取点**，把「会被截坏」的结果换成明确的 stub。当前轮永远拿到全文。

估算（开工）：未做（问题来自代码审查，不是立项功能）
估算（定稿）：3 面 / 1 缝（0 需新建探针）/ 1 次真机往返 / 0 个待拍决定 · ~0.3–0.7h

---

## 一、问题

### 1.1 现状：三条路只有主循环是「全放开」

| 路径 | 当前轮注入 | 落库 | 历史回放 | 下发客户端 |
|---|---|---|---|---|
| 客户端工具 | ≤20KB（分页） | ≤20KB | 20000 字符截断 | ≤20KB |
| 子 agent（MiniReAct） | ≤2k/6k/20k 字节 | 同左 | 20000 字符截断 | 同左 |
| **主循环服务端工具** | **全文** | **全文** | **20000 字符头尾残片** | **全文** |

对应代码：

- 落库：`orchestrator.go:1746 collectToolResultsNative` → `saveToolMsg`（`:1952`），原样写 `messages.content`（longtext）。
- 回放：`orchestrator.go:2087 BuildMessagesForFlash` → `truncateToolResult`（`:2099`/`:2106`）→ `truncateStringKeepHeadTail`（`mini_react.go:482`），**头 70% + 尾 20%**。
- 下发：`conversations.go:413 cleanMessages` → `injectToolResult`（`:450`），**无任何截断**。
- 客户端工具预算：`client_tools.go:52`（20000 字节）；子 agent：`mini_react.go:382`。

### 1.2 两类失效

1. **残片**：结果超过 20000 字符后，回放拿到的是「头 70% + 尾 20%」。对 JSON 是结构断裂，对 HTML 是半截标签——**内容已损坏，模型却以为拿到了全部**。
2. **常驻废数据**：可重建、且会过期的结果（`html.get` 的整页）在之后每一轮都被重放。它既占窗口，又可能已经过期（`patch` 之后）。

### 1.3 为什么不能「直接不提供」

Native FC 要求每个 `tool_call` 都有对应的 `role=tool` 回复。真删掉的话，`sanitizeOrphanToolCalls`（`orchestrator.go:2199`）会把那条 `tool_call` 整个剔除——等于抹掉「我调过这个工具」这件事。

所以「不给」的正确形态是**一条短的、诚实的 stub 消息**，而不是缺失或残片。

---

## 二、目标 / 非目标

**目标**

- 历史回放里**不再出现被截断的残片**：要么完整，要么明确说「已省略」。
- `html.get(view="full")` 的整页不再跨轮常驻（它会过期，且 `patch` 需要新鲜 hash）。
- 客户端不再为工具结果下载整页 HTML。
- 改动只发生在读取侧，不动写入侧、不动表结构。

**非目标**

- 不给 `message_search` / `document.get` / `list_annotations` 等逐个声明「可重建」——它们只在超预算时走通用兜底。
- 不迁移老数据（新规则按形状/大小自动生效，老行不需要脚本）。
- 不给 `tool.Result` 加字段，不改 `saveToolMsg`。
- 不做 per-turn 总预算（另一个问题）。
- 不改客户端、不改客户端工具/子 agent 的现有预算。
- 不改 SSE 的实时 `tool_result` 事件（当轮下发，不落历史；客户端用它渲染工具 pill）。

---

## 三、方案

### 3.1 决策表

| | 结果 < 预算（未损坏） | 结果 > 预算（会损坏） |
|---|---|---|
| **可重建**（`html.get`、检索类…） | 保留全文；`html.get(full)` 例外 → stub | **stub**（不给残片） |
| **不可重建**（`task.run` 子 agent 成果） | 保留全文 | **stub**（宁可说没有，也不给半截；重跑由模型/用户决定） |

> `task.run` 的正文受子 agent 的固定结构约束（summary 3–5 句、points ≤50 字），实际很少超预算；真超了就只能重跑或问用户，这比给半截报告更不容易误导。

这张表只管**模型的历史回放**。**下发客户端不按大小截断**（见 §3.2 的下半段）：两个客户端都不渲染 `tool_calls_json.result`，按字符数截断只是把没人看的数据切在半截上。

### 3.2 判定规则（回放与下发各一套）

**回放（模型输入）**

```
if tool == "html__get" && 结果 JSON 的 data 里有 "html" 字段:
        → html stub                    // 不看大小：副本会过期
else if runeLen(content) > budget:
        → 通用 stub                    // 不再头尾拼接
else:
        → 原样
```

预算 = `llm.max_tool_result_chars`（20000 字符，`config.yaml:42`），与现有回放口径一致。

**下发（客户端 UI）**

```
if tool == "html__get" && 结果 JSON 的 data 里有 "html" 字段:
        → html stub                    // 整页 HTML 几十上百 KB，客户端也用不上
else:
        → 原样
```

**不按大小截断**。理由：Web（`MessageBubble` 只渲染 `role=tool` 的 `content`）与 macOS/iOS（`Message.toolCallsJSON` 没有视图读）**都不渲染** `tool_calls_json.result`——按字符数截断只是把没人看的数据切在半截上，还多一套文案。

将来真做「查看工具结果」面板时再定下发预算（那时才需要一个数字，且应 ≥8000：`html.get(outline)` 加 `class` 之后 20 块就有 1.8–2.4KB，2000 会切在半截 JSON 上），并在面板里明示「已截断，完整内容在服务端」。

### 3.3 stub 形状

`html.get(full)`：

```json
{
  "artifact_id": "a1",
  "title": "10.18 手账",
  "summary": "一天的时间线手账",
  "html_omitted": true,
  "page_bytes": 19200,
  "hint": "整页正文未带入历史（只是历史里不带，重新读就有）。改文字/换图/加删块先 html.get(view=\"outline\") 拿 nid/hash 再 html.patch（ops / insert / remove）；只有重排版式才用 view=\"full\""
}
```

通用：

```json
{
  "omitted": true,
  "tool": "message_search__search_by_range",
  "bytes": 88000,
  "hint": "结果过大，历史中不再携带；需要请重新调用 message_search__search_by_range"
}
```

下发客户端只可能出现 html stub（其余原样），hint 用同一句即可；不必再造「客户端只保留摘要」的文案——本来就不截断。

### 3.4 为什么 `html.get` 不看大小

- `html.patch` 需要**新鲜的 `hash`**。历史里那份 `get(full)` 在第一次 `patch` 之后就过期；模型拿旧副本整页重写会**静默回滚**中间的修改。
- 即使它只有 5KB，留着也不省事：常规改版走 `get(outline)`（1–3KB）+ `patch`，那份整页副本本来就用不上。
- 这是**正确性**理由，不是省 token 的理由。

`get(outline)` / `get(block)` / `get(style)` 没有顶层 `html` 字段，不受影响，按大小走通用规则。

### 3.5 为什么保留全文落库

- 用户决定：DB 保留完整结果（调试 / 审计 / 未来检索）。
- 代价：`html.get(full)` 每次最多 128KB 的 `messages.content`；打开会话时 DB 仍要读出来（即使下发被 stub）。
- 收益：读取侧规则可以随时调整，不用回填历史数据；`replay` / debug 接口仍能拿到原文。

---

## 四、实现位置

### 4.1 新增 helper（`server/internal/agent/`）

```go
// StubToolResultForHistory 把「不该原样进历史」的工具结果换成 stub。
// 返回 (内容, 是否替换)。toolName 形如 "html__get"；未知时传 ""。
// maxChars <= 0 表示不按大小兜底（下发就是这么用的）。
func StubToolResultForHistory(content, toolName string, maxChars int) (string, bool)
```

- `html` 检测：`json.Unmarshal` 到 `map[string]any`，看 `data.html` 且值必须是字符串（工具结果落库是 `tool.Result` 信封 `{success, data}`；摊平到顶层的形状也兼容）。`null` / 对象不算命中，落到大小兜底。不解析正文结构。
- 通用检测：`utf8.RuneCountInString(content) > maxChars`（与现有回放口径一致，按字符）；`maxChars <= 0` 时跳过（下发不截断，见 §3.2）。
- 生成 stub 用 `map[string]any` + `json.Marshal`；失败则退回原文（不能因为 stub 生成失败把整条消息弄丢）。
- 回放/下发共用同一个 helper：下发传 `maxChars=0` 即「只换 `html.get(full)`，不按大小兜底」，不用再加 `forDownlink` 开关。

放在 `agent` 包：`controller` 已经 import `agent`，不需要新依赖。

### 4.2 回放（`BuildMessagesForFlash`）

签名加一个开关：

```go
func BuildMessagesForFlash(history []model.Message, maxToolResultChars int, useNativeFC, stubForHistory bool) []llm.Message
```

- 遍历时维护 `toolCallID → function.name`：从 assistant 的 `ToolCallsJSON` 解析（持久化形状是 `[{id, function:{name}}]`，`llm/adapter.go:54`）。
- `role=tool` 分支先调 helper；被替换就跳过 `truncateToolResult`。
- 映射查不到（孤儿行 / 老数据）→ `toolName=""`，只按大小兜底。

调用点：

| 位置 | `stubForHistory` | 理由 |
|---|---|---|
| `controller/chat.go:294` | `true` | 正常聊天历史 |
| `controller/replay_handler.go:114/186` | `false` | 重跑对比要保真 |
| `agent/replay/runner.go:112/183` | `false` | 同上 |

### 4.3 下发（`injectToolResult`）

- `cleanMessages` 签名**不动**（没有预算参数）。
- `injectToolResult` 在写 `tcs[i]["result"]` 之前，取第 i 个 call 的 `function.name`（与 `tcs` 下标对齐），调 helper（`maxChars=0` 表示「不按大小截断」）。
  - 只有 `html.get(full)` 会被换 stub，其余原样下发（与今天的行为一致）。
- 现有配对逻辑（第一个还没有 `result` 的 call）不动。

### 4.4 文件影响

| 文件 | 改动 |
|---|---|
| `server/internal/agent/history_stub.go`（新） | helper + 两个 stub 形状 |
| `server/internal/agent/history_stub_test.go`（新） | 表驱动 6 条 |
| `server/internal/agent/orchestrator.go` | `BuildMessagesForFlash` 签名 + 工具名映射 |
| `server/internal/controller/conversations.go` | `injectToolResult` 取工具名（`cleanMessages` 签名不动） |
| `controller/chat.go`、`controller/replay_handler.go`、`agent/replay/runner.go` | 5 个调用点补参数 |
| `docs/bugs/042-…md` | 顶部加本文链接；按 view 体系修正过时描述 |
| 客户端 | 不改 |

---

## 五、边界与失败模式

| 场景 | 行为 |
|---|---|
| 老会话（修复前落库的 `html.get` 行） | 形状相同（顶层 `html`），自动命中 stub；超大的老结果自动变通用 stub，不需要迁移 |
| `image.list_images` 的 `html` 字段 | 在 `data.items[]` 里，不是 `data.html` → 不误判 |
| 一次 assistant 带多个 tool_calls | 按 `tool_call_id` 映射，不依赖顺序 |
| `mergeConsecutiveAssistant` 合并后 | `tool_calls_json` 数组被拼接，映射仍完整 |
| 孤儿 tool 行（并发错位） | 查不到名字 → 只按大小兜底；stub 仍是合法 JSON，不会让请求非法 |
| `useNativeFC=false`（prompt engineering） | stub 同样生效，包装成 user 消息 |
| replay 实验 / debug 重跑 | 传 `false`，保持全文 |
| `MessageDebugView` / `ReplayCompareView` | 若走 `cleanMessages`，工具结果原样（只有 `html.get(full)` 是 stub）；DB 与 replay 接口仍有全文 |
| 模型下一轮只读地问「那页写了啥」 | 多一次 `get(outline)`（1–3KB），可接受 |

---

## 六、验收

**单测（表驱动）**

1. `html__get` full 5KB（**小于**预算）→ stub（证明按工具、不按大小）
2. `html__get` outline 2KB → 原样
3. `message_search__search_by_range` 30KB → 通用 stub，含工具名
4. 小结果 → 原样
5. `stubForHistory=false` → 全文
6. `injectToolResult`：html full → html stub；普通 30KB → **原样**（下发不截断）

**手工（1 次真机往返）**

1. 连改两版：第二轮的请求体里 `html.get` 结果是 stub（看服务端日志 / `debug_persist_json`）
2. 打开会话：`GET /messages` 与 `/messages-v2` 响应里 `tool_calls_json.result` 不含整页 HTML
3. replay 实验仍能拿到全文；老会话加载不报错

---

## 七、风险

- **工具名映射依赖 assistant 行**：并发写错位是既有问题，最坏是误 stub / 漏 stub——不会让请求非法，但可能让模型以为没读过。
- **模型误读 stub**：hint 必须写清「当轮读过、要改先 get」，否则它会以为页面被删了。
- **下发不截断**：整页 HTML 只在 `html.get(full)` 这一种结果上被换 stub；其他大结果（`document.get`、`search`）照旧原样下发——与今天一致，不是回归，但打开长会话的流量仍由它们决定，将来做结果预览面板时一并处理。
- **存储成本保留**：128KB × 次数的 `messages.content`；打开会话仍会读出。
- **单位仍不统一**：回放按字符（20000），客户端预算按字节（20000）。中文 2 万字 ≈ 60KB 的灰区还在（`document.get_document` 是典型），本次不处理。

---

## 八、工作量

- 面：**3**（helper + 回放接线；下发接线；测试）
- 缝：**1**（模型看到 stub 后是否按预期重新 get——只能真机看）
- 真机往返：**1 次**
- 待拍决定：**0**（stub 范围 / 是否保留全文 / 老数据，三项已定）

---

## 九、后续变更

- **2026-10-30 · 初版定稿**
  - 决定：**读时 stub**，全文照常落库（原 042 的「写入 `PersistStub`」作废）。
  - 决定：stub 范围只做 `html.get(full)` + 「超预算通用兜底」，不给检索类工具逐个声明可重建。
  - 决定：老数据不迁移；规则按形状/大小自动生效。
  - 决定：`html.get(full)` 不看大小一律 stub，理由是 `patch` 的 hash 会让旧副本过期。
  - 决定：replay 实验走 `stubForHistory=false`，保真优先。
- **2026-10-30 · 下发不按大小截断**
  - 决定：下发侧只对 `html.get(full)` 换 stub，其余原样；取消原定的 2000 字符下发预算。
  - 理由：Web 与 macOS/iOS 都不渲染 `tool_calls_json.result`，截断只是把没人看的数据切一半、还多一套文案；2000 也比 `get(outline)` 加 `class` 后的体积（20 块 ≈ 1.8–2.4KB）小，真做预览面板时反而会切坏 JSON。
  - 将来做「查看工具结果」面板时再定预算（建议 ≥8000），并在面板明示「已截断，完整内容在服务端」。
  - 影响：§3.1 表格只适用于回放；§3.2 拆成两套规则；§4.3 `cleanMessages` 签名不动；§六/§七 对应条目。
- **2026-10-30 · 实现**
  - 落在 `server/internal/agent/history_stub.go`（helper + 两种 stub）+ `orchestrator.go`（`BuildMessagesForFlash` 加 `stubForHistory` 与 `tool_call_id → 函数名` 映射）+ `controller/conversations.go`（下发只换 `get(full)`）。
  - 修正：检测点不是顶层 `html`，而是 `tool.Result` 信封里的 `data.html`（`collectToolResultsNative` 落库的就是 `{success,data}`），顶层形状保留兼容。
  - 修正：去掉 helper 的 `forDownlink` 参数——下发只是传 `maxChars=0`，hint 用同一句，没有第二套文案。
  - 调用点：`chat.go` 传 `true`；`replay_handler.go` ×2、`replay/runner.go` ×2 传 `false`（重跑保真）。
- **2026-10-30 · 复审收尾**
  - 修：`injectToolResult` 为拿函数名把同一串 JSON 解析两遍（第二次失败还会静默跳过 stub）→ 直接从已解析的 `tcs[i].function.name` 取。
  - 修：`data.html` 只看键存在会在 `null`/对象上产出 `page_bytes: 0` 的假 stub → 改类型断言。
  - 修：html stub 的 marshal 失败改为退回**原文**（原实现落到 `truncateToolResult`，等于又变回头尾残片，与 §4.1 不符）。
  - 补：多条 `tool_calls` 乱序（按 id 映射）、映射缺失的超大 `html.get`（走通用 stub）两条用例。
  - 影响：§4.1 检测口径、§4.3。
- **2026-10-30 · hint 去掉误导**
  - 改：html stub 的 hint 原写「（当轮已完整读过）」，模型在后续轮看到时会误以为「这一轮已经读过、现在拿不到」，改成「只是历史里不带，重新读就有」。
  - 澄清：stub 只作用于**请求开始时拼的历史**；同一轮里 `create` 之后调用 `get(full)` 拿到的仍是全文（该结果直接进当前 msgs，不经过 stub）。
