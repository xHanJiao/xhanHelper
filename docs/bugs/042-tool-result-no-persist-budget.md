# 工具结果没有「落库 / 下发」预算（`html.get` 128KB 全量进 DB、进历史、下客户端）

- **状态**：已修（2026-10-30，读时 stub）；**方案见 `docs/design/tool-result-history/design.md`**（落库仍保留全文）
- **发现于**：HTML 产物「改版闭环」第四轮代码审查
- **性质**：通用缺陷（服务端工具整体缺这条预算），不是 `html.get` 独有；`html.get` 只是第一个把它顶到 128KB 的工具

## 一、问题表现

`html.get` 整页读回上限 128KB（`server/internal/tool/html_artifact.go:36`）；默认 `view="outline"` 只有 1–3KB，**只有 `view="full"` 才整页**（典型页 4.8–19.1KB，见 `docs/design/html-artifact/fine-grained-edit.md:25`）。这份整页 HTML 会被完整地走三条路：

1. **整份落库**：`agent/orchestrator.go:1741 collectToolResultsNative` → `saveToolMsg`（`:1947`）把 `string(resJSON)` 原样写进 `messages.content`（`longtext`，`model/message.go:21`）。
2. **之后每轮按 20000 字符重放**：`BuildMessagesForFlash`（`:2082`，`:2094`/`:2101`）对历史 `role=tool` 跑 `truncateToolResult` → `truncateStringKeepHeadTail`（`mini_react.go:482`），**保留头 70% + 尾 20%**，中间塞一行「…[已截断 N 字符]…」。
3. **整份下发客户端**：`controller/conversations.go:413 cleanMessages` → `injectToolResult`（`:450`）把结果塞进上一条 assistant 的 `tool_calls_json`，**无任何截断**；客户端加载会话后第一件事是 `withoutToolCalls()` 把它丢掉（`ChatViewModel.swift:825/878/889`）。

结果：

- 会话里每改一版，DB 多 20–30KB（最坏 128KB）；
- 之后每一轮请求都多出约 20000 字符（≈7k token）的**历史 HTML 残片**，改五版 ≈ 35k token 常驻上下文，会挤掉真实对话；
- 每次打开该会话，客户端多下载 N × 20–30KB 的正文，**收到即丢**。

## 二、根因

服务端工具结果链路上**没有「持久化预算」这一环**，只有「历史回放时的截断」，而那条截断本来就不是预算：

- `server/conf/config.yaml:42` 写明 `max_tool_result_chars: 20000` 是「**仅作用于历史重放，当前轮不截**……只兜住病态超大结果」。所以它是兜底护栏，不是常态预算。
- 落库、下发这两处**没有任何上限**：工具返回多大就存多大、发多大。

对比一下：**客户端工具**早就做了这件事。`agent/client_tools.go:40-52` 有一条成文的预算链：

```
DB 硬底线:  messages.content TEXT（MySQL 64KB 字节）
  ↓ 留 3x 余量（JSON 转义膨胀 + 多字节字符 + 消息信封）
注入/落库预算: clientToolInjectBudgetBytes = 20000 字节
  ↓ 随 client_tool_call 事件下发（result_budget_bytes 字段）
客户端分页:  每页 ≤ 预算，超大结果走 result_read_more 续读
  ↓
HTTP 上限:  controller.maxClientToolResultBytes = 256KB（chat.go:398）
```

实现见 `normalizeClientToolData`（`client_tools.go:335`）。也就是说：**同一条管道，客户端工具有预算，服务端工具没有** —— 这才是本 bug 的根因，`html.get` 只是把缺口撑到了肉眼可见。

## 三、影响面（哪些工具已经在踩）

| 工具 | 单次结果上限 | 现状 |
|---|---|---|
| `html.get` | **128KB**（`html_artifact.go:33`） | 新引入，最严重 |
| `document` 读 | 20000 字符（`document.go:27-29`，`maxDocWindowLen`） | 恰好等于回放阈值：落库/下发仍偏大，但不会产生「头尾残片」 |
| `web.fetch` | 8000 字符（`config.yaml:137`） | 安全 |
| `web.search` | 总计 16000 字符（`config.yaml:138`） | 安全 |
| `html.list` | 10 条 × 一行 | 安全 |
| 客户端工具（fs/browser/MCP…） | 20000 字节注入预算 + 256KB HTTP 兜底 | **已有预算，无需改** |

结论：**当前只有 `html.get` 越界**，但 `document` 读已经贴着阈值，任何新工具只要返回「一次几万字」就会重演。所以修的时候应当修**管道**，而不是只给 `html.get` 打补丁。

## 四、为什么必须修（不只是性能）

1. **残片会诱导坏改版**：下一轮模型看到的是「头 70% + 已截断 + 尾 20%」，它很可能当成整页照着重写，产出结构错乱的页面。工具描述里虽然写了「下一轮要再改就重新 `get`，别凭残片改」，但这是靠提示词自觉，不是机制保证。
2. **持久化的那份按设计就是废纸**：设计文档已写明「读回的内容只在当前这一轮算数」——既然下一轮必定重读，把全文存下来只有成本没有收益。
3. **违反设计文档自己定的硬规则**：`docs/design/html-artifact/design.md` §4.1「消息列表查询必须 `Omit("html")`……一旦列表把正文带出来，每次打开会话就是 N 条 × 几十 KB 的持续性开销。这是本方案最容易被写错、且后果最严重的一处」。`html.get` 没走列表接口，却从 `tool_calls_json` 这个后门把正文送了回去。
4. **症状难归因**：不会当场报错，表现为「连着改几版之后模型开始忘事、切会话变慢」，几乎不可能想到是工具结果撑爆了上下文。

## 五、建议方案（待拍板，二选一或组合）

> **已被定稿取代**：最终选择「读时 stub、落库保留全文」，见 `docs/design/tool-result-history/design.md`。本节保留为当时的候选记录。

### 方案 A（管道级，推荐）：给服务端工具加「落库/注入预算」

在 `collectToolResultsNative` 里统一处理（客户端工具已有的思路搬过来）：

- 当前轮 append 给模型的 `llm.Message` 用**全文**（改版必须看到全文，不能截）；
- `saveToolMsg` 落库、以及 `cleanMessages` 下发用的那份，超预算就换成**摘要/占位**。

预算建议与 `clientToolInjectBudgetBytes` 同源（20000 字节），顺手把 `max_tool_result_chars` 也并进来做单一来源，别再维护两个魔数。

### 方案 B（最小）：`tool.Result` 加 `PersistStub`

- `tool.Result`（`tool/tool.go:38`）加可选字段 `PersistStub string`；
- `collectToolResultsNative`：`msgs` 仍用完整 `resJSON`，`saveToolMsg` 在 stub 非空时改存 stub；
- `html.get` 成功时给一句人话占位，例如
  `{"artifact_id":…,"title":…,"summary":…,"html_omitted":"正文只在当前轮可用，下一轮请重新 html.get"}`。

好处：下一轮模型看到的是**诚实的「正文没带过来」**，比「头尾残片」更不容易诱发瞎改；成本约 10 行。

**推荐组合**：B 负责 `html.get` 的语义化文案，A 负责兜住所有未来工具（`document` 读已经贴着线）。

**不要**用「把 `maxReadBytes` 压到 20000」来解：典型页面 4.8–19.1KB 就再也读不回来，等于废掉 `get`。

## 六、验收

> 定稿改为「读时 stub」后，落库仍是全文；验收看的是**回放与下发**，不是 `messages.content` 的长度。

- 改一版后**再发一轮**：第二轮请求体里那条 `html.get` 结果是 stub（不是头尾残片）；DB 里 `content` 仍是全文；
- 同一次改版（`get` → `create`/`patch`）在同一轮内正常完成（当前轮仍是全文）；
- 打开该会话，`GET /api/conversations/:id/messages` 与 `messages-v2` 的响应体里**不含整页 HTML**；
- 下一轮让模型「再改一次」，确认它是重新 `get` 而不是拿历史残片改；
- replay 实验（`stubForHistory=false`）仍能拿到全文；
- 回归：客户端工具结果链路（20KB 预算 + `result_read_more` 分页 + 256KB 兜底）行为不变。

## 七、待拍板

> 已全部拍板，见 `docs/design/tool-result-history/design.md` §三：字符口径（20000）、读时 stub、保留全文落库、老数据不迁移。

1. 预算口径：按**字节**（与客户端工具一致，20000 字节）还是按**字符**（与 `max_tool_result_chars` 一致）？
2. 超预算时落库内容是「截断 + note」还是「结构化占位」（方案 B）？前者通用、后者更不容易误导模型。
3. 是否顺手把 `max_tool_result_chars`（回放截断）和落库预算合并成一个配置项，避免两处魔数漂移。
4. `document` 读的 20000 字符要不要一起收到预算内（它现在是「刚好不触发残片」，但落库/下发依旧偏大）。
