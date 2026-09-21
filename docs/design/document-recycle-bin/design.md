# 文档回收站（软删除 + 7 天自动清理）

估算（开工）：14 面 / 2 缝（1 条需先建探针：MySQL 补列 + 旧行默认值）/ 2 次真机往返 / 0 个待拍决定 · ~3–6h
估算（定稿）：13 面 / 2 缝（1 需探针）/ 2 次真机往返 / 0 个待拍决定 · ~4h

## 目标

文档删除由物理删改为软删除（移入回收站），回收站内容 7 天后由后台自动物理清理。所有列表 / 词云 / 标签 / 分享 / AI 工具查询按 `status` 过滤，已删文档不可见。

## 已拍决定

- **UI 范围**：只做 macOS 客户端（Vue 前端暂不动，接口已就绪）。
- **保留期**：硬编码 7 天（不加配置项）。
- **回收站操作**：单篇「恢复」；页头另有「全部恢复」与「全部删除（清空回收站，永久）」（后加，需二次确认）。
- **AI 工具**：`delete_document` 变软删即可，不加新函数。
- **入口**：文档页 `列表 / 词云` Picker 加第三档「回收站」。
- **status 约定**：`0 = 有效`、`1 = 已删除`（与笔记的 1/2 不同；0 对 AutoMigrate 更干净，旧行默认即有效）。

## 后端

1. `model/document.go`：加 `Status int`（`not null;default:0;index`）+ 常量 `DocumentStatusActive=0` / `DocumentStatusDeleted=1`。AutoMigrate 自动补列。
2. `service/document_repo.go`：
   - 读路径加 `status=0`：`GetByID` / `GetByIDPublic` / `Update` / `ListByAllTags` / `tagGraph`。
   - `Delete` 改软删 `Update("status",1)`，顺带刷新 `updated_at`（删除时刻）。
   - 新增 `Restore`（1→0）、`RestoreAll`（该用户回收站全恢复）、`ListTrash`（分页，`updated_at DESC`）、`PurgeExpired(cutoff)` / `PurgeAll(userID)`（共用私有 `purge`：`IN` 批删批注 + 分享 + 文档，事务）。
3. `controller/document.go`：`DeleteDocument` 去掉批注级联删；新增 `ListTrashDocuments`（`GET /api/documents/trash`）、`RestoreDocument`（`POST /api/documents/:id/restore`）、`RestoreAllTrashDocuments`（`POST /api/documents/trash/restore`）、`PurgeAllTrashDocuments`（`DELETE /api/documents/trash`）。
4. `router.go`：注册两条路由。
5. `tool/document.go`：`delete_document` 描述改「移入回收站，7 天内可恢复」；`list_annotations` 加文档存在性校验。
6. `service/user_repo.go`：最近文档数统计加 `status=0`。
7. `agent/cron_runner.go`：新增 `docRepo` + `documentCleanupNext`，`documentCleanupEvery=24h`、`documentTrashRetentionDays=7`（硬编码），tick 调 `cleanupDeletedDocuments`；`HookStatus` 加 `DocCleanupRemaining`。`appinit/agents.go` 传 `repos.Document`。

`ponytail:` 复用 `updated_at` 当删除时间，不加 `deleted_at` 列；已删文档不再被更新，语义成立。

## macOS 客户端

8. `APIClient.swift`：加 `listTrashDocuments` / `restoreDocument` / `restoreAllTrashDocuments` / `purgeAllTrashDocuments`。
9. `DocumentViewModel.swift`：`DocumentBrowseMode` 加 `.trash`；加 `trashDocuments` / `loadTrash` / `loadMoreTrash` / `restore` / `restoreAll` / `purgeAll`；删除 Toast 文案改「已移入回收站」。
10. `BrowsePanelView.swift`：Picker 加「回收站」档（宽度 180）；Stepper 仅 `.cloud`；`.trash` 隐藏搜索；`.trash` 页头加「全部恢复」「全部删除」按钮（二次确认）；`.onChange(of: mode)` 切档加载。
11. `DocumentListView.swift`：加 `trashContent` 分支（卡片 + 「恢复」按钮，点卡片不打开详情）；删除确认文案改「可在回收站找回，7 天后自动清理」。

## 验证

- `go build ./... && go test ./...` + `document_repo` 单测（软删隐藏 / 回收站可见 / 恢复 / 超期清理）。
- Mac 源集 `swiftc -typecheck`（`@main` 盲区已知，最终由用户在 Xcode 编译）。
- 真机/真库 1 次：dev MySQL 起服务，确认 AutoMigrate 补列、旧文档可见、删除→回收站→恢复闭环。

## 缝

1. dev MySQL AutoMigrate 补列后旧行确为 0（需真库跑一次）。
2. 回收站 UI 手感与恢复流程（真机验收）。
