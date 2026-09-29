# 记忆卡片反馈（👍/👎）

> 日期：2026-09-29
> 估算（开工）：3 面 / 1 缝 / 1 次真机往返 / 2 个待拍决定 · ~2–4h
> 估算（定稿）：3 面 / 1 缝 / 1 次真机往返 / 1 个待拍决定 · ~2–2.5h

## 1. 人话版

记忆列表的每张卡片右下角加两枚图标按钮 👍 / 👎：点一下标记「有用 / 没用」，赞踩互斥，**再点一次取消**回到未评。评分存进数据库，将来可以拿它给检索排序加权（本期不做，只存不用）。

- 只做**记忆列表卡片**（`MemoryListView` 的卡片），不动聊天里「基于 N 条回忆」弹层、不动记忆详情浮层。
- 选 A 方案：**纯图标**（卡片已够密，不占宽）；未评保持原样（不置灰）；👎 选中变**红**。

见 `preview.png`（未评 / 已赞 / 已踩三态，真 `CardView` 代码渲的）。

## 2. 技术方案

### 2.1 数据

`user_memories` 加一列：

```sql
rating INTEGER NOT NULL DEFAULT 0   -- 0 未评 / 1 有用 / -1 没用
```

- 模型 `model.UserMemory` 加 `Rating int `gorm:"not null;default:0" json:"rating"``。
- **不加迁移文件**（仅同步 `001_init.sql` 供全新初始化用，运行库不执行该文件）：`app.InitDB` 启动即 `AutoMigrate(AllModels()...)`，本地 SQLite（`APP_ENV=local`）重启即补列，云端 MySQL 亦会补（失败仅记日志、不致命）。旧行自动取默认值 0。
- 存**单列当前值**（不建打分事件表、不留历史）。要历史再另开票。

### 2.2 后端

| 层 | 改动 |
|---|---|
| 路由 | `PATCH /api/memories/:id/rating`（`router.go` 记忆组内） |
| handler | `memory.go::SetMemoryRating`：校验 `rating ∈ {-1,0,1}` 否则 400；`FindByID(userID,id)` 不存 → 404；按 user 归属更新 |
| repo | `user_memory_repo.go::SetRating(ctx, userID, id string, rating int) error`——`Model(&model.UserMemory{ID: id}).Where("user_id = ?", userID).Update("rating", rating)`（主键进 `Model`，符合埋点纪律；本列不在 `activity` 触发字段内，不发事件） |
| 响应 | 列表/批量返回的 `UserMemory` 自动带 `rating`（json tag） |

### 2.3 客户端

| 文件 | 改动 |
|---|---|
| `CardStyle.swift` | `CardAction` 加 `isActive: Bool`（显式 init，默认 `false`，已有调用零改动）——**已改** |
| `CardView.swift` | 选中态底色加重（`0.22` vs `0.1`），颜色仍由 `role` 决定；未选中外观与改动前一致——**已改** |
| `APIClient.swift` | `UserMemory` 加 `rating: Int?` + CodingKey；新增 `setMemoryRating(id:rating:)` → PATCH |
| `MemoryViewModel.swift` | `setRating(id:rating:)`：乐观更新（改内存 + 动画）→ 失败回滚 + `errorMessage`；互斥/取消逻辑由调用方算好最终值 |
| `MemoryListView.swift` | `CardConfig.actions` 加两枚 `CardAction`：👍（`hand.thumbsup[.fill]`，active=rating==1）、👎（`hand.thumbsdown[.fill]`，active=rating==-1 且 active 时 `role: .destructive` 走红） |

交互：点 👍 → `newRating = (rating==1 ? 0 : 1)`；点 👎 → `newRating = (rating==-1 ? 0 : -1)`；互斥由「只有一个非 0」天然保证。

## 3. 影响面

- 主链路？否。只加一列、一个只写接口、一处卡片 UI。
- 检索注入/排序**不受影响**（评分不参与召回）。`xml_context` 投影不变。
- 云端 MySQL 需 AutoMigrate 补列（失败仅日志；列缺时 `rating` 恒 0，不影响功能）。

## 4. 验证

1. `cd server && go build ./... && go test ./...`。
2. Swift：`xcrun swiftc -typecheck`（Shared + xHelperMac 两个源集各一批，带项目设置）。
3. 真机：记忆列表点赞 → 图标填充；点另一枚 → 互斥切换；再点同一枚 → 取消；杀掉重进 → 状态保持（读库）；断网点赞 → 回滚 + 错误提示。

## 5. 明确不做

- 不把评分喂回检索排序（另开票）。
- 不建打分事件表 / 不留历史。
- 不埋 activity 事件（评分已在 DB，且新事件须先登记附表 A，本期不引入）。
- 不加到「基于 N 条回忆」弹层、记忆详情浮层、词云。
- 不做未评置灰。

## 6. 风险 / 回滚

- 风险：Swift 改动只在 typecheck 层验证，真接线（Xcode 构建 + 触达）靠 1 次真机往返；`CardAction` 显式 init 若与既有调用冲突会在编译期暴露（已确认现有 3 处调用无 `isActive`）。
- 回滚：删两枚 `CardAction`、去掉 VM 方法即可；DB 多一列 `rating` 留着无害。
