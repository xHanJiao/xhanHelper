# 计划：fs grep 分页 + 结果安检 + 敏感文件豁免（+ SPM 单测）

> 日期：2026-08-15
> 状态：📝 方案已确认，待开发
> 架构：纯 macOS 客户端改动 + 服务端一行 prompt；新增 SPM 包做纯逻辑单测（agent 可 swift test 自验）

## 一、背景（边界测试暴露的三个问题）

对 fs 工具做四组边界测试后发现：

1. **grep 匹配收集预算异常**：高频 pattern（e/func/return）只返回 58~69 条且无 truncated 提示。
   根因：`runCapturedProcess` 按注入预算（≈19KB）杀进程，`runRgGrep` 丢弃 capped 标志（Grep.swift:106 `let (data, _) = ...`）。
   rg JSON 单条 ~300B → 19KB ≈ 63 条 < maxResults 100 → `formatGrepOutput` 判定「未到 100」→ 无任何截断提示，静默漏结果。
   glob 同款隐患（长路径时 <100 条被杀同样无提示）。
2. **路径穿越 + 隐藏文件无限制**：`../` 可读出授权目录外（前缀授权制，父目录已授权时放行——设计使然，维持现状）；
   `.git/config` 完整可读（blocklist 无 .git 规则）；`.env` 路径可 glob 到。
   更严重：grep/glob 的 SensitiveFileBlocker 只查搜索根（resolvedPath），**不查匹配出的每个文件**——grep 目录时 `.env` 内容会漏。
3. **glob 与 read 行为不对称**：glob 尊重 .gitignore（rg 默认）+ 排除 .git；read 无此限 → 「glob 找不到、read 读得到」的幽灵文件。

## 二、方案

### 2.1 grep 分页（skip）+ 诚实截断

- grep 加 `skip` 参数（从第 N 条匹配开始返回，类似 read 的 offset；schema 描述同步）。
- `runCapturedProcess` 改行流回调：`runCapturedProcessLines(proc, budget, onLine: (String) -> Bool)`，
  onLine 返回 false 即 terminate（收满即停，不再靠预算硬掐）；预算杀仍保留为安全网（capped 标志）。
- 收集语义：跳过前 skip 条 → 收集最多 100 条 → 第 101 条触发 `moreAvailable=true` 后停。
- 提示语（formatGrepOutput 组合）：
  - 收满 100：`Found 100 matches (more matches available. Use skip=100 to continue.)`
  - 预算掐断：cappedOutput 尾注改为 `(Output capped at X KB. 已显示 N 条。可用 skip=N 续看。)`
  - 两者都不满足：正常输出。
- 兜底 system grep：加 `--exclude-dir=.git`，同款 skip/limit/提示（逐行丢弃 skip 条）。
- 已知限制：skip 靠重新扫描 + 丢弃前 N 条，两次调用间文件变动会偏移——接受。

### 2.2 结果安检（per-match 敏感过滤）

- grep：每条匹配的 filePath 过 `SensitiveFileBlocker.check`，拦下的丢弃并计数，
  输出尾注 `(N 条匹配来自敏感文件已省略。如需访问请在客户端设置中添加豁免。)`
- glob：每个条目过锁，隐藏 + 计数尾注。
- read 目录列表：条目过锁，隐藏。
- 兜底 system grep 的 `--exclude-dir=.git` 与 rg 对齐。

### 2.3 敏感文件锁增强 + 豁免列表

- `SensitiveFileBlocker` 新增 `/.git/` 规则（路径组件含 .git 即拦）——read/grep/glob 三扇门统一，幽灵文件消失。
- 新增豁免机制（**独立于授权目录，只解敏感锁，不动授权锁**）：
  - UserDefaults key `fs_sensitive_exempt_paths`，前缀匹配（大小写不敏感，与授权管理一致）。
  - `check()` 开头豁免短路。
  - 设置页（MacSettingsView）新增 Section「敏感文件豁免」：与「文件访问授权」同款 UI（ForEach + 删除 + AddDirView 添加）。

### 2.4 SPM 单测（C 方案）

- 纯逻辑抽到零依赖新文件（只依赖 Foundation）：
  - `GrepParser.swift`：rg JSON 行解析 / system grep 行解析 / 输出格式化 / skip 与截断判定（进程无关，可测）。
  - `SensitiveFileBlocker.swift`：已是纯静态函数（零改动可测），随豁免/./.git 规则一起测。
- 包结构：`client/xHelper/Package.swift`（package root = client/xHelper），target 用显式 sources 列表指向
  `xHelper/Shared/Tools/GrepParser.swift` + `SensitiveFileBlocker.swift`（单份代码，app 与测试共用；
  Xcode 工程为 fileSystemSynchronizedGroups，新文件自动入 app target）。
- 测试：`client/xHelper/Tests/FsCoreTests/`。agent 用 `swift test` 自验；app 集成行为用户 Xcode 验证（约定不变）。

## 三、改动清单

| 文件 | 改动 |
|------|------|
| GrepParser.swift（新） | 纯函数：parseRgLine/parsePlainLine/formatGrepOutput/GrepMatch |
| SensitiveFileBlocker.swift | 豁免列表 + isExempted + `/.git/` 规则 |
| FileSystemToolProvider.swift | runCapturedProcessLines（行流）；grep schema 加 skip；描述文案 |
| FileSystemToolProvider+Grep.swift | 行流收集、skip、per-match 过滤、诚实提示；删除本地 parse/format |
| FileSystemToolProvider+Glob.swift | 行流、per-entry 过滤、截断诚实化 |
| FileSystemToolProvider+Read.swift | 目录列表过滤敏感条目 |
| App.swift（xHelperMac） | 设置页豁免 Section |
| Package.swift + Tests/FsCoreTests | SPM 包 + 单测 |
| server/internal/agent/subagent.go | fileExplorePrompt 补「grep 截断用 skip 续翻」一行 |

## 四、测试计划

### SPM 单测（swift test，agent 自验）
1. parseRgLine：合法 match 行 / begin/end 行忽略 / 超长行截断
2. skip：跳过前 N 条后收集；skip ≥ 总量返回空
3. moreAvailable：第 101 条触发；恰 100 条不触发
4. 敏感过滤：filter 闭包拦下的丢弃 + omittedSensitive 计数
5. formatGrepOutput：三种提示组合（more/skip/省略尾注）
6. SensitiveFileBlocker：.env / .git / credentials / .key / 豁免短路 / 豁免前缀匹配 / 大小写

### 真机验收（用户 Xcode）
1. 高频 pattern grep → 显示 ~100 条 + more available 提示 → skip=100 翻页正常
2. grep 目录含 .env → 省略尾注出现，内容不外泄
3. read .git/config → 拦截；glob 列表无 .env/.git 条目
4. 设置页添加豁免 ~/x/project/.git → read 放行
5. 豁免不越权：.git 豁免后，写 .git/config 仍走 L3 确认

### 服务端
- `go build ./... && go test ./internal/agent/`（fileExplorePrompt 改动）

## 五、风险
- skip 分页基于重新扫描：文件变动漂移（接受，标注）。
- 行流改造影响 glob/grep 两处调用方：read/write/edit 不动。
- SPM package root 与 xcodeproj 同目录：Xcode 打开工程不受影响（包结构仅 swift test 使用）。
