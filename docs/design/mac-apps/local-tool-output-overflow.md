# 本地工具结果溢出落盘 + 子 agent 提示词批

> 日期：2026-09-15
> 状态：✅ 已实现（服务端 `go test` 绿；客户端纯逻辑 `swift test` 绿；FS 集成由 Xcode 真机验证）
> 背景：2026-09-15 subAgent 使用复盘（10 条问题）
> 架构：服务端提示词改动 + 客户端本地工具溢出落盘（模型无写权限）
> 对标：opencode `truncate.ts` / `read.ts` / explore agent

## 一、背景与问题

### 1.1 现象

用户在 Mac 客户端上用 `file_explore` 子 agent 调研本地代码库（N.E.K.O），复盘出 10 条问题，其中真正造成损耗的两条：

1. **读一次废一次**：子 agent 读大文件（`_response_arbiter.py` 115KB、`detector_runtime.py` 1300+ 行等），客户端结果超过注入预算（`clientToolInjectBudgetBytes = 20000` 字节）后被**有损截断**，后面的内容永久拿不到。报告里只能写「输出截断，未逐字确认」「读取超时，未逐字确认」。
2. **跨子任务不共享**：同一函数被多个任务各读一遍、各留一条 open_question，没有共享载体。

其余 8 条（范围线索变搜索边界、前提被照抄、没排优先级、未读项手工传递、「找不到」被当失败、3 个任务不如 2 个、任务边界交叠、大文件默认整读）属提示词/用法问题。

### 1.2 当前实现的问题点

| 位置 | 现状 | 问题 |
|---|---|---|
| `FileSystemToolProvider.swift` `budgetCap` / `cappedOutput`、`+Read.swift` `renderWindow` | 超预算按行/字节**有损截断**，附 `Use offset=N to continue` | 丢的内容无补救；弱模型（Flash）不追 footer |
| `FileSystemToolProvider.swift:9` `maxLineChars = 2000` | 超长行截成 2000 字符 | minified JSON / 单行 CSV 后面永久拿不到 |
| `MCPManager.paginateIfNeeded` + `result_read_more` | 溢出分页**只服务动态 MCP 工具**，缓存全文返回首页 + `page_token` | 内置 `fs` 没走这条，走了有损路径 |
| `client_tools.go:304` `normalizeClientToolData` | 服务端兜底截断 + 日志「client should paginate via result_read_more」 | 兜底触发说明客户端没按协议分页 |

### 1.3 不采用的方案（记录决策）

**不采用「给子 agent 开写权限 + 限定 scratch 目录」**：
- 写文件的**路径决定权会交到模型手里**，模型被待读文件内容饲喂，存在 prompt 注入诱导越界写的风险；必须再加一套 owner/组件级/symlink 守卫。
- 参考 opencode：explore 子 agent **严格只读**（`agent.ts:196-212` `"*": "deny"`，prompt 明写 "Do not create any files"），溢出由**工具运行时**落盘，不是 LLM 写。

## 二、目标 / 非目标

**目标**：
- 本地工具（`fs` 读/搜）结果超预算时，由**客户端运行时**把全文落盘，返回预览 + `output_path`，模型可 `fs__grep`/`fs__read` 该副本继续深挖——内容不再丢失。
- 超长行文件由**字节分页**天然覆盖，消除 `maxLineChars` 一刀切死的问题。
- 溢出文件可控增长：TTL + 容量 LRU + 手动清理 + 启动兜底。
- `file_explore` 保持**只读**（不开 write/edit，零逃逸面）。
- 修 8 条提示词/用法问题（A 批）。

**非目标**：
- 不做「16KB 硬 block / 强制 offset」（opencode 不做，改用溢出落盘 + 提示；如后续需要可加软提示）。
- 不新增 `fs__stat`（行数按需在浏览/搜索时获得，YAGNI）。
- 不重构 `MCPManager` 的现有分页（保持原样，避免回归；统一到磁盘是将来独立议题）。
- 不改子任务轮次预算、不做上下文压缩、不改浏览器链路。

**实现期增补**：为兑现「长行文件可读」的承诺，`fs__read` 增加 `char_offset`/`char_limit` 字符窗口模式（原计划列为非目标，实测单靠行分页无法取到单行 2000 字符之后的内容，故补上；见 5.2.5）。

## 三、调研结论（opencode 对照）

| 项 | opencode | 本方案 |
|---|---|---|
| explore 子 agent 权限 | deny-all + allow `grep/glob/list/bash/webfetch/websearch/read`，只读 | `file_explore` 白名单维持 `{glob,grep,read}`，只读 |
| 大输出处理 | `truncate.ts`：超 2000 行 / 50KB → 全文写 `Global.Path.data/tool-output/tool_<id>`，返回预览 + 路径 + `Use Grep/Read with offset` 提示 | 客户端 `ToolOutputStore` 同样落盘 + `output_path` + 提示 |
| 谁写文件 | 工具运行时（可信） | 客户端运行时（可信） |
| 长行 | `read.txt`「Use grep for files with long lines」；超 2000 字符截断 | 字节分页能切长行；提示用 grep 定位 |
| 读分页 | 2000 行默认 / 50KB 字节顶 / 2000 字符行长顶 | `fs__read` 已有 `offset`/`limit`，保留 |
| 清理 | retention 7 天，每小时 cleanup，启动后 1 分钟首扫 | 7 天 TTL + 每小时 + 启动；再加 200MB LRU 兜底 |
| 跨会话共享 | 磁盘文件，后续 agent grep/read | 同（客户端磁盘，后续子任务读副本） |
| 子任务结果 | `task.ts` `renderOutput` `<task id state>` | 已有同款 `task.go` |

## 四、架构与数据流

```
file_explore 子 agent 调 fs__read 大文件
  → 客户端 FileSystemToolProvider 生成完整文本
  → 超 result_budget_bytes（20000）
      → ToolOutputStore.save(fullText)
          目录：~/Library/Application Support/xhanHelper/tool_output/
          hash 去重：同内容复用已有文件
          写入成功 → 返回 output_path
      → 回给模型：
          {
            "truncated": true,
            "output_path": ".../tool_output/tool_<id>",
            "total_bytes": 118000,
            "preview": "<前 ~16KB 预览>",
            "note": "全文已存盘，用 fs__grep 定位或 fs__read output_path offset=N 续读"
          }
  → 子 agent 用 fs__grep(pattern, path=output_path) 或 fs__read(output_path, offset=..) 深挖
  → 结论 + output_path 写进 <file_explore_result> 报告
  → 主 agent 按需再派 file_explore 读同一 output_path（跨任务共享）

清理：
  启动 + 每小时 housekeeping → 删 mtime > 7 天的文件
  总容量 > 200MB → 按 mtime 最旧 LRU 删
  设置页「清除本地缓存」→ 删整个 tool_output/
```

## 五、详细改动

### 5.1 A 批：提示词 / 描述（服务端）

#### 5.1.1 `server/internal/agent/subagent.go` — `fileExplorePrompt`

在现有 prompt 基础上补充（不删既有条目）：

```text
工作准则补充：
- 目录线索是起点不是边界：在某目录找不到目标时，扩大搜索范围（上层目录、同名文件的其他位置、不同命名约定），
  并在 <open_questions> 里说明「搜过哪些地方、为什么没找到」——找不到同样是合格产出，禁止编造。
- 大文件先 grep 定位再定点读；单次读一段，不要整读；不要小块切割（30 行一读）。
- 任务描述里的前提若无法验证，明确标出矛盾或存疑，不要默认成立。
- 工具结果里出现 output_path 时：用 fs__grep 定位或 fs__read 该副本继续深挖，不要重复整读原文件。

最终输出补充（<open_questions> 前）：
<artifacts>本次落盘的中间结果文件路径（output_path），没有则填"无"</artifacts>
```

> 说明：`<artifacts>` 让产物路径进入报告，供后续子任务/主 agent 复用。

#### 5.1.2 `server/internal/tool/task.go` — Description + prompt 参数说明

- Description 增补：
  ```
  - 派 task 前先列清每个任务负责哪些文件/函数，确认互不交叠；宁可 2 个切干净，不要 3 个有交叠
  - 任务里写一行优先级（预算不足时先保证哪几条有结论）
  - 任务描述里的前提标注【已确认】/【推断】，并要求子任务顺手证伪
  - 上批 task 的 open_questions 未读项，贴进新任务 prompt，或直接带 task_id 续跑同一子任务
  ```
- `prompt` 参数说明同步上面 3 条。

### 5.2 C'：溢出落盘（客户端）

#### 5.2.1 新增 `client/xHelper/xHelper/Shared/Tools/ToolOutputStore.swift`（macOS）

职责：把超预算的本地工具结果全文落盘，返回引用。

```swift
#if os(macOS)
actor ToolOutputStore {
    static let shared = ToolOutputStore()

    private let baseURL: URL   // ~/Library/Application Support/xhanHelper/tool_output
    private let retention: TimeInterval = 7 * 24 * 3600   // 7 天
    private let maxTotalBytes = 200 * 1024 * 1024          // 200MB

    /// 落盘全文，返回文件绝对路径；同内容（sha256）复用已有文件。
    func save(_ text: String) throws -> URL

    /// 清理：删 mtime > retention 的文件；仍超 maxTotalBytes 时按 mtime 最旧 LRU 删。
    func cleanup() throws

    /// 清空整个目录（设置页手动清理用）。
    func clearAll() throws

    /// 启动 + 每小时的 housekeeping 定时器（复用 BrowserToolProvider 的 Timer 模式）。
    func startHousekeeping()
}
#endif
```

要点：
- 文件名 `tool_<sha256>`（**内容寻址**，与 opencode 前缀一致便于清理筛选）；同内容天然去重，无需扫描比对。
- `convenience init()` 走 Application Support；另有 `init(baseURL:retention:maxTotalBytes:)` 供单测注入临时目录。
- 写失败（磁盘满等）→ 返回 nil，由调用方降级为原有损截断，打 `os.Logger` 错误，不阻断工具返回。
- 已加入 `client/xHelper/Package.swift` 的 `FsCore` sources，纯逻辑由 `swift test` 自验。

#### 5.2.2 `FileSystemToolProvider+Read.swift` / `+Grep.swift` 接入

- `renderWindow` / `cappedOutput` 超预算分支：组装完整文本 → `ToolOutputStore.save` → 返回预览 + `output_path` 的 JSON 结果（而非当前的有损 footer）。
- 长行：`fs__read` 的字符窗口模式（5.2.5）直接按字符切片，绕开 `maxLineChars` 截断。
- 预览大小：`budget - 512` 字节（预留信封 + JSON 转义余量），按 UTF-8 字节安全截断。
- 结果 JSON 结构与 5.2.1 的 `{truncated, output_path, total_bytes, preview, note}` 对齐。

> 结果信封与现有 `MCPManager` 首页 `{truncated, page_token, chunk, content, note}` 略有差异——MCP 走自有分页不动，`fs` 走 `output_path` 路径（opencode 风格，直接指向文件，配合 `fs__grep`/`fs__read`）。

#### 5.2.3 授权：`DirectoryAuthorizationManager`

- 对 `tool_output/` 目录**隐式只读授权**（`isAuthorized` 命中该根即放行），保证子 agent 能读副本。
- 该目录由 App 拥有、非用户数据，读取不进 L3、不受敏感文件拦截影响（文件名 `tool_<id>` 不命中任何敏感 pattern）。
- 仅放行读；`tool_output/` 下的写一律由运行时完成，不经 `fs__write`。

> 顺带记录（不在本期改）：`DirectoryAuthorizationManager.isAuthorized:27` 用裸 `hasPrefix` 匹配，存在 `/proj` 放行 `/project` 的前缀混淆。本方案新逻辑按路径组件校验；既有 bug 另行评估。

#### 5.2.4 清理接入 `client/xHelper/xHelperMac/App.swift`

- 「清除本地缓存」菜单（现 `App.swift:668` 调 `LocalToolResultStore.clearAll()`）→ 追加 `ToolOutputStore.clearAll()`，确认文案补充「子任务中间结果」。
- App `.task` 启动钩子调用 `ToolOutputStore.shared.startHousekeeping()`（启动 + 每小时清扫）。

#### 5.2.5 `fs__read` 字符窗口 `char_offset`/`char_limit`

- 长行/无换行文件（压缩 JSON、单行 CSV）按行分页无效（`maxLineChars=2000` 一截走不到后面）。
- `readFile` 在二进制/图片判定后分流：传入 `char_offset` 时走 `readCharacterWindow`，读取全量文本（≤ 4MB，`maxOverflowSourceBytes`），按字符切片返回窗口 + 续读提示；超 4MB 拒绝并提示用 grep。
- 窗口超预算同样溢掉落盘（复用 `overflowResult`）。
- 与行分页（offset/limit）互斥，不进 PDF 路径。

### 5.3 明确不做

- 不给 `file_explore` 加 `fs__write/edit`；不加 owner 路径守卫（无写面即无需）。
- 不加 `fs__stat`、不加 16KB 硬 block。
- 不重构 `MCPManager` 分页到磁盘。

## 六、清理策略

| 层 | 机制 | 参数 |
|---|---|---|
| 1 TTL 定时扫 | 启动 + 每 1 小时 housekeeping，删 mtime 超期文件 | 7 天 |
| 2 容量兜底 | 超总容量按 mtime 最旧 LRU 删 | 200MB |
| 3 手动 | 设置页「清除本地缓存」删整个目录 | — |
| 4 极端保护 | 单次落盘前 hash 去重；同内容复用不新增 | — |

## 七、安全边界

- **写方是客户端运行时**（固定 App 目录），模型无法指定路径 → 无 `..`/symlink/前缀混淆/prompt 注入逃逸面。
- `file_explore` 白名单维持只读，服务端不新增任何文件写能力（服务端本身不落盘）。
- 溢出副本仅含用户已授权的文件内容；用户原始文件不受影响（读路径的 L1/L3、敏感拦截不变）。
- 授权仅新增 `tool_output/` 只读隐式放行，不放宽其他任何目录。

## 八、测试

### 8.1 客户端纯逻辑（`client/xHelper` SwiftPM `FsCore`，`swift test` 已绿）

| 用例 | 覆盖 |
|---|---|
| `ToolOutputStoreTests.testSaveReusesByContentHash` | 同内容二次 save 返回同一路径，不新增文件 |
| `testSaveDifferentContentDifferentFiles` | 不同内容各落一份 |
| `testCleanupRemovesExpired` | 造 mtime 2 小时前 + retention 1 小时 → cleanup 后消失，新文件保留 |
| `testCleanupLRUWhenOverCap` | 超 maxTotalBytes → 最旧先删 |
| `testClearAll` | 清空目录 |
| `testContentFileNameStableAndDistinct` | 内容寻址文件名稳定且可区分 |
| `SubagentResultParserTests.testParseFileExploreArtifacts` | `<artifacts>` 小节渲染为「中间结果」 |

> `isReadAuthorized`/`overflowResult`/`char_offset` 属 app 集成路径（`DirectoryAuthorizationManager`/`FileSystemToolProvider` 不在 FsCore 包内），由 Xcode 真机验证。

### 8.2 服务端

- `go build ./...` + `go test ./internal/agent/... ./internal/tool/...` 全绿。
- `TestSubAgentSpecs_DispatchTable` 既有断言保证 `file_explore` 客户端白名单仍为 `{glob,grep,read}`（不含 write/edit）。

### 8.3 真机

1. 派 `file_explore` 读 >20KB 文件 → 子任务活动面板出现 `fs__read` → 收到 `output_path` → `fs__grep` 副本命中 → 报告含结论与 `<artifacts>`。
2. 复读同一文件 → 命中 hash 去重，目录不新增。
3. 设置页「清除本地缓存」→ `tool_output/` 清空。
4. 把某文件 mtime 改到 8 天前 → 重启 App → 被清。
5. 读一个 minified 单行大文件 → 用 `fs__read(char_offset=N)` 能取到 2000 字符之后的内容；或经 `output_path` + grep。
6. 确认用户的真实文件未被任何写操作触碰。

## 九、验收分级

按 CLAUDE.md：本改动影响主链路（子 agent 工具调用 + 结果注入），但服务端仅提示词文案，客户端为本地工具层。分级：

- 服务端：`go build ./...` + `go test ./...` 全绿 + 新增白名单断言。
- 客户端：代码 + 单测由用户 Xcode 编译验证（不执行 xcodebuild）。
- 真机第 8.3 节全部用例。

## 十、风险与回滚

| 风险 | 缓解 |
|---|---|
| 磁盘增长 | 四层清理（TTL/LRU/手动/去重），单次结果无 300 页级膨胀 |
| 弱模型仍不追 `output_path` | prompt 明确写「出现 output_path 时读副本」；结果中把 `output_path` 放在显眼字段 |
| 落盘失败（权限/磁盘满） | 降级原有损截断 + 日志，不阻断 |
| 递归溢出 | hash 去重 + 提示带 offset 分页读副本 |
| 客户端改动回归 | fs 读/搜单测 + 真机回归；服务端零行为变更 |

**回滚**：服务端 A 批为文案改动，客户端 C' 为独立新文件 + fs 两处接入，`git revert` 单提交即可。

## 十一、开发顺序

1. A 批（服务端提示词，最小改动）✅
2. C'（客户端 `ToolOutputStore` + fs 接入 + 授权 + 清理）✅
3. D（服务端单测绿 / 客户端 `swift test` 绿 / FS 真机待用户在 Xcode 验证）
