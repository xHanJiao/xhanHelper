# 代码审查 · `event-bootstrap --resummarize-truncated`（2026-11-05）

范围：`11127db5`（3 文件，+278 / −0）

- 新增 `cmd/event-bootstrap --resummarize-truncated`：挑出 summary 恰好 200 字（旧硬上限）的活跃事件，按事件记忆用 Flash 重写 summary，写库前备份旧值；
- 配套单测 4 个（挑目标 / 清洗 / 输入拼装 / 预算截断）；
- 设计文档 §4.4 补一行命令说明。

相关设计：`docs/design/event-threads/design.md`（§4.4 summary 硬上限）。

验证基线：

- `cd server && go build ./cmd/event-bootstrap/` 通过
- `go test ./cmd/event-bootstrap/ -count=1` 通过
- `go vet ./cmd/event-bootstrap/`、`gofmt -l cmd/event-bootstrap` 干净
- 未连 DB / 未真跑脚本 / 未调 LLM（只做静态复核 + 单测）

结论：**方向正确、实现干净，无 P0/P1**。2 个 P2、6 个 P3，均不阻塞合并。

## 复核过、确认没问题的点

- **200 字判据不会漏目标**：三条写路径落库都恒为恰好 200 rune —— 拼装新建走 `agent.truncateRunes`（不补省略号）后 `create` 不再截断；拼装更新与 `event.update` 工具都经 `UpdateSummary` → `truncateEventSummary`。所以不存在「200 + …= 201 字被漏掉」的存量。
- `UpdateSummary(ctx, userID, id, "", next)` 名参传空是正确的（空名保持原名，只有 summary 参与更新）；`next == ""` 已被前面的非空判断挡住，不会落到「summary 为空则保持原值」的静默 no-op。
- 备份先于任何写入落盘；备份失败直接 return，不会出现「改了但没备份」；dry-run 只读 DB、不写备份。
- `ListByEventIDs` 无分页但有 ID 集约束；`listAllEvents` 分页排序 `last_memory_at DESC, id` 有 id 兜底，翻页稳定。
- 主要失败点（LLM 报错 / 空输出 / 写库失败）都只跳过当前事件并计入 `failed`，末尾非零退出；部分失败后重跑会自然重试（失败项仍是 200 字）。
- 单测覆盖了纯逻辑的边界（恰好 199/200/201、已删除、围栏/引号/换行、超预算）。

## P2

### P2-1 prompt 内部自相矛盾，鼓励在信息不足时注水

`main.go:255-263`

- 同时要求「200–600 个汉字」和「信息不足就写短」「没有依据的一律不补」。
- 目标是历史上被截断的事件，通常确实有料；但只要命中一个只有 2–3 条短记忆的事件，前一条硬指标就会把模型推向补充推测内容——这恰好是本功能要治理的病。
- 建议：只保留上限「不超过 600 字，够写就写到 200 字以上」，把「写短」的自由度留足。

### P2-2 写库前只有「非空」与「与旧值不同」两道闸，没有内容质量闸

`main.go:336-352`

- 模型一旦回「抱歉，我无法…」「好的，以下是重写后的 summary：」这类话术，会被原样清洗后写进事件 summary（`cleanResummarizedSummary` 只去围栏/引号，不去前缀话术）。
- 有备份可回滚，但回滚要手工改库或用 `event.update` 工具，代价不对称。
- 建议最省的一条：输出长度 < 20 字或命中固定拒答前缀时按失败处理（不写库、计入 `failed`），无需复杂校验。

## P3

1. **`main.go:336` 截断口径不一致**：`truncateRunes` 会补「…」（返回 601 字），随后 `UpdateSummary` 里的 `truncateEventSummary` 砍回 600 并把「…」丢掉。结果落库仍是无标记的硬切 600 字，dry-run 打印的「601 字」也和硬上限口径对不上。传 `MaxEventSummaryRunes-1` 或本处不补省略号即可。
2. **`main.go:385-404` 预算 break 会连最新一条一起吞掉**：若排第一（最新）的记忆单条超 20000 字，`listed=0`，模型只看到旧 summary；同时提示语「另有 N 条**更早的**记忆未列出」把最新那条也说成更早。建议超预算时把该行截到剩余预算再 break，或文案改成「另有 N 条记忆未列出」。
3. **`main.go:58` 帮助文案自相矛盾**：`--dry-run` 仍写「不调 LLM」，而本功能下 dry-run 会逐条真调（`main.go:61` 已注明「仍会调 LLM」）。建议全局文案改成「不做 DB 写入（个别子命令仍会调 LLM）」。
4. **`main.go:115-124` 互斥校验位置在错误分支之后**：`--prune-singles` 分支先 `return`，所以 `--prune-singles --resummarize-truncated` 会**静默只跑 prune**，`resummarize` 分支里那句 `|| *pruneSingles` 永远不可达。把互斥校验提到第一个分支之前（顺带也补上 prune 与 reset/review/limit 的同类问题）。
5. **备份文件属性**：`main.go:310` 时间戳只到秒，同秒两次运行互相覆盖；`0o644` 写的是个人 summary 内容。建议时间戳加纳秒或存在则加序号，权限收紧到 `0o600`。
6. **并发/顺序提醒缺失**：脚本头注释只对冷启动要求「停服务」，本次重写同样会和 `EventWorker` 的 summary 重算竞争（后者可能带着更新信息写，被脚本用旧记忆覆盖）。设计文档那行建议补一句「建议先停服务」。
7. **新 summary 恰好 200 字会被反复当目标**：下次重跑仍命中 `== 200`，只多花一次 LLM（内容一致会 skipped），不写库。可接受，记一笔。
8. **主循环无测试**：备份 → 调 LLM → 清洗 → 写库 → 计数这条链路没有单测（repo/LLM 都是具体类型，不好注入）。纯函数覆盖足够，主循环建议至少真机在 dev 库跑一次 3–5 个事件的用例，确认备份与新值落库。

## 未覆盖

- 未在 dev 库真跑（未验证 `ListByEventIDs` 在真实数据量下的耗时、事件多时串行 LLM 的总时长与费用）。
- 未验证 `--dry-run` 的真实输出可读性（旧/新对照带换行的日志形态）。
