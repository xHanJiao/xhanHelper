# 009: 同一会话存在多条 active 摘要

**日期:** 2026-07-16
**状态:** 待修复

## 现象

`conversation_summaries` 表设计为增量合并模式:每次生成新摘要时旧段标记 `superseded`,每个会话**始终只有 1 条 active**。但 dev 库实测发现会话 `3b3f6e2f-0ddb-42a4-a2dc-a140cc30838c` 存在 **6 条 active** 摘要(共 138 个版本),全库 active 共 10 条,而按设计应等于会话数。

```sql
SELECT conversation_id, SUM(status='active') AS active_cnt
FROM conversation_summaries GROUP BY conversation_id;
-- 3b3f6e2f...: active_cnt = 6 (应为 1)
```

## 疑似根因

`SummaryWorker.saveSummary`(`server/internal/agent/summary_worker.go`)的"插入新 active + 旧摘要 MarkSuperseded"两步操作不在同一事务内,且 supersede 只处理"上一条"(通过 `GetLatest` 拿到的 prev),存在两类缺口:

1. **竞态**:并发的 summary_generation 任务同时读到同一条 prev,各自插入新 active,只 supersede 了同一条旧记录
2. **遗漏**:若某次 MarkSuperseded 失败(仅打日志不回滚),新 active 已插入,旧 active 残留

## 影响

- `GetLatest` 按 `created_at DESC LIMIT 1` 取值,功能上仍返回最新一条,**读路径暂无用户可见错误**
- 但 active 语义已破坏:依赖 `status='active'` 的统计、Warmup(`ListAll`)会加载重复的旧版本摘要

## 修复建议

1. `saveSummary` 改为事务:先 `UPDATE ... SET status='superseded' WHERE conversation_id=? AND status='active'`(全量,不只 prev 一条),再插入新 active
2. 迁移脚本清洗历史脏数据:每个会话仅保留 `created_at` 最新的一条 active,其余标 superseded
