# 退役 notes / memories 的 tags — 设计文档

> 版本：v1｜日期：2026-09-22｜状态：设计定稿，待开发（**另起一票**，不并入首批）
>
> 上位：《记忆 / 笔记改造 · 设计文档》（`design.md`）§4.5（原文为「tags 不动」，本文改为「退役」）
> 估算（开工）：1 面 / 0 缝 / 1 次真机往返 / 1 个待拍决定 · ~1.5–2h
> 估算（定稿）：1 面 / 0 缝 / 1 次真机往返 / 0 个待拍决定 · ~1.5–2h
>
> 备注：本票范围小、开工即定稿，两行估算一致；唯一那个待拍决定（关键词检索是否继续 LIKE tags）在开工时当场拍掉，故定稿记为 0。

---

## 1. 背景：为什么退役

`notes.tags` / `user_memories.tags` 是 LLM 自由填的逗号分隔关键词，与 `topic` 语义重叠。翻代码确认，它**只有三个弱用途**：

1. **theme 发现的弱信号**：`get_topic_stats.go:161,184` 把 cluster 内 notes/memories 的 tags 汇总成 `stats["tags"]`，`theme_discover.go:43` 的 prompt 提示「tags 丰富的 cluster 多看看」。
2. **关键词检索顺带 LIKE**：`note_repo.go:105`（List 三路 OR）、`:179`、`:223`（`OR tags LIKE`）。**记忆侧根本不查 tags**（`user_memory_repo.go` 无任何 tags 条件）。
3. **纯展示**：前端 `NotesView.vue:125` / `MemoryView.vue:122`，Swift 4 个视图。

另外 **tag 概念本身早已是死的**：

- `EnsureTagsEmbedded` 是空实现（`concept_embedder.go:35-38`），调用点仍在但空转（`tool/note.go:576`、`agent/extractor.go:216`）。
- 启动时有一次幂等清理，删掉所有 `concept_embeddings.type='tag'` 及其向量行（`appinit/agents.go:316-323`）。

**关键判断**：topic 收敛（`topic-merge.md`）之后，topic 天然对应 theme；用途 1 不再需要。用途 2、3 都是弱信号/展示。至于「同一 topic 下用不同 tag 区分不同记忆」这个假想用途——**没有明确目的，放弃**。于是 tags 在 notes/memories 上彻底没有消费者 → 退役。

> `documents.tags` 是另一套完整体系（多标签交集筛选、词云、共现图），**完全不动**。

## 2. 决定（已拍板）

| # | 决定 | 选择 |
|---|---|---|
| D1 | DB 列 | **停写，保留列**。历史数据留在库里（不再有人读），零迁移风险，不 drop column |
| D2 | 展示 | **保留历史展示**。前端 / Swift / API 响应 / model 字段全不动 |
| D3 | tag 概念残骸 | **本票一起埋**（删空接口与空实现，保留启动清理） |
| D4 | 归属 | **另起一票**，不并入首批 ③④ |
| B3 | 关键词检索 | **去掉 `tags LIKE`**（否则「历史数据没人读」不成立） |

**D1(a) + D2(b) 的直接结果：本票是纯后端改动。** 前端、Swift、controller、model 字段、`eval/export.go` 一律不动。

## 3. 范围

**改**：写入侧停止产生 tags；检索/统计侧停止读取 tags；删掉 tag 概念残骸代码。

**不改**：

- `model/note.go:18`、`model/user_memory.go:14` 的 `Tags` 字段（列保留 → API 响应照旧带 tags）。
- 所有 controller / `/api/notes`、`/api/memory` 响应。
- 前端 `NotesView.vue`、`MemoryView.vue`；Swift `NoteListView` / `NoteDetailOverlay` / `NotePopupView` / `MemoryListView` / `MemoryDetailOverlay` / `APIClient`。
- `eval/export.go:95,103` 的 `COALESCE(tags,'')`（列还在）。
- `appinit/agents.go:316-323` 的一次性清理（这是清历史 tag 概念/向量行的唯一落点，不能删）。

## 4. 详细改动清单

> **前置依赖**：本清单的「停止写入」落在**旧的**提取路径（`agent/extractor.go` 的 `extractResult`）与 `tool/note.go` 的 `record_note` 上。若「记录循环」（`design.md` §4.2）先落地，`extractor.go` 的提取段会被循环取代，届时「停止写入」的落点改为循环的 `save_memory` / `save_note` 工具参数（照样不带 tags）。「停止读取」与「埋残骸」两节不受影响。

### A. 停止写入

**`agent/extractor.go`**
- 删 `extractResult.Tags` 字段（`:30`）。
- 删 `OutputFormat` 里 tags 指令与第二行 tags 说明（`:53-54`）。
- 删 `model.UserMemory{... Tags: result.Tags}` 赋值（`:152`）。
- 删 `EnsureTagsEmbedded` 调用块（`:215-217`）。

**`tool/note.go`**
- `record_note`：schema 删 `tags`（`:118`）；`recordNote` 参数结构删 `Tags`（`:270`）+ 赋值（`:288`）。
- `update_note`：schema 删 `tags`（`:177`）；`updateNote` 参数结构删 `Tags`（`:444`）；调用 `noteRepo.Update` 的 tags 实参改 `""`（`:481`）；删 tags 的 fallback 逻辑（`:491-493`）。`""` 语义是「不修改」，历史 tags 保留。
- `embedAll`：删 `EnsureTagsEmbedded` 调用块（`:575-577`）。
- **保留**输出里的 `"tags"`（`:329/:465/:533/:831`）——`get_note`/`rollback` 等仍回显历史值，配合 D2(b)。

### B. 停止读取

**`service/note_repo.go`**
- `List` 的 keyword OR 去掉 `tags LIKE`（`:105`，保留 content / topic）。
- `SearchNotesByCursor` 去掉 `OR tags LIKE`（`:179`）。
- `CountNotes` 去掉 `OR tags LIKE`（`:223`）。

**`tool/get_topic_stats.go`**
- 删 tags 聚合（`:145`、`:161-163`、`:184-186`、`:195-199`）。
- `Functions()` 的 Description 去掉 `tags`（`:40`）。

**`agent/theme_discover.go`**
- prompt 检索策略去掉「tags 丰富」（`:43`）。

### C. 埋 tag 概念残骸

- `app/interfaces.go`：`ConceptEmbedder` 删 `EnsureTagsEmbedded`（`:20`），注释「概念（topic / tag）」改「概念（topic）」。
- `agent/concept_embedder.go`：删 `EnsureTagsEmbedded` 空实现（`:35-38`）。
- `tool/note_test.go:309`：删 mock 的 `EnsureTagsEmbedded`（接口改了会编译失败）。
- `appinit/agents.go:316-323`：**保留**。

### D. 文档

- 本文件。
- `design.md` §4.5 由「tags：不动」改为「tags：退役」，指向本文；§3 非目标、§9 收尾处同步。

## 5. 风险

1. **legacy 关键词召回变窄**：去掉 `tags LIKE` 后，老笔记按 tag 词搜不到。判断：tags 与 topic/content 语义重叠，损失可接受；历史 tag 仍在卡片上可见。
2. **展示与写入不一致**：老数据有 tag、新数据没有。这是 D1(a)+D2(b) 的**有意结果**（渐进退役），非 bug。
3. **接口删除波及面**：`ConceptEmbedder` 是跨包接口（`tool` 依赖 `app`），删方法要同步改实现与 mock，编译期即可发现，风险低。

## 6. 验收

- `gofmt -l` 干净；`go build ./... && go vet ./... && go test ./...` 全绿。
- 真机跑一次聊天：
  - 新建笔记 / 触发记忆提取 → 落库 `tags` 为空。
  - 老笔记 / 老记忆在笔记页、记忆页仍显示历史 tags。
  - `search_notes` / `count_notes` 按关键词不再命中 tags。
- `get_topic_stats` 返回里不再有 `tags` 字段；日志无相关报错。
