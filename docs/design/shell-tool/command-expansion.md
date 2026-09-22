# shell__run 命令扩容 + 风险降档（严格参数白名单）

> 日期：2026-09-22
> 状态：实施中
> 前置：`design.md`（shell 工具原始设计，含 2026-09-22 降档修订）
> 分支：`main`（承接 `feature/shell-tool`）
> 服务端：零改动

## 一、动机

原 shell 工具是「只读白名单 + 每条命令人工确认」：

- 只放 29 个只读命令，写/删/执行一律拒绝；
- 执行层默认 L4（红色强制确认、信任模式不可跳），仅工作区内降 L3。

两个问题在实际使用中暴露：

1. **能力太窄**。`sort` / `uniq` / `cut` / `jq`、`find`、`rm` / `mv` / `cp`、`git commit` 这些日常操作都做不了，Agent 只能绕道或放弃。
2. **人工确认不是可靠边界**。使用者不熟悉 shell，命令又常常很长（多段管道），在确认条上无法真正判断一条命令是否安全——弹确认条只提供了「形式上的把关」。

因此本次同时做两件事：

- **降档**：工作区内 L2（免确认）、区外 L3（常规确认、信任模式可跳），把确认为数众多的只读探查降噪；
- **扩容 + 收紧**：放开一批新命令，但因为「人审不了」，安全的真正兜底必须是**解析器**，且必须 **fail-closed（认不出就拒）**。

## 二、安全模型（本方案的核心）

```
① 命令白名单（argv[0]）          放哪些程序
② 参数白名单 / 形式守卫           主边界：危险 flag / 形式 → 直接拒（不弹确认）
③ 内容守卫（sed/awk 脚本、find -exec）  挡藏在参数里的执行
④ 路径授权 + 敏感文件            挡越界读写（写语义：目标不存在查父目录）
⑤ 人工确认                       知情门，不是审阅门 → 显示「人话解释」
```

关键立场：

- **② ③ 必须严到「不认识就拒」**。宁可让模型收到「flag 不支持，请换用法」，也不放过去一条看不懂的命令。
- **⑤ 不承担安全职责**。确认条改成显示解析结果的中文解释（+ 原始命令），使用者只需判断「意图对不对」，不需要读 shell。

## 三、风险等级接线

原来 `ShellRisk` 的 `.l4` 是**死钩子**：`l4Commands` 是空集，且 `LocalToolRegistry.riskLevel` 只按位置定级、完全不看 parser 结果。本次把它接通：

```swift
nonisolated enum ShellRisk { case allow, l4, deny(String) }   // 不新增等级

classify():   命中 l4 集（过守卫/路径）→ .l4；allow 集 → .allow；否则 .deny
evaluate():   多段命令取最高档（allow < l4）；任一段 deny → 整条 deny

ShellToolProvider.shellLevel(arguments:) -> Int
  .l4    → 4                      // 写/执行类：恒 L4（工作区内也 L4）
  .allow → isScopedToAgentWorkspace ? 2 : 3
  .deny  → 4（兜底；正常已被 shellBlockedReason 前置拒绝）
LocalToolRegistry.riskLevel(shell__) 改调 shellLevel
```

执行侧无需改动：`ChatViewModel+ClientTools` 的 `shellHardened = isShell && level >= 4` 天然让 L4 走红色确认条，L3 走常规确认，L2 直接放行。

三档语义（`ChatViewModel.awaitToolConfirmation` 已实现）：

| 等级 | 确认 | 安全档 | 信任档 | 完全信任 |
|------|------|--------|--------|----------|
| L2 | 无 | 执行 | 执行 | 执行 |
| L3 | 常规 | 弹 | 跳过 | 跳过 |
| L4 | 红色 hardened | 弹 | 弹 | 跳过 |

## 四、命令分档（v2）

### allow（只读 → 工作区 L2 / 区外 L3）

- 原有 29 个不变；
- **新增 7 个**：`sort uniq cut tr nl column jq`；
- 写口拦截：`sort -o` / `--output[=]` → 拒（这批里唯一能写文件的）。

### l4（写/执行 → 恒 L4）

- **搜索/文本处理**：`find sed awk`
- **文件写删**：`rm mv cp mkdir rmdir touch ln chmod chown tee truncate`
- **git 写子命令**：`add commit checkout switch restore reset clean merge rebase
  cherry-pick revert`，以及
  `stash push/pop/apply/drop`、`tag -a/-d/-m`、`branch <name>/-d/-m`、`remote add/remove/set-url`

### 明确不收

- `perl`（完整语言，`-e` 任意）、`xargs`（本质就是执行 stdin 里的命令）——无法解析；
- `ps lsof netstat`（读全系统信息，与「工作区」语义不符）；
- `xxd base64 od hexdump strings tac comm join paste fold expand fmt seq printf expr`（用不上）；
- `git push/fetch/pull/clone/config/init`（网络组未纳入；config 是 hook/alias 注入面）。

## 五、参数守卫

### 5.1 find

硬拒以下 flag（它们本身就是命令执行 / 任意写口）：

```
-exec  -execdir  -ok  -okdir  -delete  -fls  -fprint  -fprint0  -fprintf
```

其余 flag 放行；位置参数按只读路径校验。

### 5.2 sed

- 拒 flag：`-i` / `--in-place`（就地写）、`-f` / `--file`（脚本从文件读）；
- 脚本内容扫描（`-e` 参数或首个位置参数）：出现「命令位的 `e` / `w` / `r` / `W` / `R`」或「替换带 `e` 标志」→ 拒。

### 5.3 awk

- 拒 flag：`-f` / `--file`；
- 脚本内容扫描：出现 `system` / `getline` / `ENVIRON`，或字符串字面量外的 `|` / `>` → 拒。

### 5.4 路径校验（读 / 写两种语义）

`pathBlockedReason(argv:workdir:forWrite:)`：

1. 每个「像路径」的参数先过 `SensitiveFileBlocker.check`（`.env`、`id_rsa`、`.ssh/`… 及不可豁免的硬拦截层）；
2. **读**：路径存在时必须 `isAuthorized`（已授权目录 + Agent 工作区）；
3. **写**：目标存在 → `isAuthorized`；目标不存在 → 校验**父目录** `isAuthorized`（覆盖 `mkdir` / `touch` / `cp` 目标 / `mv` 目标）；
4. 写语义额外拒绝 `/`、`~`（home）、以及硬拦截路径的祖先目录。

### 5.5 明知的缝

> `// ponytail: sed/awk 是子串扫描、不是解析器，已知漏报上限；要更严得上真解析器。`

sed / awk 的脚本守卫是**子串级 best-effort**，无法覆盖所有语法变形。缓解：这两条命令恒 L4，确认条会显示人话解释（见六）。若不接受该残余风险，把 `sed`/`awk` 从 l4 集移除即可（一行）。

## 六、确认条：人话解释 + 原始命令

因为使用者不读 shell，确认条改为两层展示：

1. **人话解释（主）**：`ShellCommandDescriber.describe(stages)` 由 parser 的结构化 stages 生成中文，逐段描述「要做什么」。
2. **完整原始命令（次）**：等宽字体、不截断，供需要时核对。

示例：

| 原始命令 | 人话解释 |
|----------|----------|
| `rm -rf build dist` | 递归强制删除：build、dist（**不可恢复**） |
| `mv a.txt docs/` | 移动：a.txt → docs/ |
| `mkdir -p x/y` | 创建目录（含父目录）：x/y |
| `git commit -m "fix"` | 创建 git 提交：fix |
| `find . -name '*.md'` | 查找文件：-name '*.md'（.） |
| `sed -n '1,10p' f` | 文本处理（sed）：打印 f 的 1-10 行 |
| `ls \| head` | 执行：ls \| head（只读） |

实现：`ToolConfirmation` 增 `explain: String?`；`ChatView.toolConfirmationBar` 在主标题下渲染解释、再渲染原始命令块（L4 保留红色）。

## 七、代码结构

| 文件 | 改动 |
|------|------|
| `Shared/Tools/ShellCommandParser.swift` | `allowCommands` +7；`l4Commands` 填实；`deniedForms` / sed-awk 扫描；`sort -o` 拦截；`pathBlockedReason(forWrite:)`；`classifyGit` 增写子命令 → `.l4` |
| `Shared/Tools/ShellCommandDescriber.swift` | **新增**：`describe(stages:) -> String`（纯逻辑，可单测） |
| `Shared/Tools/ShellToolProvider.swift` | `shellLevel(arguments:)`；重写 `toolSchemas` 描述（不再是「只读」） |
| `Shared/Tools/LocalToolRegistry.swift` | `riskLevel(shell__)` 改调 `shellLevel` |
| `Shared/ViewModels/ChatViewModel.swift` | `ToolConfirmation.explain`；`awaitToolConfirmation` 传解释 |
| `Shared/Views/ChatView.swift` | 确认条显示解释 + 原始命令 |
| `Tests/FsCoreTests/ShellCommandParserTests.swift` | 新增分档 / 守卫 / 写路径 / git 写 / 解释 用例 |

数据结构不引入新抽象：命令集是 `Set<String>`，`find` 的拒绝形式是一张 `[String: Set<String>]`，其余是小 `switch`——到需要时再抽 `ShellCommandSpec`。

## 八、验证用例

| # | 命令 | 期望 |
|---|------|------|
| 1 | `ls`（工作区） | L2，无确认直接执行 |
| 2 | `ls`（区外授权目录） | L3，常规确认 |
| 3 | `sort -o out a` | 拒（写口） |
| 4 | `rm -rf <工作区目录>` | L4 红色 + 人话解释 |
| 5 | `rm /etc/hosts` | 拒（授权外） |
| 6 | `find . -exec rm {} \;` | 拒（exec 形式） |
| 7 | `sed -i 's/a/b/' f` | 拒（-i） |
| 8 | `awk 'BEGIN{system("id")}'` | 拒（system） |
| 9 | `git commit -m x` | L4 + 解释 |
| 10 | `git status` | L2/L3（只读不变） |
| 11 | `perl -e x` / `xargs rm` | 拒（未收） |
| 12 | `mkdir -p <区外不存在的路径>` | 拒（父目录未授权） |

## 九、非目标（YAGNI）

- 不做用户可编辑白名单；
- 不做本地审计日志（确认条解释已覆盖「事后看不懂」的主要场景，需要再加）；
- 不给只读命令做逐 flag 允许表（它们没有写口/执行口，表是纯噪音）；
- 不给 `rm/mv/cp` 等做逐 flag 允许表（危险即命令本身，已 L4 + 路径校验 + 解释）。
