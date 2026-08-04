# 011: 主线程未响应（beachball 僵尸态）——非全屏也复现，真实终止为 SIGTRAP

**日期:** 2026-08-04（首次记录）· 2026-08-04（重大更新：非全屏复现 + SIGTRAP 定性）
**状态:** 监控中（看门狗已修阈值 + 陷阱抓取器已装并实测通过，待复现取证）

## 现象

- 浏览器开着时，**全屏到第二屏**、以及**普通操作（非全屏）**都会出现：app 永久彩球（beachball 一直转不停）、窗口无响应，只能强退。
- 早期误报 `Thread 1: signal SIGTERM`；**真实终止方式是 `SIGTRAP`（EXC_BREAKPOINT）**。

## 证据链

1. **`.ips`（2026-08-04 10:07，pid 82785）**：`Termination: SIGNAL code 5 "Trace/BPT trap: 5" byProc "exc handler"`——**进程被 Swift 运行时陷阱（fatalError / preconditionFailure / 强制解包 nil）或 macOS 对无响应 app 的 hang 终止杀死**，不是 SIGTERM。
2. **挂起时采样**（多次，453/275 采样）：主线程 100% 空等在 `nextEventMatchingMask → mach_msg2_trap`；`WebCore: Scrolling`、`JavaScriptCore libpas scavenger`、`CVDisplayLink`、`NSURLConnectionLoader` 全空闲；**没有任何线程在运行** → 不是 CPU 死循环、不是锁死锁。
3. **定性**：某个操作瞬间主线程**瞬时阻塞**超 macOS 未响应阈值（~2s）→ WindowServer 标记"未响应" → **停止投递事件** → 永久僵尸态，无法自愈。
4. **非全屏也复现** → 与全屏 Space 无关，是**更普适的主线程阻塞/Swift 陷阱**。

## 触发条件（已排除项）

- ❌ 与全屏/多屏无关（普通操作复现）；
- ❌ 代码内无 `runModal`（除罕见兜底）、无 `DispatchQueue.main.sync`、无同步 JS 求值；
- ❌ 无自 SIGTERM/abort 路径（grep 证实，仅杀 MCP 子进程）。

## 监控手段（已落地 + 已实测）

**1. 主线程看门狗** `MainThreadWatchdog.swift`（心跳阈值 **1.5s**，低于 macOS ~2s 判定）：
- 主线程定时器每 0.5s 更新心跳；停摆 >1.5s → `/usr/bin/sample <pid> 3 -file ~/Library/Logs/xhanHelper-hang-<时间戳>.txt`；
- 抓的是**瞬态阻塞本体**（阻塞时心跳停 → 样本即卡住代码）。

**2. 信号级陷阱抓取器**（`installTrapDumper`，已实测通过）：
- `signal(SIGTRAP/SIGABRT/SIGSEGV/SIGILL)` → async-signal-safe handler 写当前线程真实栈到 `~/Library/Logs/xhanHelper-trap.log`（append）；
- 已用 `kill -TRAP` 实测：写入完整符号化栈并正常 re-raise。

## 复现取证流程

1. Xcode Run（**必须重新构建**——此前运行的实例早于本工具）；
2. 正常操作直到复现；
3. 查 `~/Library/Logs/`：`xhanHelper-hang-*.txt`（停摆采样）或 `xhanHelper-trap.log`（Swift 陷阱活栈）；
4. 同时看 Xcode 控制台是否有 `Fatal error:` 行（秒定位 Swift 断言/解包）；
5. 文件发回分析。

## 待办

- [ ] 复现后取证 → 确认阻塞点/Swift 陷阱位置；
- [ ] 若为 Swift 陷阱：修对应 unwrap/precondition/fatalError；
- [ ] 若为主线程阻塞：修阻塞调用（WebKit 同步 IPC / 布局 / 窗口逻辑）；
- [ ] 修复后此文档更新为"已修复"。

## 关联

- `docs/bugs/010-js-dialog-runmodal-sigterm.md`（早期误判为 JS 弹窗）
- `docs/design/mac-apps/browser-tier1-ux.md`
