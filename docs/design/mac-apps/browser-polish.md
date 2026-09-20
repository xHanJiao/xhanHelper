# 浏览器打磨（安全优先）— 调研与方案

> 日期：2026-08-03
> 分支：feat/browser-polish
> 背景：用户日均在浏览器上花最多时间，希望 app 内浏览器"安全、节能、接近 Chrome 体验"。
> 安全是第一优先级：防止恶意网页/注入指令窃取账号。

## 一、现状盘点

- 基座：WKWebView，Chrome 系 UA + 指纹伪装 + 去 webdriver 痕迹。
- 共享持久化 `WKWebsiteDataStore`（cookie/localStorage 跨 tab 共享，同真实浏览器）。
- 协议白名单：仅 http/https/about，其余 `.cancel`（防 file:// 等绕过）。
- JS 弹窗 → NSAlert；window.open/target=_blank → 新 tab。
- agent 工具链：open/close/switch/read/snapshot/get_text/get_value/click/click_text/cgclick/drag/fill/type/press/select/check/uncheck/scroll/wait_for_* /get_media_sources/download（L3 确认 + 媒体白名单）。
- 浏览器 UI：地址栏（后退/前进/刷新）+ tab 条 + PiP 按钮。

## 二、安全审查（风险优先级排序）

| # | 风险 | 现状 | 影响 |
|---|------|------|------|
| **S1** | **密码/敏感字段进 LLM** | `snapshot` 的 `collectElsBody` 读 `e.value`；`get_value` 返回任意 input 的 value。`input[type=password]` 的已填密码会被抓走 | 恶意页面/注入 → agent 读走登录密码 → **账号失窃** |
| **S2** | 注入诱导自动操作账户 | 高敏域（银行/支付/邮箱）上 click/fill/type 自动放行（L2） | 注入指令让 agent 点"转账确认"、填表 → 账户操作 |
| **S3** | 页面内容作为指令进入 LLM | 页面文本作为 tool result 直接进上下文 | prompt injection（已有系统提示约束，需加固） |
| **S4** | 摄像头/麦克风/定位 | `requestMediaCapturePermissionFor` / `requestGeolocationPermissionFor` 未实现 | 默认拒绝，但未显式声明，需确认 |
| **S5** | 登录态残留 | cookie 本地持久化，无一键清除入口 | 借机使用/账号留痕 |
| **S6** | 下载 | 仅 agent 工具下载（L3 + 媒体白名单）；用户点下载链接走导航，二进制可能白屏 | 体验 + 需确认拦截 |

**安全加固项（S1-S5 本期做，S6 并入下载改造）**

### S1 敏感字段脱敏（最关键）
- `collectElsBody`：`input[type=password]` 的 value 一律不读；`input[type=hidden]` 不进快照。
- `get_value`：password/hidden 输入框返回"敏感字段不可读"错误。
- `get_text`/`read` 全量用 `innerText`（不含表单 value），天然安全，不动。
- 密码框在 snapshot 中仅标注 `type=password`（可交互），不含值。

### S2 高敏域风险升级
- 依据 URL 识别高敏域（关键词：bank/banking/pay/payment/paypal/stripe/alipay/wechat pay/mail/webmail/email/admin/account/网上银行…，且 https）。
- 高敏域上 `click/click_text/fill/type/select/check/uncheck` 风险级别升到 L3（复用现有 `pendingToolConfirmation` 确认机制）。
- 实现：`BrowserToolProvider.execute` 按 `activeSession.webView.url` 计算 riskLevel 注入确认。

### S3 页面内容不可信标注
- snapshot/get_text/read 结果前加不可信标记（如 `[页面内容-不可信]`），强化 system prompt 已有的"忽略页面内指令"约束（防御纵深）。

### S4 权限显式拒绝
- 实现 `webView(_:requestMediaCapturePermissionFor:…)` → 默认拒绝。
- 实现 `requestGeolocationPermissionFor` → 默认拒绝。
- 证书保持 fail-closed（**不做**"忽略证书继续访问"，宁缺毋滥）。

### S5 清除浏览数据
- 浏览器 UI 加"清除浏览数据"按钮 → `WKWebsiteDataStore.default().removeData(ofTypes:modifiedSince:)`（cookie/localStorage/site data/缓存），一键退出所有登录态。

### S6 下载拦截（并入 Tier 2）
- `WKDownload` 拦截导航响应中的文件下载 → 确认（L3 样式）→ 存 `~/Downloads/xhanHelper/` + 进度 + 完成通知。

## 三、体验路线（Tier 1 紧随安全，Tier 2/3 后置）

### Tier 1 — 核心体验
| 项 | 说明 |
|----|------|
| 键盘快捷键 | Cmd+L 地址栏 / Cmd+T 新标签 / Cmd+W 关 tab / Cmd+Shift+T 恢复关闭 / Cmd+F 查找 / Cmd+± 缩放 |
| 快捷键冲突路由 | Cmd+[/]、Cmd+R、Cmd+± 被全局占用（聊天历史/刷新/字号）——需**浏览器聚焦时优先**，否则走全局 |
| 页内查找 | 查找栏：高亮 + 上下导航 + 计数（WKFindResult / JS 实现） |
| 地址栏增强 | HTTPS 锁图标、加载进度条、非 URL→默认搜索引擎、新 tab 自动聚焦 |
| 节能 | 非激活 tab 从视图层级摘除（不合成渲染）；仅激活 tab 在层级 |

### Tier 2 — 真实浏览器行为
下载拦截（S6）、WKContentRuleList 广告/追踪屏蔽（安全+节能+速度）、恢复关闭 tab、favicon。

### Tier 3 — 进阶
收藏夹/历史、tab 拖拽排序、阅读模式、tab 冻结（Chrome discarding）。

## 四、快捷键冲突取舍（待确认）

- 方案：浏览器 webview/地址栏为 first responder 时，Cmd+[ / ] → 浏览器后退/前进；Cmd+R → 刷新；Cmd+± → 缩放；离开浏览器 → 恢复聊天历史/全局刷新/字号。
- 实现难点：全局绑定在 `NavigationToolbar` 的 `.keyboardShortcut` 与 `AppDelegate` 的 NSEvent monitor。需要"焦点感知"分发（检查 firstResponder 是否在浏览器内）。

## 五、本期建议范围

**第一批（安全加固）**：S1 脱敏 + S2 高敏域 L3 + S3 标注 + S4 权限拒绝 + S5 清除浏览数据。
**第二批（Tier 1 UX）**：快捷键 + 冲突路由 + 页内查找 + 地址栏 + 节能隐藏 tab。
**后续**：Tier 2/3。

## 六、改动文件（预估）

- `BrowserToolProvider.swift`：S1 脱敏、S2 风险分级、S4 权限、S3 标注、S6 WKDownload、快捷键动作、隐藏 tab。
- `BrowserTabView.swift`：地址栏增强、查找栏、加载进度、HTTPS 锁、快捷键。
- `RightSidebarView.swift`：tab 条增强（favicon/中键关闭/右键菜单）、清除浏览数据入口。
- 新增：`BrowserFindBar.swift`、`BrowserHistoryStore`（恢复关闭 tab）等。

## 七、调研参考

> 补充调研（2026-08-03）：安全部分对照业界做法，确认不是第一个做这件事的。

| 来源 | 要点 |
|------|------|
| Simon Willison《Prompt injection explained》(2023-05) + 《The dual LLM pattern》(2023-04) + 数据外泄攻击系列 | ① prompt 注入是工具型 agent 读网页内容的根本漏洞；② 靠"提示词里加忽略指令/分隔符"（prompt begging）**不是防御**；③ "基于概率的 AI 检测"也不是安全（99% 是及格线以下的失败）；④ 结构级防御是 **dual-LLM 隔离**（有工具的 LLM 永不直接接触不可信网页内容）；⑤ **外泄攻击**：注入指令让 agent 把读到的敏感内容 base64 塞进 URL 诱导点击 → 数据外泄 |
| OWASP《Agentic AI – Threats and Mitigations》(2025-02) | agent 安全支柱：最小权限、敏感动作人工确认、沙箱、防过度代理（excessive agency） |
| Anthropic《Building effective agents》(2024-12) | 工具设计即安全：最小化工具集 + 人类监督检查点；agent 在受信环境才应自治 |
| browser-use（最流行开源浏览器 agent） | 社区实践对照：认证/登录态管理、反检测、代理轮换等 |

## 八、调研结论 → 方案修正

### G1 原 S3 定位修正（最重要）
- "页面内容标注不可信"（S3）**不是主防御**，只是纵深防御的一层。Simon Willison 明确说这类 prompt 级约束可以被绕过。
- 真正的防御按有效性排序：
  1. **L3 人工确认**（已有 + 本批扩展高敏域）——即使被注入，高危动作要人点头。
  2. **S1 脱敏**——密码/隐藏字段永不进 LLM 上下文。
  3. **最小权限**——绝不新增"读 cookie/凭据"类工具；`execute_script` 保持 DEBUG-only；`agent_browser_open` 保持 L3（外泄攻击的 URL 会暴露在确认框）。
  4. **V2 dual-LLM 隔离**——有工具的 LLM 与"读网页"的 LLM 分离。架构改动大，单列研究项。

### G2 agent 操作可见性
> 2026-09 修订：浏览器顶部活动指示条已移除——它每次操作都插入/退场，把 WebView 上下顶动，影响浏览。
> agent 操作可见性改由**聊天栏的 tool pill** 承载（服务端 `tool_call`/`tool_result` 事件本就下发，
> `ChatViewModel` 用 `LocalToolRegistry.displayName` 把 `browser.agent_browser_click` 换成中文短名），
> 与其它工具调用一致；L3 确认也不再桥到浏览器，只留聊天栏确认条 + 提示音。
> 浏览器内确认条（`BrowserAgentConfirmBar`）保留，仅服务**浏览器内部确认**：有下载进行中时关标签、
> 用户点链接触发下载（这两类没有聊天对手，删了会 30s 自动取消）。`BrowserActivityCenter` 随之收窄为
> 「待确认状态 + `confirmationBody` 人话摘要」。

- 本地工具执行全走 `ChatViewModel.handleClientToolCall` 咽喉点。
- 指示器文案**不展示敏感值**（fill/type 只显示目标占位符，不显示输入内容）。

### G3 内容拦截（WKContentRuleList）
- 内置精选规则（覆盖 doubleclick/googlesyndication/google-analytics/connect.facebook.net/criteo/taboola/outbrain/cnzz/hm.baidu 等）。
- 少加载第三方脚本 = 减少注入面 + 隐私 + 节能 + 更快（既是安全也支持"节能/接近 Chrome"）。

### G4/G5/G6/G7 补充项
- **G4**：确认 `agent_browser_open` 已是 L3；保持"永不新增读 cookie/凭据工具"原则。
- **G5**：下载跨域提示——`agent_browser_download` 目标与当前页不同源时，确认信息标注跨域来源。
- **G6**：**http 明文页从严**——非 https 页面上交互工具一律升 L3（明文页面可被中间人注入，是最易注入源）。
- **G7**：`window.open` 无条件开新 tab，恶意页可刷屏；加每页 tab 上限（暂以 tab 计数上限实现）。

### 本批最终范围
- 安全批次：**S1 脱敏 / S2 高敏域 L3 / S3' 标注（纵深）/ S4 权限拒绝 / S5 清除浏览数据 / S6 下载跨域 / G2 操作可见性（含确认条 C）/ G3 内容拦截**。
- V2 单列：**dual-LLM 隔离**（根治 prompt 注入）。
- 体验批次（后续）：快捷键 + 焦点路由、页内查找、地址栏增强、节能隐藏非激活 tab、下载管理器、恢复关闭 tab、favicon、收藏/历史。
