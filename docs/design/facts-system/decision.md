# 事实系统 · 决策段 — 实现设计

> 版本：v2 | 日期：2026-09-21 | 状态：**设计定稿，已实现**
> 上级文档：[design.md](./design.md)（总览 §6 检索 / §7 决策 / §8 判重 / §9 写入）
> 已实现：[storage.md](./storage.md)（两张表 + repo + 预热）、[extraction.md](./extraction.md)（抽取段）
> 估算（开工）：4 面 / 2 缝（0 条需先建探针）/ 1–2 次真机往返 / 4 个待拍决定 · ~2–5h
> 估算（定稿）：6 面 / 2 缝（0 条探针）/ 1 次真机往返 / 0 个待拍决定 · 3–5 小时

抽取段已经落地：`llm_call_io` 里能看到「抽到了哪几条候选」，但候选只进日志、**不落库**。

这一段把候选变成库里的行：**判重 → 检索 → 决策 → 写入**，逐条串行。

---

## 1. 范围

**做**

- 归一化判重（纯代码）＋ 向量近邻检索（纯代码）。
- `FactDecider`：决策段提示词 + 原生 FC 调用 + tool call 解析/校验。
- 四种动作的写入：`ADD` / `UPDATE` / `DELETE` / `NONE`，各自带向量与 `Embedding` 行同步。
- `FactWorker` 编排：逐候选串行、场景标注 `fact_decide`、失败降级与日志。
- 接线：`SubFactDecide` 审计白名单、`FactRepo` 补一处签名、`appinit` 注入新依赖。

**不做**

- 不注入主对话、不对外的 API/前端（仍是写入系统）。
- 不做回滚（changelog 只观察）。
- 不从旧记忆/笔记迁移，不跨库去重。
- 不让 `subject` / `kind` / `fields` 参与检索（`content` 是唯一检索字段）。

**做完的效果**：一次会话结束后，「有长期价值的话」会真的落进 `facts` 表；同一件事再次被提起时，库里是**被覆盖后的最新值**而不是两条重复；被明确否定的事实会软删并留一条 `fact_changelogs`。

> 为什么必须逐候选串行、每条处理完立即写：候选 1 写进库、向量也同步进了内存，候选 2 的检索才看得见它。整批一起写会批内互不可见，两条相似候选会都判 `ADD`，库里就真重复了。这不是性能取舍，是正确性前提。

## 2. 一条候选的五步

对抽取产出的每一条候选，依次做：

| 步 | 谁做 | 输入 → 输出 |
|---|---|---|
| ① 归一化判重 | 代码 | `content` → 命中就整条跳过（不调 LLM） |
| ② 嵌入 + 检索 | 代码 | `content` → 近邻 top_k（无阈值） |
| ③ 相似度过高 | 代码 | top1 分数 ≥ 阈值 → 整条跳过（不调 LLM） |
| ④ 决策 | LLM（原生 FC） | 候选 + 近邻 → 0..N 个动作 |
| ⑤ 写入 | 代码 | 动作 → `facts` / `fact_changelogs` / 向量 |

①③ 是「静默跳过」，命中即跳过并计数（**不花 LLM 的钱**）。只有活下来的候选才进 ④。

整段结束后打一行汇总：

```
[fact] conv=xxxxxxxx candidates=3 skipped_dupe=1 skipped_similar=0 decided=2 added=1 updated=1 deleted=0 none=0
```

## 3. ① 归一化判重（纯代码）

`content_hash = md5(normalize(content))`，与库里的 `facts.content_hash` 比。命中（且 `status=active`）→ 跳过。

**归一化只做一件事：把「写法差异」抹平，「内容差异」全部保留。**

| 做 | 不做 |
|---|---|
| 去空白（含全角空格） | **不去数字** |
| 去标点、符号、下划线 | **不去日期** |
| 全角 → 半角（`ＡＢＣ` → `abc`） | 不换词、不做同义改写 |
| 大小写归一 | 不排序、不去重 |

保留现状：

```go
func normalizeFactContent(s string) string {
    var b strings.Builder
    for _, r := range s {
        if r == '\u3000' { // 全角空格
            continue
        }
        if r >= 0xFF01 && r <= 0xFF5E { // 全角 ASCII → 半角
            r -= 0xFEE0
        }
        if unicode.IsLetter(r) || unicode.IsDigit(r) {
            b.WriteRune(unicode.ToLower(r))
        }
    }
    return b.String()
}
```

**为什么必须保留数字与日期**（这条推翻了早先的写法）：原来的方案是「剥掉日期前缀再比」。那样「2026-09-18 和老K吵架」与「2026-09-25 和老K吵架」会归一化成同一个串、hash 相同 → **第二条被静默丢掉**。这是丢事实，不是去重。抹平写法差异就够了，内容差异必须留给模型判。

hash 复用现成的 `hashPrompt`（`extractor.go:249`，md5 → hex，三行）。它名字里带 prompt，但就是个字符串 md5，与其再写一个不如复用；调用点写明 `hashPrompt(normalizeFactContent(content))` 就够清楚。

## 4. ② 检索（纯代码）

```go
vec, err := embedder.Embed(ctx, cand.Content)
matches := vectorStore.Search(vec, topK, func(_ string, _ []float64, meta map[string]string) bool {
    return meta["type"] == model.EntityTypeFact && meta["user_id"] == uid
})
```

- `topK` 走 `facts.top_k`（默认 5）；**不设阈值**——0.85–0.95 那一段照常交给模型判（照 mem0）。
- 近邻的 `subject` / `kind` / `content` 从**向量 metadata 取**（预热与写入时都写进去了），不回表。回表是真实的 N+1 隐患。相似度**只用于排序与 top_k 截断，不渲染给模型**（见 §5.3 的注）。
- 内存 key 是 `fact:<id>`，交给模型前**剥掉 `fact:` 前缀**：模型看到的永远是裸 uuid，`target_id` 校验也按裸 uuid 比。
- **0 条近邻也照常调决策段**。多一个「没近邻就直接 ADD」的分支省不下多少钱（只有该用户库里一条事实都没有时才会发生，一旦有几条，无阈值的 top5 必然取满），却多一条写库路径要单独维护。统一走模型，`ADD` 是它的自然结论。

## 5. ④ 决策段（LLM，原生 FC）

### 5.1 提示词（草案，全文）

system prompt 用**专用串**（不是 design.md §7.2 里写的 `postChatSharedSystemPrompt`）：那条写死了「输入的消息均为用户角色的发言，按时间顺序排列」，而这里的输入是「一条候选 + 它的近邻」，既不是用户发言、也不是时间序对话。理由与抽取段当初分家完全一致：**内容对不上，且自己是新前缀、不会动别人的缓存**。

指令消息（常量）放在 user 消息最前，候选块紧随其后——常量前缀命中 provider 的 prompt 缓存。

```go
// factDecideSystemPrompt — 决策段专用 system prompt。
const factDecideSystemPrompt = `你是一个事实库的写入决策助手。输入是一条候选事实和它在事实库里检索到的近邻事实。`

const factDecideInstruction = `你是事实库的写入决策者。上游给你一条候选事实，以及它在事实库里检索到的近邻事实。
你要为这条候选选一个动作：新开一条、改一条、删一条，或者什么都不做。

## 输入

<candidate>：待写入的候选，字段 subject / kind / fields / content。
<neighbors>：按 content 向量相似度从事实库取回的近邻（不设阈值，可能一条都不相关）。
每个近邻写成 <fact id="..." subject="..." kind="...">正文</fact>，id 是它的唯一标识。

## 动作

- add_fact(subject, kind, content, fields?)：新开一条。
- update_fact(target_id, content, subject?, kind?, fields?)：近邻里有一条在回答同一个问题，答案变了或者更全。
- delete_fact(target_id) 加 add_fact：候选被近邻里某条明确否定。
- 什么都不调：与某条近邻同义，跳过。

## 判据

同一个问题 = 同一主体 ＋ 同一断言位。
库里有「本人只做技术」，候选是「本人兼产品开发」——同主体、同一断言位（他在这个项目里担任什么），答案变了 → update_fact。

update 还是 add，看是不是同一条事实的更完整版本：多了一件无关的新事 → add_fact；同一件事变了 → update_fact。

delete 只用于明确否定：「我不想再管那笔债了」否定「计划为家里还债」→ delete_fact；只是补充细节 → update_fact。

观点不许覆盖成「最新观点」。观点是 event，说出口就固定，只增不改。

近邻可能一条都不相关，也可能内容相近但不是同一件事。以判据为准，不要因为拿到了 id 就硬找一条来改。

## 输出

这条候选只选一个动作（delete 加 add 算一个组合），不许返回整库新状态。
写成 tool call。
target_id 只能取自输入里的 id，不许自造。
update_fact 的 content 必须写改写后的完整句子——自足成句、主体名写进句子，不是只写变化的那半句。
候选的 fields（如 due_at）与近邻旧值不同时一并给出；没有就不传。`
```

> 与 design.md §7.1 的两处差异：① prompt 从「一批候选」改成「一条候选」（串行设计的实际输入，原文那样写模型会等一批）；② 补了「content 要完整成句」「fields 变了要带上」两条——UPDATE 是把整行 content 换掉，模型只写变化的那半句会把事实库写残。

### 5.2 工具定义

手写一个含 3 个函数的 `llm.ToolDefinition`（**不进 `tool.Registry`**）：决策段的「工具调用」只是模型的输出格式，执行者是我们自己，不走注册表的校验/白名单/只读那套。

provider 侧会把函数名拼成 `facts__add_fact`（`deepseek.go:326` 的 `t.Name + "__" + fn.Name`），回来时也是这个名，用现成的 `splitToolFuncName`（`orchestrator.go:2304`）拆。

| 函数 | 参数 | 必填 | 语义 |
|---|---|---|---|
| `add_fact` | `subject, kind, content, fields?` | subject/kind/content | 新开一条 |
| `update_fact` | `target_id, content, subject?, kind?, fields?` | target_id/content | 覆盖近邻中某一条 |
| `delete_fact` | `target_id` | target_id | 删除近邻中某条 |

`kind` 的 enum 用 `[state, event]`，`fields` 是 object（`due_at` / `has_doc_ref`）。

### 5.3 输入渲染

```
<candidate index="0">
  <subject>老K小程序</subject>
  <kind>state</kind>
  <content>本人这次在老K的旅游案例展示小程序上兼产品与开发，原定位只做技术</content>
  <fields>{"due_at":"2026-09-21"}</fields>
</candidate>
<neighbors>
  <fact id="9f2c1a4e-..." subject="老K小程序" kind="state">本人只做技术</fact>
  <fact id="3b7d90c2-..." subject="配偶" kind="event">配偶因电动车一事与本人争吵</fact>
</neighbors>
```

- 文本一律过现成的 `xmlEscape`（`fact_extractor.go`），别用 `id | subject | kind | content` 那种竖线格式——content 里出现一个 `|` 就是脏输入。
- `fields` 为空时不渲染这一行；`<neighbors>` 为空时渲染成 `<neighbors></neighbors>`（显式空，别让模型猜是不是漏传了）。
- 用户消息 = `factDecideInstruction + "\n\n" + 候选块`，与抽取段同构。

> **相似度不渲染**（曾经想过加 `similarity="0.87"`）。判据是「同一主体 + 同一断言位」，分数不参与这个判断；渲染出来只会让模型拿分数替代判据（「0.87 挺高，那就是同一条」）。近邻本来就是按相似度降序截的，顺序已经隐含了强弱，分数是个多余信号。真机上若发现模型**不敢**忽略明显不相关的近邻，再考虑加回来——那时它的作用是「弱到可以无视」，得配合一句说明才有意义。

### 5.4 解析与校验

```go
// 无 tool call = NONE（模型主动什么都不调）
for _, tc := range resp.ToolCalls {
    _, fn := splitToolFuncName(tc.Function.Name)
    var p decideParams
    if err := json.Unmarshal([]byte(tc.Function.Arguments), &p); err != nil {   // 不吞
        log.Printf("[fact] decide: bad args for %s: %v (raw %.120s)", fn, err, tc.Function.Arguments)
        continue
    }
    ...
}
```

| 校验 | 处理 |
|---|---|
| 函数名不在三个之内 | 丢弃 + 日志（模型幻觉） |
| `Arguments` 解不出 JSON | 丢弃该 call + 日志（**不**拿零值继续） |
| `target_id` 不在本次近邻 id 集内 | 丢弃该 call + 日志（防自造 id，最重要的那条闸） |
| `add_fact` 缺 `kind` / `content` | 丢弃 + 日志 |
| `update_fact` 缺 `content` | 丢弃 + 日志 |
| 一次返回多个 `add_fact` | 按顺序都执行（合法：候选确实可能信息量超过一条） |

`splitToolFuncName` 拆不出 `__` 时返回 `(name, name)`——所以模型若直接调 `add_fact`（没带 `facts__` 前缀），`fn` 仍然是 `add_fact`，能正常命中。这个宽容是免费的，不额外判定前缀。

## 6. ⑤ 写入（四分支）

| 动作 | 顺序 |
|---|---|
| `add_fact` | `facts` 插入（id = uuid，status=active，hash=归一化 md5）→ embed → `vectorStore.Upsert("fact:"+id)` → `Embedding` 行 upsert |
| `update_fact` | `FindByID` 读旧值（拿 old_content）→ 更新 content/hash（subject/kind/fields 变了才带上）→ 插 `fact_changelog(UPDATE, old, new)` → 重新 embed + upsert 向量 + `Embedding` 行 |
| `delete_fact` | `FindByID` 读旧值 → `status=deleted` → 插 `fact_changelog(DELETE, old, "")` → `vectorStore.Delete("fact:"+id)` → `EmbeddingRepo.DeleteByEntity` |
| `NONE` | 什么都不做 |

细节：

- **向量是同步写的**（`Upsert` 紧跟 DB 写，同一候选内），这就是串行可见性的落点。
- **不建 chunk**。facts 是单句，`chunk_builder` 那套分句检索对它没有增益，`chunk_disabled_parents` 也不用加（根本不建）。
- `Embedding.ID` 用 `fact.ID`（与 `UserMemory` 同款：一行一实体）。
- `Embedding.EntityType = model.EntityTypeFact`（已有常量）。
- 向量 metadata：`{type: fact, user_id, subject, kind, content}`——**必须是更新后的值**，否则模型下一轮看到的近邻正文是旧的（检索命中但内容对不上）。
- **UPDATE 的 `fields`**：给了就整块覆盖，没给就保留旧值（`nil` = 不动）。这一条是待拍决定 #1。
- **`source_msg_id`**：抽取输出不带消息 id，取本 session **末条 user 消息 id**（`sess.GetUserMessages()` 的最后一个）。只在 changelog 里记。
- `facts` 的 `UpdateContent` / `SoftDelete` 只按 `id` 过滤、不带 `user_id`。可接受：`target_id` 只能来自「按 user_id 过滤过的近邻集」，而 id 是 uuid，不构成越权面。这一条写进注释，别让后来者以为是漏的。

## 7. 失败与降级

| 失败点 | 处理 | 理由 |
|---|---|---|
| 抽取失败 / 0 候选 | 已有行为：上抛给 group，整段结束 | 抽取段的事，不改 |
| embed 失败 | **日志 + 当作 0 近邻继续**（不跳过） | 不丢事实。hash 判重已经跑过了，此时冒重复的风险 < 丢一条事实的风险 |
| 决策 LLM 调用失败 | 日志 + **跳过这一条候选**，继续下一条 | 一条候选的失败不该拖垮另外几条；最终汇总里计 `failed=N` |
| tool call 解析/校验不过 | 丢弃该 call + 日志 | 见 §5.4 |
| 决策 LLM 无 tool call | 视为 `NONE` | 协议本来就这样，不是错误 |
| `facts` 插入 / 更新失败 | **上抛**（`return err`），由 `PostChatWorkerGroup` 记一次日志 | DB 写失败是真失败，不能当没发生；此时**不写向量**，下次会话重跑会重新尝试 |
| `Embedding` 行写失败 | 日志 + 继续 | 事实已经在库里，只是重启后向量预热会缺这一条（降级，可接受） |
| changelog 写失败 | 日志 + 继续 | 审计不是主契约；事实本身的更新已经生效 |

`Embedding` 与 changelog 的失败都只打一次日志（就近打，不往上抛，避免同一 error 多层重复打印）。

## 8. 代码接线

### 8.1 文件

`fact_worker.go` 一个文件装三样东西（不新开文件，与记忆模块 `extractor.go` + `memory_worker.go` 两文件制对齐）：

| 内容 | 说明 |
|---|---|
| `FactDecider` + `NewFactDecider` | 决策段：提示词常量 + `ChatSync(Tools)` + 解析成 `[]factAction` |
| `normalizeFactContent` | 归一化（§3） |
| `FactWorker` | 编排：开关 → 场景 → 逐候选 ①–⑤ → 汇总日志 |

```go
// factAction — 决策段产出的一条动作。
type factAction struct {
    Op       string          // add / update / delete
    TargetID string          // update / delete 用，已校验在近邻集内
    Subject  string
    Kind     string
    Content  string
    Fields   *string         // 已 Marshal 的 JSON；nil = 不传
}

type FactDecider struct {
    llmClient   llm.LLMClient
    temperature *float64
}

type FactDeciderConfig struct {
    LLMClient   llm.LLMClient
    Temperature *float64 // nil → 用模型默认
}

// Decide 为一条候选产出动作。返回 nil = NONE。LLM 出错时上抛。
func (d *FactDecider) Decide(ctx context.Context, cand factCandidate, neighbors []app.VectorMatch) ([]factAction, error)
```

`FactWorkerConfig`：`Extractor` / `Decider` / `Enabled` / `FactRepo` / `ChangelogRepo` / `Embedder` / `EmbeddingRepo` / `VectorStore` / `TopK`（0 → 5）/ `DupeThreshold`（<=0 → 0.95）。`TopK` 放 worker 不放 decider——检索在 worker 里做，`Decide` 只拿现成的近邻（与本文档原稿的一处偏差）。

必需依赖一律**不做 nil 检查**——为 nil 就是接线 bug，炸在明处（AGENTS 约定）。

### 8.2 修改文件

| 文件 | 改动 |
|---|---|
| `service/fact_repo.go` | `UpdateContent` 签名加 `fields *string`（nil = 不改）；`service/fact_repo_test.go` 断言补 fields 覆盖 |
| `agent/warmup.go` | 向量 key 改用 `factVectorPrefix` 常量（与写入侧同一常量，避免前缀漂移） |
| `llmcost/types.go` | 加 `SubFactDecide = "fact_decide"` |
| `llmcost/io_audit.go` | 白名单 `CategoryPostChat` 加 `SubFactDecide` |
| `appinit/agents.go` | 构造 `factDecider` 并把新依赖注入 `FactWorker` |
| `docs/design/facts-system/design.md` | §7.1 提示词（一条候选 + 两处补充）、§7.2 system prompt 改专用串、§14.3 装配补依赖 |

### 8.3 装配

```go
factWorker := agent.NewFactWorker(&agent.FactWorkerConfig{
    Extractor:     factExtractor,
    Enabled:       cfg.Facts.Enabled,
    LLMClient:     llmClients.BgFlash,
    FactRepo:      repos.Fact,
    ChangelogRepo: repos.FactChangelog,
    Embedder:      infra.Embedder,
    EmbeddingRepo: repos.Embedding,
    VectorStore:   infra.VectorStore,
    TopK:          cfg.Facts.TopK,
    DupeThreshold: cfg.Facts.DupeThreshold,
    Temperature:   cfg.Facts.Temperature,
})
```

`WarmupAll` 已经会加载 `status=active` 的 facts 向量（storage.md 那一段），SoftDelete 后重启也不会被加载——这一段的写入路径与预热口径天然吻合，**预热不用动**。

### 8.4 场景标注

| 调用 | Category / SubCategory | RefType / RefID |
|---|---|---|
| 抽取 | `post_chat` / `fact_extract` | 空 |
| 决策（每条候选一次） | `post_chat` / `fact_decide` | `fact` / 候选序号（`"0"`、`"1"`…） |

一行候选一次调用、一行 `llm_call_io`，真机验收时能直接对着候选看模型选了什么。

### 8.5 实现注记（与草案的偏差）

| 偏差 | 原因 |
|---|---|
| `TopK` 放在 `FactWorkerConfig` 而不是 `FactDeciderConfig` | 检索在 worker 里做，`Decide` 只接收现成的近邻；decider 里留一个用不到的字段不如不留 |
| 向量检索依赖声明为 `app.VectorStore`（接口）而非 `*agent.VectorStore` | 生产装配传的是 `infra.VectorStore`（已适配过的接口）；测试里用 `NewVectorSearchAdapter(deps.store)` 包一层 |
| 新增 `factMockLLM` 而不是复用 `syncMockLLM` | 需要按调用序号注入错误 |
| `mockEmbedder`（`theme_discover_test.go`）补了 `ModelName()` | 它现在要当 `app.Embedder` 用（原先只满足只有 `Embed` 的窄接口） |
| `warmup.go` 的 `"fact:"` 字面量换成 `factVectorPrefix` | 写入与预热必须同一个前缀，共用常量免得改一处漂一处 |

## 9. 测试

口径按 AGENTS「后台低危任务」：`go build ./... && go test ./...` 全绿 + 针对性单测 + 真机跑一次。

`fact_worker_test.go`（决策段部分）：sqlite（`Fact` / `FactChangelog` / `Embedding` 三表）+ **真的 `agent.NewVectorStore()`**（经 `NewVectorSearchAdapter` 转成 `app.VectorStore`）+ 固定向量 embedder（复用 `mockEmbedder`，本轮给它补了 `ModelName()`）。LLM 桩用新写的 `factMockLLM`（按下标注入错误——`syncMockLLM` 只能「用完了才报错」，做不出「候选 1 失败、候选 2 成功」）。

| 用例 | 断言 |
|---|---|
| ADD | 无近邻 → `add_fact` → `facts` 一行 + 向量在 + `Embedding` 行在（`Model` 来自 embedder）+ 无 changelog；决策场景标注 `fact_decide` / `ref_type=fact` / `ref_id=候选序号`，system prompt 是专用串 |
| NONE（hash 命中） | 库里先放同 content 的 active 事实 → **LLM 一次都没调**、无新行 |
| NONE（向量 top1 ≥ 阈值） | 阈值调低 → **LLM 一次都没调** |
| UPDATE | 近邻命中 → `update_fact` → content/hash 变了、`fields` 整块覆盖、changelog 一行（old/new + `source_msg_id` = 末条 user 消息 id 对得上）、**向量 metadata 的正文也刷新了** |
| DELETE | `delete_fact` → `status=deleted` + 向量没了 + `Embedding` 行没了 + changelog 一行 |
| NONE（连工具都不调） | 无写入 |
| 自造 id | `update_fact` 的 `target_id` 不在近邻集 → 无写入、无 changelog |
| 参数解不出来 | `Arguments` 是坏 JSON → 丢弃 + 不 panic |
| 串行可见性 | 候选二的决策请求里，`<neighbors>` 必须含候选一刚写入的 fact id（阈值设 1.1 关掉自动跳过，保证真的走到决策段） |
| 无 tool call | 视为 NONE，无写入 |
| 决策 LLM 失败 | 候选一失败、候选二照常执行；最终不向上抛 |

`fact_normalize_test.go`（纯函数，不碰 LLM）：

| 用例 | 断言 |
|---|---|
| 标点/空白/全半角/大小写 | 写法不同的同一句话 → 同一个 hash |
| **日期保留** | `2026-09-18 和老K吵架` 与 `2026-09-25 和老K吵架` → **hash 不同**（钉死「别剥日期」） |
| 空串 / 纯标点 | 结果为空串，不 panic |

真机验收清单（跑一次聊天，等 `session_gap_minutes` 到期）：

1. `llm_call_io` 出现 `fact_decide` 行，`ref_id` 是候选序号；`response` 里能看到 tool call 的原始形状（函数名模型认不认、NONE 时是否真的什么都不调，都只能真跑才知道）。
2. `facts` 表出现行；`content` 是自足成句、没有相对时间词、没有抄来的括号。
3. 把同一件事**换个说法**再说一遍 → 库里是**被 UPDATE 覆盖**（不新增行）、`fact_changelogs` 多一行 `update`。
4. 说一句明确否定 → 对应事实 `status=deleted`，`fact_changelogs` 多一行 `delete`。
5. 日志里那条汇总行的各计数与 `facts` 表实际变化对得上。
6. 同一个候选多发几次（温度 0.2 仍会有波动），看 UPDATE/ADD 边界稳不稳、有没有把明显不相关的近邻硬判成 UPDATE——这是提示词要不要加约束（比如把「不要因为拿到 id 就硬改」再写重些）的唯一判据。

## 10. 四条决定（已拍，按推荐执行）

| # | 决定 | 结论 |
|---|---|---|
| 1 | UPDATE 时 `fields` 怎么处理 | **给了就覆盖、没给（或 `{}`）就保留**——`marshalFactFields` 把空 map 归成 nil |
| 2 | 决策段 system prompt | **专用串** `factDecideSystemPrompt`（未复用 `postChatSharedSystemPrompt`） |
| 3 | 决策段温度 | **复用 `facts.temperature`**（0.2），不新增配置项 |
| 4 | embed 失败 | **日志 + 当作 0 近邻继续**（不丢候选），与 `MemoryExtractor.storeMemory` 同口径 |

## 11. 边界与已知天花板

- **`event` 不许被覆盖**只写在提示词里，代码不做强制。真出现「模型 UPDATE 了一条 event」，属于提示词调优问题，先观察。
- **批内两条候选互相矛盾**（候选一说 A、候选二说非 A）：按串行顺序，候选二会 `DELETE`/`UPDATE` 候选一刚写的行。行为正确，但结果依赖抽取的顺序——符合「以最终陈述为准」。
- **同一 session 重复跑**（cron 与 delayed task 都触发）：hash 判重兜底，基本幂等；不额外加锁。
- **决策段一次调用 = 一条候选**，`max_candidates=5` 时一个 session 最多 5 次决策调用。想省钱的旋钮是 `max_candidates`，不是合并成一批（合并会破坏串行可见性）。
- **不做事务**：DB 行、向量、changelog 三步分开写。中途失败留下的是「事实已落库但 changelog 缺一条」这类可接受的不一致（见 §7）。
- `facts.dupe_threshold=0.95` 是拍的先验值。真实 embedding 分布下 0.95 到底等价于多严，只能真机观察——**若发现「明显不同的事实被静默跳过」，第一个要动的就是它**（下调阈值 = 更多候选进模型判）。
