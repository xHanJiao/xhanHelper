# macOS 工具审批条被并发覆盖，被顶掉那条永久悬挂

## 问题表现

同时在即要审批两件客户端工具调用（典型：多个子任务各自要批准）时，第一条审批条刚弹出、用户还没点，第二条就到并把第一条顶掉；用户点完第二条后，第一条不见了，对应的操作也不会再执行。

## 根因

审批状态是**单槽可选值**，扛不住并发。

- `ChatViewModel.pendingToolConfirmation: ToolConfirmation?`（`ChatViewModel.swift:387`）注释假设「同一时刻最多一个，客户端工具顺序执行」。
- `handleClientToolCall` 对每条 `client_tool_call` SSE 各起一个独立非结构化 `Task`（`ChatViewModel+ClientTools.swift:42`），无串行/排队。
- `awaitToolConfirmation` 无条件 `pendingToolConfirmation = …` 覆盖（原 `:588`）。

并发来源：主循环客户端工具在服务端是串行的，但**子任务会把各自的 `client_tool_call` 经同一主 SSE 并发转发**（`server/internal/agent/subagent.go:793`），客户端因此会同时收到多条。

失效链：第二条覆盖第一条 → `confirmationResolved` 共享标志被重置 → 用户点第二条只 resume 它自己 → 第一条的 continuation 在 60s 超时任务醒来时因 `pendingToolConfirmation?.id == myID` 为假而直接 `return`，**永不 resume**，对应客户端工具调用永久悬挂（服务端只能等自己的 120s 预算）。

同文件里的 `ask_user` 是正确范本：`pendingUserRequests: [UserRequest]` 数组 + 按 `callID` 精确 resolve，不受影响。

## 最终方案（用户拍板：改队列 + 超时调长）

客户端确认从单槽改为队列，只显示队首：

- `pendingToolConfirmations: [ToolConfirmation] = []`；删除 `confirmationResolved`（改用「按 id 移除」保证只 resume 一次）。
- **只给队首计时**（180s）：队首变化时重臂 `confirmationTimeoutTask`；排队条目在被看到之前不计时，杜绝「默默失效」。超时 = 以 `allow: false` 走同一条 resolve 路径，然后自动轮到下一条。
- UI 只显示队首 + 「另有 N 条待确认」角标；四个按钮改传 `conf.id`；键盘 Tab/Esc 作用于队首（`resolveTopToolConfirmation`）。
- diff 放大浮层改监听 `pendingToolConfirmations.map(\.id)`，本条目不在集合里即关闭。
- 超时 60s → **180s**。为覆盖新审批时长，服务端 `llm.client_tool_timeout_secs` 120 → **240**（`server/conf/config.yaml`，`orchestrator.go`/`mini_react.go` 的兜底默认同步 240）。
- **执行预算按「剩余」重算（修掉结构性过订）**：原代码在确认**之前**就算好 `deadline = timeout_secs × 0.9`（216s），确认吃掉 180s 后 `ExecuteContext` 仍按 216s 计 → 工具会跑过服务端 240s 预算（服务端已放弃，客户端还在跑，有副作用命令会重复执行）。改为审批结束后重算：`execDeadline = 服务端预算 × 0.9 − 已耗排队/审批时间`；**若 `elapsed ≥ 服务端预算` 直接跳过执行**（不落盘、不回传注定 404 的结果）；服务端未下发 `timeout_secs` 时保持 `nil`（各工具回退自身默认：MCP 54s / fs 30s）。口径：**执行余量 = 服务端预算 − 确认耗时**。

**已知边界（已接受）**：

- **排队超预算**：只给队首计时意味着排队越深、总耗时越长，极端情况下后面条目的服务端 240s 预算可能先到期；此时用户点允许会回传 404（`client_bridge.go` 的 `unknown call_id`），客户端记日志、不崩不挂——诚实兜底，不再有静默悬挂。已叠加「超预算跳过执行」，即使 404 也不会产生副作用。
- **超预算条目不清理（后续待办）**：客户端没有对"服务端已过期"的排队条目做失效标记，它仍会正常上位、计时、弹窗，直到点下去才 404（"看得见却注定失败"，此路径现在会在执行前被 `elapsed ≥ 预算` 拦下）。彻底解法：服务端超时时推一条事件，客户端据此移除对应 `callId` 的排队项。当前未做。

## 验证

- `go build ./... && go test ./...` 全绿。
- `xcrun swiftc -typecheck`（macOS 26.3 真实设置，Shared + xHelperMac）0 error。
- 真机（用户在 Xcode）：两个子任务同时触发 L3 → 只显示第一条 +「另有 1 条」→ 处理完第一条，第二条自动上位并重新计 180s；单条 180s 不点 → 自动拒绝并回传。

## 经验教训

「同一时刻最多一个」这类注释是并发的定时炸弹：只要上游存在并发路径（此处是子任务 SSE 转发），单槽状态就会被覆盖，且被覆盖者的 `withCheckedContinuation` 会永久悬挂，没有编译错误、没有崩溃，只是一次调用悄无声息地丢失。凡是承载「等待外部应答」的状态，用队列（数组）+ 按 id 精确解决，别用单槽 + 共享布尔标志。
