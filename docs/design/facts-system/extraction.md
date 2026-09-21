# 事实系统 · 抽取段 — 实现设计

> 版本：v2 | 日期：2026-09-21 | 状态：设计定稿，待开发
> 上级文档：[design.md](./design.md)（总览 §5）· 已实现：[storage.md](./storage.md)
> 估算（定稿）：4 面 / 2 缝（0 条探针）/ 1 次真机往返 / 0 个待拍决定 → 3–4 小时

存储层（两张表 + repo + 预热）已经落地。这份文档讲**抽取段**：让 session 快照带上 assistant 消息，从一段真实对话里抽出候选事实，并把它接进现有 `session_end` 管线、让调用落进 `llm_call_io`。

判重、向量检索、决策（ADD/UPDATE/DELETE/NONE）、写入 facts —— **都不在这一刀**，下一刀单独出文档。

---

## 1. 范围

**做**
- 前置改造：`SessionSnapshot` 同时携带 user + assistant 消息，暴露 `GetUserMessages()` / `GetAllMessages()`。
- `FactExtractor`：提示词 + `ChatSync` + 解析 + 校验，产出 `[]factCandidate`。
- `FactWorker` 的**最小版**：开关判断 → 场景标注 → 调抽取 → 记日志/计数。
- 接线：配置总开关、`llm_call_io` 审计、`appinit` 装配。

**不做**
- 不判重（没有归一化、没有 hash）；不检索；不写 facts/changelog；不注入主对话。

**做完的效果**：`facts.enabled` 开着时，每次会话结束都会跑一次抽取，`llm_call_io` 里多一行 `fact_extract`，日志里能看到「抽到 N 条候选」。候选此刻只进日志，不落库。

> 为什么不并进决策段一次做完：抽取的可观测性（提示词好不好、候选质量）需要先单独跑一段真实对话才能判断。先让它能跑、能看，再上决策。

## 2. 前置改造：SessionSnapshot 带上 assistant

mem0 的输入是双向对话。助手那侧的话里虽然没有「可记的事实」，但它承载了**用户确认/纠正的语境**（用户说「对」「不是，是…」），缺了这半边，抽取模型容易误读。所以快照从「只装 user」改成「装 user + assistant」。

### 2.1 repo 层：新增查询

`post_chat_group.go` 现在调 `ListUserMessagesBetween`（只 user）。新增一个方法，**在 SQL 里就排掉 tool**——tool 结果可能是巨型 longtext，捞出来再丢是浪费：

```go
// ListChatMessagesBetween 拉取时间范围内会话的真实对话消息（user + assistant）。
// 排除 tool 消息与内部消息（verify 草稿 role=system、工具结果），按时间正序。
// after / before 为闭区间；limit <= 0 不限量。
func (r *MessageRepo) ListChatMessagesBetween(ctx context.Context, convID string, after, before time.Time, limit int) ([]model.Message, error)
```

SQL 口径：

```sql
conversation_id = ? AND status = 1 AND role IN ('user','assistant')
  AND is_internal = 0 AND created_at >= ? AND created_at <= ?
ORDER BY created_at ASC
```

两个细节：

- **`is_internal = 0` 对 user 是空操作**（user 消息从不置 internal；internal 只有 `role=system` 的 verify 草稿、`role=tool` 的工具结果、`role=assistant` 的草稿），加上是为了 assistant 侧干净。
- **子会话消息不用额外排除**：subAgent 的消息落在 `subConv.ID`（另一个 conversation），`conversation_id = ?` 天然过滤掉，`is_sub_agent` 条件不需要。

`ListUserMessagesBetween` **保留不动**——`internal/tool/message_search.go:353` 还在用。

### 2.2 snapshot：字段收口 + 两个方法

```go
type SessionSnapshot struct {
    ConvID string
    UserID string
    msgs   []model.Message // user + assistant（不含 tool / internal），时间升序
}

// GetUserMessages 返回快照中的 user 消息（记忆 / 摘要 / 状态 / 画像 worker 用）。
func (s *SessionSnapshot) GetUserMessages() []model.Message

// GetAllMessages 返回快照中的全部消息（user + assistant，抽取段用）。
func (s *SessionSnapshot) GetAllMessages() []model.Message
```

字段**故意不导出**：两个方法就是取数的唯一入口，避免有人直接摸 `msgs` 又假设只有 user。`GetUserMessages` 在内存里过滤（快照量小，不必二次查库）。

### 2.3 消费方改口

| 调用点 | 现在 | 改成 |
|---|---|---|
| `summary_worker.go:80,88` | `sess.Messages` | `sess.GetUserMessages()` |
| `memory_worker.go:45` | `sess.Messages` | `sess.GetUserMessages()` |
| `state_worker.go:33`（两处） | `sess.Messages` | `sess.GetUserMessages()` |
| `profile_worker.go:255,593` | `sess.Messages` | `sess.GetUserMessages()` |
| `fact_worker.go`（本刀新增） | — | `sess.GetAllMessages()` |

上表四个 worker 拿到的仍是 user-only，**行为与旧查询等价**，只是取数路径从「SQL 过滤」变成「内存过滤」。测试里 `&SessionSnapshot{... Messages: ...}` 的字面量跟着改成 `msgs:`（同包可见）。

## 3. 数据流

```
session 空闲 → CronRunner 触发 DelayedTask(session_end)
             → PostChatWorkerGroup.Process(convID, userID, lastMsgID)
             → ListChatMessagesBetween 拉取 user+assistant → SessionSnapshot
             → 依次分发 worker …（summary / memory / state / profile）
             → FactWorker.Process(ctx, sess)        ← 本刀新增，排在 profile 之后
                  ├─ 开关判断（enabled == false 直接返回，不消费 LLM）
                  ├─ 场景标注 WithScenario(fact_extract)
                  ├─ FactExtractor.Extract(ctx, sess.GetAllMessages())
                  │     └─ ChatSync → llm_call_io（fact_extract）
                  └─ log「[fact] extracted N candidates」
```

## 4. `agent/fact_extractor.go`

### 4.1 类型

```go
// factCandidate — 抽取产出的一条候选事实（不落库，交给决策段）
type factCandidate struct {
    Subject string         `json:"subject"`
    Kind    string         `json:"kind"`
    Content string         `json:"content"`
    Fields  map[string]any `json:"fields,omitempty"`
}

// factExtractResult — 抽取段 LLM 的输出结构
type factExtractResult struct {
    Facts []factCandidate `json:"facts"`
}
```

`Fields` 用 `map[string]any` 而不是 `map[string]string`：`due_at` 是日期串、`has_doc_ref` 是文档 id，本来是字符串，但模型偶尔会把数字（`"due_at": 20260921`）写进来——用 `string` 接会让**整批**解码失败。`any` 接住，写库前再 `json.Marshal` 成 JSON 字符串（存储层已按可空 JSON 列设计）。

### 4.2 结构体与构造

```go
type FactExtractor struct {
    llmClient     llm.LLMClient
    maxCandidates int
    temperature   *float64
}

type FactExtractorConfig struct {
    LLMClient     llm.LLMClient
    MaxCandidates int      // 0 → 默认 5
    Temperature   *float64 // 采样温度；nil → 用模型默认
}

func NewFactExtractor(cfg *FactExtractorConfig) *FactExtractor
```

构造器里 `maxCandidates <= 0 → 5`（和 `NewMemoryExtractor` 的容错同一路数）。默认值 5 与 `facts.max_candidates` 保持一致（见 §6.1）。`temperature` 由 `facts.temperature` 传入（默认 0.2），指针语义 `nil = 不传、用模型默认`。

**签名不含 `userID`**：抽取段不检索、不写库，用不到用户 ID（记忆提取器要 `userID` 是因为它当场判重）。`userID` 只在 `FactWorker` 里用于场景标注。

### 4.3 主方法

```go
// Extract 从 session 消息中抽取候选事实。返回 nil 表示无候选。
func (e *FactExtractor) Extract(ctx context.Context, msgs []model.Message) ([]factCandidate, error)
```

内部四步：

1. **渲染对话**：把消息拼成 XML 块，role 取实际值（user / assistant），每个 turn 带发生时间（北京时间，取自 `CreatedAt`）
   ```
   <对话>
     <turn role="user" at="2026-09-21 14:03">…</turn>
     <turn role="assistant" at="2026-09-21 14:03">…</turn>
     <turn role="user" at="2026-09-21 14:04">…</turn>
   </对话>
   ```
   - 内容用 `xml.EscapeText` 转义（用户消息里会有 `<`、`&`）。
   - **跳过 `Content` 为空的 turn**——只发了 tool call 的 assistant 消息 content 是空的，渲染成空 turn 只会添噪声。
   - `at` 的来源问题：user 消息落库时已被管线写成「原词（日期）」（`今天（2026-09-21）`），assistant 消息**不经管线**、没有这个标注。喂**规范化后的 `Content`**（日期已确定性解出，不让模型自己算「昨天」）＋ `at` 兜底锚点，是二者的折中。
2. **组 user 消息**：`factExtractInstruction + "\n\n" + dialog`——**常量指令在最前、变化的对话在最后**，常量前缀命中 provider 的 prompt 缓存。
3. **调用**：`ChatSync(ctx, &llm.ChatRequest{SystemPrompt: factExtractSystemPrompt, Messages: [...], Temperature: e.temperature})`，**不带 Tools**（抽取走纯 JSON，不走原生 FC）。
4. **解析校验**：见 4.5。

### 4.4 提示词常量

```go
// 抽取段专用 system prompt；不复用 postChatSharedSystemPrompt
const factExtractSystemPrompt = `你是一个对话后处理助手。…`

const factExtractInstruction = `你是事实记录员。…（design.md §5.1 全文原样粘进来）`
```

两个常量都用**原始字符串常量**（反引号），照抄 §5.1，不改一个字。提示词里含 `<对话>` 样例，但那些是字面文本、不参与渲染，所以反引号常量最省事。

**为什么单开 system prompt**（`postChatSharedSystemPrompt` 写着「输入的消息均为用户角色的发言」）：

- 那条串与事实抽取喂 user＋assistant 的事实**直接矛盾**，铁律 1「只依据用户的消息」会被它抵消，助手的话有被当成用户陈述的风险（样例里助手那句误判提问就是这类陷阱）。
- 另外三条链路（memory / summary / state / profile）只喂 user，它们**不能动**——改共享常量会连带改写它们的 prompt 前缀，命中率全掉。

> §5.1 的样例里就带 `role="assistant"` 的 turn —— 现在输入里真的会有 assistant turn，样例与实际输入同构，正好教模型「助手的话不算事实来源」。

**09-21 样例重写**：这一版样例（4 例）是**重写的**。前一版照抄设计稿的 `→ ① subject=项目K｜state｜content` 表格行，把「紧凑写法的字段行」当成了 JSON 输出示例，导致 content 全部是「去掉主语、去掉时间、用『、』串要点」的半句话（「21 号当面演示、非交付日、后台来不及则只做前端」），与铁律 4「自足成句、主体名写进句子」自相矛盾。few-shot 是实际规格、散文规则只是噪音——模型照样例走。重写要点：

- content 一律成句、含主体名、一条只放一个断言；新增 ❌／✅ 对照样例专教这一点（唯一一条不配对话的样例）。
- 时间口径见 §5.1：期限进 `fields.due_at`，其余用 ISO 写进句子，禁相对词、禁抄机器括号。
- `subject` 不再用「项目K」这类对话里没出现过的代号（会教模型造词）。
- 删掉「记结论不记过程」那例：它那条 assistant turn 是舞台提示（`（跑偏去调研了别的）`），教模型往 turn 里塞注解；且原设计稿里那例的 content 本就语义破损。
- 修掉原稿里的一处**样例幻觉**：例 3 原文并无「还债」，样例却产出「不再愿为家里还债」——样例自己违反铁律 6，已删。

### 4.5 解析与校验

```go
var res factExtractResult
if _, err := ParseFlashJSON(msg.Content, &res); err != nil {
    log.Printf("[fact] extract parse failed: %v (raw %.120s)", err, msg.Content)
    return nil, nil   // 有意降级：本 session 抽不出东西，不打断后续 worker
}
```

解析失败**记日志后当成「本次无候选」**——这是有意的降级（不影响别的 worker 继续跑），不是静默吞。`ParseFlashJSON` 已经处理了 ```json 围栏和截断恢复。

逐条校验，不合法**丢弃并计数**，最后一次性打日志：

| 校验 | 处理 |
|---|---|
| `kind` 不是 `state` / `event` | 丢 |
| `content` `TrimSpace` 后为空 | 丢 |
| 有效条数 > `maxCandidates` | 截断到 `maxCandidates`，记日志 |

```go
log.Printf("[fact] candidates sanitized: kept=%d dropped=%d truncated=%v", len(out), dropped, truncated)
```

`subject` 为空**不丢**——`content` 从句里已经写了主体，`subject` 只做统计；缺了不致命。这一点写进注释，免得后来者「补一个校验」。

### 4.6 已知天花板

整个 `{facts:[...]}` 是一次性 `json.Unmarshal` 的：**单独一条候选结构异常（比如 `content` 给成数字）会让整批解析失败**。本刀接受这个代价（失败有日志、不崩）。要隔离得改成先解 `[]json.RawMessage` 再逐条解——等真机上真见过再改。

## 5. `agent/fact_worker.go`（最小版）

这一刀只需要一个薄壳，后面的决策段会往它里面长。

```go
type FactWorker struct {
    extractor *FactExtractor
    enabled   bool
}

type FactWorkerConfig struct {
    Extractor *FactExtractor
    Enabled   bool
}

func NewFactWorker(cfg *FactWorkerConfig) *FactWorker

func (w *FactWorker) Name() string { return "fact" }

func (w *FactWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
    if !w.enabled {
        return nil   // 关掉时零成本：不消费 LLM
    }
    ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
        Category:    llmcost.CategoryPostChat,
        SubCategory: llmcost.SubFactExtract,
        UserID:      sess.UserID,
        ConvID:      sess.ConvID,
    })
    cands, err := w.extractor.Extract(ctx, sess.GetAllMessages())
    if err != nil {
        return fmt.Errorf("fact extract: %w", err)
    }
    log.Printf("[fact] conv=%s extracted %d candidate(s)", sess.ConvID, len(cands))
    return nil
}
```

两点刻意：

- **`extractor` 不做 nil 检查**。它是构造时注入的必需依赖，为 nil 就是接线 bug，让它炸在明处（与 AGENTS 的约定一致）。`memoryWorker` 里的 `w.extractor == nil` 是历史写法，不照抄。
- **返回 error 向上抛**，由 `PostChatWorkerGroup` 统一 `log.Printf`——一个 error 只打一次日志。

## 6. 接线

### 6.1 `config` 加总开关、调整默认值

`FactsConfig` 现在是 `TopK / DupeThreshold / MaxCandidates`。加 `Enabled` 与 `Temperature`，并把候选上限默认值从 20 调成 **5**（抽取更聚焦，也少花决策段的钱）：

```go
type FactsConfig struct {
    Enabled       bool     `yaml:"enabled"`        // 事实系统总开关
    TopK          int      `yaml:"top_k"`          // 决策段检索近邻数，默认 5
    DupeThreshold float64  `yaml:"dupe_threshold"` // 向量 top1 静默跳过阈值，默认 0.95
    MaxCandidates int      `yaml:"max_candidates"` // 单次抽取候选上限，默认 5
    Temperature   *float64 `yaml:"temperature"`    // 抽取/决策段采样温度；nil = 用模型默认
}
```

- 合并用**直接赋值**（`dst.Facts.Enabled = src.Facts.Enabled`），和 `server.memory_auto_extract` 同款——每个环境文件显式表态，后加载的覆盖先加载的。
- 因此 `enabled: true` 要同时写进 **`config.yaml` + `config.dev.yaml` + `config.prod.yaml`**：base 定义基线，dev/prod 显式跟随（缺了会被覆盖成 false）。给的是「每个环境可单独关」的控制力。
- `max_candidates` 的默认值（`config.go` 校验段的 `if cfg.Facts.MaxCandidates == 0 { = 5 }`）跟着改成 5，与 `conf/config.yaml` 一致。
- `temperature` 是**指针**，合并用 `if src.Facts.Temperature != nil { dst.Facts.Temperature = src.Facts.Temperature }`（同 `llm.default_temperature`）——这样 `nil`（不传、用模型默认）与 `0`（确定性）能区分开。`conf/config.yaml` 里写 `temperature: 0.2`。

```yaml
facts:
  enabled: true            # 事实系统总开关（抽取 + 后续决策）
  top_k: 5
  dupe_threshold: 0.95
  max_candidates: 5
  temperature: 0.2
```

> 后台总开关 `server.disable_background_tasks` 关掉时整条 `session_end` 管线不跑，facts 自动继承，不用重复判断。

### 6.2 `llmcost` 加审计场景

`llmcost/types.go` 的子类常量段（`// post_chat` 那块）加：

```go
SubFactExtract = "fact_extract"
```

> `SubFactDecide = "fact_decide"` 这一刀不加班——没有决策段调用点就不加常量，避免出现「有常量没人用」。

`llmcost/io_audit.go` 的 `auditWhitelist[CategoryPostChat]` 加 `SubFactExtract: true`。

### 6.3 审计为什么自动生效

`IOAuditWrapper`（`io_audit.go`）是包在 LLM client 外面的装饰器，**只拦 `ChatSync`、不拦流式 `Chat`**。抽取段用的正是 `ChatSync`，所以在 `FactWorker` 里注入了 `WithScenario` 之后，这一行会自动落进 `llm_call_io`——不需要任何额外埋点、不写自己的表。

`llm_call_io` 这一行会填上：

| 列 | 值 |
|---|---|
| `category` / `sub_category` | `post_chat` / `fact_extract` |
| `user_id` / `conv_id` | 来自 `sess` |
| `ref_type` / `ref_id` | 空（抽取段不针对具体对象） |
| `system_prompt` | `factExtractSystemPrompt`（抽取段专用） |
| `messages_json` | 指令 + 渲染好的对话 |
| `response` | LLM 的原始返回（含 `{"facts":[...]}`） |
| `prompt_tokens` / `completion_tokens` / `latency_ms` | 由 `CostTracker` / 装饰器填 |

拿到这些就能直接回答「这一段提示词输出了什么、花了多少 token」，抽取质量调优不用翻别的地方。

### 6.4 `appinit/agents.go` 装配

在 `profileWorker` 构造之后、`sessionEndGroup` 之前插一段：

```go
factExtractor := agent.NewFactExtractor(&agent.FactExtractorConfig{
    LLMClient:     llmClients.BgFlash,   // 后台统一用 llm_bg
    MaxCandidates: cfg.Facts.MaxCandidates,
    Temperature:   cfg.Facts.Temperature,
})
```

然后把 worker 追加进现有的 `[]agent.PostChatWorker` 列表（**排在 `profileWorker` 之后**）：

```go
sessionEndGroup := agent.NewPostChatWorkerGroup([]agent.PostChatWorker{
    summaryWorker,
    agent.NewMemoryWorker(...),
    agent.NewStateWorker(...),
    profileWorker,
    agent.NewFactWorker(&agent.FactWorkerConfig{
        Extractor: factExtractor,
        Enabled:   cfg.Facts.Enabled,
    }),
}, repos.Message, ephemeralDestroyer)
```

`PostChatWorkerGroup`、`CronRunner`、`DelayedTask` 都不用动——新 worker 挂进现成的 `sessionEndGroup` 就会被带上。

`BgFlash`（`llm_bg`）和其余后台 worker 一致；`llm_bg` 为空时退回主 LLM，无需额外处理。

## 7. 测试

口径按 AGENTS「只影响后台低危任务」：`go build ./... && go test ./...` 全绿 + 针对性单测 + 真机跑一次。

`service/message_repo_test.go` 新增 `ListChatMessagesBetween` 用例：

| 用例 | 断言 |
|---|---|
| 含 assistant | user + assistant 都返回 |
| 排除 tool | `role=tool` 不出现 |
| 排除 internal | `is_internal=1` 的 assistant / system 不出现 |
| 时间区间 | 闭区间含边界；区间外不出现 |
| 单条 session / 空区间 | 与 `ListUserMessagesBetween` 同构的边界行为 |

`agent/fact_extractor_test.go`（复用 `profile_worker_test.go` 的 `syncMockLLM`）：

| 用例 | 断言 |
|---|---|
| 正常解析 | 3 条候选 → `len == 3`，字段对得上 |
| `kind` 非法 | `{"kind":"fact"}` 被丢，只留合法的 |
| `content` 空白 | `"   "` 被丢 |
| 空列表 | `{"facts":[]}` → 返回空、无 error |
| 截断 | 给 12 条、`maxCandidates=5` → 返回 5 条 |
| 解析失败 | 纯文本回复 → 无 error、0 候选；结构损坏的 JSON → 无 error、nil（降级不报错） |
| 温度透传 | 设 `Temperature=&0.2` → `ChatRequest.Temperature` 为 0.2；`nil` → 不传 |
| 输入渲染 | 捕获 `syncMockLLM` 收到的 `ChatRequest`，断言 `SystemPrompt` 是 `factExtractSystemPrompt`、user 消息以指令开头、含 `<turn role="user" at="` 与 `<turn role="assistant" at="`、且空 content 的 assistant 不产生空 turn |

`agent/fact_worker_test.go`：

| 用例 | 断言 |
|---|---|
| `enabled=false` | `syncMockLLM` 一次都没被调用 |
| `enabled=true` | 调用一次，`scenario.SubCategory == fact_extract` |

pure 校验逻辑（丢非法、截断、渲染）抽成 `sanitizeCandidates` / `renderDialog` 之类的纯函数单测，轻量、不必每条都过 mock LLM。

真机验证：跑一次聊天（dev `session_gap_minutes=1`，等 1 分钟）→ 查 `llm_call_io` 有 `fact_extract` 行、日志有 `[fact] conv=… extracted N candidate(s)`、无报错。**此刻 `facts` 表应为空**（本刀不写库），这是预期。

**验收抽取质量只看真机**：从 `llm_call_io`（`sub_category=fact_extract`）拉 `response`，逐条判 content 是否自足成句、有无相对时间词、有无抄来的括号。本地库在这一步之前永远是 0 行，别拿样例当产出证据。

## 8. 文件清单

**新增**

| 文件 | 说明 |
|---|---|
| `server/internal/agent/fact_extractor.go` | 提示词 + `ChatSync` + 解析 + 校验 + 渲染 |
| `server/internal/agent/fact_worker.go` | `FactWorker`（本刀只做抽取编排与场景标注） |
| `server/internal/agent/fact_extractor_test.go` | 解析 / 校验 / 截断 / 渲染 |
| `server/internal/agent/fact_worker_test.go` | 开关 / 场景标注 |

**修改**

| 文件 | 改动 |
|---|---|
| `server/internal/service/message_repo.go` | 新增 `ListChatMessagesBetween` |
| `server/internal/service/message_repo_test.go` | 新方法用例 |
| `server/internal/agent/post_chat_group.go` | `SessionSnapshot.msgs` 收口 + 两个方法 + 换用新查询 |
| `server/internal/agent/post_chat_group_test.go` | 字面量 / 断言随快照改造更新 |
| `server/internal/agent/summary_worker.go` | `GetUserMessages()` |
| `server/internal/agent/memory_worker.go` | `GetUserMessages()` |
| `server/internal/agent/state_worker.go` | `GetUserMessages()` |
| `server/internal/agent/profile_worker.go` | `GetUserMessages()`（含测试） |
| `server/internal/config/config.go` | `FactsConfig.Enabled` + 合并；`max_candidates` 默认 5 |
| `server/conf/config.yaml` / `config.dev.yaml` / `config.prod.yaml` | `facts.enabled`；`max_candidates: 5` |
| `server/internal/llmcost/types.go` | 加 `SubFactExtract` |
| `server/internal/llmcost/io_audit.go` | 白名单加 `SubFactExtract` |
| `server/internal/appinit/agents.go` | 构造 `FactExtractor` + 挂 `FactWorker` |
| `docs/design/facts-system/design.md` | §12 默认值 5；§14 文件表同步 |

## 9. 边界与简化

- **抽取段不判重、不检索、不写库**——归口下一刀。
- **不引入 `FactDecider`、不引入 `fact_decide` 常量**：没有调用点就不写代码，避免死代码。
- **提示词原样照抄 §5.1**，不在代码里二次改写（要改回 `design.md` 改）。
- **`subject` 为空不校验**（`content` 自足，`subject` 只统计）。
- **`SessionSnapshot` 字段收口为 `msgs`**：不给外部直接摸的机会，两个方法是唯一入口。
- **单条候选结构异常会拖垮整批解析**：已知天花板，有日志兜底，真机见过了再改逐条解。
