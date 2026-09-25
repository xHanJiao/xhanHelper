# 记忆 topic 收紧到 5 字 + 存量清洗

> 状态：草案，待拍板
> 上位：《记忆 / 笔记改造 · 设计文档》（`design.md`）
> 相关：`topic-merge.md`（同义 topic 合并）、`record-loop-prompt.md`（记录循环契约）

---

## 一、出了什么问题

记忆的主题词（topic）有两个毛病：

1. **有的记忆根本没有 topic**（空的）。
2. **有的 topic 太长**，比如 `WKWebView性能调优`（7 个字）、`技术调研与反爬对抗`（8 个字）。

而 topic 是记忆卡片上的标题、也是分类检索的依据，**必须填、且要短**。

### 为什么突然冒出来

不是一开始就这样，是记录循环改版带来的：

| 阶段 | topic 限制 | 怎么产出的 |
|---|---|---|
| 改版前（`a363a117` 那会儿） | **5 字以内** | 单独调一次 LLM 专门起 topic，prompt 写死「5 字以内」 |
| 记录循环改版后（`86b7cfea` 至今） | **20 字以内** | 塞进抽取段，让 LLM 顺手输出 |
| 我们想要 | **5 字以内** | 见下 |

上限从 5 字放宽到 20 字（涨了 4 倍），所以 7~20 字的 topic 都是改版后才产生的。**这次的方案本质上是"把 topic 的规矩收回改版前的严格度"**，并且把「起 topic」这件事重新拎出来单独做——因为当初就是"塞进抽取段顺手输出"导致它被违反。

另外还有一个 bug：**改记忆时如果 LLM 没重填 topic，会把原来好好的 topic 覆盖成空**（`record_loop_write.go` 的 `updateMemory`）。这是空 topic 的来源之一。

---

## 二、总目标

- **新的规矩**：记忆的 topic 非空、≤ 5 个字。
- **新写入**：保证以后产出的 topic 都合规，不合规就打回重来。
- **存量清洗**：把已经写坏的 topic（空的、超 5 字的）一次性收拾干净。
- **前端不改**。

---

## 三、以后新写的记忆怎么办

### 1. topic 长度改成 5 字，空的直接打回

现在的校验在 `agent/record_loop_params.go`：

- 上限从 20 改成 **5**。
- 增加一条：**空 topic 直接报错打回**。
- 报错会原样返回给决策循环的 AI，它看到报错会改写重填 → 这就是"打回"。

**但"打回"是最后一道闸，不能只靠它。** 5 字这个限制必须**一开始就写进 prompt**，让模型第一次输出就尽量合规：

- 抽取段的指令消息（`extractor.go`）
- 决策循环的 system prompt / instruction（`record_loop_types.go`）
- 记忆定义的公共文案（`promptbuilder/memory_definition.go`）
- 工具 schema 的 topic 说明（`record_loop_tools.go`、`tool/record_memory.go`）

"打回"只在模型仍然犯规时兜底。

### 2. 修「改记忆把 topic 抹空」的 bug（要补单测）

`updateMemory` 里，如果 AI 没传 topic，就用**原来那条记忆的 topic**，不要写空。

**必须补单测**：
- 省略 topic → 保留旧 topic；
- 显式传 topic → 正常覆盖。

### 3. topic 单独调一次 LLM 来定

这是核心改动。现在 topic 是抽取段顺手输出的，我们**把它拎出来单独做一次调用**（跟改版前的思路一致，执行更稳）。新逻辑分三步：

**第一步：先把"已有的 topic"捞出来**
- 拿这条记忆的 summary 算向量（这个向量决策循环本来就算过一次了，直接复用，**不额外花钱**）。
- 用它去召回这个用户**已经在用的 topic**：**先按相似度挑最相近的 10 个**，再按引用次数降序展示。
  - 省钱：算相似度前先按引用次数粗截断（`topicRecallPreCap=40`），避免对全量 topic 逐个 `GetOrEmbed`。
- 每个已有 topic 的向量都走 **concept embedding 的那套缓存**（`ConceptEmbedService.GetOrEmbed`）：已有向量直接读，**不重复调 embedding API**（我们目前只能调远端嵌入，没有本地模型，能省则省）。topic 的向量一直存在 `concept_embeddings` / `embeddings` 表里，**不放进内存 VectorStore，也不需要新增任何存储**。
- **每个已有 topic 都标出「引用次数」和「相似度」**，让模型知道"又高频又相似"的最该被复用。
- 排序口径：**取 top-10 按相似度（真正该归并的），展示按引用次数**——高频但不相关的 topic 不会挤掉真正相近的低频 topic。

**第二步：调 LLM 定 topic**
- 给它的输入：这条记忆的内容 + 上面那 10 个已有 topic（带引用次数、相似度）。
- 规则告诉它：**优先用已有的 topic**；实在没有合适的才新建，新建必须 ≤ 5 字、非空、**纯中文（不许英文/数字/标点）**。
- 输出：一个 topic。

**第三步：0.8 强制归并（落库时，复用既有逻辑）**
- 定稿的 topic 落到记忆时，`newMemory → reuseTopic` 会拿 **topic 词的向量** 和已有 topic 逐一算余弦，**≥ 0.8 就强制归并**过去。
- **保持现有 `reuseTopic` 不动**（不重写为召回式）：稳定、线上用过。`topic_namer` 不再自己单独实现一套归并（否则会拿 summary 向量、`reuseTopic` 拿 topic 向量，两次不同判定）。
- （0.8 这个阈值现在就有，配置项 `memory_topic_reuse_threshold`，不用新加。）

**如果 LLM 给的不合规（超 5 字 / 空 / 含非中文）怎么办？**
**重试 2 次**；还不行就机械截断到 5 字（只留汉字）。若截断后仍为空（原始 topic 和 summary 全为英文/数字），用固定保底 `其它`。**必须落到一个非空 topic**，因为 topic 是必填，不能因此不存这条记忆。

> 注意：这 2 次重试发生在 **topic 单独调用内部**（`topic_namer` 自己的小循环），**不占决策循环的轮数预算**。

### 4. 一个时序上的注意点（写代码时要小心）

已有 topic 的向量，memory 这边是**写记忆时当场同步写好**的；但 note 那边是**异步**写的（`tool/note.go` 里起了 goroutine），所以 note 的 topic 向量可能晚一点才出现。

不过不用担心：取向量用的是 `GetOrEmbed`，**缓存里没有就当场补算**。所以最多只是"晚一点"，不会漏。写代码时知道这点就行。

### 5. 提示词、工具描述同步改

让 AI 知道新规矩，否则它不知道有 5 字限制：

- 决策循环里 `save_memory` / `update_memory` 的 topic 说明改成「≤ 5 字、非空、纯中文」。
- 记忆定义的公共文案里补一句「topic ≤ 5 字、纯中文」。
- inline 的 `record_memory` 工具同步 + 加非空校验。
- 抽取段指令消息、决策循环 system/instruction 都要带上「≤ 5 字、纯中文」（见上面第 1 节）。

### 6. ReAct 轮数预算：6 → 8

决策循环现在配置是 `record_loop_max_iterations: 6`（`conf/config.yaml:9`），其中主循环跑 5 轮，第 6 轮是**强制收敛轮**（不给工具，必须出结果）。

算一下正常要几轮：

| 场景 | 轮数 |
|---|---|
| 正常（看一眼近邻直接写） | 1~2 |
| 需要先补一轮检索 | +1 |
| 工具校验打回 1 次 | +1 |
| 打回 + 检索 + 改写叠加 | ~6 |

**6 轮会在"打回 + 补检索"时提前耗尽**——一旦进入强制收敛轮，就不给工具了，等于逼它放弃写入，这条记忆就丢了。

**改成 8 轮**，留出「正常 2 + 重试 2 + 检索 1 + 缓冲 2」的余量：

- `conf/config.yaml` 的 `record_loop_max_iterations` 6 → 8
- `internal/config/config.go` 的默认值同步 6 → 8

（topic 本身的 2 次重试在 `topic_namer` 内部，不占这个预算。）

---

## 四、已经写坏的怎么办（一次性清洗）

写一个小工具，把存量的空、超 5 字、含非中文的 topic 收拾干净。做法**照抄现有的 `topic-merge` 工具**（两步走：只读生成 → 人工审 → 落库）。**只在本地跑**（远端版即将下线）。

### 1. 清洗规则

| 类型 | 怎么处理 |
|---|---|
| 超 5 字 / 含非中文（如 `OpenMuse竞品调研`） | 调 LLM 给一个 ≤5 字纯中文短名 |
| 空的 | 调 LLM 看着记忆内容起一个 ≤5 字纯中文名字 |

- **不做机械截断**。既然都要调 LLM 了，就让 LLM 直接给个好名字，人再在表里过一眼、改一改就行。
- LLM 失败或给的名字也不合规 → 这一行标成 `skip`，留给人手填。

### 2. 调 LLM 的方式

**按用户整批提交**：把同一个用户所有要改的 topic 一次性给 LLM，同时把"这个用户已有的 topic"也一并给它作参考。
好处：同用户的 topic 本来就相关，一起给还能顺带发现"这俩其实是一回事、该合并"。

### 3. 工具长什么样

照 `topic-merge` 的范式，新增一套：

- 代码：`internal/topicclean/`（生成 + 执行）+ `cmd/topic-clean/main.go`
- 产出：`data/topic-clean/report-*.md`（看得懂的报告）+ `plan-*.tsv`（可以手改的表）
- 表里每行：`原 topic | 字数 | LLM 候选 | 动作`
- 参数：`--user`（只处理某个用户）、`--apply`、`--dry-run`（默认只打印不落库）、`--force`

### 4. 执行时的数据安全

- 先用**只读模式**跑一遍，生成报告和表 → 你审、你改。
- 确认后 `--apply`，**先 dry-run 打印将改动**，再真正落库。
- 本地跑，`--dry-run` 先看将改动；确认后再真正落库（本地服务的向量会在重启后重载）。
- 数据库层面用事务，所有改名都是批量 `IN` 操作，不会一条条循环（不做 N+1）。

### 5. 要不要动向量？（结论：不用）

查过了：记忆的向量检索里，**topic 根本不在持久化的向量数据里**（只有"补建缺失向量"时才会写进去，正常都读不到）。topic 只用于展示时的 `[主题]` 前缀。

所以**清洗只改数据库的 topic 就够了**，重启服务时向量会自动重载，不会对不上。不需要额外重算向量。

### 6. 配套命令

`Makefile` 加两个目标 `topic-clean-plan` / `topic-clean-apply`，本地直接 `go run ./cmd/topic-clean/`（不再走远端 scp/ssh）。

---

## 五、怎么验证

1. `cd server && go build ./... && go test ./...` 全绿（新增针对性的单测）。
2. 清洗工具先在数据库快照上 `--dry-run`，看报告和表对不对，再真机落库。
3. 落库后重启服务，跑一次聊天：
   - 新产出的记忆 topic 都非空、都 ≤ 5 字、都纯中文。
   - 被清洗过的记忆在记忆页正常显示。
   - 日志没有相关报错。

---

## 六、已拍板的决定

1. **topic 调用放在决策循环前面**——先定好 topic，决策循环里拿到的就是最终值。
2. **LLM 给的名字不合规时**：重试 **2 次** → 还不行就截断到 5 字，仍为空则固定保底 `其它`。（第三节已按此写。）
3. **近邻给 10 个已有 topic**（约 50 字，可接受），每个都带**引用次数 + 相似度**；**按相似度取 top-10、按引用次数展示**。
4. **存量清洗界线 = 「空 / > 5 字 / 含非中文」**：与新写规则（非空、≤5 汉字、纯中文）**完全一致**，三类脏数据一起收拾。
5. **决策循环轮数 6 → 8**（见第三节第 6 点）。
6. **修 update 抹空 bug 必须补单测**（见第三节第 2 点）。

---

## 七、涉及的文件（给实现时参考）

**新写入（代码侧）**
- `server/internal/util/string.go` — 新增 `IsAllHan` / `HanOnly`（纯中文判定，agent 与 topicclean 共用，避免两处分叉）
- `server/internal/agent/record_loop_params.go` — 上限改 5、加非空校验、`keepOrReplaceTopic`；`isAllHan` 委托 `util.IsAllHan`
- `server/internal/agent/record_loop_write.go` — 修 update 抹空 bug（+ 单测）
- 新文件 `server/internal/agent/topic_namer.go` — 单独调 LLM 定 topic（近邻按相似度取 10、按引用次数展示；重试 2 次 → 截断 → 固定保底 `其它`；0.8 归并交给落库时的 reuseTopic）
- `server/internal/agent/record_loop_persist.go` — 保持 `reuseTopic` 不动，新增 `distinctTopicsWithCount` 供近邻
- `server/internal/agent/record_loop.go` — `Decide` 里先调 `nameTopic`；`preRetrieve` 返回 summary 向量复用
- `server/internal/agent/extractor.go` — 抽取段删掉 topic 输出
- `server/internal/agent/memory_worker.go` — 不再传 topic
- `server/internal/agent/record_loop_tools.go` — topic 描述改 ≤5 字、纯中文
- `server/internal/tool/record_memory.go` — inline schema 同步
- `server/internal/agent/promptbuilder/memory_definition.go` — 补「≤5 字、纯中文」
- `server/conf/config.yaml` + `server/internal/config/config.go` — 决策循环轮数 6 → 8

**存量清洗**
- 新目录 `server/internal/topicclean/`（`run.go` / `plan.go` / `plan_test.go`）
- 新文件 `server/cmd/topic-clean/main.go`
- `server/Makefile` — 加 `topic-clean-plan` / `topic-clean-apply`（本地 `go run`，默认 `APP_ENV=local.dev` **dev 沙盒库**）
- `server/internal/service/{user_memory_repo,note_repo}.go` — 各加 `DistinctUserIDs`（工具枚举用户用）

**测试**
- `server/internal/agent/record_loop_test.go` — 空/超长/含英文 topic 校验、update 保留旧 topic、clipTopic 剥离非中文
- topic_namer 的单测（重试 2 次 → 截断、validTopic 纯中文判定）
- `server/internal/topicclean/` — validate/findViolations/plan 往返 + Generate→Apply 端到端（SQLite）
