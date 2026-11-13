# 计划：macOS 本地 Shell 命令执行工具（shell__run）

> 日期：2026-09-16
> 状态：已开发；**2026-09-22 降档**——只读 L2（免确认）、写/执行 L4（红色必弹）；其中「工作区内降 L3」已被 [`readonly-free-and-peek.md`](./readonly-free-and-peek.md) 取代
> **2026-09-22 扩容**：放开一批只读文本处理 / 文件写删 / git 写命令（写/执行类恒 L4）+ 严格参数守卫，
> 见同目录 [`command-expansion.md`](./command-expansion.md)。本文的「v1 冻结」白名单已经是历史值。
> **2026-10-16 起**：信任模式三档移除；确认规则改为「L4（shell 写/执行、browser 写盘/注入系统事件）、fs 写 / fs 读未授权路径、forcedL3 必弹，其余 L2/L3 直接执行」，规则集中在 `ToolConfirmPolicy`，见 `../mac-apps/trust-mode-three-state/design.md` 的「后续变更」。
> 架构：纯 macOS 客户端工具（与 `fs__` / `browser__` 同链路），服务端零改动
> 分支：`feature/shell-tool`

## 一、背景

Agent 目前只能在 Mac 上做结构化文件操作（`fs__glob/grep/read/write/edit`），无法执行终端命令（典型场景：`git status` 查看仓库状态、`ls | head` 快速探查目录）。

本计划新增一个客户端 Shell 工具，让 Agent 能跑终端命令，同时通过**命令白名单 + 人工审批**把风险控制在本人手里。

**硬约束**：命令在用户 Mac 本地执行；服务端仅转发 schema 与结果，严格禁止在服务器上执行任何命令。

## 二、调研结论（opencode，`../opencode`）

opencode 有两份实现：`packages/core/src/tool/bash.ts`（V2 精简内核）与 `packages/opencode/src/tool/shell.ts`（主力完整版）。关键做法：

| 项 | opencode 做法 | xhanHelper 对齐 / 偏离 |
|----|---------------|------------------------|
| 入参 | `command` / `workdir?` / `timeout`（默认 2min，上限 10min） | 同款，超时上限收窄到 100s（< 服务端 240s 预算） |
| shell 选择 | 配置 → 校验；`fish`/`nu` 禁用；darwin 回退 `/bin/zsh`，登录 shell 并 source rc | **偏离**：不走 shell；固定 PATH + 子进程 env 白名单（不 source rc，见六） |
| 进程模型 | 独立进程组，`killTree` 负 PID SIGTERM→SIGKILL；Windows `taskkill /t` | 同款：`posix_spawn` + `POSIX_SPAWN_SETPGROUP`，按组杀 |
| 超时/中断 | `raceAll(exitCode, abort信号, timeout)`，整组 kill；**中断时保全已产出输出** | 超时看门狗同款 + 保全输出；中断由 `ShellProcessRegistry` + `stopStreaming` 补（见六、十一） |
| 输出 | 默认 2000 行 / 50KB，**保留尾部**；超限全文落盘，返回尾部预览 + 文件路径；流式推送进度 | 同款尾部截断 + 两级预算：capture 1–2MB 杀进程 / inject 20KB；超限落 `ToolOutputStore` 返回 `output_path`；**偏离**：无流式（客户端工具只回最终结果） |
| 命令解析 | tree-sitter-bash/PowerShell 解析 AST，枚举所有命令节点（含 `$( )`、管道、子 shell） | **偏离**：no-shell 执行器自行切分（见四） |
| 权限 | 每条命令 `ctx.ask(permission:"bash", patterns, always)`；BashArity 生成 `"<前缀> *"` 白名单 | 借 arity 思路做代码内置三档白名单 |
| 目录 | 文件类命令解析路径参数，项目根外 → `external_directory` 许可 | 复用 `DirectoryAuthorizationManager` 授权目录 |
| 审批 | 交互式权限弹窗（每次询问/永远允许），无限等待 | 复用现有确认条；工作区内 L2 免确认、区外 L3（常规确认）；**偏离**：确认队首 180s 自动拒绝 |

## 三、目标 / 非目标

**目标**：
- `shell__run`：执行一条命令，支持 `|` / `&&` / `||` / `;` 管道与串联。
- 解析层两档（v2）：`allow`（只读 → 工作区 L2 免确认 / 区外 L3 常规确认）/ `l4`（写·执行 → 恒 L4 红色确认）/ 未命中（拒绝）。
- workdir / 路径参数限定在已授权目录内（复用 `DirectoryAuthorizationManager`），另有 Agent 工作区作为默认隐式读写根。
- 超时看门狗、进程组清理、输出预算与落盘分页。

**非目标**：
- 不走 `/bin/zsh -c`，不支持变量展开（`$VAR`）、通配符（`*?~`）、重定向（`>` `<`）、`cd`、后台（`&`）。
- **v1 安全优先，只放只读**；v2 已放开一部分（写/执行类恒 L4），见 `command-expansion.md`。以下为 v1 整类不进的清单：
  - 解释器 / 构建链（实际执行内容藏在文件里、确认框看不到）：`node` `python3` `go` `npm` `npx` `pnpm` `yarn` `pip` `pip3` `brew` `swift` `xcodebuild` `make` `cargo` `gradle` `mvn`。
  - 可执行子命令的文本工具：`sed` `awk` `perl` `xargs` `find`。
  - 文件写/删/权限/归档：`rm` `mv` `cp` `mkdir` `rmdir` `touch` `chmod` `chown` `ln` `tee` `truncate` `rsync` `tar` `zip` `unzip` `gzip`（写文件用 `fs__write` / `fs__edit`）。
  - 网络/进程/系统/容器：`curl` `wget` `ssh` `scp` `sftp` `nc` `kill` `pkill` `killall` `open` `osascript` `docker` `kubectl` `gh`（联网用浏览器；`osascript -e 'do shell script …'` 是完整 shell 逃逸）。
- **git 只读**：agent 只用 git 了解仓库现状；所有写操作（`add` `commit` `push` `config` `clean` …）一律拒绝，不代操作 git。
- 不做用户可编辑白名单（先代码内置）。
- 不做流式输出（长命令需等结束，受 100s 上限约束）。
- 不启用 macOS App Sandbox（见四点 2）。

## 四、威胁模型与关键决策

### 1. 为什么 no-shell（自己切分 + 自己 exec）

opencode 必须用 tree-sitter 精确解析，是因为它**把原始字符串交给 shell 执行**——解析器就是安全边界，差一点就是绕过。

本方案相反：**命令字符串从不交给 shell**。我们自己按 `|` / `&&` / `||` / `;` 切分、自己 tokenize、每段直接起子进程。因此：

- `$( )`、反引号、`;` 注入、`eval` 等元字符**没有解释器去执行**，白名单不可能被绕过；
- 切分器即使有 bug，最坏结果是命令跑失败或 exec 一个不存在的程序名，**不可能执行到未打算执行的东西**。

**结论**：解析器不是安全边界，只需"够用"，无需引入 tree-sitter（省掉 SPM 依赖、ABI 对齐、vendored C 构建等成本）。

### 2. 为什么不用 macOS 沙箱

- App 当前**未启用沙箱**：`client/xHelper/xHelper.xcodeproj/project.pbxproj:435` → `ENABLE_APP_SANDBOX = NO`，无 `.entitlements`，`ENABLE_HARDENED_RUNTIME = NO`。
- 现有 `fs__grep`/`fs__glob` 已在用 `Process` 起子进程（`FileSystemToolProvider+Grep.swift`），起命令无额外障碍。
- 反向：**启用 App Sandbox 会让现有本地工具（任意路径读写、起 grep）全部受限**，等于重做整套 `fs__`/`browser__`。成本极高，收益有限。
- `sandbox-exec`(seatbelt) 被 Apple 废弃、profile 无文档、跨版本脆弱，不作依赖。

**结论**：confinement 由代码承担——白名单 + 授权目录校验 + L4 人工确认（与 opencode 同思路，它也不靠沙箱）。

### 3. 为什么 L4 复用客户端工具审批机制（而非新开通道）

**先澄清：只有三类会弹确认条，其余 L3 直接放行。** 判据集中在 `ToolConfirmPolicy`（`awaitToolConfirmation` 内部已无「跳过」分支）：

```swift
// ToolConfirmPolicy.requirement(level:isFileSystemTool:) → 等级到「弹不弹」的唯一映射
if level >= 4 { return .hardened }                  // 红色必弹
if level == 3, isFileSystemTool { return .confirm } // fs 的 3 = 写文件 / 读未授权路径
return .none                                        // 其余直接执行
```

| 情况 | 判据 | 行为 |
|------|-------|----------|
| 普通 L3（`riskLevels` 表的 `browser__open`） | `.none` | **直接执行**（等价于原「信任档」语义） |
| 强制确认（代码里的 `forcedL3`：高敏域 / http 明文页 / 用户标签授权） | 调用方只升不降 | **必弹** |
| fs 写 / fs 读未授权路径 | `.confirm` | **必弹**（批准后该路径临时授权 1h） |
| L4（shell 写/执行、browser download） | `.hardened` | **必弹**，红色 |

也就是说，代码里**已经存在"不可跳的确认"**，只是它表现为 L3 上的一个 `forcedL3` 开关，而非独立等级。

**L4 既不是新通道也不是新机制**，而是「等级判定返回 4 → 强制 `force=true` → 加红色 UI」。确认机制本身建在**客户端工具已有的 RPC 通道**上（服务端 `client_tool_call` → Mac 执行 → `POST /api/chat/tool-result` 回传，`ClientToolBridge` 按 `call_id` 阻塞等待）。确认只是 Mac 在**执行该 call 之前**加的闸门，与结果共用一次往返；`ui__ask_user` 已是同款先例。因此无需新接口、新事件类型。

## 五、函数 schema

```
name: run
namespace: shell
display: 执行命令
description:
  在用户 Mac 上执行一条终端命令。只用于"运行命令"，不要用它读文件/找文件（那些用 fs__ 系列）。
  - 命令在本地运行，与用户同名权限；写/执行类命令必弹红色确认（无免确认档）。
  - 支持管道与串联： |  &&  ||  ;
  - 不支持：变量展开（$VAR）、通配符（* ? ~）、重定向（> <）、cd、后台（&）。
    需要切换目录请用 workdir 参数；需要找文件用 fs__glob。
  - 输出会进入上下文（约 20KB 上限）。输出可能很大时务必在源头收窄：
    `| head -50`、`| tail -100`、`git log --max-count=20 --oneline` 等；
    超大输出会自动落盘并返回 output_path，用 fs__grep / fs__read 续读。
  - 工作目录必须在用户已授权的目录内，否则会被拒绝。
parameters:
  command: string（要执行的命令）
  workdir: string（可选，工作目录绝对路径；默认用户 Home，需已授权）
  timeout_secs: integer（可选，默认 60，上限 100）
isReadOnly: false
```

## 六、执行内核

新增 `ShellToolProvider` + `ShellCommandParser`（均为 `#if os(macOS)`）。

### 切分与执行

1. quote-aware 切分出顶层 `|` / `&&` / `||` / `;` / 换行的操作数与操作符。裸 `&`（后台）**显式拒绝**。
2. 每个操作数 quote-aware tokenize 成 argv（支持单/双引号与反斜杠转义；无任何展开）。
3. 解析 argv[0]：
   - 含 `/`（绝对/相对路径）→ 解析后**所在目录必须 ∈ 受信 PATH 目录**，否则拒绝（防 `/tmp/x/git` 冒充）。
   - 否则在固定 PATH 中查绝对路径，找不到 → 拒绝。
4. 参数中前导 `~` 用 `FileSystemToolProvider.resolvePath` 展开（确定性，安全）。
5. 逐段起子进程（见下"进程组"），`Pipe` 把上一段 stdout 接下一段 stdin；`&&` / `||` 按退出码决定是否继续；`;` 无条件继续。
6. 白名单判定在**执行前**完成（见七）；任一档不通过则不执行。

### 环境（不泄露任何凭据）

- **固定 PATH**：`/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin`。不用用户真实 PATH——既避免读 `.zshrc` 的副作用，也避免用户可写目录（`~/bin` 等）劫持白名单命令（写→执行提权）。已砍掉各语言运行时，固定 PATH 足以覆盖 `git`/`rg`/`gh` 等 brew 命令。
- **子进程 env 白名单**，不继承 app 环境：仅 `PATH / HOME / USER / LOGNAME / SHELL / TMPDIR`，`TERM=dumb`。**app 里的 API key / token 从根上不进入子进程**（`printenv` 也不在白名单，双重防线）。输出再过 `OutputSanitizer` 兜底残漏。
- **固定覆盖**：`GIT_PAGER=cat`、`PAGER=cat`、`GIT_EDITOR=true`、`GIT_TERMINAL_PROMPT=0`、`GIT_CONFIG_NOSYSTEM=1`、`GIT_EXTERNAL_DIFF=`（防 pager/editor/交互挂起，减外部 diff 执行）。
- **git 额外注入**：所有 `git` 调用自动加 `-c core.hooksPath=/dev/null`（禁用 `.git/hooks`，防 `commit/push` 触发隐藏脚本）。

### 进程组

用 `posix_spawn` + `POSIX_SPAWN_SETPGROUP` 起独立进程组，`stdin` 接 `/dev/null`（防交互式命令卡死等输入）。**超时或用户停止时按进程组 `kill(-pgid)`**，避免 `xargs` / `rsync` / `find` 之类 fork 出的子孙变成孤儿（Foundation `Process` 不暴露 setpgid，故不走它）。

### 超时

- 看门狗到 `timeout_secs` → `SIGTERM` 进程组，宽限 2s 后 `SIGKILL`；排空管道时**只丢弃不累积**。
- `timeout_secs` 默认 60、上限 100；0/负/超上限 → clamp。
- **保全已捕获输出**：超时被杀时，返回杀之前已读到的输出 + 超时标记（对齐 opencode 的 abort 测试），而不是只回一句"超时"。

### 输出（两级预算 + 落盘分页）

- **capture 硬上限 = 2MB**：stdout+stderr 按到达顺序合并，累计到 2MB 即杀进程组、停止读取（内存有界）。**超出的字节直接丢弃、无法恢复**——所以这是"截断"，不是无损缓冲。
- **尾部截断**：按 `budgetCap(context.budgetBytes)`（注入预算 20KB − 信封余量）**保留尾部**（错误/汇总在末尾），对齐 opencode `tail()`。
- **单行限制**：注入用的展示文本按行截断超长行（`maxLineChars=2000`），并追加 `…[+N chars truncated]` 标记。
- **清洗**：归一 `\r`（进度条）、剥离 ANSI、剔除 NUL/控制字符、无效 UTF-8 明确提示。
- **落盘**：把 **raw（原样捕获，未经清洗/截行）** 写 `ToolOutputStore`，`output_path` 内容因此不受展示层清洗影响。字段：`truncated`（预览被截）、`stored_bytes`（output_path 实际字节数）、`capture_truncated`（命中 2MB 硬上限、确有字节丢失）。
- **如实告知 LLM**：
  - 仅超注入预算（未命中 2MB）：note 说"已**完整**存入 output_path（N 字节）"；
  - 命中 2MB 硬上限：`capture_truncated=true`，note 明说"超出部分**已被丢弃且无法恢复**；output_path 里只有已捕获的 N 字节，**不是完整输出**，请从命令侧收窄"——不再谎称"完整"。
- **脱敏**：进上下文的预览过 `OutputSanitizer.sanitize`（落盘原文仍在本机，读回时再脱敏）。

**无流式**——客户端工具只回最终结果，长命令需等结束。

### workdir

必须落在 `DirectoryAuthorizationManager` 已授权目录内（realpath 复检，防 symlink 逃逸）。**默认 workdir = Agent 工作区**（`~/Library/Application Support/xhanHelper/workspace`，App 自有、隐式授权、可读写）；其它目录需**手动授权**（「设置 → 文件访问授权」），越权 → 直接拒绝并列出当前已授权目录（便于模型换目录重试）。命令里引用的**文件路径参数**同样受约束（严格 `isAuthorized`，见七）。

### Agent 工作区（默认可接触目录）

- 位置：`~/Library/Application Support/xhanHelper/workspace`（`AgentWorkspace`），启动时 `ensureExists()`。
- 隐式**读写**授权：`DirectoryAuthorizationManager.isAuthorized` 命中即放行，无需手动添加。只此一个子目录，**不含**同级 `tool_output/`。
- 仍走确认：`fs__write/edit` 写工作区**照旧 L3 确认**、shell 照旧 L4 确认——免的只是"目录授权"这一道。
- 安全性：工作区在 App 自有目录内，写入内容不在 `PATH`、不被执行；`grep -r` 扫不到用户个人文件。设置页显示路径 + 「在访达中打开」。

### 并发 / 线程隔离

主循环客户端工具串行，但 `task` 子 agent 可并发 → 多个 shell 可能同时跑。全局限流（并发上限 2）。

**关键**：项目开了 `SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor`，未标注的声明默认 `@MainActor`。执行链（`executeStages` / `runPipeline` / `spawn` / `buildResult` …）与静态常量必须显式 `nonisolated`，阻塞的 `read()`/`waitpid()` 才能跑在 `Task.detached` 的后台线程；否则会落在主线程 → 耗时命令出彩球。入口 `toolSchemas/blockedReason` 保持 MainActor（只做轻量判定，由 registry 在主线程调用）。

### 停止

运行中的 shell 进程组登记到 `ShellProcessRegistry`；`ChatViewModel.stopStreaming()`（`ChatViewModel.swift:853`）除断 SSE + 通知服务端外，还调用 registry 按组杀，否则命令会一直跑到自己的超时。

## 七、白名单（v1 冻结；**v2 已扩容，见 `command-expansion.md`**）

判定按 `argv[0]` 的 basename 精确匹配（先解析到受信 PATH 目录内的绝对路径）；`git` 另按子命令/参数判定。

### allow（自动执行，只读，v1 共 29 个）

`ls pwd head tail wc file stat which type du df tree basename dirname realpath cmp diff md5 shasum echo grep rg date whoami hostname uname id sw_vers arch`

- **`cat` 不在列**：读文件走 `fs__read`，避免绕过敏感文件拦截。
- 版本查询：命令已在白名单中且参数仅 `--version` / `-V` → allow（不做 `* --version` 通配）。
- **v2 追加**：`sort uniq cut tr nl column jq`（其中 `sort -o` 会写文件，已单独拦截）。

### l4（v1 为空集；v2 已填实）

v1 时 `l4Commands` 为空集：shell 整体在 `LocalToolRegistry` 定级——工作区内 L3（后降为 L2）、区外 L4，解析层只区分「只读白名单（可执行）/ 未命中（拒绝）」。

**v2 起 `l4Commands` 填实**（写 / 执行类，恒 L4，工作区内也 L4）：`find sed awk rm mv cp mkdir rmdir touch ln chmod chown tee truncate`，另有 `find/sed/awk` 的形式守卫与写路径校验。详见 `command-expansion.md` 四、五。

### git（v1 只读；v2 起放开明确写子命令）

- 任意参数：`git status / log / show / diff / blame / rev-parse / ls-files / shortlog / describe`
- 仅只读形式：`branch`（`-l/--list/-a/-r/-v/-vv/--contains/--merged/--points-at/--sort=*/--format=*`…）、`remote`（`-v/--verbose/show/get-url`）、`tag`（`-l/-n/--contains/--points-at/--sort=*/--format=*`…）、`stash`（`list/show`）
- **v2 起写子命令 → L4**（不再一律拒绝）：`add commit checkout switch restore reset clean merge rebase cherry-pick revert`、`stash push/pop/apply/drop`、`tag -a/-d/-m`、`branch <name>/-d/-m`、`remote add/remove/set-url`。仍拒绝：`push/fetch/pull/clone/config/init`。
- 其余未列出的 git 子命令 → **拒绝**。

### 未命中 → 拒绝（无需单独 deny 列表）

`sudo` / `shutdown` / `launchctl` / `diskutil`，以及"非目标"列出的全部解释器/构建链（`node python3 go npm pip brew make xcodebuild …`）、`perl xargs`、归档类（`rsync tar zip unzip gzip`）、网络/进程类（`wget ssh scp sftp nc kill open osascript docker kubectl gh`）——天然被拒。（v2 起 `sed awk find` 与 `rm mv cp chmod ln` 等已放出，见上。）

### git 全局选项

`git -c …` / `git --no-pager …` 会让子命令落到未命中 → 拒绝，恰好挡掉 alias 注入（`git -c alias.x='!rm -rf /' x`）。我们**自己注入**的 `-c core.hooksPath=/dev/null` 不参与匹配（匹配用用户的原始 token）。

`git diff --no-index` 可绕过仓库读取任意文件（含 `~/.ssh` 私钥）→ 硬拒。

### 敏感路径 / 授权目录拦截

allow 档命令（`head` / `tail` / `grep` / `rg` / `wc` / `diff` …）的参数里凡是"像路径"的值，相对路径一律相对 workdir 解析（不用进程 cwd），然后：

1. `FsPath.resolve`（realpath，防 symlink 绕过）后过 `SensitiveFileBlocker.check`（挡 `head ~/.ssh/id_rsa`、`.env`、`.pem` 等私钥/凭据）；
2. **确实存在**的路径必须 `DirectoryAuthorizationManager.isAuthorized`（已授权目录 + Agent 工作区隐式读写根），否则拒绝——`head /etc/hosts`、`ls /usr`、`grep -r x /etc` 都会被拒。

这里用严格 `isAuthorized`，**不用** `isReadAuthorized`：`tool_output/` 溢出落盘目录只对 fs 工具开只读例外（`fs__read/grep/glob` 读回时过 `OutputSanitizer` 脱敏），shell 不得读取——防止"注入被脱敏、磁盘 raw 原文又被 shell 读回"绕过脱敏。

**递归命令的根目录**：`grep -r/-R/--recursive` 与 `rg`（默认递归）的根目录若是任一硬性拦截路径的祖先（如 `grep -r x ~`、`rg x /`），逐参数 `check` 拦不住被递归读到的子文件，因此在 `pathBlockedReason` 里用 `SensitiveFileBlocker.isAncestorOfHardPath` 直接拒绝（`grep -r x <项目目录>` 不受影响）。

只对"存在"的路径做授权目录校验，避免把含 `/` 的正则（如 `grep '/api/v1' .`）当路径误杀（不存在的参数交给命令自己报错，读不到东西）。

### 可访问目录下发给 LLM（2026-09-16）

子任务原来只能从 description 的「工作区 + 其它已授权目录」猜测能碰哪里，常盲试未授权路径被拒。现在把**具体路径清单**写进工具描述：

- `DirectoryAuthorizationManager.accessSummary()`：Agent 工作区（读写）+ `permanentDirs`（读写，无则给空态文案）+ `tool_output`（只读，仅 fs）。
- `SensitiveFileBlocker.hardBlockedDisplayPaths`：硬性拦截的高危路径（`~` 缩写），一并告知"不要尝试"。
- 二者由 `DirectoryAuthorizationManager.accessDescriptionBlock()` 拼成一块，追加到 `fs__glob/grep/read/write/edit` 与 `shell__run` 的 description 末尾。
- **动态**：description 在 `provider.toolSchemas()` 里拼，`clientToolsPayload()` **每条消息重建**（`ChatViewModel.swift:817`），服务端 `buildClientToolDefs` 原样透传、无缓存 → 用户改授权后下一条消息生效。
- **只进子任务（work 模式）**：fs/shell 属 `subagent_only_client_tools`，work 模式主循环看不到这些 def，prompt 不膨胀。plan 模式例外：只下发 fs 的 `glob/grep/read` 三个只读 def（2026-10-31）。
- **隐私**：目录绝对路径随 `client_tools` 发往 LLM provider（工具调用的 `path` 参数本来也会带）；临时授权目录（1h TTL）刻意不列，保 tool defs 字节稳定。

### 聚合规则

一条命令含多个操作数时，取最高档：**任一操作数未命中 → 整条拒绝**；否则**任一 L4 → 整条 L4**（确认时展示完整原始命令）；全 allow → 自动执行。

## 八、L4 审批链路

现有：`handleClientToolCall`（`ChatViewModel+ClientTools.swift:71`）→ `riskLevel(...)` → `if level >= 3 { awaitToolConfirmation(force: forcedL3) }`。

L4 改四处：

1. `LocalToolRegistry`：
   - 新增 `shellBlockedReason(arguments:) -> String?`（镜像 `fsBlockedPaths`）：未命中白名单时返回拒绝原因；`handleClientToolCall` 在确认前调用，直接回传错误。
   - `riskLevel(for:arguments:)` 加 shell 分支：**v2 起**调用 `ShellToolProvider.shellLevel(arguments:)`（`.l4`→4；`.allow`→ 工作区 2 / 区外 3）。
   - `shortNames` 加 `shell__run: "执行命令"`。
2. `ChatViewModel+ClientTools.swift`：shell 拒绝前置；确认 force 条件改为 `forcedL3 || level >= 4`。
3. `ChatViewModel.swift`：`awaitToolConfirmation(hardened:)` 增加参数；`ToolConfirmation` 增 `hardened: Bool`。
4. `ChatView.swift`：`toolConfirmationBar` 对 `hardened` 显示红色 + "请求执行高危操作"，并展示**完整命令**（不复用现有 `argsSummary` 的 `prefix(80)` 截断）。

超时（队首 180s）视为拒绝（fail-safe，用户已定）。

## 九、改动文件清单

| 文件 | 改动 |
|------|------|
| `client/.../Shared/Tools/ShellToolProvider.swift` | 新增：provider + 执行内核 |
| `client/.../Shared/Tools/ShellCommandParser.swift` | 新增：切分/tokenize/白名单判定/git 只读判定/敏感路径拦截 |
| `client/.../Shared/Tools/ShellProcessRegistry.swift` | 新增：运行中进程组登记 + 按组杀（`stopStreaming` 调用） |
| `client/.../Shared/Tools/LocalToolRegistry.swift` | `shellBlockedReason`、shell 分支、`shortNames` |
| `client/.../Shared/ViewModels/ChatViewModel+ClientTools.swift` | shell 拒绝前置 + force 条件 |
| `client/.../Shared/ViewModels/ChatViewModel.swift` | `awaitToolConfirmation(hardened:)` + `ToolConfirmation.hardened`、`stopStreaming` 杀进程组 |
| `client/.../Shared/Views/ChatView.swift` | L4 红色确认条 + 完整命令展示 |
| `client/xHelperMac/App.swift` | 注册 `ShellToolProvider()`（`LocalToolRegistry.shared.register`） |
| `client/.../Shared/Tools/FsPath.swift` | 新增：抽出路径解析（fs/shell 复用） |
| `client/.../Shared/Tools/AgentWorkspace.swift` | 新增：默认工作区（隐式读写根，启动建目录） |
| `client/xHelper/Tests/FsCoreTests/ShellCommandParserTests.swift` | 新增：解析/白名单判定纯逻辑单测 |

服务端零改动。

## 十、验证用例

1. **allow 直跑**：`ls -la`、`git status`、`head <授权目录文件>` → 无确认直接返回；退出码正确。
2. **管道**：`git log | head -5`、`ls | wc -l` → 正常。
3. **L4 确认**：`ls -la`（shell 全部命令都要确认）→ 弹红色确认条；允许后执行，拒绝后返回"用户拒绝执行"。
4. **L4 必经确认**：跑写/执行类 shell 命令（如 `rm x`）→ 弹红色确认条，无任何开关能跳过。
5. **未命中拒绝**：`sudo ls`、`rm -rf x`、`git commit -m x`、`curl https://x`、`node -e 'x'`、`python3 x.py`、`npm install`、`make`、`wget`、`ssh`、`osascript -e 1` → 直接拒绝，不弹确认。
6. **拒绝项组合**：`ls && sudo rm -rf /` → 整条拒绝（含未命中段）。
7. **越权 workdir**：`workdir=/etc` → 直接拒绝（不走 L4），返回信息列出当前已授权目录。
8. **超时**：`sleep 999`（`timeout_secs=3`）→ 到点终止，返回"已捕获输出 + 超时提示"；进程组被 SIGKILL，无残留；**期间 UI 不卡**（执行在后台线程）。
9. **大输出落盘**：`ls -R <授权目录>` 产生 >20KB（但 <2MB）输出 → `truncated=true` + `output_path` + `stored_bytes`，note 说"已完整存入"，保留尾部预览；`fs__grep(path=output_path)` 可定位。
9b. **2MB 硬上限如实告知**：产出 >2MB 的命令 → `capture_truncated=true`、`capture_limit_bytes=2097152`、`stored_bytes≈2MB`，note 明说"超出部分已被丢弃、**不是完整输出**"，且落盘文件确实只有 ~2MB（不谎称完整）。
10. **敏感文件 / 授权目录拦截**：`head ~/.ssh/id_rsa` → 拒绝（敏感）；`head /etc/hosts`、`ls /usr`、`grep -r x /etc` → 拒绝（授权目录外）；`grep '/api/v1' .`（正则含 `/`）→ 允许（不误杀）。
10b. **Agent 工作区**：不传 workdir → 落工作区；工作区内文件 `head "<file>"` → 允许（隐式授权）。**shell 不得读 `tool_output/`**：`head "<tool_output 文件>"` → 拒绝（该例外只给 fs）。
11. **env 不泄漏**：子进程看不到 app 的 API key（env 白名单）；输出过 `OutputSanitizer`。
12. **停止按钮**：跑 `sleep 999` 中途点停止 → 进程组被 `stopStreaming` 杀掉，无残留进程。
13. **进程组清理**：fork 子孙的命令超时后，子孙一并被杀。
14. **git 只读**：`git status` / `git branch` / `git stash list` 放行；`git commit` / `git push` / `git branch new` / `git tag v1` / `git remote add x y` → 拒绝。
15. **绝对路径**：`/tmp/evil/git status`（→ 拒绝，不在受信目录）；`/bin/ls` → allow。
16. **元字符不可绕**：`echo $(whoami)` → 因不支持展开，返回字面量/明确错误，**不执行** `whoami`；`sleep 1 &` → 拒绝。
17. **plan 模式**：客户端工具默认禁用，仅 fs 的 `glob`/`grep`/`read` 放行（服务端白名单），shell 天然不可用。

## 十一、边界与兜底

shell 的结果要进上下文，进程又跑在用户 Mac 上，边界处理是设计的核心。

| 边界 | 兜底 |
|------|------|
| 大文件读取（`cat 100MB`） | 不走 shell。读文件用 `fs__read`（行/字符分页、溢出落盘 `output_path`、>4MB 拒绝整读并提示用 `fs__grep`）；`cat` 已移出 allow |
| 命令输出过大 | 源头收窄（`\| head`、`--max-count`）+ 两级预算：capture **2MB 硬上限**（超出丢弃）/ inject 20KB **尾部**预览；被截时落 `ToolOutputStore` 返回 `output_path` + `stored_bytes` |
| **2MB 上限导致字节丢失** | 返回 `capture_truncated=true` + note 明说"超出部分已丢弃、output_path 也非完整"，不谎称"完整"；落盘存 **raw**（不被展示层截行影响） |
| 无限递归（shell 级 `for` / `while` / fork 炸弹 / `eval`） | **不存在**：no-shell，这些构造没有解释器；`for` 等不在白名单 → 直接拒 |
| 失控读（`grep -r /`、`ls -R /`） | 不侦测；**墙钟超时**（60s，上限 100s）+ 输出预算兜底，先到先杀 |
| 单命令卡死 / 等输入 | 看门狗 SIGTERM→2s→SIGKILL；`stdin=/dev/null`；pager/editor 固定为 `cat`/`true`；`vim` / `less` / `top` 不在白名单 |
| fork 出子孙 | `posix_spawn` + `POSIX_SPAWN_SETPGROUP`，按进程组杀 |
| 超时后输出丢失 | 保全杀之前已捕获的输出 + 超时标记 |
| 用户点停止 | `ShellProcessRegistry` + `stopStreaming` 按组杀（新增：原 `stopStreaming` 杀不掉本地命令） |
| **env / API key 泄露** | 子进程 env 白名单（不继承 app 环境、不 source rc）；输出过 `OutputSanitizer` 脱敏 |
| PATH 被用户可写目录劫持 | **固定 PATH**；绝对路径 argv[0] 必须落在受信目录 |
| 敏感文件泄露（`head ~/.ssh/id_rsa`） | 路径 realpath 后过 `SensitiveFileBlocker`（含**不可豁免**的硬层）；输出过 `OutputSanitizer` |
| **递归读进高危路径（`grep -r x ~` 打到 `.ssh`）** | `pathBlockedReason` 用 `isAncestorOfHardPath` 拒绝根目录为高危路径祖先的递归命令 |
| **`git diff --no-index` 读任意文件** | `classifyGit` 硬拒该参数，绕过仓库路径约束的读被堵 |
| **读授权目录外的文件（`head /etc/hosts`）** | 路径参数解析后必须 `isAuthorized`（已授权目录 + Agent 工作区）；只对存在的路径校验，避免误杀含 `/` 的正则 |
| **读 `tool_output/` 溢出原文（绕过脱敏）** | shell 用严格 `isAuthorized`（不含该只读例外）；例外只留给 fs，读回时再过 `OutputSanitizer` |
| `grep -r` 扫到 `.env` / 凭据 | 目录参数无法逐文件拦；靠 `OutputSanitizer` 事后脱敏（残余风险，见十二） |
| 隐藏代码（Makefile / npm scripts / build.rs / hooks） | 整类解释器/构建链不进白名单；git 调用注入 `core.hooksPath=/dev/null` |
| 不可信仓库触发外部 diff/fsmonitor | `GIT_EXTERNAL_DIFF=`、`GIT_CONFIG_NOSYSTEM=1`（残余风险，见十二） |
| 二进制 / NUL / ANSI / `\r` 输出 | 非 UTF-8 明确提示改用 `fs__read`；归一 `\r`、剥离 ANSI、剔除控制字符 |
| 单行超长（minified） | 注入前按行截断（`maxLineChars=2000`） |
| L4 审批被截断 | 确认条展示**完整命令**，不复用 `prefix(80)` |
| 并发 shell | 全局并发上限 2 |
| **主线程卡死（长命令）** | 执行链 `nonisolated` + `Task.detached`，阻塞 `read/waitpid` 不落主线程（项目默认 `MainActor` 隔离，需显式标注） |
| 跨轮上下文膨胀 | 单次 20KB 上限；跨轮膨胀见 `docs/design/context-window-compaction.md` |

## 十二、已知限制 / 后续

- 无变量展开、通配符、重定向、`cd`、后台、流式输出——按需再加（加时限制在授权目录内）。
- **不可信仓库残余风险**：`.gitattributes` 的 filter/textconv、`core.fsmonitor` 等仍可能借 git 执行代码；已做 hooksPath/env 加固，但不完全封死。
- **只能约束显式路径参数**：程序自身的隐式读取（git 读 `~/.gitconfig`、进程读 `$HOME` 下的配置、动态库等）不受授权目录约束，除非上沙箱。已把环境变量收窄到白名单、并做 `GIT_*` 加固。
- **Agent 工作区是唯一的隐式读写根**（App 自有目录），是"目录必须手动授权"原则的显式例外；`tool_output/` 的只读例外仅服务 fs，shell 不共享。
- **`grep -r` 扫盘**：在授权目录内递归仍可能扫到未被 `SensitiveFileBlocker` 覆盖的凭据文件（如 `.aws/config`、`.netrc`、`.npmrc`、`.docker/config.json`、`.kube/config`、`.zsh_history`、含 token 的 `.gitconfig`），仅靠 `OutputSanitizer` 正则脱敏，覆盖面有限。
- 白名单为代码内置；后续若要用户可编辑，再引入 `UserConfigStore` + macOS 设置页（`SettingsCard`，遵循 `docs/design/mac-apps/settings-sheet-style.md`）。
- 不做任何"代码藏在文件里"的构建/运行链，因此 Agent 无法在 Mac 上编译运行项目（如 `go build` / `npm test`）；这类验证需用户在终端自行完成。
- 客户端工具串行 → 长 shell 会阻塞同一轮的其他客户端工具（浏览器等）。
- **brew 目录在受信 PATH 内**：`/opt/homebrew/bin`、`/usr/local/bin` 归当前用户所有，理论上可被 `fs__write` 写入同名程序冒充白名单命令（再被 allow 档自动执行）。`fs__write` 现为**必弹确认**（2026-10-16 起无免确认档），这条"写→执行"提权路径需要用户在确认条上点允许才走得通，风险已大幅收窄；彻底封堵仍需给 fs 写加独立校验。
- **实现落地**：`FsPath.swift` 抽出路径解析供 fs / shell 复用；解析与白名单判定的单测在 `client/xHelper/Tests/FsCoreTests/ShellCommandParserTests.swift`（`cd client/xHelper && swift test`，纳入既有 FsCore SPM 测试包）。

## 十三、归属变更（2026-09-16）：shell 收进 file_explore 子任务

主循环对 LLM 隐藏的客户端工具组由 `chat.subagent_only_client_tools` 控制（当时默认 `[browser, fs]`；2026-10-15 起 browser 已放回主循环，见 docs/design/mac-apps/browser-main-session.md）。原先 shell 暴露在主循环，带来两个问题：

1. **output_path 读不回来**：shell 大输出落盘后 note 让模型用 `fs__read` 续读，但主循环**没有** `fs__*`（只在 `file_explore` 子任务里），模型只能再派一个子任务去读——多一跳且别扭。
2. **打爆主上下文**：shell 输出直接进主循环上下文。

决策：**把 `shell` 也加入 `subagent_only_client_tools`，只给 `file_explore` 子任务**（`clientWhitelist` 增 `shell: run`），主循环不再暴露。这样：

- note 里的 `fs__grep` / `fs__read(output_path)` 落在**同一子任务上下文**，零额外派发。
- shell 输出天然不进主上下文。
- 代价：主循环不能直接跑命令，任何命令都要派 `file_explore`（多一轮）；子任务里**每条命令一次 L4 红确认**（用户已接受）。

不改类型名、不改 XML 根标签（`<file_explore_result>`）、不动 DB 里的 `subagent_type`——零兼容风险；只是把 `fileExplorePrompt` 与 `task.go` / `static.go` 的描述从「文件检索」扩到「文件 + 只读命令」。

配置默认值：`server/conf/config.yaml:chat.subagent_only_client_tools` 与 `server/internal/config/config.go` 兜底均为 `[fs, shell]`（当时为 `[browser, fs, shell]`；browser 已于 2026-10-15 放回主循环）。

### 工作区内降 L3（同一批）

子任务在 Agent 工作区里随手 `ls` / `git status` 每条都弹红色 L4 太吵。决策：**命令完全落在 Agent 工作区内 → L3**（常规确认）；工作区外（含用户手动授权目录）维持 L4。

- 判定 `ShellCommandParser.isScopedToAgentWorkspace`：workdir ∈ workspace **且**所有**实际存在**的显式路径参数 ∈ workspace。只统计存在的路径，避免 `grep '/api/v1' .` 这类正则被误判为越界。
- 该降档**后来被 `readonly-free-and-peek.md` 取代**：shell 只读恒 2、写/执行恒 4，不再按工作区内/外分档（`isScopedToAgentWorkspace` 因此无调用方）。确认与否由 `ToolConfirmPolicy` 按等级判定（`need == .hardened` 出红色条），本小节只作历史记录。
- 让步：`tool_output` 仍只对 fs 开只读例外、shell 不可读；`fs__write/edit` 为必弹确认，不再存在"一键跳过"。
