# topic 收敛 — 设计文档

> 版本：v1｜日期：2026-09-22｜状态：已完成（真库已执行）
>
> 上位：《记忆 / 笔记改造 · 设计文档》（`design.md`）§4.4
> 估算：2 面 / 1 缝（1 需探针）/ 1 次往返（审阅合并报告）/ 0 待拍决定 · ~2–4h

## 1. 背景

topic 由 LLM 自由填，两边都没有相似度约束，结果是一个事散成好几个 topic。真实数据（dump `xhan_helper-20260922-114147.sql`）：

- `concept_embeddings` 里 topic 类型 **750 个**；
- 笔记（存活 1010 条）175 个 topic、`user_memories`（837 条）**495 个 topic**，合计在用 **606 个**。

用真实向量算聚类（`util.ClusterVectors`，阈值 0.80）：750 → 513 簇；阈值 0.70 → 224 簇；0.60 → 71 簇。0.80 下已能看到的合并组：

```
睡眠(185) | 睡眠管理(103) | 睡眠与作息 | 睡眠质量 | 睡眠习惯
职业规划(53) | 职业决策(47) | 人生规划(13) | 职业发展(9) | 求职决策 | 定居决策 | …（21 个成员）
记账(16) | 消费管理(12) | 消费记录(10) | 日常记账(4) | 记账管理(3) | …
求职-面试(22) | 求职面试(20) | 求职(16) | 面试准备(6) | 面试复盘 | …
```

## 2. 目标

1. **存量**：把同义 topic 合并成一套规范词（一次性的数据迁移）。
2. **增量**：今后写入时把新 topic 与已有 topic 比相似度、太相似就复用旧的（`design.md` §4.4，**本批不做**，并入后续记录循环阶段）。

本篇只覆盖存量的那一步——唯一需要写代码的部分，落成 `server/cmd/topic-merge/`。

## 3. 为什么用 leader 聚类，不用 `ClusterVectors`

`util.ClusterVectors`（`internal/util/cluster.go:9`）是并查集：只要 A~B、B~C 各自超阈值，A、B、C 就并成一个簇，**即使 A 与 C 并不相似**。用来「发现」尚可，用来「决定合并」会造出巨簇（上面 `职业规划` 那 21 个成员就是这么来的，里面已有 `人生规划`/`定居决策` 这种偏得较远的）。

改用 **leader 聚类**：每个成员必须**与簇代表**相似度 ≥ 阈值才并入，不对成员之间传递。宁可分得细、人工再合，也不要把不相关的并掉。

## 4. 设计

命令 `server/cmd/topic-merge/`，复用 `internal/config` + repo 层。两步，默认只读。

### 4.1 第一步：出方案（默认，不写库）

1. 载入 topic 概念（`ConceptEmbeddingRepo.ListByType`）+ 向量（`EmbeddingRepo` 按 `EntityTypeConcept`），对齐 `get_topic_stats.go:52-102` 的现成加载方式。
2. leader 聚类，阈值默认 `0.80`；报告里同时标出 0.60 / 0.70 / 0.80 下的差异供参考。
   （初稿定的是 0.70，对齐 `reflection.cluster_threshold`；实测 0.70 会误并——`生育决策` 吞了住房/生活/购物决策、`工作习惯` 吞了消费/娱乐习惯——故改 0.80。）
3. 每簇选**代表词**：引用数（notes + memories 计数）最高者；并列取字符最短。
4. 每个成员标注与代表词的相似度，便于人工判断边缘成员。
5. 输出报告（Markdown + 可编辑的映射文件，如 `topic-merge-plan.tsv`）：`簇 → 成员(引用数, 相似度) → 建议代表词`。人工过一遍，可改代表词、可拆簇、可整簇跳过。

### 4.2 第二步：执行（`--apply`，读人工确认后的映射）

事务内批量：

1. `UPDATE notes SET topic = 代表词 WHERE topic IN (成员...)`。
2. `UPDATE user_memories SET topic = 代表词 WHERE topic IN (成员...)`。
3. `concept_embeddings`：删成员行、保留代表词行（唯一键 `(type,value,user_id)` 决定必须去重）。
4. 同步删被删成员的 `embeddings`(concept) 行（`EmbeddingRepo.DeleteByEntities`）。

要求：

- **`--dry-run` 默认开**：先打印「将改 X 条 notes / Y 条 memories / 删 Z 个概念行」，确认后才落库。
- **幂等**：重跑不产生新差异。
- **不改 `theme_state`**：theme 已挂起，本次不动（记住这条）。

### 4.3 顺带：孤儿 topic 概念 GC

引用清零的 topic 概念行（没有 notes / memories 再引用它）在这里一并清掉——包括两类：合并后腾空的旧概念、以及历史遗留的「0 引用」概念（数据里能看到 `睡眠管理 追踪睡眠记录与优化实验` 这类复合名、引用数为 0 的行）。

先统计 notes.topic ∪ user_memories.topic 的现存集合，凡 `concept_embeddings`（type=topic）中不在集合内的，删概念行 + 对应 `embeddings` 行。同样 `--dry-run` 先行。

> 这也是 bug `017` 里那条边界（topic 概念行共享、不做 per-delete）的落点。

## 5. 执行前置

- 备份：先 `mysqldump` 一份。
- 停服执行（避免内存向量库与库不一致）；完事重启。
- 完事出一份数据库 diff（改动行数 + 前后 topic 分布 top 表）给用户复核。

## 6. 验收

- `go build ./...` 通过；聚类与代表词选择的纯逻辑加单测（给定向量 → 期望分簇 / 代表词）。
- 真机/真库：dry-run 报告人工审阅通过 → `--apply` → 复查 notes/memories topic 分布、概念表无孤儿。
- **缝**：合并结果是否符合语义（阈值合适与否）只能人工看报告判断，需一次审阅往返。若差异大，调阈值重出报告。

**已验收（2026-09-22）**：全绿 + 真库执行 + 复查无孤儿，结果见 §7.2。

## 7. 实现（已落地）

包结构：`internal/topicmerge/{cluster.go, plan.go, run.go}` + `cmd/topic-merge/main.go`（薄入口，参照 `cmd/eval-export`）。
DB 操作走 `service` repo（新增 `NoteRepo.RenameTopic`、`UserMemoryRepo.RenameTopic`、`ConceptEmbeddingRepo.DeleteByValues`；向量删除复用 `EmbeddingRepo.DeleteByEntities`）。

```bash
go run ./cmd/topic-merge                             # 只读：产出报告 + 映射文件
go run ./cmd/topic-merge --user <user-id>             # 只处理某用户（支持前缀）
go run ./cmd/topic-merge --apply                     # dry-run，打印将改动
go run ./cmd/topic-merge --apply --dry-run=false     # 落库
```

- 输出每用户一份：`<out>/report-<short8>.md`、`<out>/plan-<short8>.tsv`（默认 `data/topic-merge/`）。已存在的 plan 不覆盖（`--force` 覆盖）。
- 映射文件 `plan-*.tsv`：`cluster / representative / member / ref_count / similarity / action`。人工可改 `representative`、把某行 `action` 改成 `skip`、或删行。`representative` 必须是本簇已有成员（不支持造新词，避免产生无概念行）。

相对设计的两点细化：

1. **0 引用簇不进报告/映射**：代表词引用为 0 的簇全是历史遗留 topic（整句/复合名等），合并无意义，统一交给 apply 的孤儿 GC 清理；报告里单列「0 引用孤儿」段。
2. **dry-run 与落库同一套事务逻辑**：dry-run 在事务内执行后回滚，因此打印的「notes 改名 / memories 改名 / 删概念 / 删孤儿」与真实落库完全一致（同一用户合并 + GC 在一个事务内）。

单测（`cluster_test.go`）：leader 聚类并入、**非传递性**（A~B、B~C 但 A 不~C 时 C 不入簇）、代表词 tie-break（引用数 → 词长 → 字典序）、映射文件往返解析 / skip / 冲突 / 缺 user_id、0 引用簇不进映射。

### 7.1 对远端真库执行（编译 + scp 远端跑）

真库在远端，工具在**远端**执行（本地交叉编译 → scp → ssh 跑）。Makefile 新增（见 `build-topic-merge` / `topic-merge-plan` / `topic-merge-apply` / `restart`）：

```bash
make topic-merge-plan                              # 远端只读生成，报告/映射拉到本地 data/topic-merge/
# 本地审阅 report-*.md、编辑 plan-*.tsv
make topic-merge-apply                             # 推回映射，远端 dry-run
make topic-merge-apply ARGS="--dry-run=false"      # 落库（自动停服 → 执行 → 起服）
```

远端执行由 `scripts/run-topic-merge.sh` 负责：source `~/.zshrc` 等（非交互 ssh 不会自动 source）、`APP_ENV=prod`；**仅当参数含 `--dry-run=false` 时**才停服/起服（dry-run 不动服务）。先 `make db-pull` 备份。

> 重新生成报告/映射：远端已存在 `plan-*.tsv` 时默认跳过（防止覆盖人工编辑），需 `make topic-merge-plan ARGS="--force"`。改阈值同理 `ARGS="--threshold 0.70 --force"`。

### 7.2 真库执行结果（2026-09-22，阈值 0.80）

- 5 个用户；主用户 662 topic → 505 簇（多成员 91、改名 153）。
- 全库：**notes 改名 203、memories 改名 180、删概念 160、删孤儿 131**；`concept_embeddings` type=topic **757 → 466**，孤儿 **0**。
- 人工 `skip` 2 项：`家庭财务`（避免「窄词吞宽词」）、`生活观察`（与 `社会观察` 不同义）。
- 0.70 对比：342 簇 / 118 多成员 / 311 改名，含明显误并；0.80 干净得多，代价是少数合理合并被拆散（可在 plan 里手工改 representative 合回）。
