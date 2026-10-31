# 计划：file_edit 子 agent（开放本地文件写能力）

> 日期：2026-09-21
> 状态：✅ 已实施（服务端接线；客户端零改动，真机验收待做）
> 前置：`docs/design/mac-apps/fs-write-tools.md`（客户端 fs__write/fs__edit 已就绪，但从未接线给 agent）

估算（开工）：4 面 / 1 缝（0 需新建探针）/ 1 次真机往返 / 3 个待拍决定 · ~2–4h
估算（定稿）：4 面 / 1 缝（0 需探针）/ 1 次真机往返 / 0 个待拍决定 · ~1.5–2.5h

## 一、背景：写能力建好了却没接上

客户端 `FileSystemToolProvider` 有完整的 `fs__write` / `fs__edit`（L3 diff 确认条、敏感文件拦截、
目录授权、写前必读、原子写、BOM/行尾保留），但服务端两道闸把它们全挡住了：

1. `chat.subagent_only_client_tools = [fs, shell]`（原含 browser，2026-10-15 起 browser 已放回主循环）→ work 模式下主循环对整个 fs 组不可见；plan 模式例外：服务端白名单直接放行 fs 的 `glob/grep/read`（2026-10-31）；
2. `file_explore` 的 fs 函数白名单只有 `glob/grep/read` → 子任务里 write/edit 被函数级剥掉。

结果：**任何 agent 都够不着写能力**。这是「客户端四批完成 → 服务端零改动」留下的接线缺口。

## 二、为什么是「写子 agent」而不是「主循环直接开放 fs」

`docs/design/phase-10-token-optimization.md` §5 与对 opencode 的调研结论：

- opencode 能在主循环随便读文件，靠的是**自动 prune（旧工具结果清空）+ 溢出 compaction**；
  我们两者都没有——主循环里工具结果**只进不出**（同轮 ReAct 内更是原样进上下文，
  `max_tool_result_chars` 只作用于跨轮重放），读几个大文件就爆。
- 而 edit 必须先 read（客户端硬校验「写前必读」），所以「只开放 write/edit、不开放 read」不可行，
  主循环开放写 ≡ 主循环开放读 ≡ 当初把 fs 封进子 agent 的膨胀问题。

因此把写放进一个**独立、一次性、可丢弃的子上下文**，主循环只收最终摘要。
（`file_explore` 保持只读，不改其定位；写单独开一个 `file_edit` 类型。）

## 三、目标 / 非目标

**目标**：新增 `file_edit` 子 agent 类型，唯一开放 `fs__glob/grep/read/write/edit`，
经 task 工具派发；每次写走客户端既有 L3 确认链路。

**非目标**：主循环直接读写 fs；自动 prune / compaction；并行客户端工具执行；
`file_explore` 写权限；客户端任何改动；按 spec 覆盖 subagent 超时。

## 四、类型定义

```go
"file_edit": {
    prompt:          fileEditPrompt,
    serverWhitelist: []string{"datetime", "calculator"},
    clientWhitelist: map[string][]string{"fs": {"glob", "grep", "read", "write", "edit"}, "ui": nil},
    maxToolResult:   clientToolInjectBudgetBytes, // 要读全文才能凑精确 old_string
    requiresClient:  true,
}
```

- `ui`（ask_user）保留，同其它客户端子任务。
- 不加 `shell`（写场景不需要命令；要命令走 file_explore）。
- 超时沿用全局 `subagent_timeout`（默认 600s，含用户在确认条上的思考时间）；
  改动多时可超时，靠 `task_id` 续跑 + 部分成果回传兜底。

## 五、prompt 要点（`fileEditPrompt`）

- 角色：本地文件编辑专家（读+写）；输出只交主 agent，不面向用户。
- 定位顺序 glob → grep → read；改已有文件优先 `fs__edit` 渐进小改，新建/整体重写用 `fs__write`。
- **写确认条款**：每次写弹 L3（diff 审批），被拒**禁止原样重试同一处**，换方案或打住；
  用户思考计入时长预算，改动多时优先保证最重要几处。
- 边界：只改任务指定文件、并行任务不碰别人的文件；敏感文件被拦属预期；
  只回「改了什么」，不把原文大段抄回。
- 输出结构（严格）：

```
<file_edit_result>
<summary>…</summary>
<changes>file:line + 一句话/分号分隔，无则"无"</changes>
<rejected>被拒/失败清单（含原因），无则"无"</rejected>
<open_questions>未完成事项（供续跑判断），无则"无"</open_questions>
</file_edit_result>
```

## 六、改动清单

**后端**（客户端零改动）

| 文件 | 改动 |
|---|---|
| `internal/agent/subagent.go` | `fileEditPrompt` 常量；`subagentSpecs` 加 `file_edit`；未知类型错误枚举补全 |
| `internal/tool/task.go` | `Description` / `Catalog.ShortDesc` / `subagent_type` 枚举补 `file_edit` |
| `internal/agent/subagent_test.go` | spec 数 3→4；`file_edit` 白名单/预算/ui/prompt 断言；「写函数只应出现在 file_edit」全局守卫 |
| `internal/agent/ask_user_test.go` | file_edit 含 ui 断言 |

## 七、测试与验收

- `go build ./... && go test ./...` 全绿（已过）。
- 新增断言覆盖：`file_edit` 白名单恰为 `glob,grep,read,write,edit`；`requiresClient`；
  `maxToolResult = clientToolInjectBudgetBytes`；system prompt 含角色与输出结构；
  除 `file_edit` 外所有 spec 不含 write/edit。
- **真机验收（用户在 Xcode）**：主循环派 `task(file_edit)` 改一个已授权目录内的文件 →
  客户端弹 L3 diff 确认条 → 允许后落盘 → 主会话只收到 `<file_edit_result>` 摘要（无文件原文）→
  再看拒绝路径（LLM 不原样重试、写入 rejected）。

## 八、已知边界

- 子任务预算 20 轮 / 600s，且每次写都要等用户点确认；大改可能超时（部分成果 + `task_id` 续跑兜底）。
- 并行 `file_edit` 改同一文件会冲突：靠 task 派发纪律「文件不交叠」+ 客户端 provider 原子写兜底。
- fs 写一律弹确认（2026-10-16 起无免确认档），与 shell 白名单目录（brew）叠加的「写→执行」提权路径需用户点允许才走得通，
  属 `../shell-tool/design.md` 已记录的既有风险，本票不额外处理。
