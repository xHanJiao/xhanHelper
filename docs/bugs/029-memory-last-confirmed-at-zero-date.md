# 记忆提取 save_memory 报 Error 1292（last_confirmed_at 零值日期）

## 问题表现

本地→云端迁移（`docs/design/return-to-cloud/`）切回阿里云 MySQL 后，记忆提取的决策循环
`save_memory` 连续返回：

```
Error 1292 (22007): Incorrect datetime value: '0000-00-00' for column 'last_confirmed_at'
```

该条新记忆**未落库**，循环重试三次后放弃。用户注记「不是必现」。

## 根因

`save_memory` 走结构体插入（`agent/record_loop_persist.go:20` 的 `newMemory` →
`MemRepo.Create`），它不设 `LastConfirmedAt`。而 `model.UserMemory.LastConfirmedAt` 是
**值类型** `time.Time`（`user_memory.go:30`），Go 零值 `0001-01-01` 被
`go-sql-driver/mysql` 序列化成 `'0000-00-00'`。云端 MySQL 严格模式（`NO_ZERO_DATE`）
直接拒绝 → 1292。

本地 SQLite 容忍零日期，所以迁移前一直没暴露。

**为什么「不是必现」**：只有决策循环选 `save_memory`（INSERT 新行）的分支会撞；
选 `update_memory` 时走 `Updates(map)`（`service/user_memory_repo.go:173`），不碰该列、照常成功。
是「按分支必现」，不是随机。

同款先例在代码库里已有：`model/image_asset.go:43` 的 `LastUsedAt *time.Time` 注释一字不差
地记着这个 1292，做法就是**指针化**。`docs/design/memory-audit/design.md` 当时基于
「只在本地 SQLite 清、避免指针化」决定不动它——回云端后该决定失效。

## 最终方案

`model/user_memory.go`：`LastConfirmedAt time.Time` → `*time.Time`（附注释说明原因）。

- 全仓唯一 Go 读取方 `BatchUpdateConfirm`（用 `gorm.Expr("CURRENT_TIMESTAMP")`），指针/值都兼容。
- 前端、Swift 客户端均不引用该字段，JSON 由时间串变 `null`，无消费方受影响。
- 列本就是 `DATETIME NULL`（`migrations/001_init.sql:84`），AutoMigrate 无需 DDL。
- 迁移工具 `sqlite2mysql` 早已把零值时间写成 NULL，存量行读回即 `nil`。

`MCPServerRecord.SnapshotUpdatedAt` 同病但 `UpsertSnapshot` 无调用方（死代码），本次不动。

## 验证

- `go build ./... && go test ./...` 全绿。
- 真机：云端跑一次触发新记忆的对话，`user_memories` 新增行成功、`last_confirmed_at` 为 NULL、
  日志无 1292。

## 涉及文件

- `server/internal/model/user_memory.go`
- `docs/design/memory-audit/design.md`（后续变更：撤销「避免指针化」决定）
- `docs/design/return-to-cloud/design.md`（B 行补充：运行时写入路径同样需指针化）
