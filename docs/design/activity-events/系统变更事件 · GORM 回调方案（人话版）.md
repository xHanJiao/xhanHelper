# 系统变更事件 · GORM 回调方案（人话版）

> 范围：`doc.*` / `memory.*` / `bill.created` / `reminder.created|acknowledged` 这一组"系统变更事件"。
> 取代原先"每个仓储写方法里手写一行 `EmitChange`"的做法（P6 初版）。

## 一、问题：为什么旧做法扎人

系统变更事件要在**写库那一刻**发。旧做法是在每个写方法里插一行：

```go
func (r *DocumentRepo) Create(ctx, d) error {
    if err := r.db.WithContext(ctx).Create(d).Error; err != nil { return err }
    r.activity.EmitChange(ctx, activity.TypeDocCreated, d.ID, ...)   // ← 手写
    return nil
}
```

毛病是 **O(写方法数)**：

- 今天要改 `DocumentRepo`+`UserMemoryRepo` 的 9 个方法；P7 再加 `BillRepo`/`ReminderRepo` 的 7 个。
- 以后每加一个可写实体、或某个实体多了个写方法，都得记得回来插一行。
- 一旦漏插，事件就悄悄少了——而"少记录"不像报错那样会被发现。

一句话：**耦合点找错了**。事件关心的是"哪个模型被写了"，不是"哪个方法写的"。

## 二、办法：挂在 GORM 的写回调上

GORM 每次 Create/Update/Delete 都会走一遍回调链，回调能拿到 `*gorm.Statement`：里面有你这一次写的**模型类型、脏字段、影响行数、以及那次写用的 `ctx`**。

于是只在启动时注册一次：

```go
// main.go，events.db 打开之后
activity.RegisterChangeCallbacks(db, activityStore)
```

以后所有写库自动被看见，**仓储层一行都不用改**。新增实体 = 在回调的声明式映射表里加一行。

```
写库调用 ──► GORM callback ──► 查映射表（按模型类型）
                                  │ 命中
                                  ▼
                      从 stmt 抠出 事件名/biz_key/attrs
                                  │
                                  ▼
                        store.EmitChange(stmt.Context, …)
                                  │
                                  ▼
                    actor/trigger/parent 从 ctx 标记推导（同旧方案）
```

`actor` 的判定完全没变：工具路径由 `Registry` 往 ctx 塞 `WithToolCall`，用户 REST 由认证中间件塞 `WithUserActor`，都没有就是后台 batch。回调只是把"从 `ctx` 推导"这一步复用了。

## 三、关键点：主键（PK）怎么拿

这是唯一的硬骨头。创建和更新/删除不一样：

**创建**——回调里有真身：
```go
Create(d)   // stmt.ReflectValue 就是那个 *Document，d.ID 直接可读
```

**更新/删除**——真身可能是空壳，PK 只藏在 WHERE 里：
```go
Model(&model.Document{}).                 // ← 空壳，ID=""
    Where("id = ? AND user_id = ?", id, uid).
    Updates(updates)
```
此刻 `stmt.Model = &Document{ID:""}`，`id` 只是 WHERE 里的一个无类型位置参数，
回调要抠出来得去比对 SQL 串——**脆**。

### 约定：更新/删除时把主键放进模型

GORM 本来就推荐这么写。改一个字：

```go
Model(&model.Document{ID: id}).Where("user_id = ?", uid).Updates(updates)   // 更新
Delete(&model.UserMemory{ID: id})                                          // 删除
```

这样 : ① GORM 自动拼出 `id = ?`，行为和原来一致；② `stmt.Model` 里就有了结构化 PK，回调直接读。

> **只有更新/删除需要**。创建天生自带 PK，不需要任何额外写法。
> **不遵守会怎样**：不会崩，事件照发，只是 `doc_id`/`memory_id` 缺失（降级），并且回调会打一条警告日志——不做静默降级。

这条已写进项目 `AGENTS.md` 的「开发约定」，作为 tracked 实体（`Document`/`UserMemory`/`Bill`/`Reminder`）的规范。

## 四、声明式映射表

回调只认表里登记的模型，其它模型（Message、Embedding、Conversation…）统统无视。

| 模型 | create | update | delete |
|---|---|---|---|
| `Document` | `doc.created`，biz=id，`size_delta=len(content)` | `status→deleted` = `doc.deleted`；`content/title/tags` 变 = `doc.updated`（带 size_delta）；恢复(→active)/其余 = 不发 | `doc.deleted` |
| `UserMemory` | `memory.entity.changed` op=create，biz=id | `topic` 变 = op=retopic；`memory_value/facts` 变 = op=update；confirm 计数变 = 不发；无 PK 且 topic 变 = `memory.topic.cleaned`（带 count） | op=delete |
| `Bill` | `bill.created`，biz=id | 不发（附表 A 没有 bill.updated） | 不发 |
| `Reminder` | `reminder.created`，biz=id | `status→acknowledged` = `reminder.acknowledged`；其余（cancel/mention/generate）= 不发 | 不发 |

一看就明白为什么用表：**以后加实体就是加一行，不是改一堆方法**。

"哪些 update 该发、哪些该吞"这类判断（软删、恢复、确认计数、取消提醒）全落在每个模型的 `classify` 里——**一处**，不是散布在调用点。这就是"统一收口"。

## 五、几个必须知道的边界

- **事务回滚的幻影事件**：回调在事务提交前跑。目前 tracked 模型的写入**没有任何一个跑在事务里**（唯一带事务的是 `DocumentRepo.purge` 物理清回收站，本就不采集），所以现在不存在这个问题。将来若把某个 tracked 写入塞进事务且会回滚，可能多记一条；事件是 fire-and-forget、不重投，代价可接受。**改代码时注意即可**。
- **批量写**：`Create(&切片)` 只有一条多行 INSERT，回调在**内存里**遍历切片（纯反射，不产生 SQL）；`CreateInBatches(chunk,100)` 每 100 行触发一次回调。事件侧只是往内存有界队列 append（非阻塞、满了丢最旧），**不碰数据库连接、没有 per-row 往返**。目前 tracked 模型没有批量创建，这块是将来兜底。
- **例外：`UserMemoryRepo.BatchDelete`**：`Delete(&UserMemory{}, "id IN ?", ids)` 是"一条 SQL 删多行"，回调拿不到每一个 id。附表 A 要求 `memory.entity.changed` 带 `memory_id`，所以这一处**保留显式 per-id emit**：写库前用 `activity.WithNoEvent(ctx)` 让回调闭嘴，写完后自己按 `ids` 逐条发。这是全套方案里唯一的显式点。
- **回调绝不能弄崩写库**：handler 全程 `recover`，出错只记日志、绝不 panic 到业务；`EmitChange` 本身也是非阻塞入队。
- **`ctx` 缺失**：GORM 对没设 `WithContext` 的写默认填 `context.Background()`（`gorm.go:232`），且本项目所有仓储写都走了 `WithContext`，所以 actor 判定不会因此失真。

## 六、和旧方案的关系

- 保留：`activity/change.go` 的 ctx 标记（`WithToolCall`/`WithUserActor`）、`EmitChange` 的归组推导、`emit.go` 的事件常量、`TurnCtx.ConvID`、registry 塞 `WithToolCall`、中间件塞 `WithUserActor`。
- 丢弃：两个 repo 里逐方法手写的 `EmitChange`（`DocumentRepo` 全部还原；`UserMemoryRepo` 只留 `BatchDelete` 一处 + 它的 store 字段）。
- 结果：**改动从"每个写方法"收敛到"一张表 + 每个模型一个 classify"**，且以后加实体近乎零成本。

## 七、落地清单

1. `internal/activity/track.go`：`RegisterChangeCallbacks(db, store)` + 映射表 + classify + recover + PK 告警。
2. 方案 A 的 PK 惯用法：`document.Update/Delete`、`memory.Update/Delete/SetTopicByID`、`reminder.Ack` 把主键放进 `Model`/`Dest`。
3. `UserMemoryRepo.BatchDelete` 保留显式 emit（`WithNoEvent` + 逐条）。
4. `main.go`：`RegisterChangeCallbacks` + 仅 `repos.Mem.SetActivity`（专供 BatchDelete）。
5. `AGENTS.md`：tracked 实体 update/delete 的 PK 约定。
6. `internal/activity/track_test.go`：覆盖 create(单/slice)、doc 正文差值与 status→deleted 分流、restore/confirm 过滤、reminder ack、批量与 actor 归属。
