# fs 进程工具超时与护栏（grep/glob）

> 日期：2026-09-15
> 状态：✅ 已实现（服务端 `go test` 绿；客户端 `swift test` 绿；FS 进程超时属集成，由 Xcode 真机验证）
> 背景：2026-09-15 subAgent 第二轮试用反馈「grep 客户端超时」
> 关联：`local-tool-output-overflow.md`（溢出落盘）

## 一、问题

子任务 `file_explore` 两路 grep 都撞到「客户端工具执行超时」：

- `static/app/app-proactive.js`（131KB 单文件）连续两次 grep 超时
- `main_logic/` 全目录 grep 超时

后果：该环节未验证，子任务只能标「推断」；两次超时白烧 ~240s（每次 120s）子任务预算。

参数已无法从审计页/子会话捞回（审计页不展示工具参数），故按**结构性问题**修复，不依赖复现。

## 二、成因（代码核对）

| # | 位置 | 问题 |
|---|------|------|
| 1 | `FileSystemToolProvider.swift:74-135` `runCapturedProcessLines` | **客户端全程无进程超时**。`ExecuteContext.deadlineSecs`（≈108s）传下来了，但 fs 从不读取；唯一上限是服务端 `clientToolCaller` 的 120s（`client_tools.go:380,399-403`），到点直接失败。 |
| 2 | 同上 `:82-83` | `standardError = Pipe()` 从不读。子进程 stderr 写满 64KB 管道缓冲即阻塞，stdout 永不 EOF → `readData` 永久阻塞 → 卡到服务端超时。**单文件 grep 超时的最可能原因。** |
| 3 | 同上 `:108,121` | `terminate()`（SIGTERM）后只排空 stdout，无 SIGKILL 兜底；子进程忽略 SIGTERM 即挂死。 |
| 4 | `+Grep.swift` rg 参数 | `--hidden`（进隐藏目录）+ 无 `--max-filesize`；非 git 仓库时不吃 `.gitignore` → `main_logic/` 可能扫到 `.cache`/`node_modules` 等大目录。 |
| 5 | `+Grep.swift`/`+Glob.swift` | 无任何耗时/参数日志，超时后无法定位。 |

## 三、方案

### A. 进程硬超时 + 部分结果

- `runCapturedProcessLines` 增 `timeout`（默认 30s）：
  - 看门狗（`DispatchSourceTimer`）到点 `terminate()`；宽限 2s 仍运行则 `kill(pid, SIGKILL)`。
  - 返回 `ProcessCaptureResult{capped, timedOut}`。
- grep/glob 超时上限 = `min(context.deadlineSecs ?? 30, 30)`。**必须小于服务端 120s**，让失败快速返回而非白等。
- 超时不丢成果：
  - 有已收集匹配/文件 → 走溢出信封返回（partial + `output_path` + note「grep 超时 30s，仅返回前 N 条」）。
  - 零结果 → 可执行错误：「grep 超时（30s）：<path>。请缩小 path（指定子文件/子目录）、加 include、或简化 pattern」。

### B. 堵管道死锁

- stderr 用后台任务排空（捕获末尾 ~4KB 供日志），消除 64KB 缓冲阻塞。
- `terminate` 后统一 SIGKILL 兜底（见 A 看门狗）。

### C. rg 资源护栏

- grep/glob 的 rg 调用统一加 `--max-filesize 10M`、`--glob=!**/node_modules/**`。
- grep 去掉 `--hidden`（回到 rg 默认：不搜隐藏文件）。代价：要搜隐藏文件须显式指定文件路径——写进 fileExplorePrompt。
- `-j1`（输出确定性）保留。

### D（低成本子集）. 溢出收尾移出主线程

`SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor` 下 `FileSystemToolProvider`/`ToolOutputStore` 默认 MainActor。溢出收尾的 `readFullText`（≤4MB）+ sha256 + 写盘在 MainActor 同步跑。本轮只做**自包含**的移出，不碰隔离级联（不做完整 D）：

- `ToolOutputStore` 标 `nonisolated`（自包含：NSLock + os.Logger + Foundation/CryptoKit，无 MainActor 依赖）。
- `readFullText`/`detectEncoding`/`decodeNonUTF8`/`OutputSanitizer.sanitize` 标 `nonisolated`（纯函数/纯 IO）。
- 读管线 `readFile → readTextFile → readUTF8Text/windowText/renderWindow`、`readCharacterWindow` 改 `async`；溢出的「读全文 + 落盘」放进 `Task.detached { ... }.value`。
- **不动** `DirectoryAuthorizationManager`/`SensitiveFileBlocker` 等其余类型的隔离（完整 D 单独立项）。

### E. 可观测性

- grep/glob 完成后打客户端诊断日志：`fs__grep path=… include=… pattern=… elapsed=…ms matches=… timedOut=… terminated=…`。

### F. 提示词分片纪律（`server/internal/agent/subagent.go` `fileExplorePrompt`）

- 大目录 grep 先限定子目录/单文件或加 `include`；不要对项目根/家目录跑宽泛 pattern。
- 收到超时错误就缩小范围重试；需搜隐藏文件时显式指定文件路径。

## 四、非目标

- 完整 D（fs 全量 `nonisolated`，牵连 `DirectoryAuthorizationManager`/`SensitiveFileBlocker` 加锁）——单独一轮。
- 服务端超时/分片改造（grep 分片由子 agent 在提示词引导下手动做）。
- `fs__stat`、16KB 硬 block。

## 五、测试

- 客户端纯逻辑：`swift test` 保持全绿（本轮无新纯逻辑单测；进程超时属集成）。
- 服务端：`go build ./...` + `go test ./internal/agent/... ./internal/tool/...`（F 仅文案）。
- 真机（Xcode 由用户）：
  1. 大目录宽 pattern grep → 30s 内返回超时错误/部分结果，不再白等 120s。
  2. 单文件 grep → 正常秒回（验证 stderr 死锁已消）。
  3. grep 后诊断日志含 args/耗时/命中/超时。
  4. 并发 3 子任务时 UI 不卡（溢出落盘不再堵主线程）。
  5. 隐藏文件不再被默认搜到；显式指路可搜。

## 六、风险与回滚

| 风险 | 缓解 |
|---|---|
| `--hidden` 去掉后漏搜隐藏文件 | 提示词要求显式指路；需要可加回 |
| SIGKILL 误杀正常进程 | 仅在 terminate 后 2s 仍 `isRunning` 才杀 |
| async 化读管线引入编译错 | 改动限于读文件 1-2 个文件；纯签名 `async` + `await` |
| 30s 对极慢磁盘偏短 | 阈值可配（常量），≥30s 可调 |

**回滚**：改动集中在 `FileSystemToolProvider*.swift` + `ToolOutputStore.swift` + `OutputSanitizer.swift` + 1 处提示词，`git revert` 即可。
