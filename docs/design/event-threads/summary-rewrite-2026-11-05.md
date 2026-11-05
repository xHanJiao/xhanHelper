# 事件 summary 存量重写（2026-11-05）

## 结论

事件 summary 的写法定为：

- **≤300 字**（软目标，不是硬切）；
- **电报体**：短语 + 分号，去虚词与过渡句，不写完整句；
- **倒金字塔**：先一句结论/现状（含最关键数字或日期），再按重要性写关键事实，背景最后，不按时间平铺；
- 每条短语保留明确主体，不要只剩日期和动作；
- 硬上限 **1000 字**，只做兜底（`MaxEventSummaryRunes`），正常写不到。

`event-bootstrap --resummarize-truncated` 不截断模型输出（`EventRepo.UpdateSummaryUncapped`），并设 `temperature=0.3`。生产拼装 prompt 与脚本口径一致。

## 改动

| 文件 | 改动 |
|---|---|
| `server/internal/agent/event_assembler.go` | 生产拼装 prompt：summary ≤300 字 + 电报体 + 倒金字塔 |
| `server/internal/tool/event.go` | `event.update` 的 summary 说明同步 |
| `server/internal/model/event.go` | 注释改为「目标 ≤300，硬上限见常量」 |
| `server/internal/service/event_repo.go` | `MaxEventSummaryRunes` 600 → 1000；新增 `UpdateSummaryUncapped`（`maxRunes=0` 不截断） |
| `server/cmd/event-bootstrap/main.go` | 脚本 prompt 改为 ≤300 + 电报体 + 倒金字塔；不截断、改调 `UpdateSummaryUncapped`；`temperature=0.3` |

## 本地实跑（MySQL 沙盒，非 prod）

库：`xhan_helper_test`（APP_ENV=dev），数据来自 2026-11-05 17:50 的 dump。

- 23 个活跃事件中命中 11 个旧 200 字摘要；**11/11 重写成功，failed=0**；跑完库里恰好 200 字的摘要为 0。
- 本轮长度 **168–521 字**，9/11 ≤378，仅 1 条 521；全部结尾完整，无一条触顶。
- 旧摘要里的日期/数字 token 保留 **59/68（87%）**；未保留的主要是过程日期与小额分项（笔数、单项预算拆分、90 天保留期等）。
- 备份：`server/data/event-summary-backup-20261105-191444.989.json`（11 条旧值）。
- 明细日志（含个人内容，`data/` 已 gitignore）：`server/data/resummarize-run-pyramid.log`。

### 四轮口径对比（同一批 11 条）

| 口径 | 长度 | 数字/日期保留 | 备注 |
|---|---|---|---|
| ≤200（线上口径） | 172–297 | 55/68（81%） | 太紧，细节掉得多 |
| 无字数限制 + 电报体 | 182–1184 | 61/68（90%） | 电报体压不住长度，4 条 >1000 |
| ≤300 + 电报体 | 171–389 | 60/68（88%） | 平衡，但开头还是按时间铺 |
| **≤300 + 电报体 + 倒金字塔** | **168–521** | **59/68（87%）** | 结论先行，结构最好；1 条偏长 |

低温度（0.3）能让长度更收敛，但不能让两次重写选出相同事实（相似度最低 0.28）；幻觉的主要约束仍是 prompt 的「只用记忆里的事实」和人工 dry-run。

## 回滚

备份 JSON 是 `event_id + name + 旧 summary`；按 ID `UPDATE` 或 `event.update` 工具手工还原。脚本没有 `--restore`。

## 未做

- 未在 prod 跑：需先停服务（避免 `EventWorker` 并发重算），并加 `CONFIRM_PROD=yes`。
- 生产拼装的 `temperature` 未动（仍走厂商默认）；0.3 只在重写脚本里。
- 未改前端事件页展示。
