# 017 删除笔记 / 记忆后向量、chunk 与版本快照残留

## 问题表现

删除一条笔记或记忆后，实体行没了，但**派生数据还在**：它自己的向量行、句级 chunk、以及内存向量库里的条目。表现是：检索仍能命中已删内容（`note:<id>` / `mem:<id>` / `chunk:note:<id>/<i>`），数据库里留下指向已删实体的孤儿行。

## 各删除入口的漏项

涉及四类派生数据：实体自身的 `embeddings` 行、`embedding_chunks` 行（+ 对应的 chunk 向量行）、内存向量库条目（full 用 `note:<id>` / `mem:<id>`，chunk 用 `chunk:<type>:<id>/<i>`）、版本快照 `content_versions`。

| 数据 | note 工具删除<br>`tool/note.go:427` | note 接口删除<br>`controller/note.go:62` | memory 单删<br>`controller/memory.go:56` | memory 批删<br>`controller/memory.go:82` |
|---|---|---|---|---|
| 实体行 | 软删 ✅ | 软删 ✅ | 硬删 ✅ | 硬删 ✅ |
| `embeddings` 全文行 | ❌ | ❌ | ❌ | ❌ |
| `embedding_chunks` 行 | ✅ `cleanChunks` | ❌ 连 chunk 都没清 | ❌ | ❌ |
| `embeddings` chunk 行 | ❌ | ❌ | ❌ | ❌ |
| 内存 full 向量 | ❌ | ❌ | ✅ | ✅ |
| 内存 chunk 向量 | ⚠️ 键用错，等于没删 | ❌ | ❌ | ❌ |
| 版本快照 `content_versions` | ❌（`VersionRepo.DeleteByEntity` 存在但从未被调用） | ❌ | 无 | 无 |

## 根因

删除逻辑是「各入口各写各的」，没有一层统一的派生数据清理。`cleanChunks`（原 `tool/note.go:603`）问题尤其典型：它只覆盖 chunk，且只在那一个入口被调用；而它对内存向量调的是 `vectorStore.Delete(c.ID)`——`c.ID` 是 chunk 行的 uuid，**内存里的键却是 `chunk:<type>:<id>/<i>`**（`agent/chunk_builder.go:102`），等于一个都没删。`controller/note.go` 甚至连 chunk 都没清。记忆侧只有一行 `vectorStore.Delete("mem:"+id)`，DB 里的 `embeddings`、chunk、chunk 向量全留着。

## 修法

抽一个共享清理件 `service.EntityPurger`（`internal/service/entity_purger.go`），统一做这些事：

```go
// Purge：全文向量行 + 句级 chunk（chunk 行 / chunk 向量行 / 内存 chunk 向量）+ 内存全文向量 + 版本快照
// PurgeChunks：只清 chunk（用于「更新后重建 chunk」）
```

关键点：

- chunk 的内存键按 `chunk:<entityType>:<entityID>/<ChunkIndex>` 构造（从 chunk 行的 `chunk_index` 拼），不再误用行 uuid。
- chunk 向量行用精确键列表删除（`DeleteByEntities`），不用 `DeleteChunksByParentType`（那会删掉整个 parent 类型的全部 chunk）。
- 顺带修掉从未被调用的版本快照清理。

接到四个删除入口：`tool/note.go` 删笔记、`controller/note.go` 删笔记、`controller/memory.go` 单删 + 批删；笔记本体的更新路径用 `PurgeChunks` 重建 chunk。批删走同一件、按 ID 循环。清理器在 `appinit` 构造一次，经 `NoteTool` 与两个 handler 注入。

清理**不影响主流程契约时**（例如内存向量 delete 失败）就地 `log.Printf` 继续；DB 行删除失败则向上抛，保留错误链。删除动作本身不因清理失败而回滚——实体已删，残留留待重跑清理（幂等）。

## 边界：`concept_embeddings` 的 topic 行不在这里删

`concept_embeddings` 的唯一键是 `(type, value, user_id)`（`model/concept_embedding.go:8-10`），**跨笔记/记忆共享**，`parent_id` 只是「首次来源」的追溯信息。删一条笔记就把 topic 概念行删掉，会误伤同一 topic 下的其他记录。所以：

- **不做** per-delete 删除；
- 它的真实问题是「引用清零后没人回收」→ 归入**孤儿 GC**（见 `docs/design/memory-note-rework/topic-merge.md`，概念表清理那里顺带处理）。

## 验证

- 单测 `TestNoteSoftDelete_PurgesDerivedData`（`internal/tool/note_test.go`）：删除后 chunk 行、chunk 向量行、全文向量行、版本快照均清零；内存向量删除 `chunk:note:n1/0`、`chunk:note:n1/1`、`note:n1` 三项。
- 真机：删一条笔记、一条记忆 → 重启后检索不再命中；SQL 查 `embeddings WHERE entity_id IN (...)` 为空。

## 存量残留的一次性清扫：`cmd/entity-gc`

本 bug 修的是**今后**不再产生孤儿；此前用旧代码删掉的笔记/记忆，其派生数据仍在库里。补一个一次性工具清扫：

- `internal/entitygc` + `cmd/entity-gc`（默认 dry-run，`--write` 落库；幂等，重跑为 0）。
- 范围只覆盖 **note / memory**：软删笔记（`status != 0`）的 `embeddings`(note) 行与 `content_versions`(note) 行；`embeddings`(memory) 中已无对应记忆的行。
- 用 GORM `Pluck` 把父集合与子 id 都取到 Go 里比对，**规避 SQL 跨列 collation 坑**（`utf8mb4_unicode_ci` vs `utf8mb4_0900_ai_ci`）。
- `concept` 孤儿不在这里（见上「边界」段，由 topic-merge 的 GC 处理）；`message` 孤儿另属子系统，暂不纳入。
- 远端执行：`make entity-gc`（dry-run）→ `make entity-gc ARGS="--write"`。

## 状态

已修（分支 `feat/memory-note-rework-batch1`）：新增 `service.EntityPurger`，接入 4 个删除入口 + 笔记更新重建 chunk 路径；存量残留由 `cmd/entity-gc` 一次性清扫。
