# 事件 summary 存量重写（2026-11-05）

## 结论

- summary 的 prompt 回到线上 `ec7c9993` 的口径：**「≤200 字，写当前状态，未定的事写在里面」**（只回退，不新增规则）。
- `MaxEventSummaryRunes` 由 600 改为 **1000**，只做兜底，不参与目标长度。
- `event-bootstrap --resummarize-truncated` 不对模型输出做长度截断（`EventRepo.UpdateSummaryUncapped`），并显式设 `temperature = 0.3`。
- 本地测试库实跑 11 条存量：**172–297 字，全部结尾完整，无一条触顶**。

## 改动

| 文件 | 改动 |
|---|---|
| `server/internal/agent/event_assembler.go` | 拼装 prompt 的 summary 口径改回 `≤200 字` |
| `server/internal/tool/event.go` | `event.update` 的 summary 参数说明改回 `≤200 字` |
| `server/internal/model/event.go` | 注释改为「目标 ≤200，硬上限见常量」 |
| `server/internal/service/event_repo.go` | `MaxEventSummaryRunes` 600 → 1000；新增 `UpdateSummaryUncapped`（`maxRunes=0` 不截断） |
| `server/cmd/event-bootstrap/main.go` | 不截断输出、改调 `UpdateSummaryUncapped`；脚本 prompt 改回 `≤200 字`；`temperature=0.3` |

## 关于「回到 ec7c9993」

没有做 `git reset`：`ec7c9993` 里没有 `--resummarize-truncated`（工具是 `11127db5` 才加的），硬 reset 会把重写工具一起删掉。

实际做的是**行为对齐**：恢复 `ec7c9993` 的 prompt 与 200 字口径，再把硬上限改成 1000。生产拼装等价于「ec7c9993 的 prompt + 1000 兜底」。

## 本地实跑（MySQL 沙盒，非 prod）

库：`xhan_helper_test`（APP_ENV=dev），数据来自 2026-11-05 17:50 的 dump。

- 23 个活跃事件中命中 11 个旧 200 字摘要；**11/11 重写成功，failed=0**；跑完库里恰好 200 字的摘要为 0。
- 新长度 **172–297 字**，全部结尾完整；11 条里 9 条超过 200，全部远低于 1000 硬上限。
- 旧摘要里的日期/数字 token 保留 **55/68**：200 字目标下必然压掉细节，未保留的主要是过程日期与小额分项。这是目标长度决定的，不是截断。

### temperature 对比（同一批 11 条，默认温度 vs 0.3）

| | 默认温度（未设） | 0.3 |
|---|---|---|
| 长度范围 | 168–318 | 172–297 |
| 数字/日期保留 | 54/68 | 55/68 |
| 与另一轮的字面相似度 | — | 0.28–0.69 |

结论：0.3 让长度更收敛、保留率略好，但**不能让两次重写选出相同的事实**（相似度最低 0.28）。波动主要来自「200 字里选哪些事实」而不是采样随机性，所以低温度对幻觉只是小幅辅助，真正的约束还是 prompt 的「只用记忆里的事实」和人工 dry-run。

## 回滚

备份 JSON 是 `event_id + name + 旧 summary`；按 ID `UPDATE` 或 `event.update` 工具手工还原。脚本没有 `--restore`。

## 未做

- 未在 prod 跑：需先停服务（避免 `EventWorker` 并发重算），并加 `CONFIRM_PROD=yes`。
- 生产拼装的 `temperature` 未动（仍走厂商默认）；脚本才是 0.3。
- 未改前端事件页展示。
