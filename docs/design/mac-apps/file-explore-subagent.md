# 计划：file_explore 本地文件检索子 agent（+ web_explore 改名与收口）

> 日期：2026-08-15
> 状态：📝 方案已确认，待开发
> 架构：纯服务端改动（子 agent 分派 + 白名单 + 截断修复），客户端零改动
> 对标：opencode 的 explore subagent（../opencode-dev/packages/opencode/src/agent/agent.ts:196-218）

## 一、背景与问题

本地文件检索读取能力（fs__glob/grep/read/write/edit）已在 macOS 客户端就绪（4 批完成，见 fs-write-tools.md）。但**编排层**与 opencode 不对标：

1. **没有本地文件子 agent**。opencode 的 explore subagent 是文件搜索专家（glob/grep/read/bash 白名单），开放探索丢给子 agent，主上下文只收最终报告。我们唯一的子 agent（explore）是联网调研（webfetch/browser），本地文件探索只能主循环自己一轮轮调 fs 工具——词元全烧主上下文。
2. **子 agent 类型没有真正分派**。`RunSubAgent` 写死 `explorePrompt` + `exploreWhitelist`（subagent.go:226,242），`subagent_type` 参数形同虚设。
3. **白名单粒度太粗**。`filterClientToolsByWhitelist` 按 server_name 过滤——把 "fs" 加进白名单会连 `fs__write`/`fs__edit` 一起给子 agent（写权限不该给只读调研型子 agent）。
4. **MiniReAct 工具结果裸切 2000 字符**（mini_react.go:354-356），四个病：
   - 按字节切 JSON，结构断裂，LLM 收到非法 JSON；
   - 切在多字节 UTF-8 字符中间，中文乱码；
   - 只留头丢尾（主循环 `truncateToolResult` 是头 70% + 尾 20% + 标记，MiniReAct 是裸切头）；
   - fs_read 一页预算 20KB，子循环实际只注入 2000 字符 ≈ 几十行，读文件能力废掉。
5. **webfetch 在子 agent 里低效**。单页可达 5 万字符全量进上下文、token 消耗大，且已被 2000 字符裸切破坏——用户决策：web_explore 砍掉 webfetch。

## 二、调研结论（opencode 对照）

| 项 | opencode | 本方案 |
|----|----------|--------|
| 文件探索子 agent | explore subagent：文件搜索专家 prompt，deny-all + allow grep/glob/read/bash/webfetch/websearch | file_explore：同思路，白名单 fs(glob/grep/read) + datetime + calculator |
| 子 agent 模型 | 继承父模型 | 固定 Flash（沿用现状，日常主链路也是 Flash） |
| 写工具 | explore 明确禁止创建/修改文件 | 函数级白名单剥掉 fs__write/edit + MiniReAct guard 双保险 |
| 大文件处理 | ① read 2000 行/50KB 封顶 + offset 分页脚注；② 通用截断超限落盘 + "派 explore 子 agent，不要自己读全文"提示；③ 压缩层把旧工具输出压 2000 字符 | ① fs_read 已有 offset/limit + 50KB 顶（同款）；② 子 agent 20KB 注入预算 + 头尾截断修复（本方案）；③ 压缩层以后单独做 |
| 子 agent 可用条件 | 无（本地进程） | 硬要求 macOS 客户端在线，离线报错，不降级（fs/browser 工具都在客户端） |

## 三、目标 / 非目标

**目标**：
- 新增 `file_explore` 子 agent：本地文件检索专家（fs__glob/grep/read），主循环派发后主上下文只收结构化报告
- 原 explore 改名 `web_explore`，白名单砍 webfetch，纯 browser 调研
- 两种子 agent 都硬要求客户端在线，离线直接报错（删除现有 webfetch-only 降级分支）
- 子 agent 按类型分派（prompt/白名单/maxToolResult 查表）
- MiniReAct 工具结果截断修复：保 JSON、保头尾、保 UTF-8

**非目标**：
- 子 agent 写文件（只读）；并行客户端工具执行；上下文压缩；file_explore 带 webfetch/网页搜索；客户端任何改动

## 四、架构与数据流

```
主循环 LLM 决定派 task(file_explore)
  → TaskTool.Execute → Orchestrator.RunSubAgent(opts{SubAgentType:"file_explore"})
    → 查 subagentSpec 表：prompt / serverWhitelist / clientWhitelist(funcs) / maxToolResult / requiresClient
    → 客户端硬检查：clientBridge == nil → 返回错误（不降级）
    → 建子会话（ParentID 挂主会话，列表不可见，Extra.SubAgentType 落库）
    → 工具定义组装：服务端 GetLLMToolsFiltered + 客户端按 server_name + 函数名双过滤
    → MiniReAct 循环（Flash，20 轮，600s 超时）
        ├─ 服务端工具：registry.ExecuteByName
        └─ 客户端工具：clientToolCaller → SSE client_tool_call（owner=subagent:<convID>）
             → Mac 客户端执行 fs__* → POST /api/chat/tool-result → 结果回传
    → 结果包裹 <task id=... state=...><task_result> 返回主 LLM
```

关键复用（零改动）：MiniReAct 客户端工具桥、clientToolCaller、事件转发器 makeSubAgentForwarder、子会话落库 MsgSink、超时/部分结果组装、tab 清理（file_explore 无 browser 调用天然 no-op）。

## 五、详细改动

### 5.1 类型分派（server/internal/agent/subagent.go）

新增 spec 结构 + 查表分派，替换写死常量：

```go
// subagentSpec — 子 agent 类型定义表（prompt / 白名单 / 结果注入预算 / 客户端要求）
type subagentSpec struct {
    prompt            string   // 角色 prompt（buildSubAgentSystemPrompt 用）
    serverWhitelist   []string // 服务端工具名（registry.GetLLMToolsFiltered）
    clientWhitelist   map[string][]string // server_name → 允许函数名；nil=该组全部
    maxToolResult     int      // MiniReAct 工具结果注入上限；0=默认 2000
    requiresClient    bool     // 客户端硬要求：无 clientBridge 直接报错
}

var subagentSpecs = map[string]subagentSpec{
    "web_explore": {
        prompt:          webExplorePrompt,
        serverWhitelist: []string{"search_notes_v2", "datetime", "calculator"},
        clientWhitelist: map[string][]string{"browser": nil},
        maxToolResult:   2000, // 默认值，显式声明
        requiresClient:  true,
    },
    "file_explore": {
        prompt:          fileExplorePrompt,
        serverWhitelist: []string{"datetime", "calculator"},
        clientWhitelist: map[string][]string{"fs": {"glob", "grep", "read"}},
        maxToolResult:   clientToolInjectBudgetBytes, // 20000，对齐客户端注入预算
        requiresClient:  true,
    },
}
```

`RunSubAgent` 步骤改造：
- 步骤 0：查表，未知类型返回 `&tool.SubAgentRunResult{Success:false, Error:"未知子 agent 类型: xxx（可用：web_explore、file_explore）"}`
- 客户端硬检查（两类型都 requiresClient=true）：`o.clientBridge == nil` → 返回错误 `"子任务 web_explore/file_explore 需要 macOS 客户端在线（当前未连接）。请改用主循环工具直接完成，或告知用户先打开客户端。"`。删除现有降级分支（subagent.go:218-223 的 `agentClientTools = nil`）。
- ui（ask_user）注入保持现状：仅当 ClientTools 非空时注入（客户端在线是前置条件，逻辑自然成立）。
- MiniReActConfig 增加 `MaxToolResult: spec.maxToolResult`。
- `buildSubAgentSystemPrompt(spec)`：签名改为接受 spec，注入对应 prompt。

### 5.2 函数级客户端白名单（subagent.go）

现有 `filterClientToolsByWhitelist` 按 server_name 过滤，升级为函数级：

```go
// filterClientToolsByFuncWhitelist 按 server_name → 允许函数名 过滤客户端工具定义。
// funcs 为 nil 表示该 server 全部函数放行；否则逐函数过滤（剥掉 fs__write/fs__edit 等）。
func filterClientToolsByFuncWhitelist(defs []llm.ToolDefinition, wl map[string][]string) []llm.ToolDefinition
```

双保险：定义层剥掉后 LLM 看不到 fs__write/fs__edit；运行时 guard（见 5.3）防 LLM 幻觉直呼未授权函数。

### 5.3 MiniReAct 函数级 guard（mini_react.go）

`SubAgentExt.AllowedTools`（map[string]bool，server 名粒度）扩展为函数级：

```go
type SubAgentExt struct {
    ...
    AllowedTools map[string][]string // server_name → 允许函数名；nil value=全放行；nil map=不限
}
```

guard 逻辑（executeToolCalls，现 324 行）：

```go
if funcs, ok := m.subAgent.AllowedTools[tc.Tool]; ok { // ok=该 server 受限
    if funcs == nil || !slices.Contains(funcs, tc.Function) {
        result = 拒绝（不在白名单）
    }
}
```

兼容性：nil map = 不限（profile_worker 等既有调用方零变化）。

### 5.4 工具结果截断修复（mini_react.go:354-356）

现状：

```go
resJSON, err := json.Marshal(result)
if m.maxToolResult > 0 && len(resJSON) > m.maxToolResult {
    resJSON = resJSON[:m.maxToolResult] // 裸切：断 JSON、劈 UTF-8、丢尾
}
```

改为 JSON 保结构截断：

```go
resJSON, err := json.Marshal(result)
if m.maxToolResult > 0 && len(resJSON) > m.maxToolResult {
    resJSON = truncateToolResultJSON(resJSON, m.maxToolResult)
}
```

`truncateToolResultJSON` 实现要点（新函数，mini_react.go 或 util）：
1. `json.Unmarshal` 到 `map[string]interface{}`（失败则回退旧裸切，保底）；
2. 递归/直取 `data`、`error` 两个字符串字段，按**总预算**做截断：数据字符串留头 70% + 尾 20%，中间插 `…[已截断 N 字符]…`（复用 `truncateToolResult` 逻辑，注意**按字符**切，不劈 UTF-8）；
3. 重新 `json.Marshal`。

效果：LLM 永远收到合法 JSON；截断有头有尾；file_explore 的 maxToolResult=20000 对齐客户端注入预算（fs_read 单页 ≈19KB），正常读文件基本不触发。

### 5.5 fileExplorePrompt（新增，subagent.go 常量）

```text
你是本地文件检索专家，擅长在用户的 Mac 文件系统上快速定位文件、搜索内容并提炼结论。
你的输出将直接交给主 agent 汇总回答用户，不直接面向用户。

能力（只读）：
- fs__glob：按文件名模式找文件（绝对路径起步）
- fs__grep：正则搜索文件内容，返回文件+行号+匹配行
- fs__read：读文件（行号前缀）或列目录；offset/limit 分页读大文件
- datetime / calculator：辅助

工作准则：
- 先规划：明确找什么、在哪找（用户主目录、项目目录等）
- 定位顺序：glob 找文件 → grep 定位行 → read 读上下文。不要上来就 read 大文件
- 多文件独立读取要并行调用（一次回复多个工具调用），不要串行等
- 避免小块切割读取（30 行一读）；需要更多上下文就一次读更大范围
- 结果被截断（有 "Output capped"/"Results truncated" 提示）→ 换更精确 pattern/path，或 read 用 offset 续读
- 搜索路径无授权时会触发用户确认；被拒绝后换方案，不要重复同一路径
- 遇到二进制文件/超大文件，用 grep 而不是 read

安全与边界：
- 严禁写入/修改/删除任何文件（没有写工具）
- 敏感文件（.env、密钥、证书等）会被客户端拦截，属预期，不要尝试绕过
- 数据边界：用户的笔记、记忆、历史对话对子任务不可见（没有对应工具）

最终输出必须严格使用以下结构（不要在结构外输出任何文字）：
<file_explore_result>
<summary>3-5 句话的总体结论</summary>
<findings>发现清单，每条：文件绝对路径 + 行号（file:line）+ 内容要点，用分号分隔</findings>
<files_read>已读文件清单（绝对路径），没读填"无"</files_read>
<open_questions>未解决的问题（没有则填"无"）</open_questions>
</file_explore_result>
```

### 5.6 webExplorePrompt 调整（subagent.go）

- 常量改名：`explorePrompt` → `webExplorePrompt`；删掉 webfetch 相关行（能力第 1 条、"页面拦截与止损"中 webfetch 相关描述保留 browser 部分即可——实际上拦截段全是 browser 语义，微调措辞）。
- `exploreWhitelist` → `webExploreWhitelist`：`[]string{"search_notes_v2", "datetime", "calculator"}`（browser/ui 经客户端白名单注入；**webfetch 移除**）。
- 输出结构 `<research_result>` 不变。

### 5.7 task 工具描述（tool/task.go）

- `subagent_type` 描述改为：

```
当前可用：web_explore（联网调研，基于 Mac 浏览器，只读）、file_explore（本地文件检索，只读）
```

- Description 补"何时使用"条目：

```
- 需要在用户本地文件系统上搜索/阅读多个文件（代码、文档、配置等）→ file_explore
```

- prompt 参数说明补触发提示：本地文件开放探索优先派 file_explore 子任务，避免主上下文被大量文件内容污染。

### 5.8 主规则引导行（promptbuilder/static.go）

在 Layer 1 公共规则加一行（靠近既有 task 相关规则处）：

```
本地文件检索/读代码等开放探索类任务，优先派 task 子任务（subagent_type=file_explore），
由子 agent 完成搜索与阅读，主对话只收结论——避免大量文件内容污染主上下文。
```

### 5.9 注释与命名同步

- model/conversation.go:37 注释 `"explore"` → `"web_explore"/"file_explore"`
- tool/subagent.go:20 注释同步
- 各测试中的 "explore" 字符串 → "web_explore"

### 5.10 后续追加：可访问目录写进工具 description（2026-09-16）

子任务原来只能从 description 的「已授权目录」含糊表述猜测可访问范围。现在客户端把**具体目录清单 + 硬性禁访高危路径**追加到 `fs__glob/grep/read/write/edit` 与 `shell__run` 的 description 末尾（`DirectoryAuthorizationManager.accessDescriptionBlock()`），随 `client_tools` 每条消息重建、服务端原样透传——改授权后下一条消息生效。详见 `shell-tool.md`「可访问目录下发给 LLM」。**服务端无需改动**（子任务的 tool defs 复用同一份 description）。

## 六、测试计划

### 单测（go test ./internal/agent/... ./internal/tool/...）

1. **分派**：未知类型返回错误；file_explore 走 fileExplorePrompt + 对应白名单
2. **函数级过滤**：`filterClientToolsByFuncWhitelist` 对 fs 组只留 glob/grep/read，write/edit 被剥；nil funcs 全放行
3. **guard**：MiniReAct 收到幻觉调用 fs.write → 拒绝结果（AllowedTools 函数级）
4. **离线硬错误**：clientBridge=nil 时两种类型都返回错误且不创建子会话；web_explore 不再降级 webfetch-only
5. **截断**：`truncateToolResultJSON` 大 data 字符串 → 输出合法 JSON + 头尾保留 + 标记 + 不劈中文
6. **task.go**：subagent_type=file_explore 透传正确

### 真机验收（make test 全绿 + 聊天实测）

1. 聊天问"我 ~/Documents 下的 foo 项目里有哪些 TODO" → 观察：主循环派 file_explore → 子任务活动面板显示 → fs__grep/fs__read 工具调用流 → 结果含 file:line 引用
2. 目录未授权 → L3 确认条弹出；拒绝 → 子 agent 换方案
3. 敏感文件（.env）→ 客户端拦截，子 agent 正常继续
4. 大文件（>20KB）→ 子 agent 用 offset 分页读，结果无截断乱码
5. 客户端断开 → 派 file_explore → 主循环收到"需要 macOS 客户端在线"错误，不卡死
6. web_explore 调研：只走 browser，不再出现 webfetch 调用
7. 子会话落库可观测：debug 页能看到子会话消息

## 七、风险与回滚

- **风险 1**：子 agent Flash 在 20 轮内搜不完大目录树 → prompt 已强调 glob→grep 定位优先；超时 600s 兜底返回部分成果
- **风险 2**：fs 工具结果经 clientToolCaller 每次往返 SSE+POST，子循环 20 轮可能慢 → 文件探索每轮 1-3 个工具调用，实测可接受；并行工具调用留给后续（低优先级 D 方案）
- **风险 3**：web_explore 砍 webfetch 后调研能力下降 → 浏览器 snaphot 可覆盖绝大多数页面；用户已决策接受
- **回滚**：改动集中在 subagent.go/mini_react.go/task.go/static.go 四个文件 + 测试，git revert 单提交即可

## 八、验收分级

按 CLAUDE.md 约定：本改动影响主链路（聊天→task 工具→子 agent→SSE/DB 落库），验收 = 完整 `make test` + 新增单测 + 真机实测第 6 节全部用例。

## 九、后续（不在本计划）

- B：主循环工具结果截断恢复（头尾保结构，同 5.4 手法推广到 orchestrator.truncateToolResult 启用）
- C：长会话上下文压缩
- D：客户端工具并行执行
