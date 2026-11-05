# 代码审查 · 单记忆不建事件 + prune 存量（2026-11-05）

范围：阶段 8 之后的三个提交

- `38f023bd` chore(memory): 全环境停掉 post_chat 抽取，只留 inline（前一轮已单审，见 `../memory-quality/2026-11-05.md`；本轮复核其 P3-1 修复）
- `5add3a5f` feat(event): 单记忆不建事件并清理存量；移动端我的页六卡布局
- `f087370c` docs(event): 记录 prune 单记忆事件后的 prod 指标

关联设计：`docs/design/event-threads/design.md`（§8 拼装规则与代码闸、§10.2 指标、后续变更）。
上一轮审查：`2026-11-05-stage8.md`。

验证基线：

- `cd server && go build ./...` 通过
- `go vet ./internal/... ./cmd/...` 通过
- `go test ./... -count=1` 全绿（29 个包），含新增/改写的 `TestApplyPlan_RejectsSingleMemoryNewEvent` 等
- `gofmt -l internal cmd` 无输出
- 前端有改动（`MeView.vue`），`cd front-end && npx vite build` 通过
- 无 Swift 改动，未跑 `swiftc`
- 未连 prod / dev 库复核 §10.2 与 prune 的实际数字

## 结论

**通过，无 P0/P1/P2。** 4 个 P3（文档口径、prune 与在线拼装并发、占位页入口、审查记录时效）+ 1 条观察项。

核心逻辑没问题：`applyNewEvents` 在「无未归属记忆」之后加 `len(ids) < 2` 硬闸，prompt 同步改成「至少 2 条」；`prune-singles` 复用 `EventRepo.Delete`（记忆回池 + 事件软删），dry-run 与 prod 写入保护都接上了；6 卡片布局只是增删列表项与一条 CSS 规则。

## 发现

### P3 - Low

1. **设计 §10.2 的分配率口径写反**
   - 位置：`docs/design/event-threads/design.md:722`（「未分配 **94 条（22.5%）**……也高于预期的 40–70% 未分配」）。
   - 影响：22.5% 明显**低于** 40–70%，与前一分句「比 dev 更激进」自相矛盾；阶段 8 复核记录里已改成「低于预期区间」，设计文档没同步，后续查指标会误判。
   - 建议：改为「低于预期的 40–70% 未分配」（dev 段第 714 行同款表述可作参照）。

2. **`--prune-singles` 未提示先停服务，在线跑有短暂竞态**
   - 位置：`server/cmd/event-bootstrap/main.go:59`（flag 帮助）、`:110-118`（分支）、`:206-232`（实现）；`server/internal/service/event_repo.go:227-243`（`assignMemories` 用普通 `First` 校验事件存活，不是锁定读）。
   - 现象：`Delete` 先锁事件行、把记忆 `event_id` 置空、软删事件；并发的 `AssignMemories` 可能在 `Delete` 提交前读到「事件存活」，提交后才把 `event_id` 写回，结果记忆挂在一个已软删的事件上——既不在普通池、也不在事件块，等于静默消失。窗口很窄，且同一竞态在 `event.delete` 工具上早已存在，不是本提交引入。
   - 影响：一次性 CLI 通常停服跑，风险低；但帮助文案与设计后续变更都没写「建议先停服务」，与 `entity-gc` 的约定不一致。
   - 建议：flag 帮助 / 设计后续变更补一句「建议先停服务」；要做硬保证则让 `assignMemories` 在锁内复核 `is_deleted`（改动较大，可留给后续）。

3. **「我的」页新增「图片」入口，落地页仍是占位页**
   - 位置：`front-end/src/views/MeView.vue:17`；`front-end/src/views/ImagesView.vue`（页面文案「图片库会在 M3 接入」）。
   - 影响：移动端主入口点进去是空占位，容易让用户以为功能坏了。若这是有意提前露出（为 M2/M3 铺路），需要接受这条；否则建议先隐藏该卡片。
   - 建议：确认意图；隐藏只需删一行，恢复也只需加回。

4. **记忆质量审查记录的「未提交」已过期**
   - 位置：`docs/code-review/memory-quality/2026-11-05.md:3`（「范围：工作区**未提交**改动」）。
   - 影响：该改动已随 `38f023bd` 入库，记录仍写未提交，日后回溯容易误判成「漏提交」。
   - 建议：改为「提交 `38f023bd`（审查时未提交）」或直接写提交号。

### 观察项（非缺陷）

5. **≥2 门槛 + 关闭 post_chat，跨 session 的事件可能永远建不起来**
   - `EventWorker` 的输入是「本 session 起点之后 `updated_at` 有变化」的记忆（`event_worker.go:48`），而 `applyNewEvents` 只数**本批**未归属记忆。若同一件事每个 session 只沉淀 1 条记忆，「后续再凑够 2 条」时旧记忆已不在批内，事件始终不会出现（新事件也拿不到旧记忆，因为 `assignments` / `new_events` 都按 `batch` 过滤）。
   - 代码注释「等后续同批出现 ≥2 条相关记忆再建」只在「同一 session 内先后出现」时成立。两种改动同日上线（post_chat 停写 + ≥2 门槛），新事件产量可能接近 0。
   - 不要求本轮改；建议观察一周事件增量，并在设计里补一句预期（「新事件只在单 session 产出 ≥2 条记忆时形成」），免得日后把「没有新事件」误判成 bug。

## 处理状态

- [x] P3-1 设计 §10.2「高于预期」改「低于预期」
- [x] P3-2 `--prune-singles` 帮助/设计后续变更补「建议先停服务」
- [x] P3-3 「图片」占位入口不露出：改回 5 卡片（账单 / 记忆 / 事件 / 智能体 / 设置），奇数末张（设置）横跨整行
- [x] P3-4 记忆质量审查记录标注提交号 `38f023bd`
- [x] 观察项 5 设计 §4.3.1 补预期口径（新事件只在单 session ≥2 条记忆时形成）

## 核对过的点

- **规则落点**：`event_assembler.go:460-466` 位于「无未归属记忆」之后、重名/语义查重之前，单条记忆不会触发嵌入调用；`assembler` 是唯一建事件入口（`CreateWithMemories` 仅此一处调用），没有工具/接口绕过。
- **prompt 一致**：`event_assembler.go:668` 的规则与代码闸一致；设计 §8 第 263、275 行同步；示例 JSON 仍用单条 `memory_ids`（只是结构示意，无歧义，未改）。
- **prune 实现**：`loadEventStats` 只取未删除事件，`ListByEventIDs` 一次查询取齐记忆；`EventRepo.Delete` 是事务内「记忆回池 + 事件软删」，幂等（已删直接返回）；`--prune-singles` 与 `--reset/--review/--limit` 互斥、`--dry-run` 不写库、非 dry-run 走 `GuardProdWrite`。
- **测试改动**：原有 4 个 `applyPlan` 用例把单条 `memory_ids` 改成两条，仍保留原断言；新增 `TestApplyPlan_RejectsSingleMemoryNewEvent` 同时断言「未建事件」与「记忆未挂事件」。`TestApplyPlan_NewEventsDoNotShareMemory` 现在第二个事件因「全部已被认领」被拒，路径略变但语义仍成立。
- **前一轮 P3-1 已闭环**：`TestEventWorker_ProcessesInlineMemoryWhenExtractDisabled` 现在传非 nil `Extractor` 并断言 `callCount == 0`，flag 误写成 true 会真失败（`38f023bd`）。
- **数据核对**：§10.2 prune 行 23 个事件 / 已分配 320 / 未分配 97 自洽（320+97=417，323−3=320，94+3=97）；但 p50/p90/max 仍是 prune 前的数字，删掉 3 个单记忆事件后百分位可能位移 1 左右——属记录精度问题，未列为发现。
- **前端**：6 项两列正好 3 行，删除「奇数最后一张跨行」规则后不会留孤儿；`/images` 路由存在，meta 与其他二级页一致（`hideTabs` + `backToChat`）。

---

> 阶段 8 的审查见 `2026-11-05-stage8.md`；记忆质量第五轮见 `../memory-quality/2026-11-05.md`。
