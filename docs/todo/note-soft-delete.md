# 笔记软删除功能

**状态**: 待开发  
**日期**: 2026-05-28  
**决策依据**: 笔记有版本快照系统，属于会演化的长期资产，应与消息一样支持软删除以防误删

## 变更清单

### 1. model/note.go — 加 Status 字段

```go
const (
    NoteStatusNormal  = 1
    NoteStatusDeleted = 2
)

type Note struct {
    // ... existing fields ...
    Status int `gorm:"default:1" json:"status"` // 1=正常 2=已删除
}
```

### 2. service/note_repo.go — 各方法加 status 过滤

- `GetByID` / `GetByIDs` / `GetByIDsAndUser` / `ListAllPaginated` / `List` / `CountRecent`：全部加 `status = 1` 过滤
- `Delete`：改为 `Update status = 2`（软删除）
- 新增 `GetByIDAny`：不过滤 status，仅供 restore 内部使用

### 3. tool/note.go — 新增两个函数

- **delete_note**：校验 ID + 用户 → 软删除（Status → 2），不动版本快照，不动向量
- **restore_note**：通过 `GetByIDAny` 查找 → Status → 1，不动版本快照，不重建向量

已有函数的拦截行为（由 repo 层 status 过滤自然实现）：
- `get_note` / `get_note_versions` / `update_note` / `rollback_note`：对已删除笔记返回 "not found"
- `query_notes`：不返回已删除笔记

### 4. 迁移脚本

```sql
ALTER TABLE notes ADD COLUMN status INTEGER DEFAULT 1;
UPDATE notes SET status = 1;
```

### 5. 不需要改的部分

- 版本快照系统（删除是状态翻转，不涉及内容变更）
- Warmup (`ListAllPaginated` 自动过滤已删除)
- Chunk 向量 / 概念向量（保留，不清理）
- `search_notes_v2`（已死代码，未注册到 tool registry）
- `NoteSource.Fetch`（`GetByIDs` 自动过滤）

## 向量处理策略

- 软删除时**保留向量**，不手动词删除
- 检索时向量搜索返回候选 → `GetByIDs` DB 层过滤掉 status=2
- 恢复笔记时不需要重建向量（向量一直在）
