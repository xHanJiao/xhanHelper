# 浏览器 Agent 工作区 + 人工验证手递手

> 日期：2026-08-30
> 分支：feat/browser-agent-workspace
> 范围：macOS 右栏浏览器。改动集中在 `Shared/Tools/BrowserToolProvider.swift`、`Shared/Views/RightSidebarManager.swift`、`Shared/Views/RightSidebarView.swift`、`Shared/Views/AskUserBar.swift`、`Shared/ViewModels/ChatViewModel.swift` / `ChatViewModel+ClientTools.swift`（iOS 也编译这些文件，但浏览器仅 macOS 注册，iOS 行为不变）。服务端仅一处提示词文案（`server/internal/agent/subagent.go`）。
> 约定：客户端代码 agent 不编译验证，由用户在 Xcode 编译运行（CLAUDE.md）。

## 一、背景与目标

既有浏览器为单实例单 tab 列表：agent 与用户共享 tab 栏。痛点：

1. **agent 打开一堆 tab 影响浏览**：子任务调研标签与用户自己的标签混在同一 tab 栏。
2. **agent 误操作用户 tab**：交互工具（click/fill/type 等）无 tabId 时默认解析到 `activeSession`——即用户正在看的激活页（`runExecute` L244）。
3. **验证码/登录墙无闭环**：`isBlockedPageTitle` 对 Cloudflare/403 硬失败"换源"，人可解的登录/验证码墙却只能靠 LLM 自觉调 `ui__ask_user`——而 reCAPTCHA 跑在跨域 iframe，snapshot 读不到，LLM 几乎从不触发。

目标：
- agent 标签收进可折叠的"Agent 工作区"条，不进用户主 tab 栏（背景静默，不抢视图）。
- agent 无 tabId 的操作永不落到用户激活页；写用户标签需用户授权。
- 验证码/登录墙**客户端检测自动手递手**：页面升入主栏带角标 + AskUserBar「前往处理」，用户处理完交还 agent。
- agent 标签自动 GC（TTL 600s，既有），任务结束服务端清场（既有）。

## 二、关键决策

| 决策点 | 结论 |
|--------|------|
| 双浏览器 vs 单引擎隔离 | 单引擎 + 逻辑隔离（工作区条 + 寻址沙箱 + 授权）。双实例要拆 3800 行单文件且 WKWebView 无挂起 API，内存不省，回归面大 |
| agent 对用户标签的可见性 | **可见但写入需授权**：list_tabs 全量带归属标注；读免费；写（交互/导航/close）命中用户标签需每任务一次确认（确认条"允许本次任务"） |
| 验证码触发方式 | 客户端检测驱动（密码框/验证码 iframe 强信号 + 标题/URL 兜底），不依赖 LLM 自觉；open 返回结构化 `needsUserIntervention:true`，LLM 收到信号后调 ask_user（复用 ui 的预算豁免） |
| bot 墙（Cloudflare/403） | 维持硬失败"换源"（人解也无用，且避免频繁打断） |
| 授权缓存生命周期 | `userTabGrants: Set<owner>`，owner 为 `subagent:<convID>` 天然按任务隔离，无需清理 |
| 手递手完成后 | 一律交还工作区（完成/跳过/超时统一 demote），语义简单 |
| 交互工具 tabId | agent 写工具必须显式 tabId（schema 补 tabId 参数）；无 tabId 报错，杜绝共享 activeSession 竞态 |

## 三、实现

### 3.1 BrowserToolProvider

**寻址沙箱**
- 新增 `lastAgentTabByOwner: [String: String]`：agent 无 tabId 读工具解析到该 owner 最近触摸的 agent 标签；写工具无 tabId 直接报错。
- 读工具：tabId 可任意（用户/agent 标签都可读）；无 tabId → `lastAgentTabByOwner[owner]`。
- 写工具（`userTabWriteTools`：click/click_text/fill/type/select/check/uncheck/press/go_back/go_forward/close_tab/download/scroll）：必须 tabId；目标为**用户标签**（owner==nil）且 owner 未授权 → 报错提示需用户授权。
- `switch_tab`：agent 只更新 `lastAgentTabByOwner[owner]`，不再改 UI 激活页（用户视图不闪）。
- `close_tab`：自身 agent 标签可关（既有归属保护）；用户标签需授权；其他任务标签拒绝（既有）。
- **`agent_browser_close`（关闭全部）**：agent 上下文只关 agent 标签（原 `performCloseAll` 无归属过滤，会清光用户标签——补洞）。

**授权**
- `userTabGrants: Set<String>`（owner 字符串）+ `grantUserTabOps(owner:)`。
- `tabIsUserOwned(_ tabId:)` + `needsUserTabGrant(tool:arguments:owner:)` 供 ChatViewModel 在确认条前判定。
- 执行路径内兜底强制校验（双保险，防绕过确认条直接调用）。

**工作区 & 手递手**
- `handleOpen`：agent 标签默认进工作区（`activate` 恒 false，不抢视图）；`background:false`（用户明确要看）→ 加载成功后 `promoteAgentTab` 升主栏。
- 墙检测：加载成功且是 agent 标签 → 跑 `wallProbeJS`（`input[type=password]` 或已知验证码 iframe src：recaptcha/geetest/yidun/captcha 等）+ 标题/URL 关键词兜底（验证码/登录/login/verify/passport/2fa/sign in）。命中 → `promoteAgentTab` + ⚠角标 + `pendingHandoff[owner]=tabId`，open 返回 `{"needsUserIntervention":true, "reason", "tabId"}`（非错误）。
- `promoteAgentTab(id)` / `demoteAgentTab(id)`：驱动 RightSidebarManager 的 agentTabs↔tabs 迁移。
- `handoffTabId(forOwner:)` / `activateHandoffTab(owner:)` / `resolveHandoff(owner:)`：AskUserBar 与 ask_user 收尾用。

**GC**
- `cleanupIdleAgentTabs`：沿用原 TTL（600s），不额外激进；`pendingHandoff` 中的 owner 的标签豁免（防用户思考期间被回收）。

### 3.2 RightSidebarManager

- 新增 `@Published var agentTabs: [RightSidebarTab]`；`openTab` 按 `isAgentTab` 路由到两个数组。
- `promoteAgentTab(id)`：agentTabs→tabs（置后）+ isAgentTab=false + 激活；`demoteAgentTab(id)`：反向。
- `closeTab` / `updateTabTitle` / `updateTabHost` / `updateTabAgentState` / `replaceTabContent` 兼容两个数组。
- `RightSidebarTab` 新增 `needsUserAttention`（手递手 ⚠ 角标）。

### 3.3 RightSidebarView

- 浏览器态（非分屏）底部加可折叠 **Agent 工作区条**：数量徽标 + host favicon + 标题前缀 + 关闭按钮；点击标签 = promote 进主栏。空则隐藏。
- `browserTabButton`：`needsUserAttention` 显示橙色 ⚠。

### 3.4 AskUserBar + ChatViewModel

- `UserRequest` 增 `owner` 与 `hasHandoffTab`；`awaitUserRequest` 透传 owner。
- AskUserBar 卡片：`hasHandoffTab` 时显示 **[前往处理]** 按钮（调 `activateHandoffTab(owner)`，不自动切走）。
- ask_user resolve 后调 `resolveHandoff(owner)`（完成/跳过/超时统一交还）。

### 3.5 ChatViewModel+ClientTools

- ask_user 分支透传 owner；resolve 后 demote 手递手标签。
- 浏览器写工具 + `tabIsUserOwned` + 未授权 → 复用 `awaitToolConfirmation` 拦截，允许后 `grantUserTabOps(owner)`。
- 工具 schema（click/fill/type/select/check/uncheck/press/scroll/go_back/go_forward）补 `tabId` 参数。

### 3.6 服务端

- `subagent.go` 提示词：收到 `needsUserIntervention:true` 必须立即调 ask_user。

## 四、工具描述变更

- `agent_browser_open`：agent 标签进工作区；`background:false` 升主栏；命中验证码/登录墙返回 `needsUserIntervention`（此时调 ask_user）；写工具必须 tabId。
- `list_tabs`/`close_tab`/`switch_tab`：agent 作用域语义 + 用户标签写授权说明。

## 五、验收（用户 Xcode 编译运行）

1. 子任务调研开多页：全部进工作区条，主栏不闪不切；点击工作区标签翻牌进主栏。
2. 用户自己开页浏览时子任务并行调研：用户页面不被 agent 操作（无 tabId 写报错）。
3. "帮我操作我打开的 XX 页"：首次写操作弹确认条，允许后本任务内免确认。
4. 跑一个真实验证码/登录墙站：页面自动升主栏 + ⚠ + AskUserBar [前往处理]；处理后点完成，agent 继续；跳过则换源。
5. Cloudflare/403 站：仍硬失败换源，不弹手递手。
6. agent 标签空闲自动关闭（TTL 600s）；服务端子任务结束清场。
7. `go build ./...` + `go test ./...` 全绿。

---

## 后续变更

- **2026-10-15（browser 放回主循环）**：`browser` 组不再属于 `subagent_only_client_tools`，主会话可直接接管用户打开的页（不再必须先派 `web_explore` 子任务）。本文第 3 节用例 11「帮我操作我打开的 XX 页」现由主会话直接走通（`list_tabs` 取 user 标签 tabId → 写操作首次弹授权确认）。主循环标签归属同步改为按会话隔离 `main:<convID>`（原先恒定的 `"main"` 会让用户标签写入授权跨会话残留）；`list_tabs`/`open` 的工具描述同步调整以引导该路径。已知限制：主循环无 `ui__ask_user`，验证码/登录墙只靠客户端自动升栏 + 系统通知提示用户（无 [前往处理] 按钮）。见 `docs/design/mac-apps/browser-main-session.md`。
