# 浏览器工具回到主会话 + 接管用户打开的页

> 日期：2026-10-15
> 范围：服务端（`server/conf/config.yaml`、`server/internal/config/config.go`、`server/internal/agent/orchestrator.go`、`server/internal/agent/client_tools.go` 的 `mainOwner`）+ macOS 客户端（`BrowserToolProvider.swift` 文案与注释、`RightSidebarView.swift` owner 前缀判定、`LocalToolProvider.swift` 注释）。
> 约定：客户端代码 agent 不编译验证，由用户在 Xcode 编译运行（CLAUDE.md）。

## 一、背景与目标

用户反馈两个问题：

1. **浏览器工具在主会话中不可见**：主会话想操作页面只能派 `web_explore` 子任务，多一跳且子任务面向"调研"、常另开新页而非使用用户已开的页。
2. **不能直接接管用户打开的网页**：用户有时需要自己先打开页面（登录、过验证码、绕过爬虫检测），再让 agent 操作**这一页**，而不是让 agent 另开一个页。

目标：

- `browser` 组放回主循环，主会话可直接 `list_tabs` / 读 / 接管用户标签。
- 引导 LLM 在"用户已开好页/已登录/已过验证码"时**优先接管该页**，不另开新页。

## 二、关键事实（根因）

1. `chat.subagent_only_client_tools` 默认 `[browser, fs, shell]`，主循环在 4 处按名单剔除 `browser`：目录（`orchestrator.go:370`）、已暴露 def（`:1199`）、**分发索引**（`:1406`）、expose 校验（`:1802`）。主会话 LLM 连 browser 的存在都看不到。
2. agent 侧读工具**无 tabId 时只落到本 owner 最近开的 agent 标签**（`BrowserToolProvider.sessionForAgent`），永不碰用户正在看的页；写工具必须显式 tabId。
3. `agent_browser_list_tabs` 原描述结尾是「只操作标注为自己的标签」，等于主动劝 LLM 别碰用户标签。
4. "操作用户标签"这条路**已经实现**：读免费；写首次弹一次授权确认（`userTabGrants` 按 owner 任务级缓存，确认条标「⚠ 作用于你自己的标签页」）；`list_tabs` 返回的 `activeTabId` 就是用户当前在看的页。缺的只是"主会话够得着" + "LLM 知道该这么走"。

结论：**无需新逻辑**，只需把 browser 放回主循环 + 改工具文案。

## 三、实现

### 1. 配置（服务端）

- `server/conf/config.yaml`：`subagent_only_client_tools: [fs, shell]`（去掉 browser）。
- `server/internal/config/config.go`：兜底默认同步改为 `[]string{"fs", "shell"}`，注释同步。
- `server/internal/agent/orchestrator.go`：字段注释同步。

一处改动同时解开上述 4 个过滤点。渐进式暴露（见 `docs/design/client-tool-exposure.md`）保证成本可控：主循环每轮只多一行目录（组名 + 函数名，无 schema），LLM `enable(["browser"])` 后才注入完整 def，且同窗口持久化暴露。

### 2. 工具文案（客户端，纯字符串）

`BrowserToolProvider.swift`：

- `agent_browser_open`：补「优先接管用户已打开的页：已登录/已过验证码时先 `list_tabs` 取 `owner=user` 的 tabId 直接操作，不要另开新页（另开可能重新触发站点风控/验证）」；`needsUserIntervention` 那句补「主会话无 `ui__ask_user` 时改为文字告知用户（页面已升栏并已发系统通知）」。
- `agent_browser_list_tabs`：删掉「只操作标注为自己的标签」，改为说明 `user` / `main` / `subagent:xxx` 三类 owner 标注、`activeTabId` = 用户当前在看的页、用户要求操作其页面时用 `owner=user` 的 tabId（读免费、写首次弹一次授权确认、本任务内免再确认）。

## 四、不做的事（YAGNI）

- **不新增**"接管/采用当前页"工具或字段：`list_tabs` + `activeTabId` + 已有授权链已够。
- **不改**agent 无 tabId 读工具的落点（仍只到本 owner 的 agent 标签，绝不落到用户激活页）——保留隔离，避免并发子任务互踩用户视图。
- **不给主循环注入 `ui__ask_user`**（用户拍板：先靠客户端自动通知）。
- **不动** `fs` / `shell`：work 模式仍仅子任务；plan 模式例外放行 fs 的 `glob/grep/read`（服务端白名单，2026-10-31）。

### 3. 主循环标签归属按会话隔离（评审 P1 修正）

主循环原传恒定 owner `"main"`（`orchestrator.go:1855`）。客户端 `userTabGrants` 是按 owner 缓存的 `Set<String>`、App 运行期无清理点（`BrowserToolProvider.swift`），旧语义下它只装 `subagent:<convID>` 才"天然隔离"；改成恒定的 `"main"` 后，**首个会话点过一次允许，后续所有会话对用户标签的写操作都不再弹确认条**（click/fill/type/select/check/download/close_tab 在用户标签上默认 L2，`needsUserTabGrant` 是它们唯一的确认入口）。同理 `lastAgentTabByOwner` / `pendingHandoff` 也退化成跨会话共享。

修正：新增 `mainOwner(convID)` → 主循环 owner 变 `"main:<convID>"`，与子任务 `"subagent:<convID>"` 同构，授权/隐式目标/手递手一并回到会话级隔离。配套改动：`RightSidebarView.agentOwnerHelp` 的 `owner == "main"` 改 `hasPrefix("main")`；文案由「本任务内」改「同一任务/会话内」。

## 五、行为变化

| 场景 | 之前 | 之后 |
|---|---|---|
| 客户端在线，主会话首轮 | 目录无 browser | 目录含 browser（仅函数名） |
| 主会话要操作页面 | 必须派 `web_explore` 子任务 | `enable` 后直接调用 |
| 用户说"操作我打开的这个页" | 主会话够不着，子任务常另开新页 | `list_tabs` → 取 user 标签 tabId → 读写该页（写首次确认） |
| 主会话 agent 开页撞验证码 | 不适用 | 客户端自动升栏 + 系统通知（LLM 无 `ask_user` 可用，只文字说明） |
| 子任务 `web_explore` | 不变 | 不变 |

## 六、验证

- 服务端（影响主链路 → 全量档）：`cd server && go build ./... && go test ./...`。现有断言 `TestMkLlmChatReq_SubAgentOnlyClientTools_NotExposed` 自带名单，不受影响。
- 真机（用户 Xcode 编译）：主会话里让 agent「操作我右栏打开的那个页（已登录）」→ 应能 `list_tabs` 拿到 user 标签 → snapshot/read → 首次 click 弹授权确认 → 允许后直接操作你那个已登录页。

## 七、风险与已知限制

1. **主循环 token 微增**：客户端在线时每轮多一行 browser 目录（约 28 个函数名），无 schema；启用后同窗口持久化，成本随轮次摊薄。
2. **主会话 agent 可自发浏览**：L3 确认仍在（`open` 免确认、`download` 必确认），读仅 L1；agent 自己的标签读写免确认。
3. **主循环无 `ui__ask_user`**：撞验证码/登录墙时 LLM 那句"调用 ask_user"在主循环是死指令；客户端自动升栏 + 系统通知仍触发，用户不会无感。以后要补闭环，复用 `appendAskUserTool` 注入主循环即可。
4. **"接管已有页比另开新页更稳"**：按用户实际反馈采纳（另开新页可能重新触发站点风控），未做真机 A/B 验证；真机验收时值得顺手确认同一 WKWebView 进程内新开 tab 是否复用登录态。
5. **读类结果预算未收紧（评审 P2）**：子任务 `web_explore` 单次工具结果截断 2000 字符（`subagent.go:228`），主循环走 `clientToolInjectBudgetBytes = 20000`（10×）。`browser` 回主循环后，`read(full/article)` 的长页正文最多 20KB 直接进主对话并落库长驻历史、每轮重放（此前只以 2000 字摘要进子循环、由子任务结论收口）。取舍：要么接受主历史变胖 + 热点轮次成本上升，要么给 browser 读类结果单独定更小的注入预算（或在提示里强制先 `outline`/`article`）。**本期接受现状**。
6. **主会话标签无收尾清场（评审 P2）**：`cleanupSubAgentTabs`（`subagent.go:887`）只在子任务结束按 `subagent:<convID>` 清场；`main:<convID>` 不参与，只能靠客户端 600s 空闲 TTL（`agentTabIdleTTL`）或 LLM 主动 `close_tab`。配额 20 的理由注释「子任务结束会清理，不会跨任务积累」对主会话不再成立：主会话可长期占住配额，期间派子任务会触发 `reclaimAgentTabQuota`（仅回收他人标签空闲 >600s 者）。**本期接受现状**。
