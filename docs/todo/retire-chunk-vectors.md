# 下掉 chunk（句级分块向量检索）

**状态**: 待开发（未来）
**日期**: 2026-09-23
**决策依据**: chunk 是「长文档按句切块、各自嵌入、双路检索」的一整套机制。实际使用中收益不明确，
维护面却铺得很广（约 28 个文件）。**保留 full 向量（整条 memory/note 一条向量）即可**，
把 chunk 整条链路下掉，减少检索复杂度与落库开销。

## 现状

- **建**：`agent/chunk_builder.go` — `BuildChunks` / `DiffAndBuildChunks`，把 note/memory/summary/theme
  文本拆成句子级 chunk，逐句嵌入，写 `embedding_chunks` 表 + `embeddings` 表 + 内存向量库。
- **搜**：`tool/search_chunk.go` — `searchChunks` 先向量搜 chunk，再按 parentID 去重回 full 实体（「双路检索」）。
- **模型**：`model/embedding_chunk.go`（表）、`model/embedding.go` 的 `VectorTypeChunk` /
  `VectorPrefixChunk` / `ChunkVectorKey` / `ParseChunkVectorKey` / `ChunkVectorKeyPrefix`。
- **仓储**：`service/embedding_chunk_repo.go`。
- **接入点**：`agent/{extractor,summary_worker,state_updater,theme_analyst,theme_chunker}.go`、
  `agent/retrieval/{entity_source,memory_source,note_source,summary_source,theme_source}.go`、
  `agent/warmup.go`（启动加载）、`app/{init_db,init_repos,interfaces}.go`、
  `appinit/{agents,tools}.go`、`service/entity_purger.go`（删除时清理 chunk）。

## 为什么下掉

- 复杂度高、铺得广（28 文件），是检索链路上最重的一块。
- 短文本（memory/note 大多一两句）由 full 向量已能覆盖，chunk 收益边际。
- `ChunkBuilder` 的 `disabledParents` 已经暴露出「有些父类型本来就不该建 chunk」——
  即机制本身在给自己打补丁。
- 删除时还要额外清理 chunk（`EntityPurger`），一处遗漏就是脏数据（bug017 同类风险）。

## 变更清单（待细化，先记范围）

1. **检索侧先切**：`searchChunks` 调用点改走 full 向量 → 观察检索质量无回退。
2. **停建**：`ChunkBuilder` 全部调用点去掉；`disabledParents` 机制随之消失。
3. **删模型/仓储**：`model/embedding_chunk.go`、`service/embedding_chunk_repo.go`、
   `model/embedding.go` 里的 chunk key/常量、`app` 装配。
4. **删表**：`AutoMigrate` 去掉 `EmbeddingChunk`；写迁移清 `embedding_chunks` 表 + 内存库 `chunk:*` 向量。
5. **清理器**：`EntityPurger` 去掉 chunk 部分。
6. **启动加载**：`warmup.go` 不再加载 chunk。
7. **测试**：`chunk_builder_test.go`、`model/embedding_test.go` 的 chunk 用例、各 `*_source_test.go` 的 chunk 断言。

## 风险

- 长文档（document / theme 状态页）的检索若依赖句子级召回，下掉后可能变慢变粗 ——
  先做**检索质量对比**（离线重放或标注集），确认 full 向量足够再全删。
- 存量 chunk 数据清理需幂等，参考 `cmd/topic-merge` 的孤儿 GC 做法。
