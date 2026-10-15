# 计划：只读命令免确认 + 低耗命令下放主循环（人话版）

> 日期：2026-09-30
> 分支：`feat/shell-tool`
> 前置：[`design.md`](./design.md)（v1/v2 白名单与安全模型）、[`command-expansion.md`](./command-expansion.md)
> 状态：**已实现**（macOS/iOS typecheck 通过、FsCore 40 项单测通过、`go build ./... && go test ./internal/agent/...` 通过）

## 〇、一句话

`xhanHelper` 是自用工具，没必要像防外人一样防自己。两件事：

1. **只读命令全部免确认**——不再分"工作区内免、工作区外要点头"。
2. **把一小撮"看一眼就跑、不会灌一堆内容"的只读命令下放给主循环**——省掉"先派子任务"这一跳；会扫盘/读大内容的命令仍只走子任务。

安全底线不变：**白名单 + 敏感文件硬拦 + 授权目录**照旧，"免确认"放开的只是审批动作，不是读取边界。

---

## 一、改动一：只读命令全部免确认

### 改法

`ShellToolProvider.shellLevel(arguments:scope:)` 里 `.allow` **恒返回 2（免确认）**，不再分工作区内外：

| 命令类型 | 现在 | 效果 |
|---------|------|------|
| 只读（`ls`/`git status`/`head`…） | L2 | 免确认（**改动**：原先工作区外是 L3） |
| 写/执行（`rm`/`mv`/`git commit`…） | L4 | 红色确认，工作区内也要确认（不变） |

### 为什么可以这么放开

只读命令本来就被两层网兜住，L3 那道确认更多是心理安慰：未命中白名单的命令在解析阶段直接拒；敏感文件/越权目录在 `evaluate()` 阶段就拒——**都在确认之前**。用户点不点"同意"，读到的东西一样。

### `isScopedToAgentWorkspace` 的去留

**保留**（`ShellCommandParser.isScopedToAgentWorkspace` 与其 provider 包装 `isWorkspaceScoped`）。当前只读恒 L2、写恒 L4，已无调用方；留作未来"写命令工作区内降档"用，注释已标注。

---

## 二、改动二：只读探查命令下放主循环

### 思路

服务端 `chat.subagent_only_client_tools: [browser, fs, shell]`（当时值；browser 已于 2026-10-15 放回主循环，见 docs/design/mac-apps/browser-main-session.md）把三组客户端工具对主循环隐藏，只给子任务。`git status`、`ls` 这种输出很少的探查也被迫走"派子任务 → 跑 → 摘要"三跳，为省 token 反而费事。

不是把整个 `shell` 下放，而是**新开一个只读探查工具组 `shell_peek`**，只放"看一眼、输出天然有界、不读文件正文"的命令。会扫盘/读内容/输出不可控的仍留在完整 `shell`（仅子任务）。

### 新增工具组 `shell_peek`

- namespace `shell_peek`，函数 `run`，展示名"查看命令"，`isReadOnly = true`。
- 复用完整 shell 的**解析器 / 进程组 / 超时 / 输出预算 / 安全拦截**，只换一套更小的命令白名单。
- 只读 → **免确认**（改动一的效果）。

### `shell_peek` 白名单（已定稿）

只放**返回元信息 / 状态、不返回文件正文**的命令：

| 类 | 命令 |
|----|------|
| 定位 / 环境 | `pwd` `whoami` `hostname` `date` `uname` `sw_vers` `arch` `which` `basename` `dirname` `realpath` |
| 目录 / 文件元信息 | `ls`（**禁 `-R`**）`stat` `file` `wc` `du`（**仅 `-s`/`--summarize`**） |
| git 状态（有界） | `git status` `git rev-parse` `git branch --show-current` `git log -n N`（**必须带 `-n`/`--max-count`，`1 ≤ N ≤ 20`，多个 count 取最后一个**） |

带形式守卫的三条：`ls -R`/`--recursive` 拒；裸 `du`（逐目录列出）拒；裸 `git log`/`git show`/`git diff` 拒。
`git log` 的 count 取**最后一个**（git 多值时后者胜出），且要求 `1...20`——负值（`git` 当无限制）与 `-n 20 --max-count=99999` 这类绕法都拒。

**刻意不放**（仍只在子任务，因为扫盘 / 读正文 / 输出不可控）：`rg` `grep` `find` `tree` `df` `sort` `uniq` `cut` `tr` `nl` `column` `jq` `diff` `md5` `shasum` `head` `tail` `cat`。

---

## 三、安全兜底（改动一/二都不动它）

下面这些在 `ShellCommandParser.evaluate()` 里就跑完，**发生在弹不弹确认之前**：

### 1. 硬性拦截层（不可豁免，任何授权/信任模式都无效）

- 目录：`~/.ssh`、`~/.gnupg`、`~/.aws`、`~/.kube`、`~/.docker`、`~/Library/Keychains`、`/etc/sudoers.d`
- 文件：`~/.zshrc`、`~/.zshenv`、`~/.zprofile`、`~/.bashrc`、`~/.netrc`、`~/.npmrc`、`~/.git-credentials`、`~/.wgetrc`、`~/.curlrc`、`/etc/passwd`、`/etc/shadow`、`/etc/sudoers` 等

### 2. 敏感模式层

`.env` / `.env.` / `credentials` / `secret` / `id_rsa` / `id_ed25519` / `id_ecdsa` / `.pem` / `.key` / `/.ssh/` / `.git` 内部。

### 3. 递归命令根目录检查

`rg`（默认递归）、`grep -r/-R/--recursive` 的根目录若是任一硬性拦截路径的祖先（如 `grep -r x ~`），直接拒。

### 4. 授权目录

路径参数 realpath 后必须落在「Agent 工作区 + 用户手动授权目录」内。`ls /etc`、`head /etc/hosts` 均拒。

### 残余风险（与改动前一致）

程序隐式读取（不走路径参数）不受授权目录约束；`grep -r` 在授权目录内仍可能扫到未覆盖的凭据文件，仅 `OutputSanitizer` 事后脱敏。详见 `design.md` 第十二节。

---

## 四、实现落点

### 客户端

| 文件 | 改动 |
|------|------|
| `Shared/Tools/ShellCommandParser.swift` | 新增 `ShellScope{full,peek}`；`evaluate`/`classify` 加 `scope`（默认 `.full`）；新增 `peekCommands`、`classifyPeek`、`peekFormBlockedReason`（`ls -R`/`du` 守卫）、`classifyGitPeek`（`git` 状态子集 + `gitLogCount`）、`peekUnavailableReason`（引导派子任务）。保留 `isScopedToAgentWorkspace` |
| `Shared/Tools/ShellToolProvider.swift` | 加 `scope` 实例属性 + `init(scope:)` + `scope(forFlatName:)`；`shellLevel`/`blockedReason` 加 `scope` 参数（`.allow` 恒 2）；`toolSchemas()` 按 scope 分 `fullSchemas`/`peekSchemas`；新增 `groupDescription`（peek 组级静态描述） |
| `Shared/Tools/LocalToolProvider.swift` | 协议加 `groupDescription`（默认 `nil`） |
| `Shared/Tools/LocalToolRegistry.swift` | shell 分支改用 `scope`；`shellBlockedReason(flatName:arguments:)`；`clientToolsPayload` 上报 `server_description`；`shortNames` 加 `shell_peek__run: "查看命令"` |
| `Shared/ViewModels/ChatViewModel+ClientTools.swift` | `isShell` 覆盖 `shell_peek__`（blockedReason 照跑）；hardened/explainer 仅 `.full` |
| `Shared/ViewModels/ChatViewModel.swift` | 确认条完整命令展示前缀放宽到 `"shell"` |
| `xHelperMac/App.swift` | 注册 `ShellToolProvider(scope: .full)` 与 `ShellToolProvider(scope: .peek)` |
| `Tests/FsCoreTests/ShellCommandParserTests.swift` | 加 peek 用例（白名单 / 重命令拒 / `ls -R` / `du` 守卫 / `git log` 守卫 / 安全网） |

### 服务端（透传组描述，约 6 行）

| 文件 | 改动 |
|------|------|
| `internal/agent/client_tools.go` | `ClientTool` 加 `ServerDescription string \`json:"server_description"\``；`buildClientToolDefs` 按 server_name 记录首个非空组描述，缺省回退 `"Mac 客户端本地工具（组名）"` |
| `conf/config.yaml` | `subagent_only_client_tools` 保持 `[fs, shell]`（`shell_peek` 不列 → 主循环可见），注释说明 |

---

## 五、LLM 提示（两处）

1. **peek 被拒时**的错误信息（`peekUnavailableReason`）：
   `「<命令>」不在主对话开放（可能扫盘/读取大量内容）。如需执行请派 file_explore 子任务；主对话只支持探查类只读命令（ls/stat/file/wc/du -sh/git status 等）。`
   —— 模型一试就收到，最有效。
2. **`shell_peek` 的组级描述**（`server_description`，静态、字节稳定）：主循环在**启用前**就能从工具目录看到边界提示：
   `本地只读探查（Mac 客户端）。仅支持 ls/stat/file/wc/du -sh/git status 等低耗命令；扫描、读取内容、完整 git 历史等命令仅子任务可用，需要时派 file_explore。已授权目录见函数描述。`

> 组级描述经服务端 `ToolDefinition.Description` 透传，出现在 `clientToolCatalog`（主循环工具目录）；函数描述里仍保留动态的「已授权目录清单」。组描述必须静态，否则破坏 DeepSeek 前缀缓存。

---

## 六、验证用例（已由单测覆盖核心逻辑）

| # | 用例 | 结果 |
|---|------|------|
| 1 | peek 放行 `ls -la`/`pwd`/`stat`/`du -sh`/`which`/`git status`/`git rev-parse`/`git branch --show-current`/`git log -n N` | 单测 ✅ |
| 2 | peek 拒绝 `rg`/`grep -r`/`find`/`tree`/`sort`/`head`/`tail`/`cat`/`diff`/`md5`/`ls -R`/`ls -laR`/`ls --recursive` | 单测 ✅ |
| 3 | peek 拒绝写命令（`rm`/`mv`/`mkdir`/`git commit`…） | 单测 ✅ |
| 4 | `git log` 无 `-n` 拒、`-n 50` 拒、`-n 20` 放 | 单测 ✅ |
| 4b | `git log -n -1` / `--max-count=-1` / `-n0` 拒（负值被 git 当无限制） | 单测 ✅ |
| 4c | `git log -n 20 --max-count=99999` 拒（多 count 取最后一个）；`--max-count=99999 -n 5` 放 | 单测 ✅ |
| 5 | 裸 `du`/`du -h` 拒，`du -sh` 放 | 单测 ✅ |
| 6 | peek 仍拦 `ls ~/.ssh`（硬拦）、`ls /etc`（未授权）、管道含 `head` 整条拒 | 单测 ✅ |
| 7 | 只读免确认、写 L4：需真机在子任务里跑 `ls -la`（无确认）与 `rm x`（红条） | 待真机 |

---

## 七、已定决策

1. 只读命令 `.allow` 恒 L2 免确认（不分工作区内外）。
2. `isScopedToAgentWorkspace` / `isWorkspaceScoped` **保留**（未来写命令用）。
3. `shell_peek` 白名单如上（含 `du -sh`、`git log -n N`；不含 `ls -R`）。
4. 组名 `shell_peek`，显示名"查看命令"。
5. LLM 提示放两处（peek 拒绝信息 + peek 组级描述），组级描述经服务端小改透传。
