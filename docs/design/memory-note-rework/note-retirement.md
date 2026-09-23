# 笔记（notes）渐进退役 — 设计文档

> 版本：v1｜日期：2026-09-23｜状态：设计定稿，待开发
>
> 上位：《记忆 / 笔记改造 · 设计文档》（`design.md`）§3 / §5
> 相关：`record-loop-prompt.md`（记录循环的单一实体契约）、`tags-retirement.md`（同样走「停写、保留数据」的路子）
> 估算：阶段 0 已计入记录循环那批（~2 面）；阶段 1 无额外代码；阶段 2 / 3 另起一票

## 1. 背景：笔记与记忆本来是一个东西

翻代码 + 对着设计过了一遍，结论是：**`notes` 与 `user_memories` 记的是同一类东西**——用户透露的重要内容（决定、结论、洞察、观点、状态、人物关系、偏好、经验教训）。两层唯一的区别是**写入时机**：笔记在对话进行中当场写，记忆在会话结束后事后提炼。

时机不同确实带来过一批工程差异（当场写需要版本快照、软删、可编辑、更高的注入预算），但这些差异**不是「两种东西」的理由**，只是「两种入口」的后果。

而它们不同构带来的成本是实打实的：

- 一套决策闸门要跨两域互斥（「记忆让位笔记」这条规则就是为两张表可能写重复发明的）；
- topic 收敛要管两域（`topic-merge` 报告里 notes/memories 分开统计）；
- 检索注入两套预算（笔记 3000 / 记忆 600，`ctxflow/config.go:17-18`）；
- 前端、Swift、API 各一套视图与端点。

所以决定：**收敛到单一实体（`user_memories`），笔记停写、逐步清空。**

## 2. 决定（已拍板）

| # | 决定 | 选择 |
|---|---|---|
| D1 | 新写入 | **停写**。`record_note` 退役，主循环改用 `record_memory`（走记录循环，写 `user_memories`，`source=inline`） |
| D2 | 旧数据 | **不迁移**。1010 条存量笔记留在 `notes` 表，照旧可读、可检索注入 |
| D3 | 旧数据清理 | **不设时间硬删**。只靠「闸门顺带」（遇到重复就删）+ 「存量梳理」票批量消化 |
| D4 | 过渡期 note 工具 | **只留读 + 删**：`semantic_search` / `count_notes` / `get_note` / `query_notes` / `list_topics` / `delete_note`；去掉 `record_note` / `update_note` / `rollback_note` / `get_note_versions` |
| D5 | 读侧与展示 | **本批不动**。`note_source`、`NotesView.vue`、Swift 笔记视图、`/api/notes` 全部保留到数据清空 |
| D6 | 注入预算 | **本批不合并**（笔记 3000 / 记忆 600 并存），等清空后再并 |
| D7 | 归属 | 阶段 0 并入记录循环批；阶段 2 / 3 另起一票 |

**为什么不做时间硬删**：存量 1010 条里很多是**长期结论**（不是逐日流水），按 `created_at` 硬删会误杀。宁可留着一批只读的旧数据，也不要丢信息。

## 3. 四个阶段

### 阶段 0：停写（本批，随记录循环）

| 位置 | 改动 |
|---|---|
| `tool/note.go` | 去掉 `record_note` / `update_note` / `rollback_note` / `get_note_versions` 四个函数的 schema 与 `Execute` 分支 |
| `agent/promptbuilder/static.go:14` | 准则 4 的措辞：从「调用 note 记录」改为「调用 `record_memory` 记录」 |
| 主循环工具目录 | 由 registry 自动反映（`record_note` 不再出现） |
| `record-loop-prompt.md` | 已含「旧笔记（过渡期）」一节，告诉模型不要写旧笔记 |

**不做**：`tool/note.go` 里 `recordNote` / `updateNote` 的函数体可以先留着（无调用），等阶段 3 一起删——避免本批 diff 过大。

### 阶段 1：闸门顺带清理（本批，零额外代码）

记录循环的预检索**跨 note + mem 两域**，旧笔记会出现在 `<neighbors>` 里。模型看到「这条旧笔记与我要写的记忆是同一件事」时调 `delete_note`。

机制由三样现成的东西拼成，**不需要新代码**：

1. 预检索跨域（`design.md` §4.2）；
2. `delete_note` 已下发（`record-loop-prompt.md` §3.1）；
3. 指令里有「旧笔记（过渡期）」一节明确授权。

删除走 bug017 修好的完整清理（`Embeddings` / `EmbeddingChunks` / 内存向量 / `ContentVersion`），所以不留孤儿。

### 阶段 2：存量消化（另起一票）

并入 `design.md` §8 已经列着的**「存量梳理」**票（原话：「非流水的重复/过时记忆，从后往前捋一遍」）。本票给它补一条范围：**旧笔记一并消化**。

- 逐条判断：已被记忆覆盖 / 已过时 → 删；仍有长期价值 → 提炼成记忆后删。
- 判断规则沿用 `ledger-migration.md` §3 的精神：**不确定就保留**。
- 出一份 diff（消化了多少、删了多少、新建记忆多少）供复核。

### 阶段 3：退役读侧与展示（数据清空后）

| 位置 | 改动 |
|---|---|
| `agent/retrieval/note_source.go` | 移除检索源；`appinit/agents.go:30` 的构造与 `:42` 的 Warmup 一并删 |
| `agent/ctxflow/config.go` | 去掉 `Note` 源；`retrieval.ResourceZone` 的 `BudgetNote` / `QuotaNote` 并入 memory（`config.go:264-269`） |
| `tool/note.go` | 整个工具退役（含 `recordNote` / `updateNote` 函数体） |
| `service/note_repo.go` / `controller/note.go` / `model/note.go` | 退役；`/api/notes` 三个端点下线（`router.go:105-107`） |
| `model/content_version.go` 的 note 分支 | 退役（`VersionRepo` 保留给 document / prompt） |
| `internal/entitygc/gc.go` 的 note 部分 | 退役（`SoftDeletedNotes` / `NoteEmbeddings` / `NoteVersions`） |
| `tool/get_topic_stats.go` | 去掉 notes 聚合（`:141-149`、`:191` 的 source 计数） |
| `tool/unified_search.go` | 去掉 note 域（`semantic_search` / `keyword_search` 的 types 里去掉 `note`） |
| `front-end/src/views/NotesView.vue` + `router/index.js` | 退役 |
| Swift：`NoteListView` / `NoteDetailOverlay` / `NotePopupView` / `NoteViewModel` / `APIClient` | 退役 |
| `eval/`（`export.go` / `repo.go` / `schema.go`）与 `controller/eval_admin_handler.go` | 去掉 note 相关导出/标注 |

## 4. 风险

1. **旧笔记信息丢失**：不迁移 + 靠清理消化，可能有些旧笔记在清空前就从没被检索命中过。缓解：阶段 3 只在**数据清空后**才动手；清空的前提是阶段 2 逐条消化过。
2. **模型误删旧笔记**：闸门顺带清理依赖模型判断。缓解：指令里写了「拿不准就留着，不要为了清理而删」，且 `delete_note` 走**软删**（`note_repo.SoftDelete`，`notes.status=2`）——行本身可恢复；但派生数据（向量 / chunk / 版本快照）会被 `EntityPurger` 清掉，恢复后需重建。
3. **过渡期两套并存**：旧笔记与新记忆可能同时被注入，出现重复信息。这是**有意的过渡状态**，不是 bug。
4. **注入预算失衡**：笔记 3000 / 记忆 600，笔记清空后记忆承载全部，600 会不够。阶段 3 必须一起调。
5. **`source` 取值扩展**：`conversation` 拆成 `inline` / `post_chat`，历史行的 `conversation` 不迁移——查询侧要能容忍三种值。

## 5. 验收

**阶段 0**：

- `gofmt -l` 干净；`go build ./... && go vet ./... && go test ./...` 全绿。
- 真机跑一次聊天：
  - 对话中说一件值得记的事 → 落 `user_memories`（`source=inline`），`notes` 表**不新增**；
  - 主 LLM 的工具目录里不再有 `record_note`；
  - 旧笔记仍能被 `semantic_search` / `query_notes` 搜到、仍会被检索注入。

**阶段 1**：真机上让模型遇到一条与旧笔记重复的新信息 → 应调 `delete_note` 删掉旧笔记，且删除后 `embeddings` / `embedding_chunks` 无残留（重启后检索不再命中）。

**阶段 3**（另票）：`notes` 表清空、`/api/notes` 下线、前端与 Swift 笔记视图移除、注入预算合并。
