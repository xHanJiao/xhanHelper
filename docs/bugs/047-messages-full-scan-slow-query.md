# 047 — `messages` 全表扫描慢查询（retention + 关键词检索）

> **日期**：2026-11-06
> **环境**：阿里云 2C2G ECS，40G 云盘，MySQL 8.0.46（`disable_log_bin`）
> **触发**：早上 `DiskIOQueueSize` 连续报警；用户未主动使用，但后台仍有磁盘 IO

## 问题表现

- 06:53–06:55 系统 `unattended-upgrades` 装包，拖慢应用 30s 一次的 `delayed_tasks` 查询（0.2–1.3s）。
- 07:25 应用保留清理对 `messages` 全表扫描，两条查询各 7.3s / 7.4s，且清 0 行。
- 08:18 聊天的 `unified_search.keyword_search` 用 `content LIKE '%…%'` 扫 66k 行/595MB，单次 7.5–8.2s。
- MySQL `slow_query_log=OFF`、`long_query_time=10s`，没有 MySQL 慢日志；`Slow_queries` 计数 152，但无明细。

## 根因

`messages` 表 66k 行、约 595MB，生产 `SHOW INDEX` 只有 `(conversation_id, created_at)` / `(conversation_id, role, created_at)` 等索引，**没有 `idx_messages_created`**。仓库虽已有 `server/migrations/023_add_messages_created_index.sql`，但生产确认未应用；运行时实际建索引走 `AutoMigrate`，两边已分叉。

1. 清理逻辑的 `WHERE created_at < ?` 用不上索引；更关键的是稳态下已无旧行可清（0 命中），`LIMIT 500` 不会提前退出，每次仍扫完整区间。
2. `SearchMessagesByCursor` 的关键词 `content LIKE '%…%'` 本来就用不上 B-tree；再叠加无 `created_at` 索引，等于扫全历史。
3. `CountMessages`、时间窗消息查询同样没有可用索引，出现 filesort / 全表扫描。

`innodb_buffer_pool_size` 实际 256MB（`zz-memory.cnf` 覆盖了 `mysqld.cnf` 的 384M），装不下 595MB 的表，全表扫描直接打云盘。

## 修复

- `model.Message` 增加索引 `idx_messages_created_role (created_at, role)`，由 `AutoMigrate` 创建；`migrations/023` 同步改成复合索引（若旧环境已应用单列索引，先 DROP `idx_messages_created`）。
- `MessageRepo.SearchMessagesByCursor` 增加 `after *time.Time` 参数，向下传给 `searchMessages`。
- `unified_search.keyword_search` 的 history 域只搜**最近 90 天**（常量 `keywordHistoryWindowDays`）；**90 天无命中自动放宽到 365 天**（`keywordHistoryFallbackDays`），结果 description 会写明实际窗口，避免模型把「更早聊过」误答成「从没聊过」。检索失败时 description 写「检索失败」，不伪造成「未命中」。
- 默认 `types` 仍为 memory/note/summary/event（不含 history）；工具 Description、FunctionSchema、冷启动 prompt 都按这个默认值写清楚。
- `message_search` 不支持关键词检索，文档/工具描述统一改为「更早历史用 `message_search` 按时间/ID 查（不支持关键词）」。
- **保留清理改为水位窄带**：`appinit.InitDebugJSON` / `InitExtraTraceCleanup` 记住上次成功清到的 cutoff，下一轮只扫「新过期」的 `[lastCutoff, cutoff)`；首次启动仍全量扫一次，之后稳态不再扫整张表。
- **不上全文索引**：用户明确要求不做 ngram/FULLTEXT。

### 生产执行顺序

`AutoMigrate` 每次启动都会建索引；595MB 表在 2C2G 上建索引会造成一次重 IO，甚至让报警在发版时复现。因此必须**先手工 DDL，再发版**：

```sql
ALTER TABLE messages
  ADD INDEX idx_messages_created_role (created_at, role),
  ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE messages ANALYZE;
```

## 验证

- `go build ./...`、`go test ./...` 全绿。
- `TestUnifiedSearch_KeywordHistoryWindow`：有近期命中时，100 天前消息不返回。
- `TestUnifiedSearch_KeywordHistoryFallback`：只有 100 天前命中时，自动放宽到 365 天并返回。
- `TestUnifiedSearch_KeywordHistoryNoHitHint`：完全无命中时也返回窗口提示，而不是静默空结果。
- `TestUnifiedSearch_KeywordDefaultTypesExcludeHistory`：不带 types 时默认不搜 history，防止「默认值又把全表 LIKE 加回来」。
- `TestClearDebugJSONRange` / `TestClearExtraDebugRange`：水位窄带只清 `[after, before)` 内行。
- `TestMessageSchema_HasCreatedRoleIndex`：钉住 `messages(created_at, role)` 索引存在。
- 部署后 `EXPLAIN` 确认走 `idx_messages_created_role`；观察 GORM `SLOW SQL` 中 `messages` 的 7–12s 查询是否消失。
- 07:25 保留清理应降到 1s 内；若仍慢，再考虑 `has_request_json` / `has_extra_trace` 生成列或拆表。

## 未覆盖

- `message_search.count_messages` 带 keyword 且不带时间范围时仍是 `COUNT(*) + content LIKE`，无法靠 LIMIT 提前退出；已在工具描述里提示尽量带时间范围，后续可加默认窗口或近似计数。
- activity Recompute（`event_session` 批量插入、派生表清空重建）和 embeddings `OFFSET`/批量 DELETE 仍未治理，见下一轮。
- MySQL 慢日志未开启；当前用应用层 GORM `SLOW SQL >= 200ms` 作为基线。
